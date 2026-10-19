# Shared Memory service and paper strategy

`mnemon serve --data-dir /absolute/data --store journal --socket /tmp/mnemon.sock`
uses the same local NDJSON runtime as `mnemon records serve`. Existing records
operations retain their request/response formats. Native reads use the existing
`mnemon.db`; raw records use `records.db`. Existing CLI writes remain available
while the service runs. No old data needs conversion.

| Socket operation | Request | Response |
|---|---|---|
| `native.search` | `query`, optional `limit` | Native scored insights using the existing keyword algorithm. |
| `native.recall` | `query`, optional `limit`, `intent`, `basic`, `category`, `source` | Existing full recall response, or basic recall results. Smart recall retains existing category/source behavior. |
| `native.show` | `id` | Full existing insight. |
| `memory.write` | `text`, optional `id`, `source`, `at`, `scope`, `workspaceId`, `sessionId`, `thread`, `position` | Committed revision, original-document and block references; optional relation-build progress. |
| `memory.read` | `question`, optional `recent`, `scope`, `revision`, `trace` | Original-evidence View, index hints, needs, stop reason and resource use; optional relation statistics and trace. |
| `memory.consolidate` | Optional `scope`, `maxBatches` (1–4), `reset` | Background job progress and usage. |
| `memory.relate` | Optional `scope`, `ids` (up to 200), `maxRecords` (1–16, default 4) | Incremental relation-index progress, accepted edge proposals and decision usage. |

Native search/show/recall retain their access-count and operation-log effects.
The new service returns the full typed search/recall structures; existing CLI
projections are unchanged. `--readonly` keeps native immutable-snapshot behavior
and rejects raw writes/consolidation before model calls. A native-only old store
can serve readonly native reads without creating a records database. Use normal
serve for live visibility of independent CLI writes.

## Raw writes and evidence

`memory.write` stores the original UTF-8 bytes in an archived `memory-document`
record, plus searchable `memory-record` blocks of at most 600 code points. All
records use the existing records JSON format. Default writes run no model.
The parent is retained for direct reads and original-text proof, rather than
entering the candidate pool. Whitespace, Unicode, dates and source are preserved.
Document and block IDs must all be unused. One records CAS commits the complete
document and blocks or none of them. Corrections append another document.

A View reference contains an ID and SHA-256 of the pinned original record JSON.
Each displayed text span is a literal substring of the record. New memory blocks
also prove their exact byte range against the original parent in the same scope.
Evidence `start`/`end` and parent ranges count UTF-8 bytes; text budgets and block
sizes count Unicode code points.
Historical revisions remain readable. Derived index items cite original hashes;
changed/deleted/out-of-scope references cannot supply index hints to a read.

The standalone commands use stdin JSON and stdout JSON:

```sh
mnemon --data-dir /absolute/data --store journal memory-agent write < original.json
mnemon --data-dir /absolute/data --store journal memory-agent read --config /absolute/memory.json < question.json
mnemon --data-dir /absolute/data --store journal memory-agent consolidate --config /absolute/memory.json < job.json
mnemon --data-dir /absolute/data --store journal memory-agent relate --config /absolute/memory.json < job.json
```

## Explicit model composition

Pass `--memory-config /absolute/memory.json` to serve to enable the read strategy.
Native reads and raw writes need no model configuration. Example configuration:

```json
{
  "version": 1,
  "planner": {
    "kind": "local",
    "model": "your-existing-model",
    "baseURL": "http://127.0.0.1:8001/v1"
  },
  "judge": {
    "kind": "jev",
    "model": "jev-1.13.0",
    "keyEnv": "TYPESAFE_API_KEY"
  },
  "budgetFile": "/absolute/private/model-budget.db",
  "index": {"version": 1, "lexical": true, "entities": true},
  "embedding": {
    "url": "http://127.0.0.1:11434",
    "model": "nomic-embed-text:latest",
    "queryPrefix": "search_query: ",
    "documentPrefix": "search_document: "
  }
}
```

The index flags above are an opt-in example, not a universal recommendation.
Omit `index` to keep the original candidate retrieval. Embeddings use the existing
records cache; run `records embed` first to prepare raw vectors. Without an
embedding configuration the strategy uses lexical retrieval.

## Optional decision-scored relations

The strategy can index raw records through native entity extraction, token and
causal signals, prepared document vectors, and a decision model. Add this field
to the configuration above, choosing the capabilities independently:

```json
"relations": {
  "adjacent": true,
  "entity": true,
  "semantic": true,
  "causal": true,
  "onWrite": true
}
```

This strategy configuration is separate from `index.relations`: that older flag
follows writer-supplied `data.links`; it does not automatically construct them.
Omitting `relations` keeps the previous write/read algorithm and wire shape.
Native commands, `mnemon.db`, raw JSON and revisions retain their formats.

For separately written conversation turns, supply `thread` plus `position` on
`memory.write`. The position is the original nonnegative integer turn ordinal,
not an ingestion timestamp. Threads are bounded to 256 bytes and isolated by
record scope, workspace and session. Without these fields, chunks from one
original document can still be neighbors; unrelated writes are not silently
joined by parsing their opaque `source`/`origin` strings. Duplicate positions
disable that group's adjacency rather than guessing its order.

```json
{"text":"Joanna: I finished my first screenplay.","thread":"conversation-1/session-2","position":0}
```

Adjacent records up to two positions away form a deterministic temporal route.
Other candidate relations use at most eight previously written visible records
per source. Jev independently scores shared specific entities, complementary
meaning, and both causal directions. A common speaker/topic or chronological
order is insufficient by itself. Decisions remain hints; they never create
`supersedes`, delete originals or supply correction authority. No generative LLM
is required. Existing JEV provider limits and durable spending caps apply.

`memory-relations.db` is a disposable sidecar, partitioned by scope and the
capability/model profile. Both endpoints bind the store's immutable version IDs.
Deleted, changed or out-of-scope endpoints are checked again against each pinned
read, so stale hints cannot substitute for current originals. An unavailable
sidecar leaves raw reading available and reports `relations.stale`. Structural
neighbors can still be derived from valid raw ordering metadata. One scoped
snapshot is cached per agent, with cancellation and a single cold-build owner.

With `onWrite`, originals commit first, then at most four new blocks are indexed.
When semantic candidates use embeddings, only those new blocks' vectors may be
filled on this path. Index failure is returned inside the successful write's
`relations.error`, together with the committed original references; do not retry
the original write as if it had failed. More blocks or preexisting records are
handled through explicit `memory.relate` jobs. Without `onWrite`, building is
entirely explicit. No background worker or external harness is required.

Prepare old document vectors with `records embed` before semantic backfill.
`memory.relate` does not fill a whole corpus implicitly. Missing source vectors
leave those records pending (`semanticReady: false`, `missingVectors`), rather
than permanently checkpointing an incomplete semantic search. Repeat bounded
jobs while `remaining > 0`; if `visited == 0`, prepare missing vectors first.
Every successful source checkpoints before the next model call; failed decisions
do not advance it. Scored capabilities need a `judge` to build. Adjacency-only
jobs and retrieval diagnostics need no model.

Reads expand at most twelve base anchors through two hops, adding at most 24
records to a 48-candidate pool. Eight records per detailed decision batch leave
room for their literal neighboring context. Jev also decides which neighboring
records are needed to interpret a selected claim; those companions occupy the
same sixteen-record/12,000-code-point View budget. Missing/truncated required
companions prevent a need from being reported covered. Need-specific evidence
can drive the loop even when its global relevance score is lower; new evidence
or a better lead resets the bounded failed-step state. The existing twelve
search, six planner-call and twenty-four decision-call limits remain in force.

`{"question":"...","trace":true}` adds bounded query IDs, candidate scores,
coverage, context decisions, selection/drop reasons and traversed edges. Traces
include no extra original prose and do not change decisions or resource limits.
Model-free diagnostics are candidate-retrieval checks, not answer accuracy.

Omitting `policy` enables planning, screening, judging, coverage, looping and
consolidation lookup. A supplied policy object sets its boolean fields explicitly;
unspecified fields are false. Disabling planning also disables LLM rewrites.
Coverage is required to drive the need loop. A retrieval-only diagnostic policy
sets all six fields false. Model-free diagnostic results are not JEV judgments.

A background-only configuration can set `policy` to `{"consolidation": true}`
and configure only `planner` (plus the budget for a paid planner). Building and
filling the index need no decision model. Reading those hints requires a `judge`;
a builder-only configuration rejects that read explicitly.

The planner sees the last six bounded dialogue messages. Two owned parallel calls
produce up to three searches plus a hypothetical search cue, and up to three needs.
The question is also searched. Pages contribute equal quotas to a pool of at most
48, then RRF fills remaining slots. System 1 screens short excerpts, then judges
at most 24 candidates per round for relevance, explicit changes and per-need
coverage. There are at most three additional rounds, twelve searches, six planner
calls and twenty-four decision calls. A missing need can follow a ranked lead,
page a search, request one rewrite or stop. Empty initial retrieval can rewrite.

The View contains at most sixteen original blocks and 12,000 content code points,
including at most 3,000 index-title/text code points. It retains witnesses for
covered needs before filling with other ranked evidence. A no-longer-current
record is retained when it is still needed, with `changedLater: true`; its literal
text is unchanged. This supports historical questions without presenting an old
value as current. Unneeded superseded records are excluded. `hasEvidence` means
the View has an untrimmed judged witness for the requested information and time;
`covered` is only asserted for a single need. An ALL/collection need does not
claim exhaustive coverage. `stopReason` and `truncated` disclose budget and search
limits. Query timing includes embedding requests; planner/decision timing and
their token/currency usage are reported separately.

## Background consolidation

Consolidation is an explicit bounded job, run separately from reads. Another
client may request it while serve continues serving reads. Each batch visits at
most sixteen unprocessed record versions. A completion model proposes topic
events, changing values and standing user instructions using batch-local aliases.
Code reconstructs and validates the actual original references. The job saves
`memory-index.db` through an atomic compare-and-swap after model work finishes.
It never writes summaries into the original records.

The overlay retains at most 120 topic items, 240 value items and 60 instructions.
It may lag the raw snapshot. Reads use valid linked evidence and nearby index
items, revalidate original hashes, and run System 1 on hints together with their
selected original texts. Hints require current, selected, untruncated original
evidence; an index cannot reintroduce a rejected or superseded claim. Index
vectors reuse the records content-addressed embedding cache. A failed vector
fill can publish the lexical index; a later job fills vectors without repeating
completion work. Updated/deleted source versions retire stale items on the next
job. `reset: true` rebuilds the derived index from originals. A bad/missing
overlay does not prevent raw reads; `indexStale` reports an unusable or lagging
index. No scheduler/plugin framework or DSH process is needed.

## Model and spending boundaries

Local models use a loopback OpenAI-compatible HTTP endpoint. A local completer can
also answer the finite decision questions. Paid JEV versions and DeepSeek Flash
use pinned official endpoints, with redirects and implicit retries disabled.
Structured completions explicitly request JSON mode. Free-text planning is
limited to 1,024 output tokens, structured output to 2,048; a token-length finish
rejects the response. Calls time out after 90 seconds and a memory CLI/RPC request
has a five-minute deadline. An invalid consolidation batch is never published.
Credentials come from environment variables and never enter request arguments,
configuration files, logs or the metering database.

Paid requests require one persistent private budget file shared across profiles
and processes. The hard caps are USD 10 for JEV and CNY 50 for DeepSeek. Code
reserves USD 0.01 or CNY 0.25 before each bounded request. Successful metered calls
settle usage; failed/ambiguous calls retain their reservation. Missing probabilities
and missing paid metering fail explicitly. DeepSeek usage is conservatively
accounted at peak non-cache rates, rather than claiming an exact invoice amount.

Interfaces and prices were checked against the [JEV API](https://docs.typesafe.ai/api),
[JEV models](https://docs.typesafe.ai/models) and
[DeepSeek pricing](https://api-docs.deepseek.com/quick_start/pricing/).
Dataset results and compatibility/performance evidence belong in the workspace
report; this implementation does not claim reproduction of the paper's headline
accuracy from a small local pilot.
