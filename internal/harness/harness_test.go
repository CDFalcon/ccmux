package harness

import (
	"strings"
	"testing"
)

func TestParse_ShouldFallBackToDefault_GivenEmptyOrUnknown(t *testing.T) {
	cases := map[string]Type{
		"":         Default,
		"   ":      Default,
		"bogus":    Default,
		"claude":   Claude,
		"Claude":   Claude,
		"  CODEX ": Codex,
		"codex":    Codex,
	}
	for in, want := range cases {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValid_ShouldOnlyAcceptKnownHarnesses(t *testing.T) {
	for _, in := range []string{"claude", "codex", "CODEX", " claude "} {
		if !Valid(in) {
			t.Errorf("Valid(%q) = false, want true", in)
		}
	}
	for _, in := range []string{"", "gpt", "gemini"} {
		if Valid(in) {
			t.Errorf("Valid(%q) = true, want false", in)
		}
	}
}

func TestDefault_ShouldBeClaude(t *testing.T) {
	if Default != Claude {
		t.Errorf("Default = %q, want %q (preserves legacy behaviour)", Default, Claude)
	}
}

func TestStartCommand_ShouldReferenceHarnessSpecificCLI(t *testing.T) {
	if !strings.HasPrefix(Claude.StartCommand(), "claude ") {
		t.Errorf("Claude.StartCommand() = %q, want it to invoke claude", Claude.StartCommand())
	}
	if !strings.Contains(Claude.StartCommand(), "$SYSTEM_PROMPT") ||
		!strings.Contains(Claude.StartCommand(), "$TASK") {
		t.Errorf("Claude.StartCommand() must use SYSTEM_PROMPT and TASK: %q", Claude.StartCommand())
	}
	if !strings.HasPrefix(Codex.StartCommand(), "codex ") {
		t.Errorf("Codex.StartCommand() = %q, want it to invoke codex", Codex.StartCommand())
	}
	if !strings.Contains(Codex.StartCommand(), "$SYSTEM_PROMPT") ||
		!strings.Contains(Codex.StartCommand(), "$TASK") {
		t.Errorf("Codex.StartCommand() must use SYSTEM_PROMPT and TASK: %q", Codex.StartCommand())
	}
}

func TestContinueAndResumeCommands_ShouldMatchHarness(t *testing.T) {
	for _, h := range All() {
		if !strings.HasPrefix(h.ContinueCommand(), h.CLIName()+" ") {
			t.Errorf("%s.ContinueCommand() = %q, want it to invoke %s", h, h.ContinueCommand(), h.CLIName())
		}
		if !strings.HasPrefix(h.ResumeWithPromptPrefix(), h.CLIName()+" ") {
			t.Errorf("%s.ResumeWithPromptPrefix() = %q, want it to invoke %s", h, h.ResumeWithPromptPrefix(), h.CLIName())
		}
	}
}

func TestInstallsClaudeHooks_ShouldBeClaudeOnly(t *testing.T) {
	if !Claude.InstallsClaudeHooks() {
		t.Error("Claude should install Claude hooks")
	}
	if Codex.InstallsClaudeHooks() {
		t.Error("Codex should not install Claude hooks")
	}
}
