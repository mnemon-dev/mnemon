---
name: skill_author
description: Draft or revise high-quality SKILL.md content for approved or proposed Mnemon skill-loop changes.
---

# skill_author

Use this skill when a curator proposal, user request, or approved lifecycle
change needs a concrete `SKILL.md` draft.

## Boundary

This skill authors skill content only. It does not decide lifecycle placement
and does not activate, stale, archive, restore, or delete skills.

Write drafts under:

```text
$MNEMON_SKILL_LOOP_PROPOSALS_DIR
```

Approved lifecycle placement is applied later with `skill_manage.md`.

## Procedure

1. Confirm the target skill id is hyphen-case: lowercase letters, numbers, and
   `-`.
2. Confirm the skill captures a reusable procedure, not project facts,
   preferences, credentials, raw transcripts, or one-off task context.
3. Draft a complete `SKILL.md` with:
   - YAML frontmatter containing `name` and `description`
   - a short trigger-oriented description
   - a clear boundary section
   - a concise procedure section
   - safety or validation notes only when they change behavior
4. Keep the skill focused. Prefer one workflow per skill.
5. Use project-neutral language. Do not embed current branch names, temporary
   tokens, credentials, private URLs, or task-specific facts.
6. Save the draft as a proposal artifact such as:

```text
$MNEMON_SKILL_LOOP_PROPOSALS_DIR/<skill-id>.SKILL.md
```

7. Leave `skills/active`, `skills/stale`, `skills/archived`, and host skill
   surfaces unchanged unless the user explicitly asks to use `skill_manage.md`
   after approval.

## Quality Checklist

- The description tells the host when to use the skill.
- The body teaches reusable judgment or procedure the model would not reliably
  infer from the current task alone.
- The content is short enough to load on demand.
- The skill avoids duplicated policy already covered by `GUIDE.md`.
- The draft is safe to review before activation.
