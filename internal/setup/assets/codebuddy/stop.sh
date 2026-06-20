#!/bin/bash
INPUT=$(cat)

if echo "$INPUT" | grep -q '"stop_hook_active"[[:space:]]*:[[:space:]]*true'; then
  exit 0
fi

SESSION_ID=$(echo "$INPUT" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
if [ -z "$SESSION_ID" ]; then
  SESSION_ID=$(echo "$INPUT" | sed -n 's/.*"cwd"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1 | sed 's/[^A-Za-z0-9_.-]/_/g')
fi
if [ -z "$SESSION_ID" ]; then
  SESSION_ID="unknown"
fi

STATE_DIR="${MNEMON_DATA_DIR:-$HOME/.mnemon}/hooks"
STATE_FILE="${STATE_DIR}/codebuddy-stop-${SESSION_ID}.seen"
mkdir -p "$STATE_DIR" 2>/dev/null || true

if [ -f "$STATE_FILE" ]; then
  rm -f "$STATE_FILE" 2>/dev/null || true
  exit 0
fi

touch "$STATE_FILE" 2>/dev/null || true
cat <<'JSON'
{
  "continue": false,
  "reason": "[mnemon] Before stopping, evaluate whether this exchange contains durable preferences, decisions, insights, facts, or context worth remembering. If yes, run mnemon remember/link; if no, state that no memory update is needed, then finish."
}
JSON
