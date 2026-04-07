package project

const CurrentSchemaVersion = 6

const SetupStatusSettingUp = "setting_up"

type Project struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	FastWorktreePath  string `json:"fast_worktree_path,omitempty"`
	DefaultBaseBranch string `json:"default_base_branch,omitempty"`
	UseFastWorktrees  bool   `json:"use_fast_worktrees,omitempty"`
	SetupStatus       string `json:"setup_status,omitempty"`
	StartupScript     string `json:"startup_script,omitempty"`
	TeardownScript    string `json:"teardown_script,omitempty"`
	MergeWhenAccepted bool   `json:"merge_when_accepted,omitempty"`
}

func (p *Project) IsSettingUp() bool {
	return p.SetupStatus == SetupStatusSettingUp
}

func (p *Project) EffectivePath() string {
	if p.UseFastWorktrees && p.FastWorktreePath != "" {
		return p.FastWorktreePath
	}
	return p.Path
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
