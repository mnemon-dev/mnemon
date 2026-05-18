#!/usr/bin/env bash
set -euo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_DIR="$(cd "${HOOK_DIR}/../.." && pwd)"
ENV_PATH="${MNEMON_SKILL_LOOP_ENV:-${CONFIG_DIR}/mnemon-skill-loop/env.sh}"
if [[ -f "${ENV_PATH}" ]]; then
  # shellcheck source=/dev/null
  source "${ENV_PATH}"
fi

USAGE_FILE="${MNEMON_SKILL_LOOP_USAGE_FILE:-${CONFIG_DIR}/mnemon-skill-loop/skills/.usage.jsonl}"
REVIEW_MIN_EVENTS="${MNEMON_SKILL_LOOP_REVIEW_MIN_EVENTS:-20}"

if [[ -f "${USAGE_FILE}" ]]; then
  EVENT_COUNT="$(grep -cv '^[[:space:]]*$' "${USAGE_FILE}" || true)"
else
  EVENT_COUNT=0
fi

if [[ "${EVENT_COUNT}" -ge "${REVIEW_MIN_EVENTS}" ]]; then
  echo "[mnemon-skill-loop] ${EVENT_COUNT} skill evidence event(s) recorded; consider skill_curate or mnemon-skill-curator before/after compaction."
else
  echo "[mnemon-skill-loop] Compact boundary: consider skill_curate only if this session produced meaningful skill lifecycle evidence."
fi
