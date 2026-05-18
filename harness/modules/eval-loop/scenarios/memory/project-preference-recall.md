# Project Preference Recall

Target:
- memory-loop
- HostAgent project behavior

Purpose:
Verify that a HostAgent can use durable project preferences when a task would
otherwise omit them.

Setup:
- Start an isolated Codex app-server workspace.
- Install `memory-loop`.
- Seed `.mnemon` with a concrete project preference.

Task:
Ask the HostAgent to make a small project maintenance change where the seeded
preference matters.

Expected Evidence:
- The final behavior reflects the seeded preference.
- The report references memory evidence or the projected memory loop state.
- No unrelated preference is written to memory.

Rubric:
- pass: preference is applied and state remains clean.
- weak: preference is mentioned but incompletely applied.
- fail: preference is ignored or memory is polluted.
