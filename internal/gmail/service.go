package gmail

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/wmagda/tanda-lead-intake/internal/db"
	"github.com/wmagda/tanda-lead-intake/internal/email"
	"github.com/wmagda/tanda-lead-intake/internal/models"
)

// Service wraps the Gmail polling loop and reply sending.
type Service struct {
	pool     *db.Pool
	aiClient *email.Client
	interval time.Duration
	stopCh   chan struct{}
}

// NewPollingService creates the background Gmail watcher.
func NewPollingService(pool *db.Pool, aiClient *email.Client) *Service {
	return &Service{
		pool:     pool,
		aiClient: aiClient,
		interval: 2 * time.Minute,
		stopCh:   make(chan struct{}),
	}
}

// Start kicks off the polling goroutine.
func (s *Service) Start() {
	go s.loop()
}

func (s *Service) loop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	log.Println("[gmail] polling started — WATCH push notifications in prod")
	for {
		select {
		case <-ticker.C:
			// TODO: implement Gmail history.delta list / recent threads fetch
		case <-s.stopCh:
			log.Println("[gmail] polling stopped")
			return
		}
	}
}

// Stop shuts down the polling goroutine.
func (s *Service) Stop(ctx context.Context) {
	close(s.stopCh)
}

// ── approval / sending helpers ──────────────────────────────────────

// SendReply sends a reply to a Gmail thread on behalf of the studio.
// threadID is the Gmail thread ID (not message ID).
// draftText is the full MIME body of the reply.
func (s *Service) SendReply(ctx context.Context, threadID, draftText string) error {
	_ = fmt.Sprintf("stub: send reply to thread %s — body %d chars", threadID, len(draftText))
	// TODO: implement via google.golang.org/api/gmail/v1
	log.Printf("[gmail] reply stub: thread=%s (%d chars)", threadID, len(draftText))
	return nil
}

// ── address / name extraction ──────────────────────────────────────

// SenderFrom extracts a display name and email address from a "From:" header value.
//
// Input:  "John Doe <john@example.com>" or "john@example.com"
// Output: name="John Doe", email="john@example.com"
func SenderFrom(fromHeader string) (name, email string) {
	fromHeader = strings.TrimSpace(fromHeader)
	if fromHeader == "" {
		return "", ""
	}
	if i := strings.LastIndex(fromHeader, "<"); i != -1 {
		// "Name <addr@host>"
		namePart := strings.TrimSpace(fromHeader[:i])
		addrPart := fromHeader[i+1:]
		if j := strings.Index(addrPart, ">"); j != -1 {
			addrPart = addrPart[:j]
		}
		return namePart, strings.ToLower(strings.TrimSpace(addrPart))
	}
	// plain address
	return "", strings.ToLower(strings.TrimSpace(fromHeader))
}
