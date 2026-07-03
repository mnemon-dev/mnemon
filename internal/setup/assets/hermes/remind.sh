#!/bin/bash
# mnemon Hermes pre_llm_call hook.
# Reads Hermes' JSON payload on stdin and returns {"context": "..."} for
# injection into the next LLM call.

INPUT_FILE=$(mktemp)
trap 'rm -f "$INPUT_FILE"' EXIT
cat > "$INPUT_FILE"

python3 - "$INPUT_FILE" <<'PY'
import json
import os
import subprocess
import sys
from pathlib import Path

payload_path = Path(sys.argv[1])
try:
    payload = json.loads(payload_path.read_text(encoding="utf-8") or "{}")
except Exception:
    payload = {}

extra = payload.get("extra") if isinstance(payload.get("extra"), dict) else {}


def first_text(value):
    if isinstance(value, str):
        return value.strip()
    if isinstance(value, list):
        parts = []
        for item in value:
            if isinstance(item, str):
                parts.append(item)
            elif isinstance(item, dict):
                text = item.get("text") or item.get("content")
                if isinstance(text, str):
                    parts.append(text)
        return "\n".join(parts).strip()
    return ""


def current_query():
    for key in ("user_message", "message", "prompt", "input"):
        text = first_text(extra.get(key))
        if text:
            return text
    history = extra.get("conversation_history") or extra.get("messages")
    if isinstance(history, list):
        for msg in reversed(history):
            if not isinstance(msg, dict):
                continue
            if msg.get("role") == "user":
                text = first_text(msg.get("content"))
                if text:
                    return text
    return ""


def read_state_file(name):
    hermes_home = Path(os.environ.get("HERMES_HOME") or Path.home() / ".hermes")
    path = hermes_home / "mnemon" / name
    try:
        text = path.read_text(encoding="utf-8").strip()
        path.unlink(missing_ok=True)
        return text
    except Exception:
        return ""


def recall(query):
    if not query:
        return ""
    try:
        result = subprocess.run(
            ["mnemon", "recall", query, "--limit", "5"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=8,
            check=False,
        )
    except FileNotFoundError:
        return "[mnemon] Warning: mnemon not found in PATH."
    except Exception:
        return ""
    if result.returncode != 0 or not result.stdout.strip():
        return ""
    try:
        data = json.loads(result.stdout)
    except Exception:
        return result.stdout.strip()[:4000]
    hits = data.get("results") if isinstance(data, dict) else None
    if not hits:
        return ""
    lines = ["[mnemon recall] Relevant durable memories:"]
    for hit in hits[:5]:
        if not isinstance(hit, dict):
            continue
        content = (hit.get("content") or "").strip()
        if not content:
            insight = hit.get("insight")
            if isinstance(insight, dict):
                content = (insight.get("content") or "").strip()
        if not content:
            continue
        cat = hit.get("category") or ""
        score = hit.get("score")
        prefix = f"- [{cat}] " if cat else "- "
        if isinstance(score, (int, float)):
            prefix = f"- [{cat} score={score:.3f}] " if cat else f"- [score={score:.3f}] "
        lines.append(prefix + content)
    return "\n".join(lines) if len(lines) > 1 else ""


parts = []
prime = read_state_file("prime-context.txt")
if prime:
    parts.append(prime)

nudge = read_state_file("pending-nudge.txt")
if nudge:
    parts.append(nudge)

query = current_query()
recalled = recall(query)
if recalled:
    parts.append(recalled)

parts.append("[mnemon] Evaluate: recall needed? After responding, evaluate: remember needed?")

print(json.dumps({"context": "\n\n".join(p for p in parts if p).strip()}))
PY
