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
SESSION_ID="$(printf '%s' "${INPUT}" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
MARKER_DIR="${TMPDIR:-/tmp}/mnemon-memory-loop"
MARKER="${MARKER_DIR}/compact-${SESSION_ID:-unknown}"

mkdir -p "${MARKER_DIR}"

if [[ -f "${MARKER}" ]]; then
  rm -f "${MARKER}"
  exit 0
fi

touch "${MARKER}"
MEMORY_DIR="${MNEMON_MEMORY_LOOP_DIR:-}"
MEMORY_FILE="${MEMORY_DIR}/MEMORY.md"
MAX_NON_EMPTY_LINES="${MNEMON_MEMORY_LOOP_MAX_NON_EMPTY_LINES:-200}"

if [[ -n "${MEMORY_DIR}" && -f "${MEMORY_FILE}" ]]; then
  NON_EMPTY_LINES="$(grep -cv '^[[:space:]]*$' "${MEMORY_FILE}" || true)"
else
  NON_EMPTY_LINES=0
fi

if [[ "${NON_EMPTY_LINES}" -gt "${MAX_NON_EMPTY_LINES}" ]]; then
  REASON="[mnemon-memory-loop] Compact: MEMORY.md has ${NON_EMPTY_LINES} non-empty lines. Before compaction, spawn mnemon-dreaming to write durable content to Mnemon and compact MEMORY.md, then retry compaction."
else
  REASON="[mnemon-memory-loop] Compact: MNEMON_MEMORY_LOOP_DIR=${MEMORY_DIR:-unset}. Before compaction, preserve critical continuity with memory_set when needed. If this boundary should consolidate working memory, spawn mnemon-dreaming, then retry compaction."
fi

cat <<JSON
{
  "decision": "block",
  "reason": "${REASON}"
}
JSON
