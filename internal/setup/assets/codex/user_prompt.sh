#!/bin/bash
# mnemon Codex UserPromptSubmit hook
# Plain stdout is added as extra developer context by Codex.
cat >/dev/null || true
echo "[mnemon] Evaluate: recall needed? After responding, evaluate: remember needed?"
