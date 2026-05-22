package project

import "github.com/CDFalcon/ccmux/internal/harness"

const CurrentSchemaVersion = 9

const SetupStatusSettingUp = "setting_up"

type Project struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	FastWorktreePath  string `json:"fast_worktree_path,omitempty"`
	DefaultBaseBranch string `json:"default_base_branch,omitempty"`
	DefaultHarness    string `json:"default_harness,omitempty"`
	UseFastWorktrees  bool   `json:"use_fast_worktrees,omitempty"`
	SetupStatus       string `json:"setup_status,omitempty"`
	StartupScript     string `json:"startup_script,omitempty"`
	TeardownScript    string `json:"teardown_script,omitempty"`
	MergeWhenAccepted bool   `json:"merge_when_accepted,omitempty"`
	// DraftPRs controls whether agents are instructed to open pull requests
	// as drafts. A nil pointer means "unset" and resolves to true via
	// EffectiveDraftPRs, so projects created before this setting existed
	// keep the original draft-PR behaviour.
	DraftPRs *bool `json:"draft_prs,omitempty"`
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

// EffectiveHarness returns the coding-agent CLI new tasks for this project
// default to, falling back to harness.Default for projects created before
// harness selection existed.
func (p *Project) EffectiveHarness() harness.Type {
	return harness.Parse(p.DefaultHarness)
}

// EffectiveDraftPRs reports whether agents working in this project should
// open pull requests as drafts. It defaults to true when unset (DraftPRs ==
// nil) so projects created before this setting existed are unaffected.
func (p *Project) EffectiveDraftPRs() bool {
	if p.DraftPRs == nil {
		return true
	}
	return *p.DraftPRs
}

type storeData struct {
	Version  int                 `json:"version"`
	Projects map[string]*Project `json:"projects"`
	Order    []string            `json:"order,omitempty"`
}
