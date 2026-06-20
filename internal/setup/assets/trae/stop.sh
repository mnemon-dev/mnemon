#!/bin/bash
INPUT=$(cat)

MSG=$(echo "$INPUT" | jq -r '.last_assistant_message // ""' 2>/dev/null)
if echo "$MSG" | grep -qiE "mnemon remember|mnemon recall|mnemon link|Stored.*imp="; then
  exit 0
fi

cat <<'JSON'
{
  "decision": "block",
  "reason": "[mnemon] Before stopping, evaluate whether this exchange contains durable preferences, decisions, insights, facts, or context worth remembering. If yes, run mnemon remember/link; if no, state that no memory update is needed, then finish."
}
JSON
