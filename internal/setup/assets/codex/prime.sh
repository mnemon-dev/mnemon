#!/bin/bash
PROMPT_DIR="${HOME}/.mnemon/prompt"

if ! command -v mnemon >/dev/null 2>&1; then
  echo "[mnemon] Warning: mnemon not found in PATH."
  [ -f "${PROMPT_DIR}/guide.md" ] && cat "${PROMPT_DIR}/guide.md"
  exit 0
fi

STATS=$(mnemon status 2>/dev/null)
if [ -n "$STATS" ]; then
  INSIGHTS=$(echo "$STATS" | sed -n 's/.*"total_insights": *\([0-9]*\).*/\1/p' | head -1)
  EDGES=$(echo "$STATS" | sed -n 's/.*"edge_count": *\([0-9]*\).*/\1/p' | head -1)
  echo "[mnemon] Memory active (${INSIGHTS:-0} insights, ${EDGES:-0} edges)."
else
  echo "[mnemon] Memory active."
fi

[ -f "${PROMPT_DIR}/guide.md" ] && cat "${PROMPT_DIR}/guide.md"
