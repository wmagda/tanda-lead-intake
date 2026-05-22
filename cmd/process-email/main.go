// One-shot CLI to test email ingest without Gmail polling.
// Example:
//
//	go run ./cmd/process-email \
//	  -thread thread-test-001 -message msg-test-001 \
//	  -from 'Jane <jane@example.com>' -subject 'Lesson?' -body 'Tuesday please'
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/wmagda/tanda-lead-intake/internal/db"
	"github.com/wmagda/tanda-lead-intake/internal/email"
	"github.com/wmagda/tanda-lead-intake/internal/ingest"
)

func main() {
	_ = godotenv.Load()

	threadID := flag.String("thread", "", "Gmail thread ID")
	messageID := flag.String("message", "", "Gmail message ID")
	from := flag.String("from", "", "From header")
	subject := flag.String("subject", "", "Subject")
	body := flag.String("body", "", "Email body")
	flag.Parse()

	if *threadID == "" || *messageID == "" || *from == "" || *body == "" {
		flag.Usage()
		os.Exit(2)
	}

	pool, err := db.NewPool()
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	ai := email.NewAIClientFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), email.RequestTimeout()+30*time.Second)
	defer cancel()

	result, err := ingest.Process(ctx, pool.Pool, ai, ingest.Message{
		GmailThreadID:  *threadID,
		GmailMessageID: *messageID,
		From:           *from,
		Subject:        *subject,
		Body:           *body,
	})
	if err != nil {
		log.Fatalf("ingest: %v", err)
	}

	log.Printf("status=%s lead_id=%s thread_id=%s draft_id=%v intent=%q confidence=%.2f",
		result.Status, result.LeadID, result.ThreadID, result.DraftID, result.Intent, result.Confidence)
}
