package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// NewTasksHandler handles POST /api/leads/:id/task.
func NewTasksHandler(pool *db.Pool) *TasksHandler { return &TasksHandler{pool: pool} }

type TasksHandler struct{ pool *db.Pool }

// Create adds a follow-up task for a lead.
func (h *TasksHandler) Create(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"detail": "not implemented"})
}
