---
name: memory_set
description: Maintain prompt-facing working memory by editing MEMORY.md when GUIDE.md indicates that durable information should be kept.
---

# memory_set

Use this skill only after the HostAgent has decided, according to `GUIDE.md`,
that working memory should be updated.

## Boundary

This skill edits `MEMORY.md`. It does not write Mnemon long-term memory. Long-
term consolidation belongs to the dreaming subagent.

Resolve the working memory path as:

```text
$MNEMON_MEMORY_LOOP_DIR/MEMORY.md
```

If `MNEMON_MEMORY_LOOP_DIR` is not available, use the path injected by the Prime
hook. Do not guess a repository-root `MEMORY.md`, `~/.mnemon/MEMORY.md`, or a
runtime-specific default unless the HostAgent has explicitly provided that path.

## Procedure

1. Identify the smallest durable memory worth keeping.
2. Open `$MNEMON_MEMORY_LOOP_DIR/MEMORY.md`.
3. Preserve any organization already present in `MEMORY.md`. If the file has no
   useful structure yet, create the smallest heading or bullet layout needed for
   the current memory.
4. Apply a minimal edit:
   - add a concise bullet;
   - replace stale or superseded wording;
   - remove obsolete or unsafe content.
5. Prefer one clear sentence over a transcript excerpt.
6. Merge by default: same topic, same preference, or same decision should update
   the existing entry instead of appending a new one.
7. Defer unstable memories. If the user is still negotiating wording or making a
   first passing mention, leave `MEMORY.md` unchanged.
8. Keep the file compact. If the file is becoming long or repetitive, trigger
   or recommend dreaming instead of appending more text.

## Entry Style

Use compact bullets:

```markdown
- <durable fact or preference> (source: <user|repo|agent|command>, confidence: <high|medium|low>)
```

Omit metadata only when the source is obvious from nearby context.

## What To Keep

- stable user preferences
- project conventions
- active architecture decisions
- important operational notes
- critical open continuity
- decisions that supersede older guidance

## What To Reject

- secrets or credentials
- raw chat logs
- temporary task progress
- unverified guesses
- facts already obvious from source files
- restatements of `GUIDE.md`, memory policy, safety policy, or skip conditions
- noisy implementation details
- low-confidence speculation

## Safety

If an update could conflict with user intent or current repository facts, ask
for clarification or leave `MEMORY.md` unchanged.

Do not write a memory entry merely because the user repeated an existing safety
rule such as not storing secrets. Apply the rule for the current turn and leave
`MEMORY.md` unchanged unless the user explicitly provides a new durable policy.
