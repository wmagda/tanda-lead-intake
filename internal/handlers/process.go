package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wmagda/tanda-lead-intake/internal/email"
	"github.com/wmagda/tanda-lead-intake/internal/gmail"
	"github.com/wmagda/tanda-lead-intake/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProcessRequest is the body accepted by POST /api/email/process.
type ProcessRequest struct {
	GmailThreadID  string `json:"gmail_thread_id" binding:"required"`
	GmailMessageID string `json:"gmail_message_id" binding:"required"`
	From           string `json:"from" binding:"required"`
	Subject        string `json:"subject"`
	Body           string `json:"body" binding:"required"`
	ReceivedAt     string `json:"received_at"`
}

// ProcessResponse is the JSON returned.
type ProcessResponse struct {
	LeadID     string  `json:"lead_id"`
	ThreadID   string  `json:"thread_id"`
	DraftID    *string `json:"draft_id,omitempty"`
	Intent     string  `json:"intent,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// NewProcessHandler wires AI + DB + Gmail ingest pipeline.
func NewProcessHandler(pool *pgxpool.Pool, aiClient *email.Client, _ interface{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ProcessRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
			return
		}

		// ── dedupe by gmail_message_id ──────────────────────────────────
		existingThreadID, existingLeadID, err := lookupByGmailMsgID(pool, req.GmailMessageID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": "lookup: " + err.Error()})
			return
		}
		if existingThreadID != "" {
			c.JSON(http.StatusOK, gin.H{
				"status":    "duplicate",
				"lead_id":   existingLeadID,
				"thread_id": existingThreadID,
			})
			return
		}

		// ── parse sender ─────────────────────────────────────────────────
		displayName, senderEmail := gmail.SenderFrom(req.From)

		// ── AI parse ────────────────────────────────────────────────────
		parseCtx, parseCancel := context.WithTimeout(context.Background(), 90*time.Second)
		aiLead, draftText, parseErr := aiClient.ParseExtracted(parseCtx, req.From, req.Subject, req.Body)
		parseCancel()

		if parseErr != nil {
			// AI failed — log but continue so we save the thread
			c.Header("X-AI-Error", parseErr.Error())
			aiLead = &models.Lead{} // zero-valued lead — safe upsert
		}

		// ── transaction ─────────────────────────────────────────────────
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()

		tx, err := pool.Begin(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": "tx begin: " + err.Error()})
			return
		}

		resp, err := ingestInTx(ctx, tx, req, displayName, senderEmail, aiLead, draftText)
		if err != nil {
			_ = tx.Rollback(ctx)
			c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
			return
		}

		if err := tx.Commit(ctx); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": "tx commit: " + err.Error()})
			return
		}
		c.JSON(http.StatusCreated, resp)
	}
}

// ingestInTx does all writes inside a single transaction.
func ingestInTx(ctx context.Context, tx pgx.Tx, req ProcessRequest,
	displayName, senderEmail string, ai *models.Lead, draftText string) (ProcessResponse, error) {

	var leadID, threadUUID string
	var draftUUID *string

	// ── INSERT / UPDATE lead ─────────────────────────────────────────
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
		req.GmailThreadID,
		senderEmail,
		sOrNil(displayName),
		sOrNil(ai.RequestType),
		sOrNil(ai.DanceStyle),
		sOrNil(ai.Level),
		i32OrNil(ai.StudentCount),
		sOrNil(ai.RequestedTime),
		"new",
		"normal",
		fPtrOrNil(ai.AIConfidence),
		note,
	)
	if err := row.Scan(&leadID); err != nil {
		return ProcessResponse{}, fmt.Errorf("lead upsert: %w", err)
	}

	// ── INSERT email thread ──────────────────────────────────────────
	threadUUID = mustUUID()
	_, err := tx.Exec(ctx, `
		insert into email_threads (id, lead_id, gmail_message_id, gmail_thread_id, sender_email, subject, body)
		values ($1, $2, $3, $4, $5, $6, $7)
	`, threadUUID, leadID, req.GmailMessageID, req.GmailThreadID, senderEmail, orEmpty(req.Subject), orEmpty(req.Body))
	if err != nil {
		return ProcessResponse{}, fmt.Errorf("thread insert: %w", err)
	}

	// ── INSERT draft (if AI produced one) ────────────────────────────
	if strings.TrimSpace(draftText) != "" {
		dID := mustUUID()
		_, err = tx.Exec(ctx, `
			insert into draft_responses (id, lead_id, draft_text, approval_status)
			values ($1, $2, $3, 'pending')
		`, dID, leadID, draftText)
		if err != nil {
			return ProcessResponse{}, fmt.Errorf("draft insert: %w", err)
		}
		draftUUID = &dID
	}

	resp := ProcessResponse{
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

// ── helpers ───────────────────────────────────────────────────────

func lookupByGmailMsgID(pool *pgxpool.Pool, msgID string) (threadID, leadID string, _ error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := pool.QueryRow(ctx, `select gmail_thread_id, lead_id from email_threads where gmail_message_id=$1 limit 1`, msgID).Scan(&threadID, &leadID)
	if err != nil {
		return "", "", nil // pgx.ErrNoRows → not found
	}
	return threadID, leadID, nil
}

func mustUUID() string {
	id, err := uuid.NewRandom()
	if err != nil {
		// extremely unlikely
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
