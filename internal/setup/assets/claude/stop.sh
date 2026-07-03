#!/bin/bash
# mnemon Stop hook — lightweight memory reminder (suggestion mode).
# Non-blocking: outputs a reminder that the model sees but is not forced to act on.
# The model's CLAUDE.md instructions handle the actual memory evaluation.
# Optional dependency: jq (for smart silence). Without jq, the nudge always fires.

INPUT=$(cat)

# If model already mentioned memory operations, stay silent
MSG=$(echo "$INPUT" | jq -r '.last_assistant_message // ""' 2>/dev/null)
if echo "$MSG" | grep -qiE "mnemon remember|sub-agent.*remember|Stored.*imp="; then
  exit 0
fi

if echo "$INPUT" | grep -qE '"hook_event_name"[[:space:]]*:[[:space:]]*"SubagentStop"|"agent_id"[[:space:]]*:'; then
  echo "[mnemon] If this sub-agent was delegated memory work, complete it directly with mnemon remember/link; do not spawn another sub-agent. If no durable memory is needed, say so and finish."
  exit 0
fi

echo "[mnemon] Consider: does this exchange warrant a remember sub-agent?"
