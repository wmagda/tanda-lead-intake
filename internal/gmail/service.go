package gmail

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	gm "google.golang.org/api/gmail/v1"

	"github.com/wmagda/tanda-lead-intake/internal/db"
	"github.com/wmagda/tanda-lead-intake/internal/ai"
	"github.com/wmagda/tanda-lead-intake/internal/ingest"
	"github.com/wmagda/tanda-lead-intake/internal/parseutil"
)

// Service wraps the Gmail polling loop and reply sending.
type Service struct {
	pool      *db.Pool
	aiClient  *ai.Client
	gmailSvc  *gm.Service
	selfEmail string
	interval  time.Duration
	stopCh    chan struct{}
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

	return &Service{
		pool:      pool,
		aiClient:  aiClient,
		gmailSvc:  gmailSvc,
		selfEmail: selfEmail,
		interval:  2 * time.Minute,
		stopCh:    make(chan struct{}),
		lastPoll:  time.Now().Add(-parseutil.InitialLookback()),
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

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	log.Printf("[gmail] polling every %s for %s", s.interval, s.selfEmail)

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

// SendReply sends a reply to a Gmail thread on behalf of the studio.
// threadID is the Gmail thread ID (not message ID).
// draftText is the full MIME body of the reply.
func (s *Service) SendReply(ctx context.Context, threadID, draftText string) error {
	_ = fmt.Sprintf("stub: send reply to thread %s — body %d chars", threadID, len(draftText))
	// TODO: implement via gmail API when Lovable triggers approve
	log.Printf("[gmail] reply stub: thread=%s (%d chars)", threadID, len(draftText))
	return nil
}

// SenderFrom re-exports parseutil.SenderFrom for backward compat.
func SenderFrom(fromHeader string) (name, email string) {
	return parseutil.SenderFrom(fromHeader)
}
