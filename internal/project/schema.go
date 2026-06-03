package project

import "github.com/CDFalcon/ccmux/internal/harness"

const CurrentSchemaVersion = 11

const SetupStatusSettingUp = "setting_up"

type Project struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	DefaultBaseBranch string `json:"default_base_branch,omitempty"`
	DefaultHarness    string `json:"default_harness,omitempty"`
	// UseFastWorktrees enables rift (https://github.com/anomalyco/rift) for
	// near-instant copy-on-write worktree creation. When true, the project's
	// repo path must be `rift init`'d so subsequent `rift create` calls can
	// snapshot it. The repo path itself is unchanged — rift initialises the
	// directory in place rather than relocating it.
	UseFastWorktrees  bool   `json:"use_fast_worktrees,omitempty"`
	SetupStatus       string `json:"setup_status,omitempty"`
	StartupScript     string `json:"startup_script,omitempty"`
	TeardownScript    string `json:"teardown_script,omitempty"`
	MergeWhenAccepted bool   `json:"merge_when_accepted,omitempty"`
	// UseTrunkMerge routes "accept PR" through trunk.io's merge queue
	// instead of `gh pr merge`. When true, accepting a PR posts a
	// `/trunk merge` comment and leaves the agent in
	// StatusWaitingMergeQueue until the queue actually merges the PR;
	// cleanup runs when polling sees the PR transition to MERGED.
	// Mutually exclusive with MergeWhenAccepted in spirit — if both are
	// set, trunk wins because posting the comment is reversible whereas
	// `gh pr merge` is not.
	UseTrunkMerge     bool   `json:"use_trunk_merge,omitempty"`
	// DraftPRs controls whether agents are instructed to open pull requests
	// as drafts. A nil pointer means "unset" and resolves to true via
	// EffectiveDraftPRs, so projects created before this setting existed
	// keep the original draft-PR behaviour.
	DraftPRs *bool `json:"draft_prs,omitempty"`
}

func (p *Project) IsSettingUp() bool {
	return p.SetupStatus == SetupStatusSettingUp
}

// EffectivePath returns the repo path agents work against. With rift, the
// repo is initialised in place — there is no separate "fast worktree root"
// distinct from the project path. This helper used to swap between the two
// for the proj backend; it now just returns Path unchanged. It is kept so
// callers don't have to be aware of that history.
func (p *Project) EffectivePath() string {
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
