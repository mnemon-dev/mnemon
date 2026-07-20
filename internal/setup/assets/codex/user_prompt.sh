#!/bin/bash
# mnemon Codex UserPromptSubmit hook
# Codex classifies stdout beginning with "[" or "{" as attempted JSON; the reminder
# legitimately begins "[mnemon]", so it must ride inside additionalContext or Codex
# reports invalid JSON. Static payload; no new runtime dependency.
cat >/dev/null || true
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"[mnemon] Evaluate: recall needed? After responding, evaluate: remember needed?"}}'
