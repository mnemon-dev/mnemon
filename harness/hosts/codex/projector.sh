#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Project Mnemon harness modules into Codex.

Usage:
  projector.sh install   --module MODULE [options]
  projector.sh status    --module MODULE [options]
  projector.sh uninstall --module MODULE [options]

Common options:
  --global
  --config-dir DIR

Memory loop install options:
  --store NAME

Skill loop install options:
  --host-skills-dir DIR

Eval loop install options:
  --host-skills-dir DIR

Uninstall options:
  --purge-memory
  --purge-library
USAGE
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../../setup/lib/paths.sh
source "${SCRIPT_DIR}/../../setup/lib/paths.sh"

ACTION="${1:-}"
if [[ -z "${ACTION}" ]]; then
  usage >&2
  exit 2
fi
shift

MODULE=""
CONFIG_DIR=".codex"
CONFIG_DIR_EXPLICIT=0
GLOBAL=0
STORE_NAME=""
HOST_SKILLS_DIR=""
PURGE_MEMORY=0
PURGE_LIBRARY=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --module)
      MODULE="${2:?missing value for --module}"
      shift 2
      ;;
    --global)
      GLOBAL=1
      CONFIG_DIR="${HOME}/.codex"
      shift
      ;;
    --config-dir)
      CONFIG_DIR="${2:?missing value for --config-dir}"
      CONFIG_DIR_EXPLICIT=1
      shift 2
      ;;
    --store)
      STORE_NAME="${2:?missing value for --store}"
      shift 2
      ;;
    --host-skills-dir)
      HOST_SKILLS_DIR="${2:?missing value for --host-skills-dir}"
      shift 2
      ;;
    --purge-memory)
      PURGE_MEMORY=1
      shift
      ;;
    --purge-library)
      PURGE_LIBRARY=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${MODULE}" ]]; then
  echo "--module is required" >&2
  usage >&2
  exit 2
fi
if [[ "${MODULE}" != "memory-loop" && "${MODULE}" != "skill-loop" && "${MODULE}" != "eval-loop" ]]; then
  echo "unsupported module for Codex: ${MODULE}" >&2
  exit 1
fi

MODULE_DIR="$(mnemon_module_dir "${MODULE}")"
if [[ ! -d "${MODULE_DIR}" ]]; then
  echo "module directory not found: ${MODULE_DIR}" >&2
  exit 1
fi

if [[ "${GLOBAL}" == "1" && "${CONFIG_DIR_EXPLICIT}" == "0" ]]; then
  MNEMON_DIR="${MNEMON_HARNESS_STATE_DIR:-${HOME}/.mnemon}"
else
  MNEMON_DIR="${MNEMON_HARNESS_STATE_DIR:-.mnemon}"
fi
CANONICAL_MODULE_DIR="${MNEMON_DIR}/harness/${MODULE}"
HOST_MANIFEST_DIR="${MNEMON_DIR}/hosts/codex"
HOST_MANIFEST="${HOST_MANIFEST_DIR}/manifest.json"

install_file() {
  local src="$1"
  local dst="$2"
  local mode="$3"
  mkdir -p "$(dirname "${dst}")"
  cp "${src}" "${dst}"
  chmod "${mode}" "${dst}"
}

ensure_python() {
  if ! command -v python3 >/dev/null 2>&1; then
    echo "python3 is required" >&2
    exit 1
  fi
}

ensure_mnemon_binary() {
  if ! command -v mnemon >/dev/null 2>&1; then
    echo "mnemon binary not found in PATH. Build or install it before running Codex memory-loop evals." >&2
    exit 1
  fi
}

copy_common_canonical_assets() {
  mkdir -p "${CANONICAL_MODULE_DIR}"
  install_file "${MODULE_DIR}/GUIDE.md" "${CANONICAL_MODULE_DIR}/GUIDE.md" 0644
  install_file "${MODULE_DIR}/env.sh" "${CANONICAL_MODULE_DIR}/env.sh" 0755
  install_file "${MODULE_DIR}/module.json" "${CANONICAL_MODULE_DIR}/module.json" 0644
}

write_host_manifest() {
  local projection_path="$1"
  local module_version
  module_version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("version",""))' "${MODULE_DIR}/module.json")"
  mkdir -p "${HOST_MANIFEST_DIR}"
  MNEMON_HOST_MANIFEST="${HOST_MANIFEST}" \
  MNEMON_HOST_MODULE="${MODULE}" \
  MNEMON_HOST_MODULE_VERSION="${module_version}" \
  MNEMON_HOST_PROJECT_ROOT="$(pwd)" \
  MNEMON_HOST_MNEMON_DIR="${MNEMON_DIR}" \
  MNEMON_HOST_STORE="${STORE_NAME:-default}" \
  MNEMON_HOST_PROJECTION_PATH="${projection_path}" \
  python3 - <<'PY'
import json
import os
from datetime import datetime, timezone
from pathlib import Path

path = Path(os.environ["MNEMON_HOST_MANIFEST"])
if path.exists() and path.stat().st_size:
    data = json.loads(path.read_text())
else:
    data = {"schema_version": 1, "host": "codex", "loops": {}}

data["schema_version"] = 1
data["host"] = "codex"
data["updated_at"] = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
data["project_root"] = os.environ["MNEMON_HOST_PROJECT_ROOT"]
data["mnemon_dir"] = os.environ["MNEMON_HOST_MNEMON_DIR"]
data["store"] = os.environ["MNEMON_HOST_STORE"]
data.setdefault("loops", {})[os.environ["MNEMON_HOST_MODULE"]] = {
    "module_path": f"{os.environ['MNEMON_HOST_MNEMON_DIR']}/harness/{os.environ['MNEMON_HOST_MODULE']}",
    "module_version": os.environ["MNEMON_HOST_MODULE_VERSION"],
    "projection_path": os.environ["MNEMON_HOST_PROJECTION_PATH"],
    "lifecycle_mapping": {
        "prime": "thread/start developer instructions",
        "remind": "user prompt guidance",
        "nudge": "turn completion guidance",
        "compact": "thread compact guidance",
    },
    "surfaces": {
        "skills": f"{os.environ['MNEMON_HOST_PROJECTION_PATH']}/skills",
        "runtime": f"{os.environ['MNEMON_HOST_PROJECTION_PATH']}/mnemon-{os.environ['MNEMON_HOST_MODULE']}",
    },
}
path.write_text(json.dumps(data, indent=2) + "\n")
PY
}

remove_host_manifest_module() {
  [[ -f "${HOST_MANIFEST}" ]] || return 0
  MNEMON_HOST_MANIFEST="${HOST_MANIFEST}" MNEMON_HOST_MODULE="${MODULE}" python3 - <<'PY'
import json
import os
from pathlib import Path

path = Path(os.environ["MNEMON_HOST_MANIFEST"])
data = json.loads(path.read_text())
loops = data.get("loops")
if isinstance(loops, dict):
    loops.pop(os.environ["MNEMON_HOST_MODULE"], None)
if not data.get("loops"):
    path.unlink()
else:
    path.write_text(json.dumps(data, indent=2) + "\n")
PY
}

write_runtime_env() {
  local runtime_dir="$1"
  local env_name="$2"
  local loop_dir_var="$3"
  mkdir -p "${runtime_dir}"
  cat > "${runtime_dir}/env.sh" <<EOF
#!/usr/bin/env bash
export ${env_name}="${CANONICAL_MODULE_DIR}/env.sh"
export ${loop_dir_var}="${CANONICAL_MODULE_DIR}"
EOF
  chmod 0755 "${runtime_dir}/env.sh"
}

append_codex_runtime_note() {
  local skill_path="$1"
  local loop_dir_var="$2"
  local runtime_file="$3"
  cat >> "${skill_path}" <<EOF

## Codex Projection

This skill is projected by the Mnemon Codex host adapter.

- Canonical loop directory: \`${CANONICAL_MODULE_DIR}\`
- Runtime env file: \`${runtime_file}\`
- Before following the procedure, source the runtime env file when the expected
  environment variables are not already exported.
- The canonical loop directory is the location for \`GUIDE.md\`, runtime files,
  and loop state. Do not look for loop-owned \`GUIDE.md\`, \`MEMORY.md\`, usage
  logs, proposals, or skill libraries in the workspace root.
- If \`${loop_dir_var}\` is not already exported, use the canonical loop
  directory above.
EOF
}

install_memory_loop() {
  ensure_python
  ensure_mnemon_binary
  copy_common_canonical_assets
  if [[ ! -f "${CANONICAL_MODULE_DIR}/MEMORY.md" ]]; then
    install_file "${MODULE_DIR}/MEMORY.md" "${CANONICAL_MODULE_DIR}/MEMORY.md" 0644
  fi

  mkdir -p "${CONFIG_DIR}/skills/memory_get" "${CONFIG_DIR}/skills/memory_set" "${CONFIG_DIR}/mnemon-memory-loop"
  write_runtime_env "${CONFIG_DIR}/mnemon-memory-loop" "MNEMON_MEMORY_LOOP_ENV" "MNEMON_MEMORY_LOOP_DIR"
  install_file "${MODULE_DIR}/GUIDE.md" "${CONFIG_DIR}/mnemon-memory-loop/GUIDE.md" 0644
  install_file "${MODULE_DIR}/skills/memory_get.md" "${CONFIG_DIR}/skills/memory_get/SKILL.md" 0644
  install_file "${MODULE_DIR}/skills/memory_set.md" "${CONFIG_DIR}/skills/memory_set/SKILL.md" 0644
  append_codex_runtime_note "${CONFIG_DIR}/skills/memory_get/SKILL.md" "MNEMON_MEMORY_LOOP_DIR" "${CONFIG_DIR}/mnemon-memory-loop/env.sh"
  append_codex_runtime_note "${CONFIG_DIR}/skills/memory_set/SKILL.md" "MNEMON_MEMORY_LOOP_DIR" "${CONFIG_DIR}/mnemon-memory-loop/env.sh"

  if [[ -n "${STORE_NAME}" ]]; then
    if ! mnemon store list 2>/dev/null | sed 's/^[* ]*//' | grep -qx "${STORE_NAME}"; then
      mnemon store create "${STORE_NAME}" >/dev/null
    fi
    mnemon store set "${STORE_NAME}" >/dev/null
  fi

  write_host_manifest "${CONFIG_DIR}"
  echo "Installed Mnemon memory loop for Codex."
  echo "Config:   ${CONFIG_DIR}"
  echo "State:    ${CANONICAL_MODULE_DIR}"
}

install_skill_loop() {
  ensure_python
  [[ -n "${HOST_SKILLS_DIR}" ]] || HOST_SKILLS_DIR="${CONFIG_DIR}/skills"
  copy_common_canonical_assets
  mkdir -p \
    "${CANONICAL_MODULE_DIR}/skills/active" \
    "${CANONICAL_MODULE_DIR}/skills/stale" \
    "${CANONICAL_MODULE_DIR}/skills/archived" \
    "${CANONICAL_MODULE_DIR}/proposals" \
    "${CANONICAL_MODULE_DIR}/reports" \
    "${HOST_SKILLS_DIR}/skill_observe" \
    "${HOST_SKILLS_DIR}/skill_curate" \
    "${HOST_SKILLS_DIR}/skill_author" \
    "${HOST_SKILLS_DIR}/skill_manage" \
    "${CONFIG_DIR}/mnemon-skill-loop"
  write_runtime_env "${CONFIG_DIR}/mnemon-skill-loop" "MNEMON_SKILL_LOOP_ENV" "MNEMON_SKILL_LOOP_DIR"
  install_file "${MODULE_DIR}/GUIDE.md" "${CONFIG_DIR}/mnemon-skill-loop/GUIDE.md" 0644
  cat >> "${CONFIG_DIR}/mnemon-skill-loop/env.sh" <<EOF
export MNEMON_SKILL_LOOP_LIBRARY_DIR="${CANONICAL_MODULE_DIR}/skills"
export MNEMON_SKILL_LOOP_ACTIVE_DIR="${CANONICAL_MODULE_DIR}/skills/active"
export MNEMON_SKILL_LOOP_STALE_DIR="${CANONICAL_MODULE_DIR}/skills/stale"
export MNEMON_SKILL_LOOP_ARCHIVED_DIR="${CANONICAL_MODULE_DIR}/skills/archived"
export MNEMON_SKILL_LOOP_USAGE_FILE="${CANONICAL_MODULE_DIR}/skills/.usage.jsonl"
export MNEMON_SKILL_LOOP_PROPOSALS_DIR="${CANONICAL_MODULE_DIR}/proposals"
export MNEMON_SKILL_LOOP_HOST_SKILLS_DIR="${HOST_SKILLS_DIR}"
export MNEMON_SKILL_LOOP_PROTECTED_SKILLS="${MNEMON_SKILL_LOOP_PROTECTED_SKILLS:-skill_observe,skill_curate,skill_author,skill_manage,memory_get,memory_set}"
EOF

  install_file "${MODULE_DIR}/skills/skill_observe.md" "${HOST_SKILLS_DIR}/skill_observe/SKILL.md" 0644
  install_file "${MODULE_DIR}/skills/skill_curate.md" "${HOST_SKILLS_DIR}/skill_curate/SKILL.md" 0644
  install_file "${MODULE_DIR}/skills/skill_author.md" "${HOST_SKILLS_DIR}/skill_author/SKILL.md" 0644
  install_file "${MODULE_DIR}/skills/skill_manage.md" "${HOST_SKILLS_DIR}/skill_manage/SKILL.md" 0644
  append_codex_runtime_note "${HOST_SKILLS_DIR}/skill_observe/SKILL.md" "MNEMON_SKILL_LOOP_DIR" "${CONFIG_DIR}/mnemon-skill-loop/env.sh"
  append_codex_runtime_note "${HOST_SKILLS_DIR}/skill_curate/SKILL.md" "MNEMON_SKILL_LOOP_DIR" "${CONFIG_DIR}/mnemon-skill-loop/env.sh"
  append_codex_runtime_note "${HOST_SKILLS_DIR}/skill_author/SKILL.md" "MNEMON_SKILL_LOOP_DIR" "${CONFIG_DIR}/mnemon-skill-loop/env.sh"
  append_codex_runtime_note "${HOST_SKILLS_DIR}/skill_manage/SKILL.md" "MNEMON_SKILL_LOOP_DIR" "${CONFIG_DIR}/mnemon-skill-loop/env.sh"

  write_host_manifest "${CONFIG_DIR}"
  echo "Installed Mnemon skill loop for Codex."
  echo "Config:       ${CONFIG_DIR}"
  echo "State:        ${CANONICAL_MODULE_DIR}"
  echo "Host skills:  ${HOST_SKILLS_DIR}"
}

install_eval_loop() {
  ensure_python
  [[ -n "${HOST_SKILLS_DIR}" ]] || HOST_SKILLS_DIR="${CONFIG_DIR}/skills"
  copy_common_canonical_assets
  mkdir -p \
    "${CANONICAL_MODULE_DIR}/scratch" \
    "${CANONICAL_MODULE_DIR}/candidates" \
    "${CANONICAL_MODULE_DIR}/reports" \
    "${CANONICAL_MODULE_DIR}/artifacts" \
    "${CANONICAL_MODULE_DIR}/retired" \
    "${CANONICAL_MODULE_DIR}/scenarios" \
    "${CANONICAL_MODULE_DIR}/suites" \
    "${CANONICAL_MODULE_DIR}/rubrics" \
    "${HOST_SKILLS_DIR}/eval_plan" \
    "${HOST_SKILLS_DIR}/eval_run" \
    "${HOST_SKILLS_DIR}/eval_analyze" \
    "${HOST_SKILLS_DIR}/eval_improve" \
    "${CONFIG_DIR}/mnemon-eval-loop"

  cp -R "${MODULE_DIR}/scenarios/." "${CANONICAL_MODULE_DIR}/scenarios/"
  cp -R "${MODULE_DIR}/suites/." "${CANONICAL_MODULE_DIR}/suites/"
  cp -R "${MODULE_DIR}/rubrics/." "${CANONICAL_MODULE_DIR}/rubrics/"

  write_runtime_env "${CONFIG_DIR}/mnemon-eval-loop" "MNEMON_EVAL_LOOP_ENV" "MNEMON_EVAL_LOOP_DIR"
  install_file "${MODULE_DIR}/GUIDE.md" "${CONFIG_DIR}/mnemon-eval-loop/GUIDE.md" 0644
  cat >> "${CONFIG_DIR}/mnemon-eval-loop/env.sh" <<EOF
export MNEMON_EVAL_LOOP_SCRATCH_DIR="${CANONICAL_MODULE_DIR}/scratch"
export MNEMON_EVAL_LOOP_CANDIDATES_DIR="${CANONICAL_MODULE_DIR}/candidates"
export MNEMON_EVAL_LOOP_REPORTS_DIR="${CANONICAL_MODULE_DIR}/reports"
export MNEMON_EVAL_LOOP_ARTIFACTS_DIR="${CANONICAL_MODULE_DIR}/artifacts"
export MNEMON_EVAL_LOOP_RETIRED_DIR="${CANONICAL_MODULE_DIR}/retired"
export MNEMON_EVAL_LOOP_SCENARIOS_DIR="${CANONICAL_MODULE_DIR}/scenarios"
export MNEMON_EVAL_LOOP_SUITES_DIR="${CANONICAL_MODULE_DIR}/suites"
export MNEMON_EVAL_LOOP_RUBRICS_DIR="${CANONICAL_MODULE_DIR}/rubrics"
export MNEMON_EVAL_LOOP_HOST_SKILLS_DIR="${HOST_SKILLS_DIR}"
export MNEMON_EVAL_LOOP_DEFAULT_HOST="${MNEMON_EVAL_LOOP_DEFAULT_HOST:-codex}"
export MNEMON_EVAL_LOOP_DEFAULT_SUITE="${MNEMON_EVAL_LOOP_DEFAULT_SUITE:-smoke}"
EOF

  install_file "${MODULE_DIR}/skills/eval_plan.md" "${HOST_SKILLS_DIR}/eval_plan/SKILL.md" 0644
  install_file "${MODULE_DIR}/skills/eval_run.md" "${HOST_SKILLS_DIR}/eval_run/SKILL.md" 0644
  install_file "${MODULE_DIR}/skills/eval_analyze.md" "${HOST_SKILLS_DIR}/eval_analyze/SKILL.md" 0644
  install_file "${MODULE_DIR}/skills/eval_improve.md" "${HOST_SKILLS_DIR}/eval_improve/SKILL.md" 0644
  append_codex_runtime_note "${HOST_SKILLS_DIR}/eval_plan/SKILL.md" "MNEMON_EVAL_LOOP_DIR" "${CONFIG_DIR}/mnemon-eval-loop/env.sh"
  append_codex_runtime_note "${HOST_SKILLS_DIR}/eval_run/SKILL.md" "MNEMON_EVAL_LOOP_DIR" "${CONFIG_DIR}/mnemon-eval-loop/env.sh"
  append_codex_runtime_note "${HOST_SKILLS_DIR}/eval_analyze/SKILL.md" "MNEMON_EVAL_LOOP_DIR" "${CONFIG_DIR}/mnemon-eval-loop/env.sh"
  append_codex_runtime_note "${HOST_SKILLS_DIR}/eval_improve/SKILL.md" "MNEMON_EVAL_LOOP_DIR" "${CONFIG_DIR}/mnemon-eval-loop/env.sh"

  write_host_manifest "${CONFIG_DIR}"
  echo "Installed Mnemon eval loop for Codex."
  echo "Config:       ${CONFIG_DIR}"
  echo "State:        ${CANONICAL_MODULE_DIR}"
  echo "Host skills:  ${HOST_SKILLS_DIR}"
}

status_module() {
  echo "Codex ${MODULE}:"
  echo "  config:   ${CONFIG_DIR}"
  echo "  state:    ${CANONICAL_MODULE_DIR}"
  if [[ -f "${HOST_MANIFEST}" ]]; then
    echo "  manifest: ${HOST_MANIFEST}"
  else
    echo "  manifest: missing"
  fi
  if [[ -d "${CANONICAL_MODULE_DIR}" ]]; then
    echo "  module:   installed"
  else
    echo "  module:   missing"
  fi
}

uninstall_memory_loop() {
  rm -rf "${CONFIG_DIR}/skills/memory_get"
  rm -rf "${CONFIG_DIR}/skills/memory_set"
  rm -rf "${CONFIG_DIR}/mnemon-memory-loop"
  if [[ "${PURGE_MEMORY}" == "1" ]]; then
    rm -rf "${CANONICAL_MODULE_DIR}"
  else
    rm -f "${CANONICAL_MODULE_DIR}/GUIDE.md" "${CANONICAL_MODULE_DIR}/env.sh" "${CANONICAL_MODULE_DIR}/module.json"
    rmdir "${CANONICAL_MODULE_DIR}" 2>/dev/null || true
  fi
  remove_host_manifest_module
  echo "Removed Mnemon memory loop from ${CONFIG_DIR}."
}

uninstall_skill_loop() {
  local env_path="${CONFIG_DIR}/mnemon-skill-loop/env.sh"
  if [[ -f "${env_path}" ]]; then
    # shellcheck source=/dev/null
    source "${env_path}"
  fi
  local host_skills_dir="${MNEMON_SKILL_LOOP_HOST_SKILLS_DIR:-${HOST_SKILLS_DIR:-${CONFIG_DIR}/skills}}"
  rm -rf "${host_skills_dir}/skill_observe"
  rm -rf "${host_skills_dir}/skill_curate"
  rm -rf "${host_skills_dir}/skill_author"
  rm -rf "${host_skills_dir}/skill_manage"
  rm -rf "${CONFIG_DIR}/mnemon-skill-loop"
  if [[ "${PURGE_LIBRARY}" == "1" ]]; then
    rm -rf "${CANONICAL_MODULE_DIR}"
  else
    rm -f "${CANONICAL_MODULE_DIR}/GUIDE.md" "${CANONICAL_MODULE_DIR}/env.sh" "${CANONICAL_MODULE_DIR}/module.json"
    rmdir "${CANONICAL_MODULE_DIR}/reports" 2>/dev/null || true
    rmdir "${CANONICAL_MODULE_DIR}/proposals" 2>/dev/null || true
    rmdir "${CANONICAL_MODULE_DIR}" 2>/dev/null || true
  fi
  remove_host_manifest_module
  echo "Removed Mnemon skill loop from ${CONFIG_DIR}."
}

uninstall_eval_loop() {
  local env_path="${CONFIG_DIR}/mnemon-eval-loop/env.sh"
  if [[ -f "${env_path}" ]]; then
    # shellcheck source=/dev/null
    source "${env_path}"
  fi
  local host_skills_dir="${MNEMON_EVAL_LOOP_HOST_SKILLS_DIR:-${HOST_SKILLS_DIR:-${CONFIG_DIR}/skills}}"
  rm -rf "${host_skills_dir}/eval_plan"
  rm -rf "${host_skills_dir}/eval_run"
  rm -rf "${host_skills_dir}/eval_analyze"
  rm -rf "${host_skills_dir}/eval_improve"
  rm -rf "${CONFIG_DIR}/mnemon-eval-loop"
  rm -rf "${CANONICAL_MODULE_DIR}/scenarios"
  rm -rf "${CANONICAL_MODULE_DIR}/suites"
  rm -rf "${CANONICAL_MODULE_DIR}/rubrics"
  rm -f "${CANONICAL_MODULE_DIR}/GUIDE.md" "${CANONICAL_MODULE_DIR}/env.sh" "${CANONICAL_MODULE_DIR}/module.json"
  rmdir "${CANONICAL_MODULE_DIR}/retired" 2>/dev/null || true
  rmdir "${CANONICAL_MODULE_DIR}/artifacts" 2>/dev/null || true
  rmdir "${CANONICAL_MODULE_DIR}/reports" 2>/dev/null || true
  rmdir "${CANONICAL_MODULE_DIR}/candidates" 2>/dev/null || true
  rmdir "${CANONICAL_MODULE_DIR}/scratch" 2>/dev/null || true
  rmdir "${CANONICAL_MODULE_DIR}" 2>/dev/null || true
  remove_host_manifest_module
  echo "Removed Mnemon eval loop from ${CONFIG_DIR}."
}

case "${ACTION}:${MODULE}" in
  install:memory-loop) install_memory_loop ;;
  install:skill-loop) install_skill_loop ;;
  install:eval-loop) install_eval_loop ;;
  status:memory-loop|status:skill-loop|status:eval-loop) status_module ;;
  uninstall:memory-loop) uninstall_memory_loop ;;
  uninstall:skill-loop) uninstall_skill_loop ;;
  uninstall:eval-loop) uninstall_eval_loop ;;
  *)
    echo "unsupported action/module: ${ACTION}/${MODULE}" >&2
    exit 1
    ;;
esac
