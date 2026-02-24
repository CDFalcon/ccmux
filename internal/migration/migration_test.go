package migration

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestMigrate_ShouldApplySequentialMigrations_GivenMultipleVersions(t *testing.T) {
	// Setup.
	registry := NewRegistry()
	registry.Register(1, func(data []byte) ([]byte, error) {
		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		m["migrated_v1_to_v2"] = true
		return json.Marshal(m)
	})
	registry.Register(2, func(data []byte) ([]byte, error) {
		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		m["migrated_v2_to_v3"] = true
		return json.Marshal(m)
	})
	input := []byte(`{"version": 1}`)

	// Execute.
	result, err := registry.Migrate(input, 1, 3)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if m["migrated_v1_to_v2"] != true {
		t.Error("expected migrated_v1_to_v2 to be true")
	}
	if m["migrated_v2_to_v3"] != true {
		t.Error("expected migrated_v2_to_v3 to be true")
	}
}

func TestMigrate_ShouldReturnError_GivenMissingMigration(t *testing.T) {
	// Setup.
	registry := NewRegistry()
	registry.Register(1, func(data []byte) ([]byte, error) {
		return data, nil
	})
	input := []byte(`{"version": 1}`)

	// Execute.
	_, err := registry.Migrate(input, 1, 3)

	// Assert.
	if err == nil {
		t.Fatal("expected error for missing migration, got nil")
	}
	expected := "no migration registered for version 2 -> 3"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestMigrate_ShouldNoOp_GivenCurrentVersion(t *testing.T) {
	// Setup.
	registry := NewRegistry()
	input := []byte(`{"version": 3}`)

	// Execute.
	result, err := registry.Migrate(input, 3, 3)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != string(input) {
		t.Errorf("expected data to be unchanged, got %s", string(result))
	}
}

func TestMigrate_ShouldReturnError_GivenFailingMigration(t *testing.T) {
	// Setup.
	registry := NewRegistry()
	registry.Register(1, func(data []byte) ([]byte, error) {
		return nil, fmt.Errorf("something broke")
	})
	input := []byte(`{"version": 1}`)

	// Execute.
	_, err := registry.Migrate(input, 1, 2)

	// Assert.
	if err == nil {
		t.Fatal("expected error for failing migration, got nil")
	}
}
