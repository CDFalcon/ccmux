package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A concurrent-writer race once left agents.json ending in "}}": a complete
// document followed by one byte of the longer document it overwrote. The loader
// used to ignore the envelope parse error, read version 0, and fail every Stop
// hook with "no migration registered for version 0 -> 1" until a human repaired
// the file. The leading document is the newest full write, so load it.
func TestLoad_ShouldRecoverAgents_GivenTrailingGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	corrupt := `{"version":2,"agents":{"abc12345":{"id":"abc12345","task":"t","status":"running"}}}}`
	if err := os.WriteFile(path, []byte(corrupt), 0644); err != nil {
		t.Fatal(err)
	}
	s := &Store{filePath: path}
	agents, err := s.List()
	if err != nil {
		t.Fatalf("expected recovery, got %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "abc12345" {
		t.Fatalf("recovered wrong agents: %+v", agents)
	}

	// The next write replaces the corrupt file with clean content.
	if err := s.Update("abc12345", func(a *Agent) { a.Status = StatusReady }); err != nil {
		t.Fatal(err)
	}
	onDisk, _ := os.ReadFile(path)
	if strings.HasSuffix(strings.TrimSpace(string(onDisk)), "}}}}") {
		t.Fatalf("save did not clean the file: %s", onDisk)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 || !strings.HasPrefix(names[1], "agents.json.corrupt-") {
		t.Fatalf("expected agents.json plus one quarantine copy, got %v", names)
	}
}

// A file with no leading complete document cannot be recovered; the error must
// be the real parse error naming the file, never the misleading migration one.
func TestLoad_ShouldReportParseError_GivenTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"agents":{"abc12345":{"id":"ab`), 0644); err != nil {
		t.Fatal(err)
	}
	s := &Store{filePath: path}
	_, err := s.List()
	if err == nil {
		t.Fatal("expected error for truncated file")
	}
	if strings.Contains(err.Error(), "migration") || !strings.Contains(err.Error(), "not valid JSON") || !strings.Contains(err.Error(), path) {
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
