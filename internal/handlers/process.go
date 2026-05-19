package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// NewProcessHandler handles POST /api/email/process.
// Called by Gmail polling/webhook after fetching a new email thread.
func NewProcessHandler(pool *db.Pool, aiClient *ai.Client, gmailSvc *gmail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusAccepted, gin.H{"status": "queued"})
	}
}
