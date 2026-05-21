package agent

import "github.com/CDFalcon/ccmux/internal/migration"

var migrations = migration.NewRegistry()

func init() {
	migrations.Register(1, func(data []byte) ([]byte, error) {
		// v1 -> v2: add the optional `harness` field. Existing agents leave
		// it empty, which resolves to harness.Default (Claude Code).
		return data, nil
	})
}
