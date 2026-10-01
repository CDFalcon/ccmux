package harness

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// OpenCode support.
//
// OpenCode (github.com/anomalyco/opencode, MIT) is the open-source harness
// ccmux drives for OpenAI's GPT-6 Astra outside Codex: it ships an
// Astra-specific system prompt and authenticates either with an OpenAI API
// key or a ChatGPT Plus/Pro login (`opencode auth login`).
//
// Every OpenCode launch goes through the hidden `ccmux run-opencode` wrapper
// rather than invoking `opencode` straight from the launcher scripts, because
// three things have to happen around the CLI that a shell one-liner cannot do
// cleanly:
//
//   - The ccmux system prompt reaches OpenCode as an `instructions` file and
//     the ccmux plugin as a `plugin` entry, both injected through
//     OPENCODE_CONFIG_CONTENT so neither the worktree nor the user's
//     opencode.json is touched. OpenCode re-reads instructions on every turn,
//     so a resumed session keeps the prompt too.
//
//   - Resuming must target the agent's own session. `opencode --continue`
//     cannot be used: OpenCode keys projects by the repository's root commit,
//     every ccmux worktree of a project is the same repository, and from a
//     worktree's root the session list is filtered by project only — so
//     --continue resumes whichever sibling agent ran most recently (the same
//     trap Codex's "worktrees" feature sets, see CodexResumeLast). The plugin
//     records the agent's root session id in OpenCodeSessionFile and the
//     wrapper resumes it with `--session <id>`.
//
//   - OpenCode's TUI only auto-submits --prompt on its home screen; with
//     --session the prompt is silently dropped. Follow-up instructions (PR
//     review, CI fix, merge conflict, reload) are therefore handed to the
//     plugin, which submits them to the resumed session through OpenCode's
//     own server API once it is up.
//
// The plugin also stands in for Claude Code's hooks: it runs
// `ccmux agent-stopped` when the agent's session goes idle (the Stop hook)
// and `ccmux ci-wait` after a bash `gh pr create` / `git push` (the
// PostToolUse hook), so OpenCode agents move through the same status machine.

// OpenCodeDefaultModel is the model ccmux asks OpenCode for when the user has
// not chosen one: GPT-6 Astra through the OpenAI provider, which serves both
// API-key and ChatGPT-subscription logins.
const OpenCodeDefaultModel = "openai/gpt-6-astra"

// OpenCodeModelEnv overrides OpenCodeDefaultModel. Set it to another
// provider/model id to use that instead, or to the empty string to pass no
// --model at all and let OpenCode's own config (opencode.json "model") decide.
const OpenCodeModelEnv = "CCMUX_OPENCODE_MODEL"

// OpenCodeModel returns the provider/model id the wrapper passes to
// `opencode --model`, or "" to defer to OpenCode's configuration.
func OpenCodeModel() string {
	if v, ok := os.LookupEnv(OpenCodeModelEnv); ok {
		return strings.TrimSpace(v)
	}
	return OpenCodeDefaultModel
}

// Environment variables the wrapper hands to the plugin. The plugin runs
// inside the opencode process, so it inherits them.
const (
	openCodeSessionFileEnv = "CCMUX_OPENCODE_SESSION_FILE"
	openCodeResumeIDEnv    = "CCMUX_OPENCODE_RESUME_ID"
	openCodePromptFileEnv  = "CCMUX_OPENCODE_PROMPT_FILE"
	openCodeModelIDEnv     = "CCMUX_OPENCODE_MODEL_ID"
)

func launcherDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ccmux", "launchers"), nil
}

// OpenCodeSessionFile is where the plugin records the agent's current root
// session id: ~/.ccmux/launchers/<id>-opencode-session.txt, beside the
// agent's other launcher files so it is removed with them.
func OpenCodeSessionFile(agentID string) (string, error) {
	dir, err := launcherDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, agentID+"-opencode-session.txt"), nil
}

// openCodePromptFile holds a follow-up instruction for the plugin to submit
// to a resumed session. The plugin deletes it once read, so a later plain
// restart never replays it.
func openCodePromptFile(agentID string) (string, error) {
	dir, err := launcherDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, agentID+"-opencode-prompt.txt"), nil
}

// systemPromptFile mirrors SystemPromptFileBlock's path.
func systemPromptFile(agentID string) (string, error) {
	dir, err := launcherDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, agentID+"-system-prompt.txt"), nil
}

// openCodePluginPath is where the wrapper writes the plugin. It is shared by
// every agent and rewritten on each launch, so an upgraded ccmux binary
// always runs its own plugin.
func openCodePluginPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ccmux", "opencode", "ccmux-plugin.js"), nil
}

// writeFileIfChanged avoids rewriting the shared plugin under a sibling
// agent's running opencode on every launch.
func writeFileIfChanged(path string, data []byte, mode os.FileMode) error {
	if cur, err := os.ReadFile(path); err == nil && string(cur) == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// OpenCodeConfigContent merges ccmux's plugin and instructions file into an
// existing OPENCODE_CONFIG_CONTENT value (the user may already route config
// through it), returning the JSON to export. OpenCode concatenates the
// plugin and instructions arrays across config sources, so this adds to the
// user's opencode.json rather than replacing anything in it.
func OpenCodeConfigContent(existing, pluginPath, instructionsFile string) (string, error) {
	cfg := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &cfg); err != nil {
			return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT is not a JSON object: %w", err)
		}
	}
	appendUnique := func(key, value string) {
		list, _ := cfg[key].([]any)
		for _, v := range list {
			if s, ok := v.(string); ok && s == value {
				return
			}
		}
		cfg[key] = append(list, value)
	}
	if pluginPath != "" {
		appendUnique("plugin", (&url.URL{Scheme: "file", Path: pluginPath}).String())
	}
	if instructionsFile != "" {
		appendUnique("instructions", instructionsFile)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// OpenCodeLaunch is everything `ccmux run-opencode` needs to exec opencode.
type OpenCodeLaunch struct {
	Args []string // argv, starting with "opencode"
	Env  []string // full environment for the exec
}

// PrepareOpenCodeLaunch writes the plugin (and, for a resume with a
// follow-up, the prompt file) and assembles opencode's argv and environment.
//
// resume asks for the agent's recorded session; when none is recorded yet
// (the agent has never run on OpenCode) the launch falls back to a fresh
// session seeded with prompt, mirroring CodexResumeLast. prompt may be empty
// only on a fresh start that should open the TUI idle.
func PrepareOpenCodeLaunch(agentID string, resume bool, prompt string, environ []string) (*OpenCodeLaunch, error) {
	pluginPath, err := openCodePluginPath()
	if err != nil {
		return nil, err
	}
	if err := writeFileIfChanged(pluginPath, []byte(OpenCodePluginJS), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write the ccmux OpenCode plugin: %w", err)
	}

	env := make([]string, 0, len(environ)+6)
	existingConfig := ""
	for _, kv := range environ {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		switch key {
		case "OPENCODE_CONFIG_CONTENT":
			existingConfig = strings.TrimPrefix(kv, key+"=")
			continue
		case openCodeSessionFileEnv, openCodeResumeIDEnv, openCodePromptFileEnv, openCodeModelIDEnv:
			continue
		}
		env = append(env, kv)
	}

	instructions := ""
	if agentID != "" {
		if p, err := systemPromptFile(agentID); err == nil {
			if _, statErr := os.Stat(p); statErr == nil {
				instructions = p
			}
		}
	}
	content, err := OpenCodeConfigContent(existingConfig, pluginPath, instructions)
	if err != nil {
		return nil, err
	}
	env = append(env, "OPENCODE_CONFIG_CONTENT="+content)

	args := []string{"opencode", "--auto"}
	model := OpenCodeModel()
	if model != "" {
		args = append(args, "--model", model)
		env = append(env, openCodeModelIDEnv+"="+model)
	}

	resumeID := ""
	if agentID != "" {
		sessionFile, err := OpenCodeSessionFile(agentID)
		if err != nil {
			return nil, err
		}
		env = append(env, openCodeSessionFileEnv+"="+sessionFile)
		if resume {
			if data, err := os.ReadFile(sessionFile); err == nil {
				resumeID = strings.TrimSpace(string(data))
			}
		}
		// A prompt file left by an earlier launch that died before the
		// plugin picked it up must not be delivered to this one.
		if promptFile, err := openCodePromptFile(agentID); err == nil {
			os.Remove(promptFile)
		}
	}

	if resumeID != "" {
		args = append(args, "--session", resumeID)
		env = append(env, openCodeResumeIDEnv+"="+resumeID)
		if prompt != "" {
			promptFile, err := openCodePromptFile(agentID)
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(promptFile), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(promptFile, []byte(prompt), 0o600); err != nil {
				return nil, fmt.Errorf("failed to write the follow-up prompt: %w", err)
			}
			env = append(env, openCodePromptFileEnv+"="+promptFile)
		}
	} else if prompt != "" {
		args = append(args, "--prompt", prompt)
	}

	return &OpenCodeLaunch{Args: args, Env: env}, nil
}

// OpenCodeResumeMessage is the first message of a plain resume (session
// recovery, restart). The full context — the original task and the "you are
// resuming" explanation — is in the refreshed system prompt OpenCode reads as
// an instructions file, so unlike Codex the prompt never travels in argv.
const OpenCodeResumeMessage = "Your ccmux session was restarted. Continue where you left off; your task and the reason for the restart are in your instructions. If your conversation history is not visible, review your progress with git log, git status and git diff first."

// OpenCodePluginJS is the OpenCode plugin ccmux installs at
// ~/.ccmux/opencode/ccmux-plugin.js. It is inert unless CCMUX_AGENT_ID is
// set, so a user who points their own config at it is unaffected.
const OpenCodePluginJS = `// ccmux OpenCode plugin. Generated by ccmux and rewritten on every agent
// launch; edits here are lost. See internal/harness/opencode.go.
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { dirname } from "node:path"
import { spawn } from "node:child_process"

const AGENT_ID = process.env.CCMUX_AGENT_ID || ""
const SESSION_FILE = process.env.CCMUX_OPENCODE_SESSION_FILE || ""
const RESUME_ID = process.env.CCMUX_OPENCODE_RESUME_ID || ""
const PROMPT_FILE = process.env.CCMUX_OPENCODE_PROMPT_FILE || ""
const MODEL_ID = process.env.CCMUX_OPENCODE_MODEL_ID || ""

// Fire-and-forget: a ccmux call must never block or fail the agent's turn.
function ccmux(args) {
  try {
    const child = spawn("ccmux", args, { detached: true, stdio: "ignore" })
    child.on("error", () => {})
    child.unref()
  } catch {}
}

function model() {
  const i = MODEL_ID.indexOf("/")
  if (i <= 0) return undefined
  return { providerID: MODEL_ID.slice(0, i), modelID: MODEL_ID.slice(i + 1) }
}

const PR_CREATE = /(^|[\s&;|(])gh\s+pr\s+create(\s|$)/
const GIT_PUSH = /(^|[\s&;|(])git\s+push(\s|$)/
const PR_URL = /https:\/\/github\.com\/[^\s]+\/pull\/[0-9]+/g

export const CcmuxPlugin = async ({ client }) => {
  if (!AGENT_ID) return {}

  // Sub-agent (task tool) sessions have a parentID. They go busy and idle
  // inside the agent's own turn, so only the root session's idle means the
  // agent stopped.
  const children = new Set()
  const busy = new Set()
  let recorded = RESUME_ID

  function record(id) {
    if (!SESSION_FILE || id === recorded) return
    recorded = id
    try {
      mkdirSync(dirname(SESSION_FILE), { recursive: true })
      writeFileSync(SESSION_FILE, id + "\n")
    } catch {}
  }

  // A follow-up for the resumed session: the TUI drops --prompt when given
  // --session, so submit it ourselves once the server answers.
  if (RESUME_ID && PROMPT_FILE) {
    let text = ""
    try {
      text = readFileSync(PROMPT_FILE, "utf8")
      rmSync(PROMPT_FILE, { force: true })
    } catch {}
    if (text.trim()) {
      let tries = 0
      const send = async () => {
        tries++
        try {
          const res = await client.session.promptAsync({
            path: { id: RESUME_ID },
            body: { parts: [{ type: "text", text }], model: model() },
          })
          if (!res || !res.error) return
        } catch {}
        if (tries < 60) setTimeout(send, 1000)
      }
      setTimeout(send, 500)
    }
  }

  return {
    event: async ({ event }) => {
      const p = event.properties || {}
      if (event.type === "session.created" || event.type === "session.updated") {
        const info = p.info || {}
        if (info.id && info.parentID) children.add(info.id)
        return
      }
      if (event.type !== "session.status") return
      const id = p.sessionID
      if (!id || children.has(id)) return
      const type = p.status && p.status.type
      if (type === "busy") {
        busy.add(id)
        record(id)
      } else if (type === "idle" && busy.delete(id)) {
        ccmux(["agent-stopped", AGENT_ID])
      }
    },
    "tool.execute.after": async (input, output) => {
      if (!input || input.tool !== "bash") return
      const command = String((input.args && input.args.command) || "")
      if (PR_CREATE.test(command)) {
        const urls = String((output && output.output) || "").match(PR_URL)
        if (urls) ccmux(["ci-wait", urls[urls.length - 1]])
        return
      }
      if (GIT_PUSH.test(command)) ccmux(["ci-wait"])
    },
  }
}
`
