package ingest

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wmagda/tanda-lead-intake/internal/ai"
	"github.com/wmagda/tanda-lead-intake/internal/models"
	"github.com/wmagda/tanda-lead-intake/internal/parseutil"
)

// Message is one inbound email to ingest (from Gmail poll or a dev CLI).
type Message struct {
	GmailThreadID  string
	GmailMessageID string
	From           string
	Subject        string
	Body           string
}

// Result is returned after a successful ingest.
type Result struct {
	Status     string // "created", "duplicate", or "skipped"
	LeadID     string
	ThreadID   string
	DraftID    *string
	Intent     string
	Confidence float64
	SkipReason string
}

// Process runs AI parse + DB writes for one inbound message.
// Safe to call from the Gmail worker; does not send email.
func Process(ctx context.Context, pool *pgxpool.Pool, aiClient *ai.Client, msg Message) (Result, error) {
	if strings.TrimSpace(msg.GmailMessageID) == "" {
		return Result{}, fmt.Errorf("gmail_message_id required")
	}
	if strings.TrimSpace(msg.From) == "" {
		return Result{}, fmt.Errorf("from required")
	}

	existingThreadID, existingLeadID, err := lookupByGmailMsgID(ctx, pool, msg.GmailMessageID)
	if err != nil {
		return Result{}, fmt.Errorf("lookup: %w", err)
	}
	if existingThreadID != "" {
		log.Printf("[ingest] duplicate msg=%s lead=%s thread=%s",
			msg.GmailMessageID, existingLeadID, existingThreadID)
		return Result{
			Status:   "duplicate",
			LeadID:   existingLeadID,
			ThreadID: existingThreadID,
		}, nil
	}

	displayName, envelopeEmail := parseutil.SenderFrom(msg.From)
	formRelay := parseutil.IsFormRelay(msg.From)
	voiceRelay := parseutil.IsGoogleVoiceRelay(msg.From, msg.Subject, msg.Body)
	log.Printf("[ingest] new msg=%s thread=%s from=%s form_relay=%v voice_relay=%v subject=%q body=%d chars",
		msg.GmailMessageID, msg.GmailThreadID, envelopeEmail, formRelay, voiceRelay, logTruncate(msg.Subject, 80), len(msg.Body))

	parseCtx, parseCancel := context.WithTimeout(ctx, ai.RequestTimeout())
	log.Printf("[ingest] AI parse start msg=%s (timeout %s)", msg.GmailMessageID, ai.RequestTimeout())
	aiStart := time.Now()
	pr, parseErr := aiClient.ParseExtracted(parseCtx, msg.From, msg.Subject, msg.Body, formRelay, voiceRelay)
	parseCancel()

	var aiLead *models.Lead
	var draftText string
	if parseErr != nil {
		log.Printf("[ingest] skipped msg=%s: AI parse failed after %s: %v",
			msg.GmailMessageID, time.Since(aiStart).Round(time.Millisecond), parseErr)
		return Result{Status: "skipped", SkipReason: "ai parse failed"}, nil
	}
	if !pr.IsLeadIntent() {
		log.Printf("[ingest] skipped msg=%s: not a potential client (is_lead=false)", msg.GmailMessageID)
		return Result{Status: "skipped", SkipReason: "not a lead"}, nil
	}

	aiLead = pr.ToLead()
	draftText = pr.Draft
	intent := ""
	conf := 0.0
	if aiLead.RequestType != nil {
		intent = *aiLead.RequestType
	}
	if aiLead.AIConfidence != nil {
		conf = *aiLead.AIConfidence
	}
	log.Printf("[ingest] AI parse ok after %s is_lead=true intent=%q confidence=%.2f draft=%d chars",
		time.Since(aiStart).Round(time.Millisecond), intent, conf, len(draftText))

	custName, custEmail, custPhone := resolveCustomer(msg, formRelay, voiceRelay, &pr, displayName, envelopeEmail)
	if custEmail == "" && custPhone == "" {
		log.Printf("[ingest] skipped msg=%s: no customer contact", msg.GmailMessageID)
		return Result{Status: "skipped", SkipReason: "no customer contact"}, nil
	}
	log.Printf("[ingest] customer %q <%s> phone=%q", custName, custEmail, custPhone)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("tx begin: %w", err)
	}

	resp, err := ingestInTx(ctx, tx, msg, custName, custEmail, custPhone, aiLead, draftText)
	if err != nil {
		_ = tx.Rollback(ctx)
		return Result{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("tx commit: %w", err)
	}
	resp.Status = "created"
	log.Printf("[ingest] saved lead=%s email_thread=%s draft=%v msg=%s",
		resp.LeadID, resp.ThreadID, resp.DraftID != nil, msg.GmailMessageID)
	return resp, nil
}

func resolveCustomer(msg Message, formRelay, voiceRelay bool, pr *ai.ParseResult,
	headerName, headerEmail string) (name, email, phone string) {
	notificationRelay := formRelay || voiceRelay

	if formRelay {
		_, relay := parseutil.SenderFrom(msg.From)
		bodyName, bodyEmail := parseutil.ExtractContactFromFormBody(msg.Body)
		if bodyEmail != "" && bodyEmail != relay && !parseutil.IsNotificationSenderEmail(bodyEmail) {
			email = bodyEmail
		}
		if bodyName != "" {
			name = bodyName
		}
	}

	if voiceRelay {
		vName, vPhone := parseutil.ExtractGoogleVoiceContact(msg.Subject, msg.Body)
		if vPhone != "" {
			phone = vPhone
		}
		if vName != "" {
			name = vName
		}
	}

	if !notificationRelay {
		email = headerEmail
		name = headerName
	}

	if pr != nil {
		if pr.CustomerEmail != nil {
			candidate := strings.ToLower(strings.TrimSpace(*pr.CustomerEmail))
			if candidate != "" && !parseutil.IsNotificationSenderEmail(candidate) {
				email = candidate
			}
		}
		if pr.CustomerName != nil {
			candidate := strings.TrimSpace(*pr.CustomerName)
			if candidate != "" && !parseutil.IsGoogleVoiceDisplayName(candidate) {
				name = candidate
			}
		}
		if pr.CustomerPhone != nil && strings.TrimSpace(*pr.CustomerPhone) != "" {
			phone = strings.TrimSpace(*pr.CustomerPhone)
		}
	}

	if phone == "" {
		phone = parseutil.ExtractPhoneFromBody(msg.Subject + "\n" + msg.Body)
	}

	if !notificationRelay && email == "" {
		email = headerEmail
	}
	if name == "" && !parseutil.IsGoogleVoiceDisplayName(headerName) {
		name = headerName
	}
	if parseutil.IsNotificationSenderEmail(email) {
		email = ""
	}
	if voiceRelay && name == "" && phone != "" {
		name = phone // better label than "Google Voice" in UI
	}

	return name, email, phone
}

func logTruncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func ingestInTx(ctx context.Context, tx pgx.Tx, msg Message,
	displayName, senderEmail, customerPhone string, ai *models.Lead, draftText string) (Result, error) {

	var leadID, threadUUID string
	var draftUUID *string

	note := fmt.Sprintf("ingested from gmail: %s", time.Now().Format(time.RFC3339))
	if customerPhone != "" {
		note += fmt.Sprintf("\nphone: %s", customerPhone)
	}

	row := tx.QueryRow(ctx, `
		insert into leads (
			gmail_thread_id, customer_email, customer_name,
			request_type, dance_style, level,
			student_count, requested_time, status, priority, ai_confidence, notes
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		on conflict (gmail_thread_id) do update
		set
			customer_email = excluded.customer_email,
			customer_name  = excluded.customer_name,
			request_type   = excluded.request_type,
			dance_style    = excluded.dance_style,
			level          = excluded.level,
			student_count  = excluded.student_count,
			requested_time = excluded.requested_time,
			ai_confidence  = excluded.ai_confidence,
			notes          = leads.notes || chr(10) || $12,
			updated_at     = now()
		returning id
	`,
		msg.GmailThreadID,
		senderEmail,
		sOrNil(displayName),
		ptrStrOrNil(ai.RequestType),
		ptrStrOrNil(ai.DanceStyle),
		ptrStrOrNil(ai.Level),
		i32OrNil(ai.StudentCount),
		ptrStrOrNil(ai.RequestedTime),
		"new",
		"normal",
		fPtrOrNil(ai.AIConfidence),
		note,
	)
	if err := row.Scan(&leadID); err != nil {
		return Result{}, fmt.Errorf("lead upsert: %w", err)
	}

	threadUUID = mustUUID()
	_, envelopeEmail := parseutil.SenderFrom(msg.From)
	_, err := tx.Exec(ctx, `
		insert into email_threads (id, lead_id, gmail_message_id, gmail_thread_id, sender_email, subject, body)
		values ($1, $2, $3, $4, $5, $6, $7)
	`, threadUUID, leadID, msg.GmailMessageID, msg.GmailThreadID, envelopeEmail, orEmpty(msg.Subject), orEmpty(msg.Body))
	if err != nil {
		return Result{}, fmt.Errorf("thread insert: %w", err)
	}

	if strings.TrimSpace(draftText) != "" {
		dID := mustUUID()
		_, err = tx.Exec(ctx, `
			insert into draft_responses (id, lead_id, draft_text, approval_status)
			values ($1, $2, $3, 'pending')
		`, dID, leadID, draftText)
		if err != nil {
			return Result{}, fmt.Errorf("draft insert: %w", err)
		}
		draftUUID = &dID
	}

	resp := Result{
		LeadID:   leadID,
		ThreadID: threadUUID,
		DraftID:  draftUUID,
	}
	if ai.RequestType != nil && *ai.RequestType != "" {
		resp.Intent = *ai.RequestType
	}
	if ai.AIConfidence != nil {
		resp.Confidence = *ai.AIConfidence
	}
	return resp, nil
}

// KnownMessageIDs returns which Gmail message IDs already exist in email_threads.
func KnownMessageIDs(ctx context.Context, pool *pgxpool.Pool, ids []string) (map[string]bool, error) {
	known := make(map[string]bool)
	if len(ids) == 0 {
		return known, nil
	}
	rows, err := pool.Query(ctx,
		`select gmail_message_id from email_threads where gmail_message_id = any($1)`,
		ids,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		known[id] = true
	}
	return known, rows.Err()
}

func lookupByGmailMsgID(ctx context.Context, pool *pgxpool.Pool, msgID string) (threadID, leadID string, err error) {
	err = pool.QueryRow(ctx,
		`select gmail_thread_id, lead_id::text from email_threads where gmail_message_id=$1 limit 1`,
		msgID,
	).Scan(&threadID, &leadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return threadID, leadID, nil
}

func mustUUID() string {
	id, err := uuid.NewRandom()
	if err != nil {
		panic("uuid generation failed: " + err.Error())
	}
	return id.String()
}

func sOrNil(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func ptrStrOrNil(p *string) any {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil
	}
	return *p
}

func i32OrNil(p *int32) any {
	if p == nil || *p <= 0 {
		return nil
	}
	return *p
}

func fPtrOrNil(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func orEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}
