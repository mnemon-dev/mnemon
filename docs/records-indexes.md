# Composable records indexes

`records query` and the same request over `records serve` accept an optional
`index` object. Without it, or with `{}`, the existing records-v1 ranking and
JSON response remain unchanged. The original JSON, revisions and database format
are unchanged. Indexes are derived in memory for an immutable, scoped selection.

```json
{
  "revision": "a previously returned revision",
  "scope": {"workspaceId": "/absolute/project"},
  "input": {"query": "Why did Athena fail?", "match": "words"},
  "index": {"version": 1, "entities": true, "relations": true},
  "candidateLimit": 200
}
```

| Field | Candidate route |
|---|---|
| `lexical` | Native Unicode token overlap through postings; existing BM25 stays available. |
| `entities` | Native entity extraction and known vocabulary, plus explicit `data.entities` and `data.aliases`. Inferred hot entities are skipped. `input.entities` supplies explicit exact names. |
| `relations` | At most five valid links from each of at most twenty query anchors. Native question intent selects causal/entity, temporal/entity, or entity/semantic links. `input.intent` overrides lexical intent. |
| `temporal` | `data.eventAt`, then `date`, then `createdAt`, parsed as absolute time. Inclusive `since` and `until` constrain all routes. Only explicit `input.timeOrder: "oldest"` or `"newest"` reorders anchors by event time; lexical WHEN intent alone keeps relevance order. |
| `supersedes` | Follow explicit corrections in both directions; current queries place retained, explicitly superseded evidence behind current evidence. `input.history: true`, explicit IDs and an `until` bound retain historical ranking. |
| `recency` | Native Smart Recall's recency anchor signal: the twenty newest records of the selection by index time (`data.eventAt`, then `date`, then `createdAt`), ties by ID. It is a seed list, fused with the base, `lexical` and `entities` lists before `relations`, `supersedes`, `temporal` and `traversal` read the seeds. |
| `traversal` | Native Smart Recall from the twenty best fused seeds: beam search over the valid links (writer and derived-metadata links, as `relations` reads them; `supersedes` links are not traversed), multi-factor re-ranking of every record collected, and for WHY causes before effects. See below. |

### Traversal

`traversal` ports native recall's steps 3–5 (`internal/memory/search/recall.go`,
[Smart Recall](design/05-pipelines.md#52-read-pipeline-smart-recall)). Each anchor starts
with its reciprocal-rank fusion score divided by the best anchor's. A step along a link
adds `1.0 × link weight × intent weight + 0.4 × cosine` to the path's score, where the
intent weight is native's for the link type:

| Intent | causal | temporal | entity | semantic |
|---|---|---|---|---|
| WHY | 0.70 | 0.20 | 0.05 | 0.05 |
| WHEN | 0.15 | 0.65 | 0.10 | 0.10 |
| ENTITY | 0.10 | 0.05 | 0.55 | 0.30 |
| GENERAL | 0.25 | 0.25 | 0.25 | 0.25 |

The cosine term counts only when it is positive, the query is `match: "hybrid"` with an
`embedding` (so the base route has the query's vector) and the neighbor's vector is
already stored; traversal never embeds. From each anchor, the beam keeps the best nodes
of each level and stops at native's budgets (the anchor counts as visited):

| Intent | Beam width | Max depth | Max visited |
|---|---|---|---|
| WHY | 15 | 5 | 500 |
| WHEN | 10 | 5 | 400 |
| ENTITY | 10 | 4 | 400 |
| GENERAL | 10 | 4 | 500 |

Every record keeps its best path score (an anchor its own unless a path beats it);
scores add up along a path. Each anchor's beam starts from the anchor's own normalized
score, even when another anchor's path reached it with a higher one, so the anchors'
order does not change the result. The path score is not the route's order: every collected
record, anchors included, is re-ranked by four signals in [0, 1]:

- keyword: the share of the query's tokens in the record's search text (the `lexical`
  tokenizer);
- entity: the query's entities, as the `entities` route resolves them (known names in the
  query, then `input.entities`), that the record names, over max(1, their number);
- similarity: the cosine of the stored vectors, as in the beam (0 without them);
- graph: the path score, min-max normalized over the collected records (0 when all equal).

The final score is the weighted sum native recall's code runs
(`internal/memory/search/recall.go`), which depends on whether vectors are available, not
on the intent:

| Vectors | keyword | entity | similarity | graph |
|---|---|---|---|---|
| a stored vector was read for some collected record (hybrid query) | 0.30 | 0.15 | 0.35 | 0.20 |
| none | 0.45 | 0.25 | 0 | 0.30 |

The intent-weighted re-ranking table in
[Smart Recall](design/05-pipelines.md#52-read-pipeline-smart-recall) step 4 is outdated
relative to native's code and is not used.

The route is the best `candidateLimit` records by final score (ties by ID). For WHY,
Kahn's algorithm then orders them along the causal links among them, a link's source (the
cause) before its target: the best ranked record whose causes are placed goes next, and
records on a causal cycle follow in rank order. An index with `traversal` also builds the
word and entity indexes for these signals. The intent is `input.intent` when given, else detected from `input.query`, as for
`relations`. Causal and temporal links are followed in their stated direction only (native
recall follows its edges both ways); entity and semantic links are mutual.

An explicit link in `data.links` is
`{"id":"target","version":1,"type":"causal","weight":1}`.
Allowed types are `entity`, `semantic`, `temporal`, `causal`, `supersedes`.
Targets must exist in the same visible selection, have the exact record version,
and have the same scope, workspace and session as the source. Entity and semantic
links are mutual; causal/temporal links retain their stated direction.
General links, including a link labeled supersedes, are advisory retrieval hints.
Only the existing reserved `data.mnemonSupersedes: {"id":"target","version":1}`
channel supplies a correction claim for demotion. The Source SDK protects that
channel from unreviewed model data. Similarity and consolidation proposals never
become correction authority automatically.

Nonempty queries fuse the selected routes with existing RRF. The candidate limit
defaults to 200 and may be 1–2000. The response adds `index` with a canonical
policy identity, pinned revision, route counts, indexed count and a truncation
flag. Literal enumeration and explicit ID reads remain complete. Unknown policy
fields/versions and invalid temporal bounds fail explicitly.

The cache retains at most four enhanced selections and 200,000 additional
document references. This is separate from the unchanged baseline cache budget;
enhanced queries can therefore use more memory. A canceled build does not poison
later requests. Cold build time and warm lookup time must be measured separately.

All features are opt-in. See the workspace experiment report for measured
single-feature and combined effects; an enabled route is not evidence that a
particular corpus benefits from it.

## Derived metadata

A process that derives index metadata from the records (for example the memory
service's consolidation with `nativeEdges`) keeps it beside them, in the
`derived_metadata` table of `records.db`, never in the records. Each source owns
one row per record, `{"version":1,"entities":[…],"aliases":[…],"links":[…],"eventAt":"…"}`,
and replaces all its rows at once (Go: `Store.ReplaceDerivedMetadata`; readonly
stores refuse), writing only the rows that changed. A row has at most 64 entities, 64 aliases and 50 links, with the
link rules above. It applies only while its record has the row's `version` (the
record JSON's version, which links also bind), and only adds to the writer's
`data`: its entities and aliases join the writer's (the entity index takes them
after the writer's entities and aliases and the names extracted from the text,
within the same 64), its links follow the writer's
and, whatever their type, are advisory hints (a derived `supersedes` link expands
but never demotes), and its `eventAt` counts only when the writer gave none. A
replace that changes anything bumps a generation that keys the enhanced caches, so
no query is served a replaced overlay, and one that changes nothing keeps them. Without rows, every response keeps its bytes; records-v1
queries never read the table. A store opened for writing gains the table.
