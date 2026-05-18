---
name: skill_observe
description: Record lightweight skill usage evidence when GUIDE.md indicates that a turn produced reusable workflow or lifecycle signal.
---

# skill_observe

Use this skill only after the HostAgent has decided, according to `GUIDE.md`,
that skill evidence should be recorded.

## Boundary

This skill records evidence only. It does not create, patch, move, archive, or
restore skills.

Resolve the usage log as:

```text
$MNEMON_SKILL_LOOP_USAGE_FILE
```

If the variable is unavailable, use the path injected by Prime. Do not guess a
host-specific default.

## Procedure

1. Identify the smallest evidence item worth keeping.
2. Append one JSON object per line to `$MNEMON_SKILL_LOOP_USAGE_FILE`.
3. Use these fields when available:
   - `time`: ISO-8601 timestamp
   - `skill`: skill id, or `null` for missing-skill evidence
   - `event`: `used`, `helped`, `missing`, `misleading`, `outdated`, `duplicate`, `workflow`, `feedback`, or `patched`
   - `outcome`: `positive`, `negative`, `neutral`, or `unknown`
   - `note`: short evidence note
   - `source`: `user`, `agent`, `repo`, or `manual`
4. Use `source: "user"` only for explicit user feedback or user-requested
   lifecycle evidence. Use `source: "agent"` when the agent infers reusable
   workflow evidence from its own turn.
5. Keep notes short and avoid raw conversation excerpts.
6. If evidence is sensitive or uncertain, skip it or record a sanitized note.

## Example

```json
{"time":"2026-05-14T10:00:00Z","skill":"release-checklist","event":"helped","outcome":"positive","note":"Reusable release verification checklist matched the current task.","source":"agent"}
```

## Safety

Never store secrets. Evidence is input for later review, not authority.
