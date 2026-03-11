// Package windowlayout handles parsing window layout configs for agent windows.
package windowlayout

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// PaneDef defines a single additional tmux pane to create in an agent window.
type PaneDef struct {
	// Direction of the split: "h" (horizontal/left-right) or "v" (vertical/top-bottom).
	// Defaults to "h".
	Direction string `yaml:"direction"`
	// Size of the new pane, e.g. "40%" or "40". Defaults to "50%".
	Size string `yaml:"size"`
	// Command to run in the pane. Empty means an interactive shell.
	// Shell variables like $WORKTREE_PATH and $AGENT_ID are expanded.
	Command string `yaml:"command"`
}

// Config defines the window layout for agent windows.
type Config struct {
	Panes []PaneDef `yaml:"panes"`
}

// Load reads and parses a window layout YAML config file.
// Supports ~ in path.
func Load(path string) (*Config, error) {
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(homeDir, path[2:])
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read window layout config %q: %w", path, err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse window layout config %q: %w", path, err)
	}

	for i := range config.Panes {
		dir := strings.ToLower(strings.TrimSpace(config.Panes[i].Direction))
		switch dir {
		case "horizontal", "h", "":
			config.Panes[i].Direction = "h"
		case "vertical", "v":
			config.Panes[i].Direction = "v"
		default:
			config.Panes[i].Direction = "h"
		}
		if config.Panes[i].Size == "" {
			config.Panes[i].Size = "50%"
		}
	}

	return &config, nil
}

// GenerateSetupScript returns a bash snippet that creates the additional panes.
// The snippet uses $MAIN_PANE and $WORKTREE_PATH variables which must be set
// in the calling script before this snippet runs.
func (c *Config) GenerateSetupScript() string {
	if len(c.Panes) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Create additional window panes (from window layout config)\n")
	for _, pane := range c.Panes {
		if pane.Command == "" {
			sb.WriteString(fmt.Sprintf(
				`tmux split-window -%s -t "$MAIN_PANE" -c "$WORKTREE_PATH" -l %s 2>/dev/null || true`+"\n",
				pane.Direction, pane.Size,
			))
		} else {
			// Double-quote the command; escape backslash and double-quote chars.
			// Shell variables in the command (e.g. $WORKTREE_PATH) are expanded by bash.
			escaped := strings.ReplaceAll(pane.Command, `\`, `\\`)
			escaped = strings.ReplaceAll(escaped, `"`, `\"`)
			sb.WriteString(fmt.Sprintf(
				`tmux split-window -%s -t "$MAIN_PANE" -c "$WORKTREE_PATH" -l %s "%s" 2>/dev/null || true`+"\n",
				pane.Direction, pane.Size, escaped,
			))
		}
	}
	sb.WriteString(`tmux select-pane -t "$MAIN_PANE"` + "\n")
	return sb.String()
}
