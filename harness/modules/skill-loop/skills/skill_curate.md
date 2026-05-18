---
name: skill_curate
description: Start a low-frequency review of skill evidence and canonical skill lifecycle state.
---

# skill_curate

Use this skill when `GUIDE.md` indicates that accumulated skill evidence should
be reviewed.

## Boundary

This skill starts review. It should normally spawn the `mnemon-skill-curator`
subagent or prepare the exact review request for a host-specific subagent
mechanism.

It does not directly apply lifecycle changes. Approved changes are applied with
`skill_manage.md`.

## Procedure

1. Resolve runtime paths from `MNEMON_SKILL_LOOP_DIR`, `MNEMON_SKILL_LOOP_USAGE_FILE`,
   and `MNEMON_SKILL_LOOP_PROPOSALS_DIR`.
2. Ask the curator to review:
   - `GUIDE.md`
   - `skills/active`
   - `skills/stale`
   - `skills/archived`
   - `.usage.jsonl`
   - existing proposals
3. Request proposals for create, patch, consolidate, stale, archive, or restore
   actions only when evidence supports them. When a proposal needs concrete
   skill content, use `skill_author.md` to draft reviewable `SKILL.md` content
   under the proposals directory.
4. Keep the output proposal-first. Do not enable a new active skill in the
   current session unless the user explicitly approves and the host supports it.

## Review Request Template

```text
Review the Mnemon skill loop library at $MNEMON_SKILL_LOOP_DIR.
Use GUIDE.md as policy. Read usage evidence and current skills. Produce
proposal files under $MNEMON_SKILL_LOOP_PROPOSALS_DIR. Do not apply changes.
```
