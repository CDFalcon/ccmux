package repo

const CurrentSchemaVersion = 5

const SetupStatusSettingUp = "setting_up"

type Repo struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	FastWorktreePath  string `json:"fast_worktree_path,omitempty"`
	DefaultBaseBranch string `json:"default_base_branch,omitempty"`
	UseFastWorktrees  bool   `json:"use_fast_worktrees,omitempty"`
	SetupStatus       string `json:"setup_status,omitempty"`
}

func (p *Repo) IsSettingUp() bool {
	return p.SetupStatus == SetupStatusSettingUp
}

func (p *Repo) EffectivePath() string {
	if p.UseFastWorktrees && p.FastWorktreePath != "" {
		return p.FastWorktreePath
	}
	return p.Path
}

func (p *Repo) EffectiveBaseBranch() string {
	if p.DefaultBaseBranch == "" {
		return "origin/master"
	}
	return p.DefaultBaseBranch
}

type storeData struct {
	Version  int                 `json:"version"`
	Repos map[string]*Repo `json:"repos"`
}
