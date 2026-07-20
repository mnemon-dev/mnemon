#!/bin/bash
# mnemon Codex SessionStart hook
# Same context as before (memory-active status / missing-binary warning / guide),
# wrapped in one SessionStart JSON object so Codex does not mis-parse the leading
# "[mnemon]". python3 is already required by the sibling stop.sh.
PROMPT_DIR="${MNEMON_DATA_DIR:-$HOME/.mnemon}/prompt"
# Fall back to legacy location if the resolved path has no guide yet but the
# legacy ~/.mnemon/prompt/ does — preserves existing installs that pre-date
# MNEMON_DATA_DIR-aware setup.
if [ ! -f "${PROMPT_DIR}/guide.md" ] && [ -f "${HOME}/.mnemon/prompt/guide.md" ]; then
  PROMPT_DIR="${HOME}/.mnemon/prompt"
fi

{
  if ! command -v mnemon >/dev/null 2>&1; then
    echo "[mnemon] Warning: mnemon not found in PATH."
  else
    STATS=$(mnemon status 2>/dev/null)
    if [ -n "$STATS" ]; then
      INSIGHTS=$(echo "$STATS" | sed -n 's/.*"total_insights": *\([0-9]*\).*/\1/p' | head -1)
      EDGES=$(echo "$STATS" | sed -n 's/.*"edge_count": *\([0-9]*\).*/\1/p' | head -1)
      echo "[mnemon] Memory active (${INSIGHTS:-0} insights, ${EDGES:-0} edges)."
    else
      echo "[mnemon] Memory active."
    fi
  fi

  [ -f "${PROMPT_DIR}/guide.md" ] && cat "${PROMPT_DIR}/guide.md"
} | python3 -c 'import json, sys; print(json.dumps({"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": sys.stdin.read()}}))'
