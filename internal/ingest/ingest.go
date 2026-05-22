package ingest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wmagda/tanda-lead-intake/internal/email"
	"github.com/wmagda/tanda-lead-intake/internal/gmail"
	"github.com/wmagda/tanda-lead-intake/internal/models"
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
	Status     string // "created" or "duplicate"
	LeadID     string
	ThreadID   string
	DraftID    *string
	Intent     string
	Confidence float64
}

// Process runs AI parse + DB writes for one inbound message.
// Safe to call from the Gmail worker; does not send email.
func Process(ctx context.Context, pool *pgxpool.Pool, aiClient *email.Client, msg Message) (Result, error) {
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
		return Result{
			Status:   "duplicate",
			LeadID:   existingLeadID,
			ThreadID: existingThreadID,
		}, nil
	}

	displayName, senderEmail := gmail.SenderFrom(msg.From)

	parseCtx, parseCancel := context.WithTimeout(ctx, email.RequestTimeout())
	aiLead, draftText, parseErr := aiClient.ParseExtracted(parseCtx, msg.From, msg.Subject, msg.Body)
	parseCancel()

	if parseErr != nil {
		// AI failed — log but continue so we save the thread
		fmt.Printf("[ingest] AI parse failed (continuing): %v\n", parseErr)
		aiLead = &models.Lead{}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("tx begin: %w", err)
	}

	resp, err := ingestInTx(ctx, tx, msg, displayName, senderEmail, aiLead, draftText)
	if err != nil {
		_ = tx.Rollback(ctx)
		return Result{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("tx commit: %w", err)
	}
	resp.Status = "created"
	return resp, nil
}

func ingestInTx(ctx context.Context, tx pgx.Tx, msg Message,
	displayName, senderEmail string, ai *models.Lead, draftText string) (Result, error) {

	var leadID, threadUUID string
	var draftUUID *string

	note := fmt.Sprintf("ingested from gmail: %s", time.Now().Format(time.RFC3339))

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
	_, err := tx.Exec(ctx, `
		insert into email_threads (id, lead_id, gmail_message_id, gmail_thread_id, sender_email, subject, body)
		values ($1, $2, $3, $4, $5, $6, $7)
	`, threadUUID, leadID, msg.GmailMessageID, msg.GmailThreadID, senderEmail, orEmpty(msg.Subject), orEmpty(msg.Body))
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
