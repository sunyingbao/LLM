package planning

type Todo struct {
	ID          string     `json:"id"`
	Content     string     `json:"content"`
	Status      TodoStatus `json:"status"`
	Priority    int        `json:"priority"`
	CreatedAt   string     `json:"created_at"`
	CompletedAt string     `json:"completed_at,omitempty"`
	Subtasks    []Subtask  `json:"subtasks,omitempty"`
}

type Subtask struct {
	Index       int    `json:"index"`
	Description string `json:"description"`
	AgentType   string `json:"agent_type,omitempty"`
	Status      string `json:"status"`
	Result      string `json:"result,omitempty"`
	Error       string `json:"error,omitempty"`
	ElapsedMs   int64  `json:"elapsed_ms,omitempty"`
}

type TodoUpdateEvent struct {
	TodoID   string    `json:"todo_id"`
	Status   string    `json:"status"`
	Subtasks []Subtask `json:"subtasks,omitempty"`
}

type TodoStatus string

const (
	TodoStatusPending    TodoStatus = "pending"
	TodoStatusInProgress TodoStatus = "in_progress"
	TodoStatusCompleted  TodoStatus = "completed"
	TodoStatusFailed     TodoStatus = "failed"
	TodoStatusSkipped    TodoStatus = "skipped"
)
