package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func readClaudeConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, raw)
	}
	return config
}

func trustedPaths(t *testing.T, path string) map[string]bool {
	t.Helper()
	config := readClaudeConfig(t, path)
	projects, _ := config["projects"].(map[string]any)
	got := make(map[string]bool)
	for p, v := range projects {
		if entry, ok := v.(map[string]any); ok {
			if trusted, ok := entry["hasTrustDialogAccepted"].(bool); ok && trusted {
				got[p] = true
			}
		}
	}
	return got
}

func TestTrustClaudeProject_ShouldCreateConfig_GivenNoExistingFile(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := filepath.Join(home, "Code", "ccmux-new-11111111")

	// Execute.
	if err := trustClaudeProject(wt); err != nil {
		t.Fatal(err)
	}

	// Assert.
	if !trustedPaths(t, filepath.Join(home, ".claude.json"))[wt] {
		t.Errorf("expected %s to be trusted", wt)
	}
}

// TestTrustClaudeProject_ShouldPreserveUnrelatedState is the reason this does a
// structured mutation rather than rewriting the file: ~/.claude.json is Claude
// Code's own state — history, MCP servers, auth — and clobbering any of it would
// be far worse than the trust dialog it avoids.
func TestTrustClaudeProject_ShouldPreserveUnrelatedState(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".claude.json")
	existing := `{
  "numStartups": 42,
  "installMethod": "brew",
  "mcpServers": {"some-server": {"command": "run-me"}},
  "projects": {
    "/existing/project": {"hasTrustDialogAccepted": true, "history": ["a", "b"]}
  }
}`
	if err := os.WriteFile(configPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	// Execute.
	if err := trustClaudeProject("/new/worktree"); err != nil {
		t.Fatal(err)
	}

	// Assert.
	config := readClaudeConfig(t, configPath)
	if got := config["numStartups"]; fmt.Sprint(got) != "42" {
		t.Errorf("expected numStartups preserved, got %v", got)
	}
	if _, ok := config["mcpServers"]; !ok {
		t.Error("expected mcpServers preserved")
	}
	projects := config["projects"].(map[string]any)
	existingEntry, ok := projects["/existing/project"].(map[string]any)
	if !ok {
		t.Fatal("expected the pre-existing project entry to survive")
	}
	if _, ok := existingEntry["history"]; !ok {
		t.Error("expected the pre-existing project's history to survive")
	}
	if !trustedPaths(t, configPath)["/new/worktree"] {
		t.Error("expected the new worktree to be trusted")
	}
}

// TestTrustClaudeProject_ShouldNotLoseUpdates_GivenConcurrentSpawns is the
// reproduction of the reported spawn race.
//
// The shell it replaces ran, per spawn:
//
//	jq ... "$CLAUDE_JSON" > "${CLAUDE_JSON}.tmp" && mv "${CLAUDE_JSON}.tmp" "$CLAUDE_JSON"
//
// Launching several agents at once ran several of those concurrently against one
// fixed scratch path, so a sibling's `mv` failed with "No such file or directory"
// (fatal under `set -e`, killing the pane mid-setup) and, even when both renames
// won, both jq processes had read the same pre-image so one worktree's trust entry
// silently vanished.
//
// Every concurrent call must now succeed and every entry must survive.
func TestTrustClaudeProject_ShouldNotLoseUpdates_GivenConcurrentSpawns(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".claude.json")

	const spawns = 16
	want := make(map[string]bool, spawns)
	for i := 0; i < spawns; i++ {
		want[filepath.Join(home, "Code", fmt.Sprintf("ccmux-agent-%02d", i))] = true
	}

	var wg sync.WaitGroup
	errs := make(chan error, spawns)

	// Execute. All spawns pretrust at the same moment.
	for wt := range want {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			if err := trustClaudeProject(path); err != nil {
				errs <- fmt.Errorf("%s: %w", path, err)
			}
		}(wt)
	}
	wg.Wait()
	close(errs)

	// Assert.
	for err := range errs {
		t.Errorf("concurrent pretrust failed: %v", err)
	}
	got := trustedPaths(t, configPath)
	for wt := range want {
		if !got[wt] {
			t.Errorf("lost trust entry for %s (%d of %d survived)", wt, len(got), spawns)
		}
	}
}

// TestTrustClaudeProject_ShouldRefuse_GivenUnparseableConfig: overwriting a
// corrupt ~/.claude.json would destroy the user's Claude Code state. Failing the
// pretrust is recoverable (the agent sees a trust dialog); losing that file is not.
func TestTrustClaudeProject_ShouldRefuse_GivenUnparseableConfig(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".claude.json")
	garbage := []byte(`{"projects": {"broken"`)
	if err := os.WriteFile(configPath, garbage, 0o644); err != nil {
		t.Fatal(err)
	}

	// Execute.
	err := trustClaudeProject("/some/worktree")

	// Assert.
	if err == nil {
		t.Error("expected an error rather than overwriting an unparseable config")
	}
	raw, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("config was removed: %v", readErr)
	}
	if string(raw) != string(garbage) {
		t.Errorf("expected the original file untouched, got %q", raw)
	}
}

func TestTrustClaudeProject_ShouldBeIdempotent(t *testing.T) {
	// Setup.
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".claude.json")
	wt := "/some/worktree"

	if err := trustClaudeProject(wt); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	// Execute.
	if err := trustClaudeProject(wt); err != nil {
		t.Fatal(err)
	}

	// Assert.
	second, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("expected a no-op second call, file changed:\n%s\n->\n%s", first, second)
	}
}

func TestTrustClaudeProject_ShouldHandleEmptyFile(t *testing.T) {
	// Setup. An empty (e.g. truncated) config must still be usable.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Execute.
	if err := trustClaudeProject("/some/worktree"); err != nil {
		t.Fatal(err)
	}

	// Assert.
	if !trustedPaths(t, filepath.Join(home, ".claude.json"))["/some/worktree"] {
		t.Error("expected the worktree to be trusted")
	}
}

func TestTrustClaudeProject_ShouldError_GivenEmptyPath(t *testing.T) {
	// Setup.
	t.Setenv("HOME", t.TempDir())

	// Execute / Assert.
	if err := trustClaudeProject("  "); err == nil {
		t.Error("expected an error for an empty worktree path")
	}
}
