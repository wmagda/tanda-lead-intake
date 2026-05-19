package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// NewTasksHandler handles POST /api/leads/:id/task.
func NewTasksHandler(_ interface{}) *TasksHandler { return &TasksHandler{} }

type TasksHandler struct{}

// Create adds a follow-up task for a lead.
func (h *TasksHandler) Create(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"detail": "not implemented"})
}
