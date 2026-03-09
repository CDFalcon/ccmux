package project

import "github.com/CDFalcon/ccmux/internal/migration"

var migrations = migration.NewRegistry()

func init() {
	migrations.Register(1, func(data []byte) ([]byte, error) {
		return data, nil
	})
	migrations.Register(2, func(data []byte) ([]byte, error) {
		return data, nil
	})
}
