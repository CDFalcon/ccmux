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
}
