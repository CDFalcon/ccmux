package repo

import (
	"encoding/json"

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
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		if projects, ok := raw["projects"]; ok {
			raw["repos"] = projects
			delete(raw, "projects")
		}
		raw["version"], _ = json.Marshal(5)
		return json.Marshal(raw)
	})
}
