#!/usr/bin/env bash
set -euo pipefail

if cat | grep -q '"stop_hook_active"[[:space:]]*:[[:space:]]*true'; then
  exit 0
fi

echo "[mnemon-skill-loop] Apply GUIDE.md; if this turn produced skill evidence or reusable workflow signal, load skill_observe."
