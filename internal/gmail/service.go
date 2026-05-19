package gmail

import (
	"context"
	"log"
	"time"

	"github.com/wmagda/tanda-lead-intake/internal/db"
)

// Service wraps the Gmail polling loop and reply sending.
// Production: uses real google.golang.org/api/gmail/v1 client.
type Service struct {
	pool      *db.Pool
	aiClient  interface{} // *ai.Client
	interval  time.Duration
	stopCh    chan struct{}
}

// NewPollingService creates the background Gmail watcher.
func NewPollingService(pool *db.Pool, aiClient interface{}) (*Service, error) {
	return &Service{
		pool:     pool,
		aiClient: aiClient,
		interval: 2 * time.Minute,
		stopCh:   make(chan struct{}),
	}, nil
}

// Start kicks off the polling goroutine.
func (s *Service) Start() {
	go s.loop()
}

func (s *Service) loop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	log.Println("[gmail] polling started — mode is set to WATCH push notifications in prod")
	for {
		select {
		case <-ticker.C:
			// TODO: call Gmail API, enqueue jobs for /api/email/process
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
