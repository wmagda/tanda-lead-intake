package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// NewLeadsHandler handles GET /api/leads and GET /api/leads/:id.
func NewLeadsHandler(pool *db.Pool) *LeadsHandler { return &LeadsHandler{pool: pool} }

type LeadsHandler struct{ pool *db.Pool }

// List returns the lead queue, optionally filtered by status/request_type.
// GET /api/leads?status=new&request_type=private_lesson
func (h *LeadsHandler) List(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"detail": "not implemented"})
}

// Get returns a single lead with threads, draft, and tasks.
func (h *LeadsHandler) Get(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"detail": "not implemented"})
}
