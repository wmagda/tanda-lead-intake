package gmail

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	gm "google.golang.org/api/gmail/v1"

	"github.com/wmagda/tanda-lead-intake/internal/ai"
	"github.com/wmagda/tanda-lead-intake/internal/db"
	"github.com/wmagda/tanda-lead-intake/internal/ingest"
	"github.com/wmagda/tanda-lead-intake/internal/parseutil"
)

// Service wraps the Gmail polling loop and reply sending.
type Service struct {
	pool      *db.Pool
	aiClient  *ai.Client
	gmailSvc  *gm.Service
	selfEmail     string
	pollInterval  time.Duration
	sendInterval  time.Duration
	stopCh        chan struct{}
	lastPoll  time.Time
	pollMu    sync.Mutex
}

// NewPollingService creates the background Gmail watcher.
func NewPollingService(pool *db.Pool, aiClient *ai.Client) *Service {
	selfEmail := strings.ToLower(strings.TrimSpace(os.Getenv("GMAIL_USER_EMAIL")))

	var gmailSvc *gm.Service
	var err error
	if os.Getenv("GMAIL_CREDENTIALS") != "" && os.Getenv("GMAIL_TOKEN") != "" {
		gmailSvc, err = NewGmailService(context.Background())
		if err != nil {
			log.Printf("[gmail] WARNING: could not init Gmail API: %v", err)
			log.Println("[gmail] polling will be disabled — use cmd/process-email for testing")
		}
	} else {
		log.Println("[gmail] GMAIL_CREDENTIALS/GMAIL_TOKEN not set — polling disabled")
	}

	pollInterval := parseutil.EnvDuration("GMAIL_POLL_INTERVAL", 2*time.Minute)
	sendInterval := parseutil.EnvDuration("SEND_POLL_INTERVAL", 30*time.Second)

	return &Service{
		pool:         pool,
		aiClient:     aiClient,
		gmailSvc:     gmailSvc,
		selfEmail:    selfEmail,
		pollInterval: pollInterval,
		sendInterval: sendInterval,
		stopCh:       make(chan struct{}),
		lastPoll:     time.Now().Add(-parseutil.InitialLookback()),
	}
}

// Start kicks off the polling goroutine.
func (s *Service) Start() {
	if s.gmailSvc == nil {
		log.Println("[gmail] polling not started (no Gmail API client)")
		return
	}
	go s.loop()
}

func (s *Service) loop() {
	// Poll immediately on start, then on interval
	s.poll()

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	log.Printf("[gmail] polling every %s for %s", s.pollInterval, s.selfEmail)

	for {
		select {
		case <-ticker.C:
			s.poll()
		case <-s.stopCh:
			log.Println("[gmail] polling stopped")
			return
		}
	}
}

func (s *Service) poll() {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()

	since := s.lastPoll
	messages, err := FetchNewMessages(s.gmailSvc, s.selfEmail, since)
	if err != nil {
		log.Printf("[gmail] fetch error: %v", err)
		return
	}

	now := time.Now()
	watermark := since
	for _, msg := range messages {
		if msg.Date.After(watermark) {
			watermark = msg.Date
		}
	}

	already, err := ingest.KnownMessageIDs(context.Background(), s.pool.Pool, messageIDs(messages))
	if err != nil {
		log.Printf("[gmail] known-msg lookup error: %v", err)
	} else if len(already) > 0 {
		log.Printf("[gmail] %d message(s) already in DB, skipping before AI", len(already))
	}
	toProcess := filterOutKnown(messages, already)

	total := len(toProcess)
	if total == 0 {
		log.Printf("[gmail] poll complete: 0 to process (%d fetched, watermark=%s)",
			len(messages), watermark.Format(time.RFC3339))
		s.advanceWatermark(watermark, now)
		return
	}

	log.Printf("[gmail] processing %d message(s) (fetched %d, since %s)...",
		total, len(messages), since.Format(time.RFC3339))
	var ingested, duplicates, skipped, errors int

	for i, msg := range toProcess {
		n := i + 1
		log.Printf("[gmail] [%d/%d] start msg=%s subject=%q from=%s",
			n, total, msg.MessageID, logTruncate(msg.Subject, 80), logTruncate(msg.From, 60))

		ctx, cancel := context.WithTimeout(context.Background(), ai.RequestTimeout()+30*time.Second)

		result, err := ingest.Process(ctx, s.pool.Pool, s.aiClient, ingest.Message{
			GmailThreadID:  msg.ThreadID,
			GmailMessageID: msg.MessageID,
			From:           msg.From,
			Subject:        msg.Subject,
			Body:           msg.Body,
			ReceivedAt:     msg.Date,
		})
		cancel()

		if err != nil {
			errors++
			log.Printf("[gmail] [%d/%d] ingest error msg=%s: %v", n, total, msg.MessageID, err)
			continue
		}

		switch result.Status {
		case "duplicate":
			duplicates++
			log.Printf("[gmail] [%d/%d] duplicate msg=%s lead=%s", n, total, msg.MessageID, result.LeadID)
			continue
		case "skipped":
			skipped++
			log.Printf("[gmail] [%d/%d] skipped msg=%s: %s", n, total, msg.MessageID, result.SkipReason)
			continue
		}

		ingested++
		log.Printf("[gmail] [%d/%d] ingested lead=%s intent=%q confidence=%.2f draft=%v",
			n, total, result.LeadID, result.Intent, result.Confidence, result.DraftID != nil)
	}

	s.advanceWatermark(watermark, now)
	log.Printf("[gmail] poll complete: %d processed, %d ingested, %d duplicate, %d skipped, %d error (next after %s)",
		total, ingested, duplicates, skipped, errors, s.lastPoll.Format(time.RFC3339))
}

func (s *Service) advanceWatermark(watermark, now time.Time) {
	next := now
	if watermark.After(next) {
		next = watermark
	}
	// Gmail after: is exclusive; bump 1s so we don't re-fetch the last message.
	s.lastPoll = next.Add(time.Second)
}

func messageIDs(msgs []FetchedMessage) []string {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.MessageID
	}
	return ids
}

func filterOutKnown(msgs []FetchedMessage, known map[string]bool) []FetchedMessage {
	if len(known) == 0 {
		return msgs
	}
	out := make([]FetchedMessage, 0, len(msgs))
	for _, m := range msgs {
		if !known[m.MessageID] {
			out = append(out, m)
		}
	}
	return out
}

func logTruncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// Stop shuts down the polling goroutine.
func (s *Service) Stop(ctx context.Context) {
	close(s.stopCh)
}

// StartDraftSender kicks off a background loop that polls for approved drafts and sends them.
func (s *Service) StartDraftSender() {
	if s.gmailSvc == nil {
		log.Println("[send] draft sender not started (no Gmail API client)")
		return
	}
	go s.sendLoop()
}

func (s *Service) sendLoop() {
	ticker := time.NewTicker(s.sendInterval)
	defer ticker.Stop()
	log.Printf("[send] sender started (drafts + task notifications, polling every %s)", s.sendInterval)

	for {
		select {
		case <-ticker.C:
			s.pollApprovedDrafts()
			s.pollTaskNotifications()
		case <-s.stopCh:
			log.Println("[send] sender stopped")
			return
		}
	}
}

func (s *Service) pollApprovedDrafts() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rows, err := s.pool.Pool.Query(ctx, `
		select dr.id, dr.lead_id, dr.draft_text,
		       l.gmail_thread_id, l.customer_email, l.customer_name
		from draft_responses dr
		join leads l on l.id = dr.lead_id
		where dr.approval_status = 'approved' and dr.sent_at is null
		order by dr.created_at
		limit 10
	`)
	if err != nil {
		log.Printf("[send] query error: %v", err)
		return
	}
	defer rows.Close()

	type pending struct {
		DraftID       string
		LeadID        string
		DraftText     string
		GmailThreadID string
		CustomerEmail string
		CustomerName  string
	}

	var drafts []pending
	for rows.Next() {
		var d pending
		if err := rows.Scan(&d.DraftID, &d.LeadID, &d.DraftText,
			&d.GmailThreadID, &d.CustomerEmail, &d.CustomerName); err != nil {
			log.Printf("[send] scan error: %v", err)
			return
		}
		drafts = append(drafts, d)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[send] rows error: %v", err)
		return
	}

	if len(drafts) == 0 {
		return
	}

	log.Printf("[send] found %d approved draft(s) to send", len(drafts))

	for _, d := range drafts {
		if strings.TrimSpace(d.CustomerEmail) == "" {
			// For Google Voice notifications, reply in-thread — Google Voice
			// forwards the reply as SMS to the original caller.
			if voiceSender, ok := isGoogleVoiceThread(d.LeadID, s.pool); ok {
				sendErr := s.sendReplyInThread(ctx, d.GmailThreadID, voiceSender, d.DraftText)
				if sendErr != nil {
					log.Printf("[send] FAILED voice-sms draft=%s lead=%s to=%s: %v", d.DraftID, d.LeadID, voiceSender, sendErr)
				} else {
					_, err := s.pool.Pool.Exec(ctx,
						`update draft_responses set sent_at = now() where id = $1`, d.DraftID)
					if err != nil {
						log.Printf("[send] sent voice-sms but failed to mark sent_at draft=%s: %v", d.DraftID, err)
					} else {
						log.Printf("[send] sent voice-sms draft=%s lead=%s to=%s (sent as SMS via Google Voice)", d.DraftID, d.LeadID, voiceSender)
					}
				}
			} else {
				log.Printf("[send] skip draft=%s lead=%s: no customer_email", d.DraftID, d.LeadID)
			}
			continue
		}

		var sendErr error
		if isFormRelayThread(d.GmailThreadID, d.LeadID, s.pool) {
			sendErr = s.sendNewEmail(ctx, d.CustomerEmail, d.CustomerName, d.DraftText)
		} else {
			sendErr = s.sendReplyInThread(ctx, d.GmailThreadID, d.CustomerEmail, d.DraftText)
		}

		if sendErr != nil {
			log.Printf("[send] FAILED draft=%s lead=%s to=%s: %v", d.DraftID, d.LeadID, d.CustomerEmail, sendErr)
			continue
		}

		_, err := s.pool.Pool.Exec(ctx,
			`update draft_responses set sent_at = now() where id = $1`, d.DraftID)
		if err != nil {
			log.Printf("[send] sent but failed to mark sent_at draft=%s: %v", d.DraftID, err)
		} else {
			log.Printf("[send] sent draft=%s lead=%s to=%s", d.DraftID, d.LeadID, d.CustomerEmail)
		}
	}
}

func (s *Service) pollTaskNotifications() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rows, err := s.pool.Pool.Query(ctx, `
		select t.id, t.assigned_to, t.assignee_email, t.task_type, t.notes, t.due_date,
		       l.customer_name, l.customer_email, l.request_type
		from tasks t
		join leads l on l.id = t.lead_id
		where t.assignee_email is not null
		  and t.notified_at is null
		  and t.status = 'open'
		order by t.created_at
		limit 10
	`)
	if err != nil {
		log.Printf("[notify] query error: %v", err)
		return
	}
	defer rows.Close()

	type taskNotif struct {
		TaskID        string
		AssignedTo    string
		AssigneeEmail string
		TaskType      string
		Notes         *string
		DueDate       *time.Time
		CustomerName  *string
		CustomerEmail *string
		RequestType   *string
	}

	var tasks []taskNotif
	for rows.Next() {
		var t taskNotif
		if err := rows.Scan(&t.TaskID, &t.AssignedTo, &t.AssigneeEmail,
			&t.TaskType, &t.Notes, &t.DueDate,
			&t.CustomerName, &t.CustomerEmail, &t.RequestType); err != nil {
			log.Printf("[notify] scan error: %v", err)
			return
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[notify] rows error: %v", err)
		return
	}

	if len(tasks) == 0 {
		return
	}

	log.Printf("[notify] found %d task notification(s) to send", len(tasks))

	for _, t := range tasks {
		body := buildTaskNotificationBody(t.AssignedTo, t.TaskType, t.Notes, t.DueDate,
			t.CustomerName, t.CustomerEmail, t.RequestType)

		sendErr := s.sendNewEmail(ctx, t.AssigneeEmail, "", body)
		if sendErr != nil {
			log.Printf("[notify] FAILED task=%s to=%s: %v", t.TaskID, t.AssigneeEmail, sendErr)
			continue
		}

		_, err := s.pool.Pool.Exec(ctx,
			`update tasks set notified_at = now() where id = $1`, t.TaskID)
		if err != nil {
			log.Printf("[notify] sent but failed to mark notified_at task=%s: %v", t.TaskID, err)
		} else {
			log.Printf("[notify] sent task=%s to=%s", t.TaskID, t.AssigneeEmail)
		}
	}
}

func buildTaskNotificationBody(assignedTo, taskType string, notes *string, dueDate *time.Time,
	customerName, customerEmail, requestType *string) string {

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Hi %s,\n\n", assignedTo))
	b.WriteString("You have been assigned a new task from Salsa Collective.\n\n")

	b.WriteString(fmt.Sprintf("Task type: %s\n", taskType))
	if requestType != nil && *requestType != "" {
		b.WriteString(fmt.Sprintf("Request type: %s\n", *requestType))
	}
	if customerName != nil && *customerName != "" {
		b.WriteString(fmt.Sprintf("Customer: %s", *customerName))
		if customerEmail != nil && *customerEmail != "" {
			b.WriteString(fmt.Sprintf(" (%s)", *customerEmail))
		}
		b.WriteString("\n")
	}
	if dueDate != nil {
		b.WriteString(fmt.Sprintf("Due: %s\n", dueDate.Format("Monday, January 2, 2006")))
	}
	if notes != nil && *notes != "" {
		b.WriteString(fmt.Sprintf("\nNotes: %s\n", *notes))
	}

	b.WriteString("\nPlease check the admin inbox for full details.\n")
	b.WriteString("\n— Salsa Collective")
	return b.String()
}

// isFormRelayThread checks if the original email was from a form relay sender.
func isFormRelayThread(gmailThreadID, leadID string, pool *db.Pool) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var senderEmail string
	err := pool.Pool.QueryRow(ctx,
		`select sender_email from email_threads where lead_id = $1::uuid order by received_at limit 1`,
		leadID,
	).Scan(&senderEmail)
	if err != nil {
		return false
	}
	return parseutil.IsFormRelay(senderEmail)
}

// isGoogleVoiceThread checks if the original email was a Google Voice notification.
// Returns the original sender email (e.g. [VOICE-NOREPLY]) and true, or \"\" and false.
func isGoogleVoiceThread(leadID string, pool *db.Pool) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var senderEmail string
	err := pool.Pool.QueryRow(ctx,
		`select sender_email from email_threads where lead_id = $1::uuid order by received_at limit 1`,
		leadID,
	).Scan(&senderEmail)
	if err != nil {
		return "", false
	}
	if parseutil.IsGoogleVoiceRelay(senderEmail, "", "") {
		return senderEmail, true
	}
	return "", false
}

func (s *Service) sendReplyInThread(ctx context.Context, threadID, toEmail, body string) error {
	raw := buildMIME(s.selfEmail, toEmail, "Re: Your inquiry", body, threadID)
	msg := &gm.Message{
		ThreadId: threadID,
		Raw:      raw,
	}
	_, err := s.gmailSvc.Users.Messages.Send("me", msg).Context(ctx).Do()
	return err
}

func (s *Service) sendNewEmail(ctx context.Context, toEmail, toName, body string) error {
	to := toEmail
	if toName != "" {
		to = fmt.Sprintf("%s <%s>", toName, toEmail)
	}
	raw := buildMIME(s.selfEmail, to, "From Salsa Collective", body, "")
	msg := &gm.Message{Raw: raw}
	_, err := s.gmailSvc.Users.Messages.Send("me", msg).Context(ctx).Do()
	return err
}

// buildMIME constructs a base64url-encoded RFC 2822 message for the Gmail API.
func buildMIME(from, to, subject, body, inReplyToThreadID string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return base64.URLEncoding.EncodeToString([]byte(b.String()))
}

// SenderFrom re-exports parseutil.SenderFrom for backward compat.
func SenderFrom(fromHeader string) (name, email string) {
	return parseutil.SenderFrom(fromHeader)
}
