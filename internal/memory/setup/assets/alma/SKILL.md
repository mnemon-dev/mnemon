---
name: mnemon
description: Cross-tool long-term memory backed by mnemon. Recall before answering anything that depends on past decisions, preferences, or project context, and store durable facts the user will expect you to remember. Triggers include "记不记得 / 之前 / 上次 / 我们说过 / do you remember / we discussed".
---

# mnemon

mnemon is the source of truth. Alma's own `memory/YYYY-MM-DD.md` notes are a
per-session log, not the long-term store; durable facts belong in mnemon.

## Use the shim, not the raw CLI

`~/.config/alma/mnemon-bridge/mem` is the only supported entry point. It pins the
store, keeps reads read-only, resolves short ids, and owns the `MEMORY.md` block.
Call it with Bash:

```bash
MEM=~/.config/alma/mnemon-bridge/mem

$MEM find "<query>" [--limit 5] [--chars 240] [--cat preference|decision|fact|insight|context]
$MEM save "<content>" --cat decision --imp 4 [--tags a,b] [--entities X,Y] [--source user|agent]
$MEM show <id-prefix>
$MEM related <id-prefix> [--edge causal|entity|semantic|supersedes|temporal]
$MEM recent [N]
$MEM status
```

Calling `mnemon` directly is only correct when the shim is missing. The raw CLI
needs a full UUID for `show`/`related`/`forget`, does not share the shim's store
pinning, and its `recall --cat` flag is accepted but ignored in 0.2.13.

## Recall

Do **not** query on every turn. A recall costs 0.3–1.2 s and most turns do not
need one. Recall when:

- the user asks "记不记得 / 之前 / 上次 / 我们说过 / do you remember"
- the topic switches to a specific project, service, repo, or host
- the user names something you do not recognise but that clearly comes from
  their history
- you are about to make a decision that may already have been made

Skip it for greetings, small talk, self-contained code questions, and any turn
where the user already supplied the context.

Use the result silently. Do not narrate "I checked my memory".

## Remember

Store one claim per entry, self-contained enough to make sense months later
(with dates, file names, numbers). A conversation usually yields 0–3 durable
entries; store nothing rather than padding the store.

| kind | example | `--cat` | `--imp` |
|---|---|---|---|
| user preference | wording, taste, working style | `preference` | 4 |
| decided course | "use A not B, because …" | `decision` | 3–5 |
| durable technical fact | root cause, config shape, measured number | `insight` / `fact` | 3–4 |
| project background | what a module currently is | `context` | 3 |

`--cat` is mandatory and `general` is rejected: it is the default that turns into
noise. Reserve `--imp 5` for facts that should be visible in every session.

Never store secrets, tokens, passwords, or transient state that a command can
recompute.

## MEMORY.md ownership

Alma injects `~/.config/alma/MEMORY.md` into the system prompt on every request,
and Alma's own instructions tell the model to write to that same file. mnemon
therefore owns exactly one region of it:

```text
<!-- mnemon:begin -->
...
<!-- mnemon:end -->
```

Never write outside those markers, and never edit inside them by hand — use
`mem save` and let `prime.sh` regenerate the block. Everything outside the
markers belongs to Alma and the user.

Keep the block small. It is re-sent on every single turn; treat anything past a
few KB as a permanent cost.

## Guardrails

- Categories: `preference` · `decision` · `insight` · `fact` · `context`
- Edge types: `temporal` · `semantic` · `causal` · `entity` · `supersedes`
- Max 8,000 chars per insight.
- No cross-store queries; `--store "a,b"` is rejected. Query twice and merge.
- Do not create new stores to separate topics.
