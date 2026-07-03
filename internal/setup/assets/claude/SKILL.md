---
name: mnemon
description: Persistent memory CLI for LLM agents. Store facts, recall past knowledge, link related memories, manage lifecycle.
---

# mnemon

## Workflow

1. **Remember**: `mnemon remember "<fact>" --cat <cat> --imp <1-5> --entities "e1,e2" --source agent`
   - Diff is built-in: duplicates skipped, conflicts auto-replaced.
   - Output includes `action` (added/updated/skipped), `semantic_candidates`, `causal_candidates`.
2. **Link** (evaluate candidates from step 1 — use judgment, not mechanical rules):
   - Review `causal_candidates`: does a genuine cause-effect relationship exist? `causal_signal` is regex-based and prone to false positives — only link if the memories are truly causally related.
   - Review `semantic_candidates`: are these memories meaningfully related? High `similarity` alone is not sufficient — skip candidates that share keywords but discuss unrelated topics.
   - Syntax: `mnemon link <id> <candidate> --type <causal|semantic> --weight <0-1> [--meta '<json>']`
3. **Recall**: `mnemon recall "<query>" --limit 10`

## Commands

```bash
mnemon remember "<fact>" --cat <cat> --imp <1-5> --entities "e1,e2" --source agent
mnemon link <id1> <id2> --type <type> --weight <0-1> [--meta '<json>']
mnemon recall "<query>" --limit 10
mnemon search "<query>" --limit 10
mnemon import --dry-run <file>
mnemon import <file>
mnemon forget <id>
mnemon related <id> --edge causal
mnemon gc --threshold 0.4
mnemon gc --keep <id>
mnemon status
mnemon log
mnemon store list
mnemon store create <name>
mnemon store set <name>
mnemon store remove <name>
```

## Import Historical Chats

When the user asks to import old chats, notes, or exported context, create a
`memory_draft.json` with `schema_version: "1"`, `insights` entries containing
`content`, `category`, `importance`, `tags`, `entities`, and optional
`created_at`, plus optional `edges` using `source_index`, `target_index`,
`edge_type`, `weight`, and `reason`. Run `mnemon import --dry-run <file>`,
then run `mnemon import <file>` only after validation passes. After import,
verify with `mnemon status` and a focused `mnemon search` or `mnemon recall`.
Check the output `errors` field because imports can partially succeed.

## Guardrails

- Main conversation: never run `remember` or `link` directly; delegate to a Task sub-agent.
- Memory sub-agent: you are the leaf writer. Run `mnemon remember` and any justified `mnemon link` commands directly; do not spawn another sub-agent.
- Do not store secrets, passwords, or tokens.
- Categories: `preference` · `decision` · `insight` · `fact` · `context`
- Edge types: `temporal` · `semantic` · `causal` · `entity`
- Max 8,000 chars per insight.
