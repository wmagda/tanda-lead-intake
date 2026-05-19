package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/wmagda/tanda-lead-intake/internal/db"
	"github.com/wmagda/tanda-lead-intake/internal/email"
	"github.com/wmagda/tanda-lead-intake/internal/gmail"
	"github.com/wmagda/tanda-lead-intake/internal/handlers"

	"github.com/gin-gonic/gin"
)

func main() {
	_ = os.Setenv("PORT", os.Getenv("PORT"))
	if os.Getenv("PORT") == "" {
		os.Setenv("PORT", "8080")
	}

	// Connect Supabase (Postgres). The pool is managed via DATABASE_URL env.
	pool, err := db.NewPool()
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	// Load AI inference client. If LM Studio env is set use that, else no-op.
	aiClient := email.NewAIClientFromEnv()

	// Gmail polling service (background).
	gmailSvc, err := gmail.NewPollingService(pool, aiClient)
	if err != nil {
		log.Fatalf("gmail service: %v", err)
	}
	go gmailSvc.Start()

	r := gin.Default()

	// Health check
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "service": "tanda-backend"})
	})

	api := r.Group("/api")
	{
		// ── Lead queue for Lovable admin UI ──────────────────────────
		leads := handlers.NewLeadsHandler(pool)
		api.GET("/leads", leads.List)
		api.GET("/leads/:id", leads.Get)

		// ── Approval + send ───────────────────────────────────────────
		approve := handlers.NewApproveHandler(pool, gmailSvc)
		api.POST("/leads/:id/approve", approve.Approve)

		// ── Tasks ────────────────────────────────────────────────────
		tasks := handlers.NewTasksHandler(pool)
		api.POST("/leads/:id/task", tasks.Create)

		// ── Email processing (called by Gmail webhook/poll) ───────────
		process := handlers.NewProcessHandler(pool, aiClient, gmailSvc)
		api.POST("/email/process", process.Process)
	}

	port := os.Getenv("PORT")
	log.Printf("tanda backend listening on :%s", port)

	// graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("shutdown signal received")
		os.Exit(0)
	}()

	if err := r.Run(":" + port); err != nil {
		log.Fatalf("server: %v", err)
	}
}
