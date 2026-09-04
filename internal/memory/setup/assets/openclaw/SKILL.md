---
name: mnemon
description: "Persistent memory CLI for LLM agents. Store facts, recall past knowledge, link related memories, manage lifecycle."
metadata:
  openclaw:
    emoji: "🧠"
    requires:
      bins: ["mnemon"]
    install:
      - id: "node"
        kind: "node"
        package: "@mnemon-dev/mnemon@latest"
        bins: ["mnemon"]
        label: "Install mnemon (npm)"
---

# mnemon

## Install & Configure

### 1. Install the binary

**npm** (macOS / Linux / Windows, Node.js 22+):

```bash
npm install --global @mnemon-dev/mnemon
```

Upgrade an npm-managed installation with:

```bash
mnemon update
```

### 2. Set up OpenClaw integration

```bash
mnemon setup --target openclaw --yes
```

This single command deploys all components:
- **Skill** → `~/.openclaw/skills/mnemon/SKILL.md`
- **Hook** → `~/.openclaw/hooks/mnemon-prime/` (agent:bootstrap — injects behavioral guide)
- **Plugin** → `~/.openclaw/extensions/mnemon/` (remind, nudge, compact hooks)
- **Prompts** → `~/.mnemon/prompt/` (guide.md, skill.md)

Restart the OpenClaw gateway to activate.

### 3. Customize (optional)

Edit `~/.mnemon/prompt/guide.md` to tune recall/remember behavior.

Plugin hooks are configured in `~/.openclaw/openclaw.json`:

```json
{
  "plugins": {
    "entries": {
      "mnemon": {
        "enabled": true,
        "config": {
          "remind": true,
          "nudge": true,
          "compact": false
        }
      }
    }
  }
}
```

| Hook | Default | Description |
|------|---------|-------------|
| `remind` | on | Recall relevant memories + remind agent on each message |
| `nudge` | on | Suggest remember sub-agent after each reply |
| `compact` | off | Save key insights before context compaction |

### 4. Uninstall

```bash
mnemon setup --eject --target openclaw --yes
```

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

- Use the `exec` tool to run mnemon commands.
- Do not store secrets, passwords, or tokens.
- Categories: `preference` · `decision` · `insight` · `fact` · `context`
- Edge types: `temporal` · `semantic` · `causal` · `entity`
- Max 8,000 chars per insight.
