package lockfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadJSON_ShouldReturnFileUnchanged_GivenValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	want := "{\n  \"version\": 1,\n  \"items\": []\n}\n"
	if err := os.WriteFile(path, []byte(want), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadJSON_ShouldReportNotExist_GivenMissingFile(t *testing.T) {
	_, err := ReadJSON(filepath.Join(t.TempDir(), "missing.json"))
	if !os.IsNotExist(err) {
		t.Fatalf("expected a not-exist error, got %v", err)
	}
}

// The real shape from a session's queue.json: a shorter, newer document
// written over a longer one without truncation.
func TestReadJSON_ShouldRecoverFirstDocument_GivenTrailingGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queue.json")
	newer := "{\n  \"version\": 1,\n  \"items\": null,\n  \"counter\": 339\n}"
	olderTail := "0\",\n      \"type\": \"idle\"\n    }\n  ],\n  \"counter\": 340\n}\n"
	raw := newer + olderTail
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != newer {
		t.Fatalf("got %q, want the leading document %q", got, newer)
	}

	// The file itself is left alone; one quarantine copy sits beside it.
	onDisk, _ := os.ReadFile(path)
	if string(onDisk) != raw {
		t.Fatalf("ReadJSON rewrote the file: %q", onDisk)
	}
	entries, _ := os.ReadDir(dir)
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "queue.json.corrupt-") {
			backups = append(backups, e.Name())
		}
	}
	if len(backups) != 1 {
		t.Fatalf("expected one quarantine file, got %v", entries)
	}
	saved, _ := os.ReadFile(filepath.Join(dir, backups[0]))
	if string(saved) != raw {
		t.Fatalf("quarantine copy %q differs from the corrupt file", backups[0])
	}

	// Loading again (a polling reader) must not multiply quarantine files.
	if _, err := ReadJSON(path); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("second load created more files: %v", entries)
	}
}

func TestReadJSON_ShouldReportParseErrorNamingPath_GivenTruncatedDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"agents":{"abc":{"id":"ab`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadJSON(path)
	if err == nil {
		t.Fatal("expected an error for a truncated document")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error should name the file: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("nothing to recover, so nothing should be quarantined: %v", entries)
	}
}
