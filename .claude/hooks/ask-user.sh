#!/bin/bash
INPUT=$(cat)
QUESTIONS=$(echo "$INPUT" | jq -r '.tool_input.questions[]?.question // empty' 2>/dev/null | head -5 | tr '\n' ' ')
if [ -n "$QUESTIONS" ]; then
  ccmux need-help "$QUESTIONS" 2>/dev/null || true
fi
