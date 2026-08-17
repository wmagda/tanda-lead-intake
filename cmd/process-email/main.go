// One-shot CLI to test email ingest without Gmail polling.
//
// First message (creates lead + draft):
//
//	go run ./cmd/process-email \
//	  -thread thread-test-001 -message msg-test-001 \
//	  -from 'Jane <jane@example.com>' -subject 'Lesson?' \
//	  -body 'My partner and I want beginner salsa lessons.'
//
// Follow-up (loads prior context — watch prior_msgs= in logs, or use -show-context):
//
//	go run ./cmd/process-email -show-context \
//	  -thread thread-test-001 -message msg-test-002 \
//	  -from 'Jane <jane@example.com>' -subject 'Re: Lesson?' \
//	  -body 'Tuesday at 6pm works. What is the couple rate?'
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/wmagda/tanda-lead-intake/internal/ai"
	"github.com/wmagda/tanda-lead-intake/internal/db"
	"github.com/wmagda/tanda-lead-intake/internal/ingest"
	"github.com/wmagda/tanda-lead-intake/internal/parseutil"
)

func main() {
	_ = godotenv.Load()
	ai.LoadSystemPrompt() // loads external prompts/intake-system.prompt if present

	threadID := flag.String("thread", "", "Gmail thread ID")
	messageID := flag.String("message", "", "Gmail message ID")
	from := flag.String("from", "", "From header")
	subject := flag.String("subject", "", "Subject")
	body := flag.String("body", "", "Email body")
	showContext := flag.Bool("show-context", false, "Print prior thread context and exit (no AI, no DB writes)")
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

	msg := ingest.Message{
		GmailThreadID:  *threadID,
		GmailMessageID: *messageID,
		From:           *from,
		Subject:        *subject,
		Body:           *body,
		ReceivedAt:     time.Now(),
	}

	if *showContext {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		prior := ingest.PreviewConversationContext(ctx, pool.Pool, msg)
		fmt.Printf("prior messages: %d\n\n", len(prior))
		for i, m := range prior {
			fmt.Printf("[%d] role=%s from=%q subject=%q\n", i+1, m.Role, m.From, m.Subject)
			fmt.Println("---")
			fmt.Println(m.Body)
			fmt.Println("---")
		}

		formRelay := parseutil.IsFormRelay(*from)
		voiceRelay := parseutil.IsGoogleVoiceRelay(*from, *subject, *body)
		preview := ai.UserPrompt(*from, *subject, *body, formRelay, voiceRelay, prior)
		fmt.Println("=== LLM user prompt preview (first 2000 chars) ===")
		if len(preview) > 2000 {
			fmt.Println(preview[:2000])
			fmt.Println("\n…[truncated for display]")
		} else {
			fmt.Println(preview)
		}
		return
	}

	aiClient := ai.NewClientFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), ai.RequestTimeout()+30*time.Second)
	defer cancel()

	result, err := ingest.Process(ctx, pool.Pool, aiClient, msg)
	if err != nil {
		log.Fatalf("ingest: %v", err)
	}

	log.Printf("status=%s lead_id=%s thread_id=%s draft_id=%v intent=%q confidence=%.2f",
		result.Status, result.LeadID, result.ThreadID, result.DraftID, result.Intent, result.Confidence)
}
