package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func envValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return strings.TrimPrefix(kv, key+"="), true
		}
	}
	return "", false
}

func TestOpenCodeConfigContent_ShouldAddToExistingUserConfig(t *testing.T) {
	got, err := OpenCodeConfigContent(`{"model":"anthropic/x","plugin":["user-plugin"],"instructions":["mine.md"]}`, "/h/.ccmux/opencode/ccmux-plugin.js", "/h/.ccmux/launchers/a-system-prompt.txt")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["model"] != "anthropic/x" {
		t.Errorf("user model dropped: %v", cfg["model"])
	}
	if want := []any{"user-plugin", "file:///h/.ccmux/opencode/ccmux-plugin.js"}; !slices.Equal(cfg["plugin"].([]any), want) {
		t.Errorf("plugin = %v, want %v", cfg["plugin"], want)
	}
	if want := []any{"mine.md", "/h/.ccmux/launchers/a-system-prompt.txt"}; !slices.Equal(cfg["instructions"].([]any), want) {
		t.Errorf("instructions = %v, want %v", cfg["instructions"], want)
	}

	again, _ := OpenCodeConfigContent(got, "/h/.ccmux/opencode/ccmux-plugin.js", "/h/.ccmux/launchers/a-system-prompt.txt")
	if again != got {
		t.Errorf("merging twice should be idempotent:\n%s\n%s", got, again)
	}
}

func TestOpenCodeConfigContent_ShouldRejectNonObject(t *testing.T) {
	if _, err := OpenCodeConfigContent(`["x"]`, "/p.js", ""); err == nil {
		t.Error("want an error for a non-object OPENCODE_CONFIG_CONTENT")
	}
}

func TestOpenCodeModel_ShouldDefaultToAstra_AndHonourOverride(t *testing.T) {
	os.Unsetenv(OpenCodeModelEnv)
	if got := OpenCodeModel(); got != "openai/gpt-6-astra" {
		t.Errorf("default model = %q, want openai/gpt-6-astra", got)
	}
	t.Setenv(OpenCodeModelEnv, "anthropic/claude-opus-5-5")
	if got := OpenCodeModel(); got != "anthropic/claude-opus-5-5" {
		t.Errorf("override = %q", got)
	}
	t.Setenv(OpenCodeModelEnv, "")
	if got := OpenCodeModel(); got != "" {
		t.Errorf("empty override should defer to OpenCode config, got %q", got)
	}
}

func TestPrepareOpenCodeLaunch_ShouldStartFresh_WithPromptAndInstructions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(OpenCodeModelEnv, "openai/gpt-6-astra")
	sp := filepath.Join(home, ".ccmux", "launchers", "a1-system-prompt.txt")
	os.MkdirAll(filepath.Dir(sp), 0o755)
	os.WriteFile(sp, []byte("prompt"), 0o644)

	l, err := PrepareOpenCodeLaunch("a1", false, "do the task", []string{"PATH=/bin", "OPENCODE_CONFIG_CONTENT={\"theme\":\"x\"}"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"opencode", "--auto", "--model", "openai/gpt-6-astra", "--prompt", "do the task"}
	if !slices.Equal(l.Args, want) {
		t.Errorf("args = %q, want %q", l.Args, want)
	}
	cfg, _ := envValue(l.Env, "OPENCODE_CONFIG_CONTENT")
	if !strings.Contains(cfg, `"theme":"x"`) || !strings.Contains(cfg, sp) || !strings.Contains(cfg, "ccmux-plugin.js") {
		t.Errorf("config content = %s", cfg)
	}
	n := 0
	for _, kv := range l.Env {
		if strings.HasPrefix(kv, "OPENCODE_CONFIG_CONTENT=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("OPENCODE_CONFIG_CONTENT appears %d times in env", n)
	}
	plugin, err := os.ReadFile(filepath.Join(home, ".ccmux", "opencode", "ccmux-plugin.js"))
	if err != nil || string(plugin) != OpenCodePluginJS {
		t.Errorf("plugin not written: %v", err)
	}
}

// A resume must target the agent's own recorded session — never
// `opencode --continue`, which resumes whichever sibling worktree of the
// same repository ran last — and hand the follow-up to the plugin, because
// the TUI drops --prompt when given --session.
func TestPrepareOpenCodeLaunch_ShouldResumeRecordedSession_ViaPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(OpenCodeModelEnv, "")
	sessionFile, _ := OpenCodeSessionFile("a2")
	os.MkdirAll(filepath.Dir(sessionFile), 0o755)
	os.WriteFile(sessionFile, []byte("ses_abc\n"), 0o644)

	l, err := PrepareOpenCodeLaunch("a2", true, "address the review", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"opencode", "--auto", "--session", "ses_abc"}
	if !slices.Equal(l.Args, want) {
		t.Errorf("args = %q, want %q", l.Args, want)
	}
	if slices.Contains(l.Args, "--continue") || slices.Contains(l.Args, "-c") {
		t.Error("must never use --continue")
	}
	if id, _ := envValue(l.Env, openCodeResumeIDEnv); id != "ses_abc" {
		t.Errorf("resume id env = %q", id)
	}
	pf, ok := envValue(l.Env, openCodePromptFileEnv)
	if !ok {
		t.Fatal("prompt file env missing")
	}
	if data, _ := os.ReadFile(pf); string(data) != "address the review" {
		t.Errorf("prompt file = %q", data)
	}
}

func TestPrepareOpenCodeLaunch_ShouldFallBackToFresh_GivenNoRecordedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(OpenCodeModelEnv, "")
	// A stale prompt file from a launch that died must not leak into this one.
	pf, _ := openCodePromptFile("a3")
	os.MkdirAll(filepath.Dir(pf), 0o755)
	os.WriteFile(pf, []byte("stale"), 0o600)

	l, err := PrepareOpenCodeLaunch("a3", true, "continue", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"opencode", "--auto", "--prompt", "continue"}
	if !slices.Equal(l.Args, want) {
		t.Errorf("args = %q, want %q", l.Args, want)
	}
	if _, err := os.Stat(pf); !os.IsNotExist(err) {
		t.Error("stale prompt file should have been removed")
	}
}

// The plugin is embedded in a Go raw string; make sure it keeps the hooks
// ccmux's status machine depends on.
func TestOpenCodePluginJS_ShouldWireStatusAndCIHooks(t *testing.T) {
	for _, want := range []string{
		`"agent-stopped", AGENT_ID`,
		`"ci-wait"`,
		`"session.status"`,
		`"tool.execute.after"`,
		"promptAsync",
		"parentID",
	} {
		if !strings.Contains(OpenCodePluginJS, want) {
			t.Errorf("plugin is missing %q", want)
		}
	}
}
