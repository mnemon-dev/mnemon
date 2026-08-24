# Mnemon Memory — Usage & Reference

> You don't run Memory commands yourself — the agent does, driven by hooks and guided by the skill file. This document covers the root Memory CLI for understanding what the agent can do, debugging, and advanced manual operation. For durable Agent work and peer collaboration, see the [Agency Preview guide](AGENCY.md).

---

## Memory Root Flags

These root flags configure Memory commands:

| Flag | Default | Description |
|---|---|---|
| `--store <name>` | (auto) | Named memory store (overrides `MNEMON_STORE` and active file) |
| `--data-dir <path>` | `~/.mnemon` | Base data directory |
| `--embed-model <name>` | `nomic-embed-text` | Embedding model (overrides `MNEMON_EMBED_MODEL`) |
| `--readonly` | `false` | Open the Memory database read-only, without creating WAL files |
| `--version` | | Print version and exit |

---

## Memory Setup

Deploy mnemon into LLM CLI environments. This is the first command to run after installation.

```bash
# Interactive: detect environments and install (project-local)
mnemon setup

# User-wide install (all projects)
mnemon setup --global

# Non-interactive: specific target only
mnemon setup --target claude-code
mnemon setup --target codex
mnemon setup --target cursor
mnemon setup --target zcode --global
mnemon setup --target minimax-code
mnemon setup --target trae
mnemon setup --target qoder
mnemon setup --target qoderwork
mnemon setup --target codebuddy
mnemon setup --target workbuddy
mnemon setup --target kimi
mnemon setup --target opencode
mnemon setup --target openclaw
mnemon setup --target pi
mnemon setup --target nanobot --global
mnemon setup --target hermes

# Auto-confirm all prompts (CI-friendly)
mnemon setup --yes

# Remove mnemon integrations
mnemon setup --eject
mnemon setup --eject --target claude-code
```

| Flag | Default | Description |
|---|---|---|
| `--global` | `false` | Install to user-wide config instead of project-local (required for ZCode lifecycle hooks; MiniMax Code installs to `~/.minimax/`; recommended for Nanobot: installs to `~/.nanobot/workspace/`; Pi installs to `~/.pi/agent/`; Hermes installs to `~/.hermes/`; QoderWork installs to `~/.qoderwork/`; Kimi Code installs to `~/.kimi-code/` or `$KIMI_CODE_HOME/`; OpenCode installs to `~/.config/opencode/`) |
| `--target <name>` | (auto-detect) | Target environment: `claude-code`, `codex`, `cursor`, `zcode`, `minimax-code`, `trae`, `qoder`, `qoderwork`, `codebuddy`, `workbuddy`, `kimi`, `opencode`, `openclaw`, `nanobot`, `pi`, or `hermes` |
| `--eject` | `false` | Remove mnemon integrations |
| `--yes` | `false` | Auto-confirm all prompts |

---

## Memory CLI Commands

### Core

```bash
# Remember — store a new insight (built-in diff: duplicates skipped, conflicts auto-replaced)
mnemon remember "Chose Qdrant over Milvus for vector search" \
  --cat decision --imp 5 --entities "Qdrant,Milvus" --tags "architecture,search" --source agent

# Skip duplicate/conflict detection
mnemon remember "Raw note" --no-diff

# Recall — intent-aware graph-enhanced retrieval (default: compact output)
mnemon recall "vector database" --limit 10

# Recall with full verbose output (signals, meta, timestamps)
mnemon recall "vector database" --verbose

# Recall with explicit intent override
mnemon recall "why did we choose Qdrant" --intent WHY

# Recall with category/source filter
mnemon recall "auth" --cat decision --source agent

# Simple SQL LIKE matching (faster, no graph traversal)
mnemon recall "auth" --basic

# Search — token-scored keyword search
mnemon search "authentication" --limit 10

# Import — bulk-import a memory draft file (see docs/IMPORT.md for schema and LLM prompt)
mnemon import memory_draft.json
mnemon import --dry-run memory_draft.json   # validate without writing
mnemon import --no-diff memory_draft.json   # skip deduplication

# Forget — soft-delete an insight
mnemon forget <id>
```

**Remember flags:**

| Flag | Default | Description |
|---|---|---|
| `--cat` | `general` | Category: `preference`, `decision`, `fact`, `insight`, `context`, `general` |
| `--imp` | `3` | Importance: 1–5 |
| `--tags` | | Comma-separated tags |
| `--entities` | | Comma-separated entities (merged with auto-extraction) |
| `--entity-mode` | `merge` | Entity handling: `merge` (provided + auto), `provided` (only `--entities`), `auto` (only auto-extraction) |
| `--source` | `user` | Source: `user`, `agent`, `external` |
| `--no-diff` | `false` | Skip duplicate/conflict detection |

**Recall flags:**

| Flag | Default | Description |
|---|---|---|
| `--limit` | `10` | Max results |
| `--intent` | (auto-detect) | Override intent: `WHY`, `WHEN`, `ENTITY`, `GENERAL` |
| `--cat` | | Filter by category |
| `--source` | | Filter by source |
| `--basic` | `false` | Use simple SQL LIKE matching instead of smart recall |
| `--verbose` | `false` | Output full recall response (signals, meta, timestamps) |

The default compact output is optimized for LLM/agent consumption. It includes
`id`, `content`, `category`, `importance`, `intent`, `matched_via`, `confidence`,
and `score`. Use `--verbose` to restore the full payload with signals, traversal
metadata, and timestamps. The confidence label is only emitted in compact mode;
verbose payloads return the raw score for callers that prefer their own thresholds.

### Graph Operations

```bash
# Link — create a typed edge
mnemon link <source_id> <target_id> --type semantic --weight 0.85
mnemon link <source_id> <target_id> --type causal --weight 0.8 \
  --meta '{"sub_type":"causes","reason":"..."}'
mnemon link <new_id> <old_id> --type supersedes --weight 1.0

# Related — BFS traversal from an insight
mnemon related <id> --edge causal --depth 2
```

### Lifecycle Management

```bash
# GC — view low-retention candidates
mnemon gc --threshold 0.5 --limit 20

# GC keep — boost an insight's retention
mnemon gc --keep <id>
```

### Store Management

Mnemon supports named stores for data isolation. Each store has its own independent database.

```bash
# List all stores (* marks the active one)
mnemon store list

# Create a new store
mnemon store create work

# Switch the default active store
mnemon store set work

# Remove a store (cannot remove the active store)
mnemon store remove old-project
```

**Store resolution priority** (highest to lowest):

1. `--store <name>` CLI flag
2. `MNEMON_STORE` environment variable
3. `~/.mnemon/active` file
4. Falls back to `"default"`

Different agents or processes can use different stores via the `MNEMON_STORE` environment variable — no global state contention. Legacy databases (`~/.mnemon/mnemon.db`) are automatically migrated to `~/.mnemon/data/default/` on first run.

### Observability

```bash
mnemon status              # memory statistics
mnemon log                 # operation log (default: last 20)
mnemon log --limit 50      # show more entries
mnemon receipt             # JSON receipt with hashed recent operations
mnemon receipt --limit 50  # include more operations in the receipt
```

`mnemon receipt` is a privacy-reduced audit export for sharing or archiving
Memory-boundary observations without publishing raw memories, recall queries,
paths, or operation details. It emits operation names, timestamps, and SHA-256
hashes for identifiers/details so a team can correlate observed `remember`,
`recall`, `forget`, or GC activity without exposing the underlying content. It
is not signed third-party-verifiable proof.

Example shape:

```json
{
  "schema": "mnemon.memory.receipt.v1",
  "privacy": {
    "raw_detail_included": false,
    "hash_algorithm": "sha256"
  },
  "events": [
    {
      "event_name": "mnemon.memory.operation.observed",
      "operation": "remember",
      "detail_present": true,
      "detail_hash": "..."
    }
  ]
}
```

### Visualization

Export the knowledge graph for visual exploration:

```bash
# DOT format — render with Graphviz (brew install graphviz)
mnemon viz --format dot -o graph.dot
dot -Tpng graph.dot -o graph.png

# Interactive HTML — open directly in the browser (vis.js, no install needed)
mnemon viz --format html -o graph.html
open graph.html
```

Nodes are colored by category (decision, fact, insight, preference, context); edges are colored by type (temporal, semantic, causal, entity, supersedes).

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `MNEMON_DATA_DIR` | `~/.mnemon` | Base data directory |
| `MNEMON_STORE` | `default` | Active named store |
| `MNEMON_EMBED_ENDPOINT` | `http://localhost:11434` | Embedding API endpoint |
| `MNEMON_EMBED_MODEL` | `nomic-embed-text` | Embedding model |
| `MNEMON_EMBED_PROTOCOL` | (auto-detect) | `ollama` or `openai`; endpoints ending in `/v1` select `openai` |
| `MNEMON_EMBED_API_KEY` | (none) | Bearer token for OpenAI-compatible servers |
| `MNEMON_EMBED_DIMENSIONS` | (native) | Embedding dimensions; set to truncate (e.g., `256` for Matryoshka models) |
| `MNEMON_MAX_INSIGHTS` | `1000` | Active-insight ceiling before auto-pruning starts; `0` disables auto-pruning |

---

## Embedding Support (Optional)

Mnemon works fully without an embedding provider — all core features (remember, recall, link, graph traversal) function out of the box. Configuring Ollama or an OpenAI-compatible server enhances recall precision through vector similarity, but is never required.

### What changes with and without embeddings

| Capability | Without embeddings | With embeddings |
|---|---|---|
| **Recall anchors** | Keyword + recency | Keyword + vector + recency (RRF hybrid) |
| **Semantic edges** | Token overlap (coarser) | Cosine similarity ≥ 0.50 (precise) |
| **Traversal scoring** | Pure structural | Structural + semantic |
| **Rerank weights** | Keyword 45%, Entity 25%, Graph 30% | Keyword 30%, Entity 15%, Similarity 35%, Graph 20% |

When the configured provider is unavailable, the reranking system automatically redistributes similarity weight to keyword and graph signals — no configuration or degraded-mode flag is needed. Mnemon checks provider availability at runtime with a 2-second timeout.

### Setup

Ollama remains the default provider:

```bash
brew install ollama              # or see https://ollama.ai
ollama pull nomic-embed-text     # download the embedding model
```

For an OpenAI-compatible server, point the endpoint at its `/v1` base URL and
select the server's embedding model. The API key is optional for keyless local
servers:

```bash
export MNEMON_EMBED_ENDPOINT=http://127.0.0.1:18000/v1
export MNEMON_EMBED_MODEL=bge-m3-mlx-8bit
export MNEMON_EMBED_API_KEY=sk-... # omit for keyless local servers
```

Set `MNEMON_EMBED_PROTOCOL=openai` explicitly only when the compatible endpoint
does not end in `/v1`.

Verify with:

```bash
mnemon embed --status
```

```json
{
  "total_insights": 87,
  "embedded": 87,
  "coverage": "100%",
  "embedding_available": true,
  "ollama_available": true,
  "protocol": "ollama",
  "model": "nomic-embed-text"
}
```

`ollama_available` is retained as a compatibility alias for existing scripts;
new integrations should use `embedding_available` and `protocol`.

### Backfilling existing insights

If you configure an embedding provider after already using mnemon, existing insights won't have embeddings. Backfill them in one command:

```bash
mnemon embed --all
```

This generates embeddings for all un-embedded insights and automatically creates semantic edges. You can check coverage before and after with `mnemon embed --status`.

---

## Architecture

```
┌──────────────────┐     CLI commands      ┌──────────────────┐
│   LLM Agent      │ ───────────────────── │     Mnemon       │
│ (Claude Code,    │  remember, recall,    │                  │
│  Cursor, etc.)   │  link, forget, gc     │  SQLite (WAL)    │
└──────────────────┘                       │  ┌────────────┐  │
                                           │  │ Insights   │  │
        The LLM decides WHAT               │  ├────────────┤  │
        to remember and link.              │  │ 4 Edge     │  │
                                           │  │ Types:     │  │
        Mnemon handles HOW                 │  │ temporal   │  │
        to store, index, and               │  │ entity     │  │
        retrieve.                          │  │ causal     │  │
                                           │  │ semantic   │  │
      ┌──────────────────┐                 │  ├────────────┤  │
      │ Embedding server │  (optional)     │  │ Embeddings │  │
      │ configured model │ ◄───────────── │  └────────────┘  │
      └──────────────────┘                 └──────────────────┘
```

Inspired by [MAGMA](https://arxiv.org/abs/2601.03236) four-graph model. See [Design & Architecture](DESIGN.md) for the full deep dive.
