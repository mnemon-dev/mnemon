---
name: skill_manage
description: Apply approved skill lifecycle and content changes to the canonical Mnemon skill library.
---

# skill_manage

Use this skill only after a proposal has been approved by the user or by an
explicit host policy.

## Boundary

This skill modifies canonical Mnemon skill state. It does not modify host
runtime behavior directly. New active skills become host-visible at the next
Prime sync.

Resolve canonical directories from:

```text
$MNEMON_SKILL_LOOP_ACTIVE_DIR
$MNEMON_SKILL_LOOP_STALE_DIR
$MNEMON_SKILL_LOOP_ARCHIVED_DIR
```

## Allowed MVP Operations

- create an approved skill under `active/<skill-id>/SKILL.md`
- apply approved `SKILL.md` content drafted by `skill_author.md`
- patch an existing skill in its current lifecycle directory
- consolidate duplicated skills with an approved replacement
- move `active -> stale`
- move `stale -> archived`
- restore `stale -> active`
- restore `archived -> stale` or `archived -> active` when explicitly approved
- update metadata or usage notes needed by the lifecycle

## Procedure

1. Read the approved proposal and confirm the intended operation.
2. Check `MNEMON_SKILL_LOOP_PROTECTED_SKILLS`; do not modify protected skills
   unless the approval explicitly covers the exception.
3. Keep new user-facing skill ids hyphen-case: lowercase letters, numbers, and
   `-`. Existing protocol skill ids may keep their established underscore names.
4. Apply the smallest canonical change under the lifecycle directories.
5. Prefer moving to `archived` over deletion.
6. Do not edit the host skill surface directly. Let Prime regenerate it.
7. Record the applied change in the proposal or usage log when useful.

## Safety

If the proposal is ambiguous, risky, or conflicts with current repository state,
stop and ask for approval instead of guessing.
