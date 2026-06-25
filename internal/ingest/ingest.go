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
	ReceivedAt     time.Time // when Gmail received the message (InternalDate)
	// StudioRepliedAfter is set when Gmail shows an outbound studio message later in the same thread.
	StudioRepliedAfter bool
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
	prior := loadConversationContext(ctx, pool, msg, formRelay)
	log.Printf("[ingest] AI parse start msg=%s (timeout %s, prior_msgs=%d)", msg.GmailMessageID, ai.RequestTimeout(), len(prior))
	aiStart := time.Now()
	pr, parseErr := aiClient.ParseExtracted(parseCtx, msg.From, msg.Subject, msg.Body, formRelay, voiceRelay, prior)
	parseCancel()

	var aiLead *models.Lead
	var draftText string
	if parseErr != nil {
		log.Printf("[ingest] skipped msg=%s: AI parse failed after %s: %v",
			msg.GmailMessageID, time.Since(aiStart).Round(time.Millisecond), parseErr)
		recordSkipped(ctx, pool, msg)
		return Result{Status: "skipped", SkipReason: "ai parse failed"}, nil
	}
	if !pr.IsLeadIntent() {
		log.Printf("[ingest] skipped msg=%s: not a potential client (is_lead=false)", msg.GmailMessageID)
		recordSkipped(ctx, pool, msg)
		return Result{Status: "skipped", SkipReason: "not a lead"}, nil
	}

	aiLead = pr.ToLead()
	draftText = pr.Draft
	if msg.StudioRepliedAfter {
		draftText = ""
		log.Printf("[ingest] skip draft msg=%s: studio already replied in Gmail thread", msg.GmailMessageID)
	}
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
		recordSkipped(ctx, pool, msg)
		return Result{Status: "skipped", SkipReason: "no customer contact"}, nil
	}
	log.Printf("[ingest] customer %q <%s> phone=%q", custName, custEmail, custPhone)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("tx begin: %w", err)
	}

	leadStatus := "new"
	if msg.StudioRepliedAfter {
		leadStatus = "waiting_customer"
	}
	resp, err := ingestInTx(ctx, tx, msg, custName, custEmail, custPhone, aiLead, draftText, leadStatus)
	if err != nil {
		_ = tx.Rollback(ctx)
		return Result{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("tx commit: %w", err)
	}
	if resp.Status == "" {
		resp.Status = "created"
	}
	log.Printf("[ingest] saved lead=%s email_thread=%s draft=%v msg=%s status=%s",
		resp.LeadID, resp.ThreadID, resp.DraftID != nil, msg.GmailMessageID, resp.Status)
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
		if pr.CustomerPhone != nil {
			candidate := strings.TrimSpace(*pr.CustomerPhone)
			if candidate != "" && !parseutil.IsStudioPhone(candidate) {
				phone = candidate
			}
		}
	}

	if phone == "" {
		phone = parseutil.ExtractPhoneFromBody(msg.Subject + "\n" + msg.Body)
	}
	if parseutil.IsStudioPhone(phone) {
		phone = ""
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

// recordSkipped inserts into email_threads with lead_id=NULL so the message
// is recognized as already-processed on future runs (avoids re-running the LLM).
func recordSkipped(ctx context.Context, pool *pgxpool.Pool, msg Message) {
	_, envelopeEmail := parseutil.SenderFrom(msg.From)
	_, err := pool.Exec(ctx, `
		insert into email_threads (id, lead_id, gmail_message_id, gmail_thread_id, sender_email, subject, body)
		values ($1, null, $2, $3, $4, $5, $6)
		on conflict (gmail_message_id) do nothing
	`, mustUUID(), msg.GmailMessageID, msg.GmailThreadID, envelopeEmail, orEmpty(msg.Subject), orEmpty(msg.Body))
	if err != nil {
		log.Printf("[ingest] warning: failed to record skipped msg=%s: %v", msg.GmailMessageID, err)
	}
}

func logTruncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func ingestInTx(ctx context.Context, tx pgx.Tx, msg Message,
	displayName, senderEmail, customerPhone string, ai *models.Lead, draftText, leadStatus string) (Result, error) {

	if strings.TrimSpace(leadStatus) == "" {
		leadStatus = "new"
	}

	var leadID, threadUUID string
	var draftUUID *string
	var resultStatus string

	note := fmt.Sprintf("ingested from gmail: %s", time.Now().Format(time.RFC3339))

	var receivedAt any
	if !msg.ReceivedAt.IsZero() {
		receivedAt = msg.ReceivedAt
	}

	// Same customer often appears under a new Gmail thread after we send a standalone reply
	// (e.g. form relay → sendNewEmail "From Salsa Collective" → customer replies on that thread).
	if canonicalID, ok := lookupCanonicalLeadByEmail(ctx, tx, senderEmail); ok {
		if err := mergeDuplicateLeadsByEmail(ctx, tx, canonicalID, senderEmail); err != nil {
			return Result{}, err
		}
		err := tx.QueryRow(ctx, `
			update leads set
				gmail_thread_id = $1,
				customer_email = $2,
				customer_name  = coalesce($3, customer_name),
				customer_phone = coalesce($4, customer_phone),
				request_type   = $5,
				dance_style    = $6,
				level          = $7,
				student_count  = $8,
				requested_time = $9,
				ai_confidence  = $10,
				received_at    = coalesce(received_at, $11),
				status         = case when $12 = 'waiting_customer' then 'waiting_customer' else leads.status end,
				notes          = leads.notes || chr(10) || $13,
				updated_at     = now()
			where id = $14::uuid
			returning id::text
		`,
			msg.GmailThreadID,
			senderEmail,
			sOrNil(displayName),
			sOrNil(customerPhone),
			ptrStrOrNil(ai.RequestType),
			ptrStrOrNil(ai.DanceStyle),
			ptrStrOrNil(ai.Level),
			i32OrNil(ai.StudentCount),
			ptrStrOrNil(ai.RequestedTime),
			fPtrOrNil(ai.AIConfidence),
			receivedAt,
			leadStatus,
			note,
			canonicalID,
		).Scan(&leadID)
		if err != nil {
			return Result{}, fmt.Errorf("lead update (same customer): %w", err)
		}
		resultStatus = "updated"
		log.Printf("[ingest] attached msg=%s to existing lead=%s (customer %s, gmail thread %s)",
			msg.GmailMessageID, leadID, senderEmail, msg.GmailThreadID)
	} else {
		row := tx.QueryRow(ctx, `
			insert into leads (
				gmail_thread_id, customer_email, customer_name, customer_phone,
				request_type, dance_style, level,
				student_count, requested_time, status, priority, ai_confidence, received_at, notes
			) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			on conflict (gmail_thread_id) do update
			set
				customer_email = coalesce(excluded.customer_email, leads.customer_email),
				customer_name  = coalesce(excluded.customer_name, leads.customer_name),
				customer_phone = coalesce(excluded.customer_phone, leads.customer_phone),
				request_type   = excluded.request_type,
				dance_style    = excluded.dance_style,
				level          = excluded.level,
				student_count  = excluded.student_count,
				requested_time = excluded.requested_time,
				ai_confidence  = excluded.ai_confidence,
				received_at    = coalesce(leads.received_at, excluded.received_at),
				status         = case when excluded.status = 'waiting_customer' then 'waiting_customer' else leads.status end,
				notes          = leads.notes || chr(10) || $14,
				updated_at     = now()
			returning id
		`,
			msg.GmailThreadID,
			senderEmail,
			sOrNil(displayName),
			sOrNil(customerPhone),
			ptrStrOrNil(ai.RequestType),
			ptrStrOrNil(ai.DanceStyle),
			ptrStrOrNil(ai.Level),
			i32OrNil(ai.StudentCount),
			ptrStrOrNil(ai.RequestedTime),
			leadStatus,
			"normal",
			fPtrOrNil(ai.AIConfidence),
			receivedAt,
			note,
		)
		if err := row.Scan(&leadID); err != nil {
			return Result{}, fmt.Errorf("lead upsert: %w", err)
		}
		resultStatus = "created"
	}

	threadUUID = mustUUID()
	_, envelopeEmail := parseutil.SenderFrom(msg.From)
	_, err := tx.Exec(ctx, `
		insert into email_threads (id, lead_id, gmail_message_id, gmail_thread_id, sender_email, subject, body, received_at)
		values ($1, $2, $3, $4, $5, $6, $7, coalesce($8, now()))
	`, threadUUID, leadID, msg.GmailMessageID, msg.GmailThreadID, envelopeEmail, orEmpty(msg.Subject), orEmpty(msg.Body), receivedAt)
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
		// Reject any older pending drafts for the same lead —
		// only the most recent draft should stay pending.
		if _, err := tx.Exec(ctx, `
			update draft_responses
			set approval_status = 'rejected'
			where lead_id = $1::uuid
			  and id <> $2::uuid
			  and approval_status = 'pending'
			  and sent_at is null
		`, leadID, dID); err != nil {
			log.Printf("[ingest] warning: failed to reject older draft(s) for lead=%s: %v", leadID, err)
		}
		draftUUID = &dID
	}

	resp := Result{
		Status:   resultStatus,
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

// lookupCanonicalLeadByEmail picks one lead per customer inbox (most messages, then oldest).
func lookupCanonicalLeadByEmail(ctx context.Context, tx pgx.Tx, customerEmail string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(customerEmail))
	if email == "" {
		return "", false
	}
	var id string
	err := tx.QueryRow(ctx, `
		select l.id::text
		from leads l
		where lower(trim(l.customer_email)) = $1
		order by (select count(*)::int from email_threads et where et.lead_id = l.id) desc,
		         l.created_at asc
		limit 1
	`, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) || id == "" {
		return "", false
	}
	if err != nil {
		return "", false
	}
	return id, true
}

// mergeDuplicateLeadsByEmail moves threads/drafts off duplicate lead rows and deletes them.
func mergeDuplicateLeadsByEmail(ctx context.Context, tx pgx.Tx, canonicalID, customerEmail string) error {
	email := strings.ToLower(strings.TrimSpace(customerEmail))
	if email == "" {
		return nil
	}

	var merged int
	if err := tx.QueryRow(ctx, `
		with dupes as (
			select id from leads
			where lower(trim(customer_email)) = $1 and id <> $2::uuid
		),
		moved_threads as (
			update email_threads set lead_id = $2::uuid
			where lead_id in (select id from dupes)
			returning 1
		),
		moved_drafts as (
			update draft_responses set lead_id = $2::uuid
			where lead_id in (select id from dupes)
			returning 1
		),
		deleted as (
			delete from leads where id in (select id from dupes)
			returning 1
		)
		select (select count(*) from moved_threads) + (select count(*) from deleted)
	`, email, canonicalID).Scan(&merged); err != nil {
		return fmt.Errorf("merge duplicate leads: %w", err)
	}
	if merged > 0 {
		log.Printf("[ingest] merged duplicate lead(s) for %s into lead=%s", customerEmail, canonicalID)
	}
	return nil
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
