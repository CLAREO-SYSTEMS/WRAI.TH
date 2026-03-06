package models

type Task struct {
	ID              string  `json:"id"`
	ProfileSlug     string  `json:"profile_slug"`
	AssignedTo      *string `json:"assigned_to,omitempty"`
	DispatchedBy    string  `json:"dispatched_by"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	Priority        string  `json:"priority"`
	Status          string  `json:"status"`
	Result          *string `json:"result,omitempty"`
	BlockedReason   *string `json:"blocked_reason,omitempty"`
	Project         string  `json:"project"`
	DispatchedAt    string  `json:"dispatched_at"`
	AcceptedAt      *string `json:"accepted_at,omitempty"`
	StartedAt       *string `json:"started_at,omitempty"`
	CompletedAt     *string `json:"completed_at,omitempty"`
	ParentTaskID    *string `json:"parent_task_id,omitempty"`
	AckNotifiedAt   *string `json:"ack_notified_at,omitempty"`
	AckEscalatedAt  *string `json:"ack_escalated_at,omitempty"`
	BoardID         *string `json:"board_id,omitempty"`
	Subtasks        []Task  `json:"subtasks,omitempty"`
}

type Board struct {
	ID          string `json:"id"`
	Project     string `json:"project"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
}
