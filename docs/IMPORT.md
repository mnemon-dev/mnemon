# Mnemon — Memory Import Guide

[中文](zh/IMPORT.md) | **English**

This guide explains how to bulk-import historical chat logs or external context
into the Mnemon memory graph.

---

## Workflow

```
Chat export / Markdown -> LLM extraction prompt -> memory_draft.json -> mnemon import <file>
```

1. Export the source chat log or notes as Markdown or plain text.
2. Send the text to an LLM together with the reference prompt below to generate
   a `memory_draft.json` file that matches this schema.
3. Run `mnemon import memory_draft.json`. Mnemon handles deduplication, graph
   edge construction, embeddings when available, and lifecycle scoring.

---

## Import File Format (`schema_version: "1"`)

```json
{
  "schema_version": "1",
  "source": "chat-export",
  "insights": [
    {
      "content": "Chose Qdrant over Milvus for vector search because filtered query performance was better.",
      "category": "decision",
      "importance": 5,
      "tags": ["architecture", "search", "vector-db"],
      "entities": ["Qdrant", "Milvus"],
      "source": "agent",
      "created_at": "2024-03-15T09:30:00Z"
    },
    {
      "content": "The user prefers concise API responses without extra explanatory text.",
      "category": "preference",
      "importance": 4,
      "tags": ["ux", "api"]
    }
  ],
  "edges": [
    {
      "source_index": 0,
      "target_index": 1,
      "edge_type": "causal",
      "weight": 0.7,
      "reason": "The vector engine decision influenced API response design."
    }
  ]
}
```

---

## Fields

### Top-Level Fields

| Field | Type | Required | Description |
|---|---|---|---|
| `schema_version` | string | **yes** | Must be `"1"` |
| `source` | string | no | Source label for the whole import, such as `"chat-export"` or `"manual"`. A per-insight `source` overrides this value |
| `insights` | array | **yes** | Memory nodes to import; must contain at least one item |
| `edges` | array | no | Explicit relationships. Mnemon also creates graph edges automatically; these supplement strong relationships the draft already knows |

### Insight Fields

| Field | Type | Required | Constraint | Description |
|---|---|---|---|---|
| `content` | string | **yes** | Max 8000 characters | Memory text |
| `category` | string | no | See below; default `general` | Knowledge type |
| `importance` | integer | no | 1-5; default 3 | Retention and ranking signal |
| `tags` | array | no | Max 20 items; each max 100 characters | Free-form labels for filtering and search |
| `entities` | array | no | Max 50 items; each max 200 characters | Named subjects such as people, projects, tools, libraries, or organizations. Mnemon merges these with automatic extraction |
| `source` | string | no | - | Overrides the top-level `source` for this insight |
| `created_at` | string | no | RFC 3339 | Original creation timestamp; import time is used when omitted |

#### Categories

| Value | Use For |
|---|---|
| `preference` | User preferences, habits, style requirements |
| `decision` | Confirmed technical or product decisions |
| `fact` | Objective facts, data, limits, specifications |
| `insight` | Reasoning conclusions, analysis, lessons learned |
| `context` | Project background, status, constraints |
| `general` | Anything that does not fit the categories above |

#### Importance Guidance

| Value | Meaning |
|---|---|
| 5 | Core decision or strong preference that should be retained long-term |
| 4 | Important context that should usually be retained |
| 3 | Normal memory; default |
| 2 | Minor detail that may be pruned later |
| 1 | Temporary or low-value information |

### Edge Fields

| Field | Type | Required | Constraint | Description |
|---|---|---|---|---|
| `source_index` | integer | **yes** | Zero-based index into `insights` | Edge source |
| `target_index` | integer | **yes** | Must differ from `source_index` | Edge target |
| `edge_type` | string | **yes** | See below | Relationship type |
| `weight` | float | no | 0.0-1.0; default 0.5 | Relationship strength |
| `reason` | string | no | - | Explanation for the relationship; stored as edge metadata |

#### Edge Types

| Value | Meaning |
|---|---|
| `temporal` | Time ordering; event A happened before event B |
| `causal` | Causal influence; A caused or affected B |
| `semantic` | Semantic similarity; A and B discuss the same topic |
| `entity` | Entity co-occurrence; A and B mention the same named subject |
| `supersedes` | Authority claim; A replaces B and B is demoted at recall |

---

## Commands

```bash
# Basic import
mnemon import memory_draft.json

# Validate without writing
mnemon import --dry-run memory_draft.json

# Skip duplicate/conflict detection and insert every entry as new
mnemon import --no-diff memory_draft.json

# Import into a specific store
mnemon import --store project-alpha memory_draft.json
```

### Output Example

```json
{
  "imported": 8,
  "updated": 1,
  "skipped": 2,
  "errors": 0,
  "edges_inserted": 3,
  "auto_pruned": 0,
  "results": [
    {"index": 0, "id": "a1b2c3d4...", "content": "Chose Qdrant...", "action": "added"},
    {"index": 1, "id": "e5f6a7b8...", "content": "The user prefers...", "action": "skipped"}
  ]
}
```

| Field | Description |
|---|---|
| `imported` | Number of newly added memories |
| `updated` | Number of existing conflicting memories replaced |
| `skipped` | Number of duplicate memories skipped |
| `errors` | Number of failed writes. Import allows partial success; script callers should check this is `0` |
| `edges_inserted` | Number of explicit edges inserted |
| `auto_pruned` | Number of memories auto-pruned after capacity checks |

---

## Reference Prompt for `memory_draft.json`

Send this prompt to an LLM together with the source chat log or document:

```text
You are a memory extraction assistant. Extract valuable durable knowledge from
the chat log or document below and generate a JSON file that matches the Mnemon
memory draft format (schema_version: "1").

## Extraction rules

1. Each insight must be an independent, complete knowledge unit that can be
   understood without the original conversation.
2. Remove small talk, repeated phrasing, and content with no durable value.
3. If the same topic appears multiple times, merge it into one complete insight
   instead of duplicating it.
4. Assign importance with this priority:
   - 5: critical architecture decision or explicit core user preference
   - 4: important context or repeated pattern
   - 3: normal fact or background
   - 2: minor detail or one-off mention
   - 1: temporary state or very low-value information
5. Put concrete nouns in entities: people, projects, tools, libraries,
   organizations, APIs, services, or product names.
6. Use lowercase English tags with hyphen separators, such as "vector-db" or
   "api-design".
7. If the original timestamp is inferable, write it in created_at using RFC 3339.
8. Only add edges for obvious strong relationships. Do not over-connect.

## Output requirements

- Output JSON only. Do not include explanatory text.
- Follow this schema exactly:

{
  "schema_version": "1",
  "source": "chat-export",
  "insights": [
    {
      "content": "...",
      "category": "preference|decision|fact|insight|context|general",
      "importance": 1-5,
      "tags": ["tag1", "tag2"],
      "entities": ["Entity1", "Entity2"],
      "created_at": "2024-01-15T09:30:00Z"
    }
  ],
  "edges": [
    {
      "source_index": 0,
      "target_index": 1,
      "edge_type": "causal|semantic|temporal|entity",
      "weight": 0.0-1.0,
      "reason": "..."
    }
  ]
}

## Source content

[Paste the chat log or document here]
```

---

## FAQ

**Is `created_at` required?**

No. Mnemon uses import time when it is omitted. If the source chat log includes
timestamps, include them to preserve historical ordering.

**How do I verify the import?**

Run `mnemon log`, `mnemon status`, or `mnemon search <keyword>` after importing.

**Is the `edges` array required?**

No. Mnemon automatically creates temporal, semantic, and entity edges. Explicit
edges are for strong relationships the LLM can confidently identify.

**How should I import large chat histories?**

Split them by time range or topic and import each draft in order. Duplicate
content is skipped by the built-in diff unless `--no-diff` is used.

**Can I import non-English content?**

Yes. `content`, `tags`, and `entities` support Unicode text.
