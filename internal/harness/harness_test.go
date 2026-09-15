package harness

import (
	"os"
	"os/exec"
	"path/filepath"
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
	if !strings.Contains(Claude.StartCommand(), `--system-prompt-file "$SYSTEM_PROMPT_FILE"`) ||
		!strings.Contains(Claude.StartCommand(), "$TASK") {
		t.Errorf("Claude.StartCommand() must use SYSTEM_PROMPT_FILE and TASK: %q", Claude.StartCommand())
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

func TestContinueWithPromptCommand_ShouldResumeClaude_AndRestartCodex(t *testing.T) {
	c := Claude.ContinueWithPromptCommand()
	if !strings.HasPrefix(c, "claude --continue") {
		t.Errorf("Claude.ContinueWithPromptCommand() = %q, want it to resume with --continue", c)
	}
	if !strings.Contains(c, `--system-prompt-file "$SYSTEM_PROMPT_FILE"`) || !strings.Contains(c, "$PROMPT") {
		t.Errorf("Claude.ContinueWithPromptCommand() must use SYSTEM_PROMPT_FILE and PROMPT: %q", c)
	}
	x := Codex.ContinueWithPromptCommand()
	if !strings.HasPrefix(x, "codex ") || strings.Contains(x, "--continue") {
		t.Errorf("Codex.ContinueWithPromptCommand() = %q, want a fresh codex session", x)
	}
	if !strings.Contains(x, "$SYSTEM_PROMPT") || !strings.Contains(x, "$PROMPT") {
		t.Errorf("Codex.ContinueWithPromptCommand() must use SYSTEM_PROMPT and PROMPT: %q", x)
	}
}

// The system prompt must never appear in the Claude harness's argv: it is a
// dozen KB of prose that any `pkill -f` on the machine can match, and one
// agent's pkill aimed at its own background job took down every sibling agent
// that way. See SystemPromptFileBlock.
func TestClaudeCommands_ShouldKeepSystemPromptOutOfArgv(t *testing.T) {
	for name, cmd := range map[string]string{
		"StartCommand":              Claude.StartCommand(),
		"ContinueCommand":           Claude.ContinueCommand(),
		"ContinueWithPromptCommand": Claude.ContinueWithPromptCommand(),
	} {
		if strings.Contains(cmd, `"$SYSTEM_PROMPT"`) || strings.Contains(cmd, "--system-prompt ") {
			t.Errorf("Claude.%s() = %q, must pass the prompt via --system-prompt-file, not argv", name, cmd)
		}
		if !strings.Contains(cmd, `--system-prompt-file "$SYSTEM_PROMPT_FILE"`) {
			t.Errorf("Claude.%s() = %q, want --system-prompt-file \"$SYSTEM_PROMPT_FILE\"", name, cmd)
		}
	}
}

func TestSystemPromptFileBlock_ShouldBeSprintfSafe_AndUseItsInputs(t *testing.T) {
	if strings.Contains(SystemPromptFileBlock, "%") {
		t.Error("SystemPromptFileBlock is embedded in Sprintf format strings and must not contain percent signs")
	}
	for _, want := range []string{"$AGENT_ID", "$SYSTEM_PROMPT\n", `SYSTEM_PROMPT_FILE="`, "-system-prompt.txt"} {
		if !strings.Contains(SystemPromptFileBlock, want) {
			t.Errorf("SystemPromptFileBlock must contain %q", want)
		}
	}
}

// The block hands the prompt to the file through an unquoted heredoc, which
// expands $SYSTEM_PROMPT exactly once. Prove that shell-significant content in
// the prompt (dollars, backticks, quotes, backslashes, percent signs, a
// leading dash) reaches the file verbatim rather than being expanded again or
// treated as options.
func TestSystemPromptFileBlock_ShouldWritePromptVerbatim(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	home := t.TempDir()
	prompt := "-n first line with $HOME and ${UNSET:-x} and `whoami` and $(id)\n" +
		"second line: 100%% done, a \\backslash\\, \"double\" and 'single' quotes\n" +
		"third line\ttab # not a comment\n" +
		"CCMUX_SYSTEM_PROMPT_EOF is fine mid-line\n" +
		"last line"
	script := "set -e\nAGENT_ID=abc123\nSYSTEM_PROMPT=\"$1\"\n" + SystemPromptFileBlock +
		"cat \"$SYSTEM_PROMPT_FILE\"\n"
	cmd := exec.Command(bash, "-c", script, "bash", prompt)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("block failed: %v\n%s", err, out)
	}
	if got := strings.TrimSuffix(string(out), "\n"); got != prompt {
		t.Errorf("prompt round-trip mismatch:\n got: %q\nwant: %q", got, prompt)
	}
	if _, err := os.Stat(filepath.Join(home, ".ccmux", "launchers", "abc123-system-prompt.txt")); err != nil {
		t.Errorf("prompt file not written where launcher cleanup expects it: %v", err)
	}
}

func TestTelemetryEnvBlock_ShouldBeSprintfSafe_AndClaudeOnly(t *testing.T) {
	if strings.Contains(TelemetryEnvBlock, "%") {
		t.Error("TelemetryEnvBlock is embedded in Sprintf format strings and must not contain percent signs")
	}
	if !strings.Contains(TelemetryEnvBlock, `[ "$HARNESS" = "claude" ]`) {
		t.Error("TelemetryEnvBlock should only enable telemetry for the Claude harness")
	}
}
