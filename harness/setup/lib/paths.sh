#!/usr/bin/env bash
set -euo pipefail

mnemon_setup_dir() {
  cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd
}

mnemon_harness_dir() {
  cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd
}

mnemon_repo_root() {
  cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd
}

mnemon_module_dir() {
  local module="$1"
  local harness_dir
  harness_dir="$(mnemon_harness_dir)"
  printf '%s/modules/%s\n' "${harness_dir}" "${module}"
}

mnemon_host_dir() {
  local host="$1"
  local harness_dir
  harness_dir="$(mnemon_harness_dir)"
  printf '%s/hosts/%s\n' "${harness_dir}" "${host}"
}

mnemon_project_mnemon_dir() {
  printf '%s\n' "${MNEMON_HARNESS_STATE_DIR:-.mnemon}"
}
