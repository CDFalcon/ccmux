package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A concurrent-writer race once left agents.json ending in "}}". The loader
// ignored the envelope parse error, read version 0, and reported
// "no migration registered for version 0 -> 1" instead of the real problem.
func TestLoad_ShouldReportParseError_GivenTrailingGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"agents":{}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	s := &Store{filePath: path}
	_, err := s.List()
	if err == nil {
		t.Fatal("expected error for corrupt file")
	}
	if strings.Contains(err.Error(), "migration") || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestSave_ShouldNotLeaveTempFiles(t *testing.T) {
	dir := t.TempDir()
	s := &Store{filePath: filepath.Join(dir, "agents.json")}
	if err := s.Create(&Agent{ID: "abc12345", Task: "t", Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := s.Update("abc12345", func(a *Agent) { a.Status = StatusReady }); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "agents.json" {
		t.Fatalf("unexpected files in store dir: %v", entries)
	}
}
