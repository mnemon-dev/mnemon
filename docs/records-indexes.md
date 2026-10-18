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
| `temporal` | `data.eventAt`, then `date`, then `createdAt`, parsed as absolute time. Inclusive `since` and `until` constrain all routes. WHEN queries rerank existing anchors by event time. |
| `supersedes` | Follow explicit corrections in both directions; current queries place retained, explicitly superseded evidence behind current evidence. `input.history: true`, explicit IDs and an `until` bound retain historical ranking. |

An explicit link in `data.links` is
`{"id":"target","version":1,"type":"causal","weight":1}`.
Allowed types are `entity`, `semantic`, `temporal`, `causal`, `supersedes`.
Targets must exist in the same visible selection, have the exact record version,
and have the same scope, workspace and session as the source. Entity and semantic
links are mutual; causal/temporal links retain their stated direction.
Supersession is an explicit writer claim, never inferred from similarity or
installed automatically from a model's suggestion.

Nonempty queries fuse the selected routes with existing RRF. The candidate limit
defaults to 200 and may be 1–2000. The response adds `index` with a canonical
policy identity, pinned revision, route counts, indexed count and a truncation
flag. Literal enumeration and explicit ID reads remain complete. Unknown policy
fields/versions and invalid temporal bounds fail explicitly.

The cache retains at most four enhanced selections and 150,000 additional
document references. This is separate from the unchanged baseline cache budget;
enhanced queries can therefore use more memory. A canceled build does not poison
later requests. Cold build time and warm lookup time must be measured separately.

All features are opt-in. See the workspace experiment report for measured
single-feature and combined effects; an enabled route is not evidence that a
particular corpus benefits from it.
