package ingest

import (
	"context"
	"errors"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wmagda/tanda-lead-intake/internal/ai"
	"github.com/wmagda/tanda-lead-intake/internal/parseutil"
)

type timelineItem struct {
	at      time.Time
	role    string
	from    string
	subject string
	body    string
}

// PreviewConversationContext loads prior messages for a lead without side effects (for debugging).
func PreviewConversationContext(ctx context.Context, pool *pgxpool.Pool, msg Message) []ai.ConversationMessage {
	formRelay := parseutil.IsFormRelay(msg.From)
	return loadConversationContext(ctx, pool, msg, formRelay)
}

// loadConversationContext returns prior customer/studio messages for an existing lead,
// so the LLM can draft a follow-up instead of treating every inbound as a first contact.
func loadConversationContext(ctx context.Context, pool *pgxpool.Pool, msg Message, formRelay bool) []ai.ConversationMessage {
	leadID, ok := lookupLeadIDForContext(ctx, pool, msg, formRelay)
	if !ok {
		return nil
	}

	items, err := loadLeadTimeline(ctx, pool, leadID, msg.GmailMessageID)
	if err != nil {
		log.Printf("[ingest] thread context lead=%s: %v", leadID, err)
		return nil
	}
	if len(items) == 0 {
		return nil
	}

	out := make([]ai.ConversationMessage, 0, len(items))
	for _, it := range items {
		out = append(out, ai.ConversationMessage{
			Role:    it.role,
			From:    it.from,
			Subject: it.subject,
			Body:    it.body,
		})
	}
	log.Printf("[ingest] thread context lead=%s: %d prior message(s) for msg=%s",
		leadID, len(out), msg.GmailMessageID)
	return out
}

func lookupLeadIDForContext(ctx context.Context, pool *pgxpool.Pool, msg Message, formRelay bool) (string, bool) {
	var leadID string

	err := pool.QueryRow(ctx, `
		select lead_id::text
		from email_threads
		where gmail_thread_id = $1 and lead_id is not null
		order by received_at desc
		limit 1
	`, msg.GmailThreadID).Scan(&leadID)
	if err == nil && leadID != "" {
		return leadID, true
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[ingest] thread context lookup by thread: %v", err)
	}

	err = pool.QueryRow(ctx,
		`select id::text from leads where gmail_thread_id = $1 limit 1`,
		msg.GmailThreadID,
	).Scan(&leadID)
	if err == nil && leadID != "" {
		return leadID, true
	}

	if formRelay {
		return "", false
	}
	_, envelopeEmail := parseutil.SenderFrom(msg.From)
	return lookupCanonicalLeadByEmailPool(ctx, pool, envelopeEmail)
}

func lookupCanonicalLeadByEmailPool(ctx context.Context, pool *pgxpool.Pool, customerEmail string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(customerEmail))
	if email == "" {
		return "", false
	}
	var id string
	err := pool.QueryRow(ctx, `
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

func loadLeadTimeline(ctx context.Context, pool *pgxpool.Pool, leadID, excludeMsgID string) ([]timelineItem, error) {
	var items []timelineItem

	rows, err := pool.Query(ctx, `
		select sender_email, subject, body, received_at
		from email_threads
		where lead_id = $1::uuid
		  and gmail_message_id is distinct from $2
		order by received_at asc
	`, leadID, excludeMsgID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sender, subject, body string
		var at time.Time
		if err := rows.Scan(&sender, &subject, &body, &at); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, timelineItem{
			at:      at,
			role:    "customer",
			from:    sender,
			subject: subject,
			body:    body,
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	draftRows, err := pool.Query(ctx, `
		select draft_text, sent_at
		from draft_responses
		where lead_id = $1::uuid and sent_at is not null
		order by sent_at asc
	`, leadID)
	if err != nil {
		return nil, err
	}
	for draftRows.Next() {
		var body string
		var at time.Time
		if err := draftRows.Scan(&body, &at); err != nil {
			draftRows.Close()
			return nil, err
		}
		items = append(items, timelineItem{
			at:   at,
			role: "studio",
			body: body,
		})
	}
	if err := draftRows.Err(); err != nil {
		draftRows.Close()
		return nil, err
	}
	draftRows.Close()

	sort.Slice(items, func(i, j int) bool {
		return items[i].at.Before(items[j].at)
	})
	return items, nil
}
