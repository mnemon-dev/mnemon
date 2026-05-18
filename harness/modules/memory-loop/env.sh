#!/usr/bin/env bash
# Mnemon memory loop runtime config.
# Copy this file next to GUIDE.md and MEMORY.md, then edit values in place.

MNEMON_MEMORY_LOOP_ENV_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export MNEMON_MEMORY_LOOP_ENV="${MNEMON_MEMORY_LOOP_ENV:-${MNEMON_MEMORY_LOOP_ENV_DIR}/env.sh}"
export MNEMON_MEMORY_LOOP_DIR="${MNEMON_MEMORY_LOOP_DIR:-${MNEMON_MEMORY_LOOP_ENV_DIR}}"
export MNEMON_MEMORY_LOOP_MAX_NON_EMPTY_LINES="${MNEMON_MEMORY_LOOP_MAX_NON_EMPTY_LINES:-200}"
