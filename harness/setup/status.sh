#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Show Mnemon harness projection status for a host runtime.

Usage:
  status.sh --host HOST [--module MODULE ...] [host options]

Examples:
  bash harness/setup/status.sh --host claude-code
  bash harness/setup/status.sh --host claude-code --module memory-loop
USAGE
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

HOST=""
MODULES=()
HOST_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)
      HOST="${2:?missing value for --host}"
      shift 2
      ;;
    --module)
      MODULES+=("${2:?missing value for --module}")
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      HOST_ARGS+=("$1")
      shift
      ;;
  esac
done

if [[ -z "${HOST}" ]]; then
  echo "--host is required" >&2
  usage >&2
  exit 2
fi
if [[ "${#MODULES[@]}" -eq 0 ]]; then
  MODULES=("memory-loop" "skill-loop")
fi

PROJECTOR="${SCRIPT_DIR}/../hosts/${HOST}/projector.sh"
if [[ ! -x "${PROJECTOR}" ]]; then
  echo "unsupported host or missing projector: ${HOST}" >&2
  exit 1
fi

for module in "${MODULES[@]}"; do
  "${PROJECTOR}" status --module "${module}" "${HOST_ARGS[@]}"
done
