package agent

import "time"

const CurrentSchemaVersion = 1

type Status string

const (
	StatusSpawning   Status = "spawning"
	StatusRunning    Status = "running"
	StatusReady      Status = "ready"
	StatusWaitingCI  Status = "waiting_ci"
	StatusCleaningUp Status = "cleaning_up"
	StatusKilling    Status = "killing"
	StatusMerged     Status = "merged"
	StatusFailed     Status = "failed"
)

type Agent struct {
	ID           string    `json:"id"`
	Task         string    `json:"task"`
	ProjectName  string    `json:"project_name,omitempty"`
	WorktreePath string    `json:"worktree_path"`
	BranchName   string    `json:"branch_name"`
	BaseBranch   string    `json:"base_branch"`
	TmuxWindow   string    `json:"tmux_window"`
	PRURL        string    `json:"pr_url,omitempty"`
	Status       Status    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type storeData struct {
	Version int               `json:"version"`
	Agents  map[string]*Agent `json:"agents"`
}
