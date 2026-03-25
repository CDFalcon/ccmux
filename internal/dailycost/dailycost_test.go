package dailycost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	tmpDir := t.TempDir()
	return &Store{
		filePath: filepath.Join(tmpDir, "daily_costs.json"),
	}
}

func TestAddCosts_ShouldPersistCosts_GivenNewDate(t *testing.T) {
	// Setup.
	store := setupTestStore(t)

	// Execute.
	err := store.AddCosts(map[string]float64{"2026-03-25": 1.50})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cost, err := store.GetCost("2026-03-25")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost != 1.50 {
		t.Errorf("expected 1.50, got %.2f", cost)
	}
}

func TestAddCosts_ShouldAccumulate_GivenExistingDate(t *testing.T) {
	// Setup.
	store := setupTestStore(t)
	store.AddCosts(map[string]float64{"2026-03-25": 1.50})

	// Execute.
	err := store.AddCosts(map[string]float64{"2026-03-25": 2.25})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cost, err := store.GetCost("2026-03-25")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost != 3.75 {
		t.Errorf("expected 3.75, got %.2f", cost)
	}
}

func TestAddCosts_ShouldTrackMultipleDays_GivenMultipleDates(t *testing.T) {
	// Setup.
	store := setupTestStore(t)

	// Execute.
	err := store.AddCosts(map[string]float64{
		"2026-03-24": 5.00,
		"2026-03-25": 3.00,
	})

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cost24, _ := store.GetCost("2026-03-24")
	cost25, _ := store.GetCost("2026-03-25")
	if cost24 != 5.00 {
		t.Errorf("expected 5.00 for 03-24, got %.2f", cost24)
	}
	if cost25 != 3.00 {
		t.Errorf("expected 3.00 for 03-25, got %.2f", cost25)
	}
}

func TestGetCost_ShouldReturnZero_GivenNonexistentDate(t *testing.T) {
	// Setup.
	store := setupTestStore(t)

	// Execute.
	cost, err := store.GetCost("2026-01-01")

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost != 0 {
		t.Errorf("expected 0, got %.2f", cost)
	}
}

func TestGetCost_ShouldReturnZero_GivenNoFile(t *testing.T) {
	// Setup.
	store := setupTestStore(t)

	// Execute.
	cost, err := store.GetCost("2026-03-25")

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost != 0 {
		t.Errorf("expected 0, got %.2f", cost)
	}
}

func TestGetAllCosts_ShouldReturnAll_GivenMultipleDates(t *testing.T) {
	// Setup.
	store := setupTestStore(t)
	store.AddCosts(map[string]float64{"2026-03-24": 5.00, "2026-03-25": 3.00})

	// Execute.
	costs, err := store.GetAllCosts()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(costs) != 2 {
		t.Errorf("expected 2 entries, got %d", len(costs))
	}
	if costs["2026-03-24"] != 5.00 {
		t.Errorf("expected 5.00, got %.2f", costs["2026-03-24"])
	}
}

func TestLoad_ShouldHandleCorruptedFile_GivenBadJSON(t *testing.T) {
	// Setup.
	store := setupTestStore(t)
	os.WriteFile(store.filePath, []byte("not json"), 0644)

	// Execute.
	_, err := store.GetCost("2026-03-25")

	// Assert.
	if err == nil {
		t.Error("expected error for corrupted file")
	}
}

func TestSave_ShouldPersistToDisk_GivenValidData(t *testing.T) {
	// Setup.
	store := setupTestStore(t)
	store.AddCosts(map[string]float64{"2026-03-25": 1.50})

	// Execute.
	raw, err := os.ReadFile(store.filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assert.
	var data storeData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("failed to parse saved file: %v", err)
	}
	if data.DailyCosts["2026-03-25"] != 1.50 {
		t.Errorf("expected 1.50 in saved file, got %.2f", data.DailyCosts["2026-03-25"])
	}
}
