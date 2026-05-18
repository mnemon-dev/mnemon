---
name: mnemon
description: Persistent memory CLI for LLM agents. Store facts, recall past knowledge, link related memories, manage lifecycle.
---

# mnemon

## Workflow

1. **Remember**: `mnemon remember "<fact>" --cat <cat> --imp <1-5> --entities "e1,e2" --source agent`
   - Diff is built in: duplicates are skipped, conflicts are auto-replaced.
   - Output includes `action` (added/updated/skipped), `semantic_candidates`, and `causal_candidates`.
2. **Link** (evaluate candidates from step 1 using judgment):
   - Review `causal_candidates`: link only when the memories are genuinely causally related.
   - Review `semantic_candidates`: high `similarity` alone is not enough; skip unrelated keyword matches.
   - Syntax: `mnemon link <id> <candidate> --type <causal|semantic> --weight <0-1> [--meta '<json>']`
3. **Recall**: `mnemon recall "<query>" --limit 10`

## Commands

```bash
mnemon remember "<fact>" --cat <cat> --imp <1-5> --entities "e1,e2" --source agent
mnemon link <id1> <id2> --type <type> --weight <0-1> [--meta '<json>']
mnemon recall "<query>" --limit 10
mnemon search "<query>" --limit 10
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

## Guardrails

- Use memory only when it can materially improve continuity or task quality.
- Do not store secrets, passwords, tokens, private keys, or short-lived operational noise.
- Categories: `preference` · `decision` · `insight` · `fact` · `context`
- Edge types: `temporal` · `semantic` · `causal` · `entity`
- Max 8,000 chars per insight.
