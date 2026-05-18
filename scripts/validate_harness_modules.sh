#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
MODULES_DIR="${ROOT_DIR}/harness/modules"

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 1
fi

validate_module() {
  local module_dir="$1"
  local manifest="${module_dir}/module.json"
  local name

  if [[ ! -f "${manifest}" ]]; then
    echo "missing module manifest: ${manifest}" >&2
    return 1
  fi

  jq . "${manifest}" >/dev/null
  name="$(jq -r '.name // empty' "${manifest}")"
  if [[ -z "${name}" ]]; then
    echo "module manifest missing name: ${manifest}" >&2
    return 1
  fi

  while IFS= read -r rel; do
    [[ -n "${rel}" ]] || continue
    if [[ ! -e "${module_dir}/${rel}" ]]; then
      echo "missing ${name} asset: ${rel}" >&2
      return 1
    fi
  done < <(
    jq -r '
      .assets.guide,
      .assets.env,
      ((.assets.runtime_files // [])[]),
      (.assets.hooks[]),
      (.assets.skills[]),
      (.assets.subagents[])
    ' "${manifest}"
  )

  while IFS= read -r rel; do
    [[ -n "${rel}" ]] || continue
    if [[ ! -e "${module_dir}/${rel}" ]]; then
      echo "missing ${name} host adapter path: ${rel}" >&2
      return 1
    fi
  done < <(jq -r '.host_adapters[]' "${manifest}")

  echo "ok ${name}"
}

for module_dir in "${MODULES_DIR}"/*; do
  [[ -d "${module_dir}" ]] || continue
  validate_module "${module_dir}"
done
