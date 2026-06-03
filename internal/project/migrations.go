package project

import (
	"encoding/json"
	"sort"

	"github.com/CDFalcon/ccmux/internal/migration"
)

var migrations = migration.NewRegistry()

func init() {
	migrations.Register(1, func(data []byte) ([]byte, error) {
		return data, nil
	})
	migrations.Register(2, func(data []byte) ([]byte, error) {
		return data, nil
	})
	migrations.Register(3, func(data []byte) ([]byte, error) {
		var store struct {
			Version  int                        `json:"version"`
			Projects map[string]json.RawMessage `json:"projects"`
		}
		if err := json.Unmarshal(data, &store); err != nil {
			return nil, err
		}
		for name, raw := range store.Projects {
			var p struct {
				Path             string `json:"path"`
				UseFastWorktrees bool   `json:"use_fast_worktrees,omitempty"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				continue
			}
			if p.UseFastWorktrees {
				var full map[string]interface{}
				json.Unmarshal(raw, &full)
				full["fast_worktree_path"] = p.Path
				updated, err := json.Marshal(full)
				if err != nil {
					continue
				}
				store.Projects[name] = updated
			}
		}
		store.Version = 4
		return json.Marshal(store)
	})
	migrations.Register(4, func(data []byte) ([]byte, error) {
		return data, nil
	})
	migrations.Register(5, func(data []byte) ([]byte, error) {
		return data, nil
	})
	migrations.Register(6, func(data []byte) ([]byte, error) {
		// v6 -> v7: introduce an explicit `order` array preserving the
		// previous alphabetical-by-name display order so existing users
		// see no change until they start reordering themselves.
		var store struct {
			Version  int                        `json:"version"`
			Projects map[string]json.RawMessage `json:"projects"`
			Order    []string                   `json:"order,omitempty"`
		}
		if err := json.Unmarshal(data, &store); err != nil {
			return nil, err
		}
		if len(store.Order) == 0 && len(store.Projects) > 0 {
			names := make([]string, 0, len(store.Projects))
			for name := range store.Projects {
				names = append(names, name)
			}
			sort.Strings(names)
			store.Order = names
		}
		store.Version = 7
		return json.Marshal(store)
	})
	migrations.Register(7, func(data []byte) ([]byte, error) {
		// v7 -> v8: add the optional `default_harness` field. Existing
		// projects leave it empty, which resolves to harness.Default
		// (Claude Code) — no behaviour change until a user opts in.
		return data, nil
	})
	migrations.Register(8, func(data []byte) ([]byte, error) {
		// v8 -> v9: add the optional `draft_prs` field. Existing projects
		// leave it unset, which resolves to true (open PRs as drafts) via
		// EffectiveDraftPRs — no behaviour change until a user opts out.
		return data, nil
	})
	migrations.Register(9, func(data []byte) ([]byte, error) {
		// v9 -> v10: switch the fast-worktree backend from proj to rift.
		// Drop the now-unused `fast_worktree_path` field. For projects that
		// had `use_fast_worktrees: true`, the `path` already points at the
		// real git repo (the v3->v4 migration only added fast_worktree_path
		// alongside it), so they continue to work after a one-time
		// `rift init` on that path. The migration itself is a pure schema
		// scrub — operators or the TUI run `rift init` separately.
		var store struct {
			Version  int                        `json:"version"`
			Projects map[string]json.RawMessage `json:"projects"`
			Order    []string                   `json:"order,omitempty"`
		}
		if err := json.Unmarshal(data, &store); err != nil {
			return nil, err
		}
		for name, raw := range store.Projects {
			var full map[string]interface{}
			if err := json.Unmarshal(raw, &full); err != nil {
				continue
			}
			delete(full, "fast_worktree_path")
			updated, err := json.Marshal(full)
			if err != nil {
				continue
			}
			store.Projects[name] = updated
		}
		store.Version = 10
		return json.Marshal(store)
	})
	migrations.Register(10, func(data []byte) ([]byte, error) {
		// v10 -> v11: add the optional `use_trunk_merge` field. Existing
		// projects leave it unset (false), so they keep using the standard
		// `gh pr merge` path when MergeWhenAccepted is on.
		return data, nil
	})
}
