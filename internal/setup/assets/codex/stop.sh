#!/bin/bash
# mnemon Codex Stop hook
# Stop requires JSON on stdout. Keep this non-blocking; do not force continuation.
INPUT=$(cat)
python3 - "$INPUT" <<'PY'
import json
import sys

try:
    payload = json.loads(sys.argv[1])
except Exception:
    payload = {}

if payload.get("stop_hook_active"):
    sys.exit(0)

last_message = (payload.get("last_assistant_message") or "").lower()
if "mnemon" in last_message or "durable memory" in last_message:
    sys.exit(0)

print(json.dumps({
    "continue": True,
    "systemMessage": "[mnemon] Consider: does this exchange warrant durable memory?",
}))
PY
