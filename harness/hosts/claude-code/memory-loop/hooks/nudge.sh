#!/usr/bin/env bash
set -euo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_DIR="$(cd "${HOOK_DIR}/../.." && pwd)"
ENV_PATH="${MNEMON_MEMORY_LOOP_ENV:-${CONFIG_DIR}/mnemon-memory-loop/env.sh}"
if [[ -f "${ENV_PATH}" ]]; then
  # shellcheck source=/dev/null
  source "${ENV_PATH}"
fi

INPUT="$(cat)"
MEMORY_DIR="${MNEMON_MEMORY_LOOP_DIR:-}"
MEMORY_FILE="${MEMORY_DIR}/MEMORY.md"
MAX_NON_EMPTY_LINES="${MNEMON_MEMORY_LOOP_MAX_NON_EMPTY_LINES:-200}"

if printf '%s' "${INPUT}" | grep -q '"stop_hook_active"[[:space:]]*:[[:space:]]*true'; then
  exit 0
fi

if [[ -n "${MEMORY_DIR}" && -f "${MEMORY_FILE}" ]]; then
  NON_EMPTY_LINES="$(grep -cv '^[[:space:]]*$' "${MEMORY_FILE}" || true)"
else
  NON_EMPTY_LINES=0
fi

if [[ "${NON_EMPTY_LINES}" -gt "${MAX_NON_EMPTY_LINES}" ]]; then
  echo "[mnemon-memory-loop] MEMORY.md is long (${NON_EMPTY_LINES} lines); consider mnemon-dreaming."
else
  echo "[mnemon-memory-loop] Consider: does this exchange warrant memory_set?"
fi
