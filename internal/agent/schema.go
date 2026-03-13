package agent

import "time"

const CurrentSchemaVersion = 1

type Status string

const (
	StatusSpawning      Status = "spawning"
	StatusRunning       Status = "running"
	StatusReady         Status = "ready"
	StatusWaitingReview Status = "waiting_review"
	StatusWaitingCI     Status = "waiting_ci"
	StatusCleaningUp    Status = "cleaning_up"
	StatusKilling       Status = "killing"
	StatusMerged        Status = "merged"
	StatusFailed        Status = "failed"
)

func (s Status) DisplayName() string {
	switch s {
	case StatusSpawning:
		return "spawning"
	case StatusRunning:
		return "running"
	case StatusReady:
		return "idle"
	case StatusWaitingReview:
		return "waiting for review"
	case StatusWaitingCI:
		return "waiting on CI"
	case StatusCleaningUp:
		return "cleaning up"
	case StatusKilling:
		return "killing"
	case StatusMerged:
		return "merged"
	case StatusFailed:
		return "failed"
	default:
		return string(s)
	}
}

type Agent struct {
	ID           string    `json:"id"`
	Task         string    `json:"task"`
	RepoName     string    `json:"repo_name,omitempty"`
	WorktreeName string    `json:"worktree_name,omitempty"`
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
