package project

const CurrentSchemaVersion = 4

const DefaultCIWaitMinutes = 5

type Project struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	FastWorktreePath  string `json:"fast_worktree_path,omitempty"`
	DefaultBaseBranch string `json:"default_base_branch,omitempty"`
	CIWaitMinutes     int    `json:"ci_wait_minutes,omitempty"`
	UseFastWorktrees  bool   `json:"use_fast_worktrees,omitempty"`
}

func (p *Project) EffectivePath() string {
	if p.UseFastWorktrees && p.FastWorktreePath != "" {
		return p.FastWorktreePath
	}
	return p.Path
}

func (p *Project) EffectiveCIWaitMinutes() int {
	if p.CIWaitMinutes <= 0 {
		return DefaultCIWaitMinutes
	}
	return p.CIWaitMinutes
}

func (p *Project) EffectiveBaseBranch() string {
	if p.DefaultBaseBranch == "" {
		return "origin/master"
	}
	return p.DefaultBaseBranch
}

type storeData struct {
	Version  int                 `json:"version"`
	Projects map[string]*Project `json:"projects"`
}
