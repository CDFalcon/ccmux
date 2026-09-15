package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/CDFalcon/ccmux/internal/harness"
)

// The TUI's restart script is the fourth generator that starts a Claude
// harness (with the spawn, recovery and reload scripts in cmd/ccmux). It must
// hand the system prompt over through a file, not argv — see
// harness.SystemPromptFileBlock for the sibling-killing pkill this prevents.
func TestWriteRestartScript_ShouldPassSystemPromptViaFile(t *testing.T) {
	path, err := writeRestartScript("spf-restart", "/tmp/repo/wt", "origin/main", "task", harness.Claude, true)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	block := strings.Index(content, harness.SystemPromptFileBlock)
	if block < 0 {
		t.Fatal("restart script does not write the system prompt file")
	}
	lastAppend := strings.LastIndex(content, "${PROMPTS_CONTENT}")
	call := strings.Index(content, `--system-prompt-file "$SYSTEM_PROMPT_FILE"`)
	if call < 0 {
		t.Fatal("restart script does not pass --system-prompt-file to claude")
	}
	if !(lastAppend < block && block < call) {
		t.Errorf("prompt file must be written after the last SYSTEM_PROMPT append and before the harness call (append@%d block@%d call@%d)", lastAppend, block, call)
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "claude ") && strings.Contains(line, `$SYSTEM_PROMPT"`) {
			t.Errorf("restart script puts the system prompt in claude's argv: %q", line)
		}
	}
}
