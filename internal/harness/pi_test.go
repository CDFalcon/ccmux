package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPiModel_ShouldDefaultToAstra_AndHonourOverride(t *testing.T) {
	os.Unsetenv(PiModelEnv)
	if got := PiModel(); got != "openai/gpt-6-astra" {
		t.Errorf("default = %q", got)
	}
	t.Setenv(PiModelEnv, "openai-codex/gpt-6-astra:high")
	if got := PiModel(); got != "openai-codex/gpt-6-astra:high" {
		t.Errorf("override = %q", got)
	}
	t.Setenv(PiModelEnv, "")
	if got := PiModel(); got != "" {
		t.Errorf("empty override should defer to Pi settings, got %q", got)
	}
}

func TestPreparePiLaunch_ShouldStartFresh_WithPromptFileAndExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(PiModelEnv, "openai/gpt-6-astra")
	sp := filepath.Join(home, ".ccmux", "launchers", "p1-system-prompt.txt")
	os.MkdirAll(filepath.Dir(sp), 0o755)
	os.WriteFile(sp, []byte("prompt"), 0o644)

	args, err := PreparePiLaunch("p1", false, "-do the task")
	if err != nil {
		t.Fatal(err)
	}
	ext := filepath.Join(home, ".ccmux", "pi", "ccmux-extension.js")
	want := []string{"pi", "--approve", "--extension", ext, "--model", "openai/gpt-6-astra", "--append-system-prompt", sp, "--", "-do the task"}
	if !slices.Equal(args, want) {
		t.Errorf("args =\n%q\nwant\n%q", args, want)
	}
	if data, err := os.ReadFile(ext); err != nil || string(data) != PiExtensionJS {
		t.Errorf("extension not written: %v", err)
	}
}

// The system prompt must reach Pi as a file path, never as argv text.
func TestPreparePiLaunch_ShouldNeverPutSystemPromptTextInArgv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sp := filepath.Join(home, ".ccmux", "launchers", "p2-system-prompt.txt")
	os.MkdirAll(filepath.Dir(sp), 0o755)
	os.WriteFile(sp, []byte("SECRET-SYSTEM-PROMPT-PROSE"), 0o644)
	args, _ := PreparePiLaunch("p2", true, "x")
	if strings.Contains(strings.Join(args, " "), "SECRET-SYSTEM-PROMPT-PROSE") {
		t.Error("system prompt text leaked into argv")
	}
}

func TestPreparePiLaunch_ShouldContinue_AndSkipMissingPieces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(PiModelEnv, "")
	args, err := PreparePiLaunch("p3", true, "")
	if err != nil {
		t.Fatal(err)
	}
	ext := filepath.Join(home, ".ccmux", "pi", "ccmux-extension.js")
	want := []string{"pi", "--approve", "--extension", ext, "--continue"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}
}

func TestPiExtensionJS_ShouldWireStatusAndCIHooks(t *testing.T) {
	for _, want := range []string{`"agent_settled"`, `"agent-stopped", AGENT_ID`, `"tool_result"`, `"ci-wait"`, "export default function"} {
		if !strings.Contains(PiExtensionJS, want) {
			t.Errorf("extension is missing %q", want)
		}
	}
}
