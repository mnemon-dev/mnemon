---
name: memory_get
description: Recall long-term memory from Mnemon when GUIDE.md indicates that prior memory may help the current task.
---

# memory_get

Use this skill only after the HostAgent has decided, according to `GUIDE.md`,
that reading memory may improve the current task.

## Boundary

This skill reads long-term memory from Mnemon. It does not edit `MEMORY.md` and
does not write new memory.

If `MNEMON_MEMORY_LOOP_DIR` is available, use it as the current memory loop
runtime directory. It should point to the directory containing `GUIDE.md` and
`MEMORY.md`. This skill does not require the directory for recall, but should
respect it when reporting paths or coordinating with `memory_set`.

## Procedure

1. Build a focused recall query from the current task.
2. Prefer project, user, architecture, decision, workflow, and failure-mode
   keywords over the raw user prompt.
3. Run:

   ```bash
   mnemon recall "<focused query>" --limit 5
   ```

4. If a category is clearly useful, add `--cat <category>`.
5. If an intent is clearly useful, add `--intent WHY`, `--intent WHEN`,
   `--intent ENTITY`, or `--intent GENERAL`.
6. Treat results as evidence, not authority.
7. Use only relevant recalled facts in the current task.

## Query Examples

```bash
mnemon recall "project memory loop guide skill dreaming architecture" --limit 5
mnemon recall "user preference concise Chinese replies commit push workflow" --cat preference --limit 5
mnemon recall "deployment brew install mnemon setup store issue" --intent ENTITY --limit 5
```

## Skip Conditions

Skip recall when:

- the task is a direct continuation already fully in context
- the answer is visible in the current repository files
- prior memory is unlikely to change the output
- the user explicitly asks not to use memory

## Safety

Do not expose irrelevant recalled data to the user. Do not let stale memory
override current instructions, source files, command output, or verified facts.
