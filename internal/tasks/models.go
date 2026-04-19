package tasks

type TaskStatus string

const (
	StatusPending    TaskStatus = "pending"
	StatusInProgress TaskStatus = "in_progress"
	StatusVerifying  TaskStatus = "verifying"
	StatusCompleted  TaskStatus = "completed"
	StatusFailed     TaskStatus = "failed"
	StatusCancelled  TaskStatus = "cancelled"
)

type Task struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"` // subagent, verification, research
	Subject      string     `json:"subject"`
	Description  string     `json:"description"`
	Status       TaskStatus `json:"status"` // pending, in_progress, verifying, completed, failed, cancelled
	DependsOn    []string   `json:"depends_on,omitempty"`
	ParentTaskID string     `json:"parent_task_id,omitempty"`
	SessionID    string     `json:"session_id,omitempty"`
	ResultPath   string     `json:"result_path,omitempty"`
	PlanPath     string     `json:"plan_path,omitempty"`
	Summary      string     `json:"summary,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	RetryCount   int        `json:"retry_count,omitempty"`
	Owner        string     `json:"owner,omitempty"`
	CreatedAt    int64      `json:"created_at"`
	UpdatedAt    int64      `json:"updated_at"`
	StartedAt    int64      `json:"started_at,omitempty"`
	CompletedAt  int64      `json:"completed_at,omitempty"`
}

type TaskPatch struct {
	Status      *TaskStatus
	Subject     *string
	Description *string
	Summary     *string
	LastError   *string
	ResultPath  *string
	PlanPath    *string
	DependsOn   *[]string
}
