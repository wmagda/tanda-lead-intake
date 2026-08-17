// Worker ingests Gmail, runs AI parsing, and writes to Postgres.
// Admin UI (Lovable) talks to Supabase directly — not this process.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/wmagda/tanda-lead-intake/internal/db"
	"github.com/wmagda/tanda-lead-intake/internal/ai"
	"github.com/wmagda/tanda-lead-intake/internal/gmail"
)

func main() {
	_ = godotenv.Load()
	ai.LoadSystemPrompt() // loads external prompts/intake-system.prompt if present

	pool, err := db.NewPool()
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	aiClient := ai.NewClientFromEnv()

	gmailSvc := gmail.NewPollingService(pool, aiClient)
	gmailSvc.Start()
	gmailSvc.StartDraftSender()

	log.Println("tanda worker running (Gmail poll + DB ingest + draft sender; no HTTP API)")
	log.Println("press Ctrl+C to stop")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutdown")
}
