package models

import "time"

// Lead is the subset of `leads` columns populated by AI parse (ingest uses SQL for full rows).
type Lead struct {
	ID            string     `json:"id"`
	GmailThreadID *string    `json:"gmail_thread_id,omitempty"`
	CustomerName  *string    `json:"customer_name,omitempty"`
	CustomerEmail *string    `json:"customer_email,omitempty"`
	CustomerPhone *string    `json:"customer_phone,omitempty"`
	RequestType   *string    `json:"request_type,omitempty"`
	DanceStyle    *string    `json:"dance_style,omitempty"`
	Level         *string    `json:"level,omitempty"`
	StudentCount  *int32     `json:"student_count,omitempty"`
	RequestedTime *string    `json:"requested_time,omitempty"`
	Status        *string    `json:"status,omitempty"`
	Priority      *string    `json:"priority,omitempty"`
	AIConfidence  *float64   `json:"ai_confidence,omitempty"`
	ReceivedAt    *time.Time `json:"received_at,omitempty"`
	Notes         *string    `json:"notes,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}
