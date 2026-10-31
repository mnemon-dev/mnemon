#!/bin/sh
# mnemon Alma app.ready hook.
#
# Alma buffers hook stdout and only consumes the last parseable JSON line, of
# which just `decision` / `reason` / `updatedInput` have any effect. This hook
# therefore prints nothing: its only job is to refresh the mnemon-owned block in
# ~/.config/alma/MEMORY.md.
#
# It deliberately contains no SQL. The shim (`mem sync`) is the single writer of
# that block, so the hook and any manual `mem sync` can never drift apart.

set -u

ALMA_DIR="${ALMA_CONFIG_DIR:-${HOME}/.config/alma}"
BRIDGE_DIR="${ALMA_DIR}/mnemon-bridge"
MEM="${BRIDGE_DIR}/mem"
LOG_FILE="${BRIDGE_DIR}/alma-hook.log"
LOCK_DIR="${BRIDGE_DIR}/.prime.lock"

log_error() {
  mkdir -p "${BRIDGE_DIR}" 2>/dev/null || true
  printf '%s %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z' 2>/dev/null || date)" "$*" >>"${LOG_FILE}" 2>/dev/null || true
}

cleanup() {
  rm -f "${LOCK_DIR}/pid" 2>/dev/null || true
  rmdir "${LOCK_DIR}" 2>/dev/null || true
}

# Alma exports ALMA_HOOK=true for every hook process. Refusing to run without it
# keeps a manual invocation from silently masquerading as a hook.
[ -n "${ALMA_HOOK:-}" ] || exit 0
[ "${MNEMON_ALMA_PRIME_ACTIVE:-}" = "1" ] && exit 0
export MNEMON_ALMA_PRIME_ACTIVE=1

[ -x "${MEM}" ] || { log_error "shim missing or not executable: ${MEM}"; exit 0; }

mkdir -p "${BRIDGE_DIR}" 2>/dev/null || exit 0
if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
  pid="$(cat "${LOCK_DIR}/pid" 2>/dev/null || true)"
  if [ -n "${pid}" ] && kill -0 "${pid}" 2>/dev/null; then
    exit 0
  fi
  rm -f "${LOCK_DIR}/pid" 2>/dev/null || true
  rmdir "${LOCK_DIR}" 2>/dev/null || exit 0
  mkdir "${LOCK_DIR}" 2>/dev/null || exit 0
fi
printf '%s\n' "$$" >"${LOCK_DIR}/pid" 2>/dev/null || true
trap cleanup EXIT HUP INT TERM

# mem sync is idempotent and refuses to write when the block would be unchanged,
# so a no-op start costs one sqlite read and leaves the file's mtime alone.
if ! out=$("${MEM}" sync 2>&1); then
  log_error "mem sync failed: ${out}"
else
  printf '%s\n' "${out}" | grep -q '^unchanged ' || log_error "${out}"
fi

exit 0
