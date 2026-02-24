package migration

import "fmt"

type MigrationFunc func(data []byte) ([]byte, error)

type Registry struct {
	migrations map[int]MigrationFunc
}

func NewRegistry() *Registry {
	return &Registry{
		migrations: make(map[int]MigrationFunc),
	}
}

func (r *Registry) Register(fromVersion int, fn MigrationFunc) {
	r.migrations[fromVersion] = fn
}

func (r *Registry) Migrate(data []byte, currentVersion, targetVersion int) ([]byte, error) {
	if currentVersion >= targetVersion {
		return data, nil
	}

	result := data
	for v := currentVersion; v < targetVersion; v++ {
		fn, ok := r.migrations[v]
		if !ok {
			return nil, fmt.Errorf("no migration registered for version %d -> %d", v, v+1)
		}
		var err error
		result, err = fn(result)
		if err != nil {
			return nil, fmt.Errorf("migration %d -> %d failed: %w", v, v+1, err)
		}
	}

	return result, nil
}
