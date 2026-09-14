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
| `--readonly` | `false` | Open an immutable Memory database snapshot; reject write commands and create no WAL files |
| `--version` | | Print version and exit |

`--readonly` is intended for a static database snapshot on a read-only mount.
It rejects commands that mutate Memory data and suppresses incidental recall
counters/oplog writes. Do not use it to follow a database another process is
actively changing; immutable snapshots deliberately ignore concurrent WAL
updates. Pass a filesystem path to `--data-dir`, including Windows drive-letter
paths or paths relative to the current directory. Mnemon resolves and encodes
the read-only SQLite file URI internally; do not prepend `file:` yourself.

---

## CLI Updates

The canonical npm installation can update itself to the package tagged
`latest`:

```bash
mnemon update
```

The npm launcher proves that the active package belongs to the same global npm
prefix before invoking npm. It fails closed when `mnemon` came from Homebrew,
`go install`, a source build, another Node package manager, or a different npm
prefix, preventing a second installation from being created silently. Migrate
once with `npm install --global @mnemon-dev/mnemon@latest`, then make sure that
npm's global bin directory is the first `mnemon` on `PATH`.

Updating replaces only the CLI package. It does not modify Memory data or
silently rewrite installed host integrations. Review release notes and rerun
`mnemon setup` when an integration release explicitly requires a refresh.

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
# Remember — store a new insight (exact repeats skipped; distinct content preserved)
mnemon remember "Chose Qdrant over Milvus for vector search" \
  --cat decision --imp 5 --entities "Qdrant,Milvus" --tags "architecture,search" --source agent

# Skip duplicate/conflict detection
mnemon remember "Raw note" --no-diff

# Recall — intent-aware graph-enhanced retrieval (default: compact output)
mnemon recall "vector database" --limit 10

# Discovery-only recall — short excerpts, then fetch one full result by ID
mnemon recall "vector database" --brief --excerpt-chars 160
mnemon show <id>

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
mnemon search "authentication" --brief --excerpt-chars 160

# Import — bulk-import a memory draft file (see docs/IMPORT.md for schema and LLM prompt)
mnemon import memory_draft.json
mnemon import --dry-run memory_draft.json   # validate without writing
mnemon import --no-diff memory_draft.json   # skip deduplication

# Forget — soft-delete an insight
mnemon forget <id>
```

Recall filters match the stored category and source exactly. Smart recall applies
them before candidate selection and the result limit; graph traversal stays
within matching memories.

`remember` and `import` skip only byte-identical content already present in an
active memory. Different subjects, changed values, reordered statements, and
near-duplicates are stored as new memories. `remember` still reports advisory
`diff_suggestion` values (`UPDATE`, `CONFLICT`, or `DUPLICATE`); read `action` to
see whether the write was `added` or `skipped`. On an exact repeat, the legacy
`replaced_id` field identifies the existing memory, which remains unchanged.
`--no-diff` also inserts exact repeats.

To retire a superseded memory, store the new fact, verify it with `mnemon show
<new-id>`, then explicitly run `mnemon forget <old-id>`. Similarity alone never
authorizes replacement. Capacity-based auto-pruning still applies separately.

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
| `--brief` | `false` | Emit compact JSON with short excerpts for discovery; fetch selected full content with `mnemon show <id>` |
| `--excerpt-chars` | `240` | Maximum Unicode characters per `--brief` excerpt |
| `--verbose` | `false` | Output full recall response (signals, meta, timestamps) |

The default compact output is optimized for LLM/agent consumption. It includes
`id`, `content`, `category`, `importance`, `intent`, `matched_via`, `confidence`,
and `score`. Use `--verbose` to restore the full payload with signals, traversal
metadata, and timestamps. The confidence label is only emitted in compact mode;
verbose payloads return the raw score for callers that prefer their own thresholds.
For large memories, `--brief` is a smaller discovery projection: it flattens
whitespace, caps each excerpt, emits unindented JSON, and includes one
`detail_command` hint. `search` supports the same two flags. JSON remains the
machine-readable interchange format; the opt-in projection avoids changing
existing parsers or adopting a draft serialization format.

#### Recall intent detection

Automatic intent selection uses a fixed set of lexical cues. It runs locally,
without an LLM or provider. Intent changes graph traversal and ranking; it does
not translate the query or the stored memories. The recognized question forms
include the following (examples use `PostgreSQL` as the subject):

| Language / script | WHY | WHEN | ENTITY |
|---|---|---|---|
| English | Why PostgreSQL? | When did we choose PostgreSQL? | What is PostgreSQL? |
| Mandarin Chinese, simplified | 为什么选择 PostgreSQL？ | 什么时候选择 PostgreSQL？ | PostgreSQL 是什么？ |
| Mandarin Chinese, traditional | 為什麼選擇 PostgreSQL？ | 什麼時候選擇 PostgreSQL？ | PostgreSQL 是什麼？ |
| Hindi, Devanagari | PostgreSQL क्यों? | PostgreSQL कब? | PostgreSQL क्या है? |
| Spanish | ¿Por qué PostgreSQL? | ¿Cuándo elegimos PostgreSQL? | ¿Qué es PostgreSQL? |
| Modern Standard Arabic | لماذا PostgreSQL؟ | متى اخترنا PostgreSQL؟ | ما هو PostgreSQL؟ |
| French | Pourquoi PostgreSQL ? | Quand avons-nous choisi PostgreSQL ? | Qu'est-ce que PostgreSQL ? |
| Bengali, Bengali script | PostgreSQL কেন? | PostgreSQL কখন? | PostgreSQL কী? |
| Portuguese | Por que PostgreSQL? | Quando escolhemos PostgreSQL? | O que é PostgreSQL? |
| Indonesian, Latin script | Mengapa PostgreSQL? | Kapan memilih PostgreSQL? | Apa itu PostgreSQL? |
| Russian, Cyrillic | Почему PostgreSQL? | Когда выбрали PostgreSQL? | Что такое PostgreSQL? |
| German | Warum PostgreSQL? | Wann wurde PostgreSQL gewählt? | Was ist PostgreSQL? |

Matching is case-insensitive and uses Unicode word boundaries for spaced scripts;
Chinese cues also match without spaces. The additional-language forms accept
Unicode whitespace, straight/curly French apostrophes, and composed/decomposed
accents in the listed Spanish/Portuguese cues. Accents are not generally removed.
Arabic accepts ordinary Arabic letters with or without common vowel marks
(harakat and superscript alif) and tatweel. A few explicit variants are included,
such as `為甚麼`, `क्यूँ`, `por quê`, `kenapa`, `зачем`, and `wieso`. Bengali
`কী`/`কি`/`কে` must end the question for ENTITY; bare Hindi `क्या` does not imply
ENTITY. Other dialects, spellings, Arabic presentation forms, and Latin
transliterations of non-Latin scripts are not covered systematically.

Unrecognized queries use `GENERAL`. Additional-language cues that disagree with
one another or with an English/Chinese cue also use `GENERAL`, regardless of
keyword counts. Same-intent mixed-language cues can agree. For compatibility,
English/Chinese-only queries retain their keyword scoring and ENTITY tie-break;
for example, `what is the reason` selects ENTITY. Paired quoted/code spans are
ignored for additional-language cues, while legacy quoted keywords retain their
old behavior. This is a lexical heuristic: it does not resolve negation,
incidental word mentions, nested quotations, or the meaning of mixed questions.
These examples test intent selection, not retrieval accuracy across languages.

The supervising agent can choose an intent from the user's meaning and retain
the original-language query and memories:

```bash
mnemon recall '¿Por qué elegimos PostgreSQL?' --intent WHY --verbose
mnemon recall 'हमने PostgreSQL कब चुना?' --intent WHEN --verbose
mnemon recall 'Was ist PostgreSQL?' --intent ENTITY --verbose
mnemon recall 'PostgreSQL index tuning' --intent GENERAL --verbose
```

The override is language-independent: WHY selects reasons, WHEN timing, ENTITY
what/who, and GENERAL neutral traversal. It takes precedence over detection.
Verbose output reports `meta.intent` and `meta.intent_source` (`auto` or
`override`), including when there are no results. `--basic` bypasses intent
selection entirely.

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
| `MNEMON_AUTO_PRUNE_MIN_AGE` | `24h` | Minimum age before automatic pruning; accepts durations such as `24h`, integer days such as `7d`, or `0` to disable the grace period |

Auto-prune may temporarily leave the active count above the ceiling when every
eligible insight is still inside the grace period. Age is measured from local
store insertion, so newly imported historical memories are protected too. Each deletion is soft,
commits atomically with a `prune` oplog entry, and is returned by ID in
`auto_pruned_ids` alongside the existing `auto_pruned` count.

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
