package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// NewApproveHandler handles POST /api/leads/:id/approve.
// Body: { "editedDraft": "optional reply text" }
func NewApproveHandler(_ interface{}, _ interface{}) *ApproveHandler {
	return &ApproveHandler{}
}

type ApproveHandler struct{}

// Approve handles approval + send.
func (h *ApproveHandler) Approve(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"detail": "not implemented"})
}
