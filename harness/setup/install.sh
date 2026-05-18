#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Install Mnemon harness modules into a host runtime.

Usage:
  install.sh --host HOST --module MODULE [--module MODULE ...] [host options]

Examples:
  bash harness/setup/install.sh --host claude-code --module memory-loop
  bash harness/setup/install.sh --host claude-code --module skill-loop --global
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
  echo "at least one --module is required" >&2
  usage >&2
  exit 2
fi

PROJECTOR="${SCRIPT_DIR}/../hosts/${HOST}/projector.sh"
if [[ ! -x "${PROJECTOR}" ]]; then
  echo "unsupported host or missing projector: ${HOST}" >&2
  exit 1
fi

for module in "${MODULES[@]}"; do
  "${PROJECTOR}" install --module "${module}" "${HOST_ARGS[@]}"
done
