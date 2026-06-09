package hookscript

// InstallCodexPrePush installs a worktree-local pre-push hook for Codex
// agents. Codex does not expose a Bash PostToolUse hook, so this hook spawns a
// background watcher that waits until the pushed branch's remote SHA matches
// the local SHA before handing the agent back to ccmux's CI poller.
const InstallCodexPrePush = `
# Codex has no Bash tool hook. Use Git's pre-push hook to detect successful
# branch pushes and hand the PR back to the CI poller.
if [ "$HARNESS" = "codex" ]; then
  (
  set +e
  echo "Installing Codex Git push hook..."
  CCMUX_PRE_PUSH_HOOK=$(git rev-parse --git-path hooks/pre-push 2>/dev/null || true)
  if [ -z "$CCMUX_PRE_PUSH_HOOK" ] || [[ "$CCMUX_PRE_PUSH_HOOK" == /dev/null/* ]]; then
    echo "Codex Git push hook skipped"
    exit 0
  fi

  CCMUX_PRE_PUSH_DIR=$(dirname "$CCMUX_PRE_PUSH_HOOK")
  CCMUX_USER_PRE_PUSH_HOOK="$CCMUX_PRE_PUSH_HOOK.ccmux-user"
  mkdir -p "$CCMUX_PRE_PUSH_DIR" || exit 0

  if [ -f "$CCMUX_PRE_PUSH_HOOK" ] && ! grep -q "ccmux git pre-push hook" "$CCMUX_PRE_PUSH_HOOK"; then
    cp "$CCMUX_PRE_PUSH_HOOK" "$CCMUX_USER_PRE_PUSH_HOOK" || exit 0
    if [ -x "$CCMUX_PRE_PUSH_HOOK" ]; then
      chmod +x "$CCMUX_USER_PRE_PUSH_HOOK"
    fi
  fi

  printf '%s\n' "$AGENT_ID" > "$CCMUX_PRE_PUSH_HOOK.ccmux-agent-id" || exit 0

  if cat > "$CCMUX_PRE_PUSH_HOOK" << 'CCMUXHOOK'
#!/bin/bash
# ccmux git pre-push hook: after a successful Codex branch push reaches the
# remote, ask ccmux to monitor CI for this agent.
set -u

REMOTE_NAME="${1:-origin}"
TMP_INPUT=$(mktemp "${TMPDIR:-/tmp}/ccmux-pre-push.XXXXXX") || exit 0
cat > "$TMP_INPUT"

USER_HOOK="$0.ccmux-user"
if [ -x "$USER_HOOK" ]; then
  "$USER_HOOK" "$@" < "$TMP_INPUT"
  USER_STATUS=$?
  if [ "$USER_STATUS" -ne 0 ]; then
    rm -f "$TMP_INPUT"
    exit "$USER_STATUS"
  fi
fi

CCMUX_HOOK_AGENT_ID="$(cat "$0.ccmux-agent-id" 2>/dev/null || true)"
CCMUX_HOOK_AGENT_ID="${CCMUX_HOOK_AGENT_ID:-${CCMUX_AGENT_ID:-}}"
if [ -z "$CCMUX_HOOK_AGENT_ID" ] || ! command -v ccmux >/dev/null 2>&1; then
  rm -f "$TMP_INPUT"
  exit 0
fi

while read -r LOCAL_REF LOCAL_OID REMOTE_REF REMOTE_OID; do
  case "$LOCAL_OID" in
    ""|0000000000000000000000000000000000000000)
      continue
      ;;
  esac
  case "$REMOTE_REF" in
    refs/heads/*)
      ;;
    *)
      continue
      ;;
  esac

  (
    for _ in {1..60}; do
      REMOTE_OID_NOW=$(git ls-remote "$REMOTE_NAME" "$REMOTE_REF" 2>/dev/null | awk '{print $1}' | head -n1)
      if [ "$REMOTE_OID_NOW" = "$LOCAL_OID" ]; then
        CCMUX_AGENT_ID="$CCMUX_HOOK_AGENT_ID" ccmux ci-wait >/dev/null 2>&1
        exit 0
      fi
      sleep 5
    done
  ) >/dev/null 2>&1 </dev/null &
done < "$TMP_INPUT"

rm -f "$TMP_INPUT"
exit 0
CCMUXHOOK
  then
    chmod +x "$CCMUX_PRE_PUSH_HOOK" && echo "Codex Git push hook installed"
  fi
  ) || true
  echo ""
fi
`
