package models

import "time"

// Lead matches the `leads` table in Supabase.
type Lead struct {
	ID               string     `json:"id"`
	GmailThreadID    *string    `json:"gmail_thread_id,omitempty"`
	CustomerName     *string    `json:"customer_name,omitempty"`
	CustomerEmail    *string    `json:"customer_email,omitempty"`
	RequestType      *string    `json:"request_type,omitempty"`
	DanceStyle       *string    `json:"dance_style,omitempty"`
	Level            *string    `json:"level,omitempty"`
	StudentCount     *int32     `json:"student_count,omitempty"`
	RequestedTime    *string    `json:"requested_time,omitempty"`
	Status           *string    `json:"status,omitempty"`
	Priority         *string    `json:"priority,omitempty"`
	AIConfidence     *float64   `json:"ai_confidence,omitempty"`
	Notes            *string    `json:"notes,omitempty"`
	CreatedAt        *time.Time `json:"created_at,omitempty"`
	UpdatedAt        *time.Time `json:"updated_at,omitempty"`
}

// EmailThread matches the `email_threads` table.
type EmailThread struct {
	ID            string     `json:"id"`
	LeadID        string     `json:"lead_id"`
	GmailMsgID    *string    `json:"gmail_message_id,omitempty"`
	GmailThreadID *string    `json:"gmail_thread_id,omitempty"`
	SenderEmail   *string    `json:"sender_email,omitempty"`
	Subject       *string    `json:"subject,omitempty"`
	Body          *string    `json:"body,omitempty"`
	ReceivedAt    *time.Time `json:"received_at,omitempty"`
}

// DraftResponse matches the `draft_responses` table.
type DraftResponse struct {
	ID             string     `json:"id"`
	LeadID         string     `json:"lead_id"`
	DraftText      *string    `json:"draft_text,omitempty"`
	ApprovalStatus *string    `json:"approval_status,omitempty"`
	ApprovedBy     *string    `json:"approved_by,omitempty"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
	CreatedAt      *time.Time `json:"created_at,omitempty"`
}

// Task matches the `tasks` table.
type Task struct {
	ID        string     `json:"id"`
	LeadID    string     `json:"lead_id"`
	TaskType  *string    `json:"task_type,omitempty"`
	Status    *string    `json:"status,omitempty"`
	AssignedTo *string  `json:"assigned_to,omitempty"`
	DueDate   *time.Time `json:"due_date,omitempty"`
	Notes     *string    `json:"notes,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}
