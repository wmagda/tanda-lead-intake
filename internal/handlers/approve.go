package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// NewApproveHandler handles POST /api/leads/:id/approve.
// Body: { "editedDraft": "optional reply text" }
func NewApproveHandler(pool *db.Pool, gmailSvc interface{}) *ApproveHandler {
	return &ApproveHandler{pool: pool, gmail: gmailSvc}
}

type ApproveHandler struct {
	pool  *db.Pool
	gmail interface{} // *gmail.Service — kept interface to avoid import cycle in stub
}

// Approve sends an approved draft as a Gmail reply and marks the lead as waiting_customer_reply.
func (h *ApproveHandler) Approve(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"detail": "not implemented"})
}
