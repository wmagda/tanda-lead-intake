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
	"encoding/json"
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
	showContext := flag.Bool("show-context", false, "Print prior thread context + upcoming events and exit (no AI, no DB writes)")
	showCalendar := flag.Bool("show-calendar", false, "Print the upcoming-events context block and exit (no AI, no DB writes)")
	dryRun := flag.Bool("dry-run", false, "Run the full AI parse with current context but do NOT write to DB")
	flag.Parse()

	if *showCalendar {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pool, err := db.NewPool()
		if err != nil {
			log.Fatalf("db: %v", err)
		}
		defer pool.Close()
		block := ingest.CalendarContext(ctx, pool.Pool)
		if block == "" {
			fmt.Println("(no upcoming events in lookahead window)")
		} else {
			fmt.Println(block)
		}
		return
	}

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
		calendar := ingest.CalendarContext(ctx, pool.Pool)
		fmt.Println("=== SYSTEM PROMPT + schedule context (first 3000 chars) ===")
		sys := ai.SystemPrompt + ai.CalendarSystemSection(calendar)
		if len(sys) > 3000 {
			fmt.Println(sys[:3000])
			fmt.Println("\n…[truncated for display]")
		} else {
			fmt.Println(sys)
		}
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

	if *dryRun {
		ctx, cancel := context.WithTimeout(context.Background(), ai.RequestTimeout()+30*time.Second)
		defer cancel()

		prior := ingest.PreviewConversationContext(ctx, pool.Pool, msg)
		formRelay := parseutil.IsFormRelay(*from)
		voiceRelay := parseutil.IsGoogleVoiceRelay(*from, *subject, *body)
		calendar := ingest.CalendarContext(ctx, pool.Pool)

		fmt.Println("=== DRY RUN: no DB writes ===")
		fmt.Printf("prior messages: %d\ncalendar context: %d chars\n\n", len(prior), len(calendar))
		fmt.Println("=== SYSTEM PROMPT + schedule context (first 4000 chars) ===")
		sys := ai.SystemPrompt + ai.CalendarSystemSection(calendar)
		if len(sys) > 4000 {
			fmt.Println(sys[:4000])
			fmt.Println("\n…[truncated for display]")
		} else {
			fmt.Println(sys)
		}
		fmt.Println("\n=== LLM user prompt (first 4000 chars) ===")
		preview := ai.UserPrompt(*from, *subject, *body, formRelay, voiceRelay, prior)
		if len(preview) > 4000 {
			fmt.Println(preview[:4000])
			fmt.Println("\n…[truncated for display]")
		} else {
			fmt.Println(preview)
		}

		aiClient := ai.NewClientFromEnv()
		pr, err := aiClient.ParseExtracted(ctx, msg.From, msg.Subject, msg.Body, formRelay, voiceRelay, prior, calendar)
		if err != nil {
			log.Fatalf("dry-run AI parse: %v", err)
		}

		fmt.Println("\n=== LLM PARSE RESULT ===")
		b, _ := json.MarshalIndent(struct {
			IsLead        *bool    `json:"is_lead"`
			CustomerEmail *string  `json:"customer_email"`
			CustomerName  *string  `json:"customer_name"`
			CustomerPhone *string  `json:"customer_phone"`
			Intent        *string  `json:"intent"`
			DanceStyle    *string  `json:"dance_style"`
			Level         *string  `json:"level"`
			StudentCount  *int     `json:"student_count"`
			RequestedTime *string  `json:"requested_time"`
			NeedsPricing  *bool    `json:"needs_pricing"`
			Confidence    *float64 `json:"ai_confidence"`
			Draft         string   `json:"draft"`
		}{pr.IsLead, pr.CustomerEmail, pr.CustomerName, pr.CustomerPhone, pr.Intent,
			pr.DanceStyle, pr.Level, pr.StudentCount, pr.RequestedTime, pr.NeedsPricing,
			pr.Confidence, pr.Draft}, "", "  ")
		fmt.Println(string(b))
		fmt.Println("\n=== DRAFT ===")
		fmt.Println(pr.Draft)
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
