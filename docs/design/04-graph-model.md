# 4. Graph Model & Theory

[< Back to Design Overview](../DESIGN.md)

---

Within the [RLM paradigm](02-philosophy.md#25-theoretical-foundations), MAGMA provides the specific data structure for the external environment that the LLM orchestrates. The core idea of the MAGMA paper is: **a single edge type (such as pure vector similarity) is insufficient to capture the multidimensional relationships between memories.** Different query intents require different relational perspectives — asking "why" requires causal chains, asking "when" requires timelines, asking "about X" requires entity associations.

Mnemon implements four graphs, each capturing one dimension of relationships:

![MAGMA Four-Graph Model](../diagrams/04-magma-four-graph.jpg)

## 4.1 Temporal Graph

**Purpose**: Capture the chronological order of memories, building a temporal skeleton of the knowledge flow.

**Automatically created edges**:

- **Backbone**: New insight → most recent insight from the same source (bidirectional)
  - Ensures memories from each source (user/agent) form a continuous timeline
- **Proximity**: New insight <-> insights within a 24-hour window (bidirectional)
  - Weight formula: `w = 1 / (1 + hours_diff)`
  - Up to 10 proximity edges

```
Insight A (2h ago) ←── backbone ──→ Insight B (1h ago) ←── backbone ──→ Insight C (now)
     ↑                                     ↑
     └──────── proximity (w=0.33) ─────────┘
```

**Metadata**: `{"sub_type": "backbone"|"proximity", "hours_diff": "2.34"}`

## 4.2 Entity Graph

**Purpose**: Link insights that mention the same entities.

**Entity extraction (hybrid approach)**:
1. **Regex patterns**: CamelCase (`HttpServer`), ALL_CAPS (`API`), file paths (`./cmd/root.go`), URLs, @-mentions, Chinese book title marks
2. **Technical dictionary**: 200+ common terms (Go, React, SQLite, Kubernetes...)
3. **User-provided**: `--entities` flag for direct specification

`--entity-mode` controls how these sources are combined: `merge` keeps the default hybrid behavior, `provided` uses only `--entities`, and `auto` uses only automatic extraction.

**Automatically created edges**: New insight <-> up to 5 existing insights per shared entity (bidirectional), weight 1.0.

```
                   ┌─── "Qdrant" ───┐
                   │                │
Insight A ←── entity ──→ Insight B ←── entity ──→ Insight C
("Chose Qdrant")         ("Qdrant perf test")     ("Qdrant deployment config")
```

**Metadata**: `{"entity": "Qdrant"}`

## 4.3 Causal Graph

**Purpose**: Capture the reasons behind decisions and cause-effect relationships.

**Automatic detection**:
1. Content contains causal keywords (`because`, `therefore`, `due to`, `caused by`, `as a result`, etc.)
2. Token overlap with recent insights >= 15%
3. Direction inference: causal direction is determined based on whether the causal keyword appears in the new or existing insight

**LLM-assisted evaluation**:
- `remember` outputs a causal candidate list (discovered via 2-hop BFS)
- The host LLM evaluates these candidates and decides whether to establish connections via `link --type causal`
- Supports sub-type hints: `causes` (direct cause), `enables` (enabling condition), `prevents` (preventing factor)

```
Insight A ──── causal ────→ Insight B
("Team lacks Redis exp.")   ("Chose SQLite as storage")
  sub_type: "causes"
  weight: 0.75
```

This is a quintessential example of the LLM-Supervised philosophy: Binary handles low-cost candidate discovery (regex + token overlap), while the LLM handles high-value causal judgment.

## 4.4 Semantic Graph

**Purpose**: Connect semantically similar insights based on meaning.

**Two-tier confidence system**:

| Tier | Cosine Similarity | Behavior |
|------|-------------------|----------|
| **Auto-link** | >= 0.80 | Automatically create bidirectional edges (high confidence), up to 3 |
| **Candidate review** | 0.40 ~ 0.79 | Output to LLM for evaluation; LLM decides whether to link |
| **Ignore** | < 0.40 | No action |

**Fallback** (without embeddings): Token overlap rate is used instead of cosine similarity.

```
Insight A ←── semantic (auto, cos=0.92) ──→ Insight B

Insight C ←── semantic (LLM review) ──→ Insight D
                cos=0.65, manually linked after LLM judged "related"
```

## 4.5 Four-Graph Synergy: Intent-Adaptive Weighting

Different query intents activate different graph traversal weights:

| Intent | Causal | Temporal | Entity | Semantic |
|--------|--------|----------|--------|----------|
| **WHY** | **0.70** | 0.20 | 0.05 | 0.05 |
| **WHEN** | 0.15 | **0.65** | 0.10 | 0.10 |
| **ENTITY** | 0.10 | 0.05 | **0.55** | 0.30 |
| **GENERAL** | 0.25 | 0.25 | 0.25 | 0.25 |

When asking "why was SQLite chosen," the causal edge weight is highest, so the system traces decision rationale along causal chains. When asking for "memories related to React," the entity edge weight is highest, so the system finds all insights mentioning React.

---

## 4.6 Graph-LLM Theoretical Foundations

The following sections establish the theoretical basis for why graph databases are the native storage model for LLMs, and why `remember / link / recall` constitutes a universal protocol for agent memory systems.

### Structural Isomorphism

LLM attention, graph data models, and natural language all describe the same thing: weighted associations between entities.

```
LLM Attention:     token ←weight→ token
Graph Model:       node  ←edge→   node
Natural Language:  subject ←predicate→ object
```

Relational databases force network relationships into tables. Vector databases retain only one relationship type (similarity). Only graphs preserve full relational semantics.

### The Three-Step Paradigm: Extract → Candidate → Associate

Graph construction engines universally decompose into three steps:

| Step | Purpose | mnemon Implementation |
|------|---------|----------------------|
| **Extract** | Parse raw input into structured units | `remember` → nodes + entities |
| **Candidate** | Find potential connections | `semantic_candidates` / `causal_candidates` |
| **Associate** | Establish typed, weighted edges | `link` → 5 edge types |

### Spectrum Across Database Types

The three-step model is a spectrum — the more semantically rich the data model, the more complete all three steps are:

| Database Type | Extract | Candidate | Associate |
|--------------|---------|-----------|-----------|
| **Graph** | Full | Full | Full (multi-type edges) |
| **Relational** | Schema mapping | PK/unique dedup | Foreign keys (fixed at DDL time) |
| **Document** | Structure mapping | _id dedup | Nested refs (lose global traversability) |
| **Vector** | Text → embedding | ANN dedup | Metadata only (single relation type) |
| **KV** | Key:value | Key existence check | _(nearly none)_ |

### Read-Write Symmetry (Unique to Graphs)

On graph databases, the read and write paths mirror each other using the same three-step model:

```
Write:  Extract → Candidate → Associate     (text → graph)
Read:   Extract → Candidate → Associate     (graph → text)
        (parse    (retrieve)   (traverse)
         query)
```

| | Write (Construction) | Read (Query) |
|--|---------------------|--------------|
| **Extract** | Text → entities/facts | Question → intent/keywords |
| **Candidate** | Find potential related nodes | Find potential matching nodes |
| **Associate** | Create edges (persist) | Traverse edges (rank & return) |
| **Reason** | _(optional: LLM judges whether to link)_ | LLM synthesizes results into answer |

This symmetry does NOT hold for other database types — relational write is schema mapping while read is join planning; the two share no cognitive model.

**Implication**: An LLM needs to master only one cognitive pattern to handle both graph reads and writes.

### From the LLM Perspective: Query → Reason

Regardless of the underlying database, LLM interactions on the read side collapse to two steps:

```
Natural language → [Query (tool call)] → Structured results → [Reason] → Natural language answer
```

This is the RAG paradigm applied to any data store. The variation lies in the translation layer complexity:

- **Text-to-SQL**: must understand schema
- **Text-to-Cypher**: must understand graph structure
- **Text-to-Vector**: encode only, near-zero translation

### Other Storage Types as Degenerate Graphs

| Storage Type | What's Lost Compared to Graph |
|-------------|------------------------------|
| **KV** | Isolated nodes, zero edges |
| **Relational** | Edges compressed to foreign keys, types fixed in schema |
| **Document** | Edges inlined as nesting, global traversability lost |
| **Vector** | All edges are a single type (similarity), no semantic distinction |

A vector database can answer "what is **similar** to what" but cannot answer "what **caused** what" or "what **belongs to** what". Graphs can.

### remember / link / recall as Universal Algebra

The three-step paradigm (Extract → Candidate → Associate) maps directly to three primitive operations: **remember**, **link**, **recall**. This is not an implementation detail of mnemon — it is the minimal complete interface for any agent memory system.

```
Any memory system = remember(write) + link(associate) + recall(retrieve)
```

### Cross-System Validation

| System | remember | link | recall |
|--------|----------|------|--------|
| **mnemon** | Explicit three-step | Explicit 5 edge types | Graph traversal |
| **OpenViking** | File write to viking:// | Directory placement (implicit, containment only) | Path navigation + semantic search |
| **mem0** | add() | Auto dedup/merge | search() |
| **Letta/MemGPT** | insert() | Tiered storage (core/recall/archival) | query() |
| **Native RAG** | embed + upsert | _(none)_ | ANN search |

Every system implements all three operations. The differences lie in:

1. **Explicitness of link**: From mnemon's explicit multi-type edges to RAG's complete absence
2. **Timing of link**: Pre-computed at write time (mnemon) vs inferred by LLM at query time (OpenViking)
3. **Signal dimensions of recall**: Multi-signal weighted (mnemon) vs single-signal (vector/path)

### OpenViking: link Folded into remember

OpenViking adopts a file system paradigm — memories, resources, and skills are organized as directories under the `viking://` protocol with L0/L1/L2 tiered context loading. Its `link` step has not disappeared but is **folded into `remember`**: choosing which directory to place a file in IS the linking decision, reduced to a single classification problem (containment edge only).

This represents an explicit architectural trade-off: push association complexity from storage time to inference time, relying on LLM's reasoning capability within the context window. This works when memory volume fits within context limits, but loses advantage as memories scale beyond what the LLM can process in a single pass.

### Degeneracy Spectrum

The three primitives form a spectrum of degeneracy:

```
mnemon          fully explicit remember + link + recall
OpenViking      link folded into remember (implicit containment)
mem0            link automated (dedup/merge heuristics)
Letta/MemGPT    link reduced to tier placement
Native RAG      link absent entirely
```

The more degenerate the `link` operation, the more burden falls on the LLM at recall time to infer associations that were never stored.

### The Protocol Gap: LLM ↔ Database

### The Missing Layer

The existing protocol stack has a gap between LLMs and databases:

```
  LLM
   ↕  MCP (LLM ↔ Tools)         ← standardized
  Tools
   ↕  ??? (LLM ↔ Database)      ← no protocol exists
  Database
   ↕  ODBC/JDBC (App ↔ Database) ← standardized
  Storage
```

MCP standardizes how LLMs discover and invoke tools. ODBC/JDBC standardizes how applications access databases. But **how LLMs interact with databases using memory semantics** — this layer has no protocol.

Every project reinvents this layer independently: Mem0 builds its own, OpenViking builds its own, Claude Code's CLAUDE.md builds its own (by bypassing the problem entirely with file injection). Each conflates two fundamentally different problems:

1. **LLM-DB interaction protocol** (how to read and write) — an LLM problem
2. **DB engine optimization** (how to store and query efficiently) — a database problem

### The Industry Anti-Pattern

Current agent memory systems are monoliths that couple protocol and storage:

```
Mem0             = protocol + custom storage engine
Claude Code Mem  = no protocol (file injection into context window)
OpenViking       = protocol + virtual filesystem engine
MemGPT           = protocol + tiered memory manager
```

This is equivalent to every web application inventing its own HTTP. The result: no interoperability, no backend portability, no leverage of the existing database ecosystem.

### remember / link / recall as Protocol Primitives

The three primitives derived from our analysis are not just a taxonomy — they are the specification of an **LLM-to-Database interaction protocol**, analogous to MCP:

```
MCP:  LLM ↔ Tool
      3 primitives: resources / tools / prompts

MLP:  LLM ↔ Database
      3 primitives: remember / link / recall
      Write path = remember + link
      Read path  = recall
```

| Dimension | MCP | Memory Layer Protocol |
|-----------|-----|-----------------------|
| **Problem** | How LLMs discover and invoke tools | How LLMs read/write databases with memory semantics |
| **Primitives** | 3 (resources / tools / prompts) | 3 (remember / link / recall) |
| **Backend-agnostic** | Any tool implements MCP server | Any DB implements protocol adapter |
| **Protocol nature** | Discovery + invocation | Write + associate + retrieve |

### Protocol Definition

```
Write protocol:
  remember(content, metadata) → node_id, candidates[]
  link(source, target, type, weight) → edge_id

Read protocol:
  recall(query, options) → ranked_results[]
```

Three verbs covering all LLM-DB memory interactions. Any database that implements an adapter for these three interfaces can serve as an LLM memory backend.

### Backend Adapter Spectrum

The protocol naturally accommodates different storage backends with varying levels of expressiveness:

```
              remember    link              recall
              ─────────   ────────────────   ──────────────────
Neo4j         CREATE node  CREATE edge       MATCH + traverse
TigerGraph    add vertex   add edge          GSQL query
Milvus        upsert vec   metadata ref      ANN search
PostgreSQL    INSERT row   INSERT FK/join     SELECT + JOIN
Redis         SET key      _(degenerate)_     GET key
SQLite        INSERT row   INSERT edge table  multi-signal query
```

Graph databases implement the protocol most naturally — all three primitives map directly. Relational databases need a translation layer. KV stores can only implement remember + recall (link degenerates).

### Strategic Implication

This reframes mnemon's position in the ecosystem:

```
         Monolithic systems              Protocol layer
         (product approach)              (platform approach)

Mem0  ──┐                         ┌── Neo4j adapter
CC Mem──┤ Each reinvents its      │── TigerGraph adapter
Viking──┤ own storage layer       │── Milvus adapter
MemGPT──┘                         │── SQLite adapter (mnemon current)
                                   └── PostgreSQL adapter

                                   ↑
                              mnemon's position:
                              not another database,
                              but the LLM ↔ DB protocol gateway
```

- **Not competing with Neo4j** on storage engines (DB problems belong to DB)
- **Not competing with Mem0** on product features (it is a product bound to its implementation)
- **Analogous to MCP** — MCP connected LLMs to the tool ecosystem; this protocol connects LLMs to the database ecosystem

### Academic Landscape and Positioning

### Prior Art Assessment

| Claim | Closest Prior Art | Novelty |
|-------|------------------|---------|
| **Structural isomorphism** (graphs = native LLM storage) | Transformers-as-GNNs (arXiv 2506.22084, 2012.09699) — computational equivalence proven, but not extended to external storage | **High** |
| **remember/link/recall as universal algebra** | CoALA (Princeton, TMLR 2024) — retrieval/reasoning/learning, but link not separated as first-class primitive | **High** |
| **Extract → Candidate → Associate** | NER → Entity Linking → Relation Extraction — classical KG pipeline | **Low** (generalization across memory systems is new) |
| **Read-write symmetry on graphs** | MAGMA (arXiv 2601.03236) argues for intentional asymmetry (fast write / slow read) | **High** (counter-evidence exists) |
| **Other storage as degenerate graphs** | arXiv 2602.05665 (HK PolyU, Feb 2026): "traditional memory forms can be viewed as degenerate or simplified cases within the graph memory paradigm" | **Medium** (convergent discovery) |
| **LLM ↔ DB interaction protocol** | No prior art found — all existing systems couple protocol with storage | **High** |

### Positioning in the Field

```
Textbook-level (established)
  │  NER → Entity Linking → Relation Extraction
  │
Widely recognized (high-citation papers)
  │  CoALA retrieval/reasoning/learning (Princeton, TMLR)
  │  Transformers = GNNs (computational equivalence)
  │
Emerging consensus (2026 surveys)
  │  Storage types as degenerate graphs (arXiv 2602.05665)
  │  GraphRAG > Vector RAG (Neo4j Manifesto)
  │
── our position ──────────────────────────────
  │
Original contributions (no prior formulation found)
  │  ① remember/link/recall universal algebra with link as first-class primitive
  │  ② Structural isomorphism → external storage prescription
  │  ③ Read-write symmetry on graphs (note: MAGMA's asymmetry is an engineering counter-argument)
  │  ④ LLM ↔ DB protocol layer — separating protocol from storage engine
```

### Key References

**Foundational frameworks:**
- CoALA: Cognitive Architectures for Language Agents (Sumers et al., Princeton, TMLR 2024)
- Memory in the Age of AI Agents (Yuyang Hu et al., arXiv 2512.13564, Dec 2025)
- Graph-based Agent Memory survey (Chang Yang et al., arXiv 2602.05665, Feb 2026)

**Graph memory systems:**
- MAGMA (Jiang et al., arXiv 2601.03236, Jan 2026)
- Graphiti/Zep (Rasmussen et al., arXiv 2501.13956, Jan 2025)
- Mem0 (arXiv 2504.19413, Apr 2025)

**Transformers-as-graphs:**
- Transformers are Graph Neural Networks (arXiv 2506.22084, Jun 2025)
- A Generalization of Transformer Networks to Graphs (arXiv 2012.09699, 2020)

### Validation: mnemon Architecture

mnemon's design directly reflects these insights:

```
remember → Extract + Candidate (semantic_candidates / causal_candidates)
link     → Associate (semantic / causal / entity / temporal / narrative)
recall   → Extract + Candidate + Associate (intent detection → multi-signal retrieval → graph traversal)
```

Five edge types preserve five distinct relational semantics. Degenerating to pure vector retrieval would retain only `semantic` — losing ~80% of relational information. MAGMA ablation studies confirm: removing causal edges drops accuracy 3-5%, removing temporal edges drops it further.

### Summary

- **Extract → Candidate → Associate** is the universal paradigm for graph construction engines
- This three-step model achieves its **most complete expression** on graphs and degenerates toward KV
- On graphs, read and write paths are **symmetric** — both follow the same three-step model in opposite directions
- From the LLM perspective, reads universally collapse to **Query → Reason**
- Graphs are the native storage model for LLMs because they are **structurally isomorphic**: both represent weighted associations between entities
- **remember / link / recall** is the universal algebra for agent memory systems — every system is an instantiation of these three primitives, with varying degeneracy of `link`
- The three primitives define an **LLM ↔ Database interaction protocol** — analogous to MCP for tools, filling the missing layer between LLMs and the database ecosystem
- mnemon's strategic position is **protocol gateway**, not database engine — separating the LLM interaction problem from the storage optimization problem
