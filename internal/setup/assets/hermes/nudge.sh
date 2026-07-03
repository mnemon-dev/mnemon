#!/bin/bash
# mnemon Hermes post_llm_call hook.
# post_llm_call is observational, so queue a reminder for the next pre_llm_call
# instead of trying to force another model turn.

INPUT_FILE=$(mktemp)
trap 'rm -f "$INPUT_FILE"' EXIT
cat > "$INPUT_FILE"

python3 - "$INPUT_FILE" <<'PY'
import json
import os
from pathlib import Path
import sys

try:
    payload = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8") or "{}")
except Exception:
    payload = {}

extra = payload.get("extra") if isinstance(payload.get("extra"), dict) else {}
response = ""
for key in ("response_text", "response", "assistant_message"):
    value = extra.get(key)
    if isinstance(value, str):
        response = value
        break

if "mnemon" not in response.lower() and "durable memory" not in response.lower():
    hermes_home = Path(os.environ.get("HERMES_HOME") or Path.home() / ".hermes")
    state_dir = hermes_home / "mnemon"
    try:
        state_dir.mkdir(parents=True, exist_ok=True)
        (state_dir / "pending-nudge.txt").write_text(
            "[mnemon] Consider whether the previous exchange warrants durable memory. "
            "Use mnemon remember only for stable decisions, preferences, facts, or insights.\n",
            encoding="utf-8",
        )
    except Exception:
        pass

print("{}")
PY
