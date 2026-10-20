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
| `memory.write` | `text`, optional `id`, `source`, `at`, `scope`, `workspaceId`, `sessionId` | Committed revision, original-document reference and block references. |
| `memory.writeConversation` | `messages` (`role`, `content`, optional RFC 3339 `at`), `sessionId`, optional `id`, `source`, `scope`, `workspaceId` | Same as `memory.write`. |
| `memory.read` | `question`, optional `recent`, `scope`, `revision`, `maxItems` | Original-evidence View, index hints, needs, stop reason and resource use. |
| `memory.consolidate` | Optional `scope`, `maxBatches` (1–4 for memory-agent/v1, 1–64 for timelines and layered), `reset`, `hold` (timelines and layered), `deferEdges`, `rebuildEdges` | Background job progress and usage. |

`maxItems` caps the records of a `"strategy": "simple"` View: 0 or absent takes the
configured `view.maxItems`, and more than it is an error. memory-agent/v1 reads have no
View setting and reject a nonzero `maxItems`. A simple read's `stats.degraded` lists the
stages that fell back instead of failing the read, each once and in order: `scan-budget`
and `loop-budget` (the decision budget ran out and the need loop stopped), `judge` (the
final judgement failed or found the budget spent; JEV's screen stands in), `layer-state`
(the consolidated state did not load; no layer), `layer-lexical` (the layer's embedding
failed; shared words rank the topics), `layer` (the layer's judgement failed) and `expand`
(linked records could not be read or judged). It is absent when nothing fell back, and
always for memory-agent/v1. With an `index` setting, a simple read's `stats.indexRoutes`
counts, for each native route (`lexical`, `entities`, `relations`, `temporal`,
`supersedes`, `recency`, `traversal`), the read's searches in which that route found a
candidate, and under `traversal+` the searches whose traversal reached a record over a link
(not only its anchors).

`"view": {"intent": "question"}` makes a simple read detect the intent of its `question`
once (native recall's WHY, WHEN, ENTITY or GENERAL detection) and pass it as `input.intent`
to every records search it sends, the message's, the note's, the planner's and the
loop's, so the `relations` and `traversal` routes weigh links by what was asked rather
than by each search's own words; `stats.intent` reports it. Absent or `""`, each search
detects its intent from its own text, as before, and `stats.intent` is absent. Other
values are refused. The setting changes nothing without an `index` that reads intent.

With `"consolidation": {"mode": "timelines"}` a job reads the records written since the
last one into a pending list, folds every full batch of `batchCharacters`, and folds the
short rest only when it reads nothing new and nothing is left unread. `hold: true` folds
full batches only, even then, and leaves the rest for a later job without `hold` (for
example once the conversation pauses). The pending text, headers included, stays within
`maxBatches × batchCharacters` UTF-16 units: records past it stay unread until the pending
list drains (when that leaves no full batch, the pending records fold as one, since more
follow), and `remaining` counts them with the pending ones. The result also reports
`folded` (batches taken off the pending list), `dropped` (records given up on) and
`drops` (each drop's record count and reason). A batch whose reply does not parse is
asked twice and then in halves; a half that still fails is dropped. A request too large
to send is split down to single records; a single record still too large is dropped. A
model error keeps the batch pending and ends the job. After three such errors on the
batch at the head of the pending list, the first at least ten minutes earlier (the
caller's cancellation and a spent budget do not count), the next job splits that batch
once and drops a half whose call fails again. Dropped records stay searchable; the
consolidated index does not cover them.

With `"consolidation": {"mode": "layered"}` a job reads, holds, bounds, flushes and saves
the pending records as in timelines mode, writes the same state (marked `"mode":
"layered"`) and simple reads add the same layer, but each batch is folded in two steps.
First a model extracts the batch alone (L0): the timelines prompt without its references
to an index of the memory so far, plus one sentence asking for the entities each event
and fact is about, and a request that holds only the batch's records, with the same
retries, splits and drops. Up to `parallel` (default 4, 1–16) of a job's batches are
extracted at once. Then, strictly in batch order, JEV links each batch into the stored
memory (L1a): every new thread is compared with the `linkCandidates` (default 8, 1–32)
stored threads nearest to it, every new key with the nearest stored keys (as
`key: latest value`), nearest by the cosine of document embeddings when `embedding` is
set (the stored texts' vectors are kept, the batch's own are not), else, or when the
embedder fails, by shared words. JEV answers whether the thread continues a candidate,
whether the key is the same attribute of the same thing, and whether its value
contradicts the candidate's latest without the records saying it changed. A thread joins
its best yes (at least 0.5; ties go to the nearer candidate) or starts a thread. A batch
key with the same name as a stored key always links to it (only the contradiction
question is asked); otherwise it links to its best yes, else starts a key. A
contradiction marks the appended values as conflicts. Directives are de-duplicated as in
timelines mode. The questions are packed into as few JEV calls as fit 128 questions and
96,000 UTF-16 units. When a JEV call fails or its budget is spent, the batch links nothing
but its keys of the same name and the job goes on. A model error in a batch's extraction
stops the job there: the batches before it stay linked, it and the later ones stay
pending, and the error counts toward the drop rule as in timelines mode. Layered mode
requires a decision model (`judge`) and a completion model (`consolidator`, else
`planner`). It costs one extraction call per batch (more on retries and splits), whose
request does not grow with the memory; once the memory holds anything, a few JEV calls per
batch (up to `linkCandidates` questions per new thread and twice that per new key, at
most 128 a call); and embeddings of the new and changed thread and key texts. The result
adds `linkCalls` and `linkUsage` (the JEV calls, and the part of `usage` of every JEV call),
`threadsLinked`/`threadsNew` and `keysLinked`/`keysNew` (extracted threads and keys that
joined stored ones or started new ones), `conflicts` (joined keys JEV found contradicted),
`linkFallbacks` (batches linked without JEV) and `linkLexical` (batches whose candidates
shared words found because the embedder failed). Extractions run in parallel, so
`modelMs`, which sums the time of every model call, can exceed the job's wall time.

With `"nativeEdges": true` (timelines or layered mode) the consolidated state also
feeds the records' native indexes ([records-indexes.md](records-indexes.md)): after
every job that changed the memory since they were last built (also one that ended in a
model error or a reset, not a canceled one), and on `rebuildEdges: true`, which folds
nothing, the job replaces the records' derived metadata of source `consolidation` with
what every stored scope state says (writing only the rows that changed). A job with
`deferEdges: true` skips that step; its result then reports `edgesPending`, and one later
job or `rebuildEdges` builds the edges of many (the AML adapter defers its jobs and
rebuilds once after them). A failed edge step does not fail the job: it reports
`edgeError` and `edgesPending`, and the next job retries. Without `nativeEdges`,
`rebuildEdges` removes the consolidation's derived metadata. A record's `entities` are
the names of the events and values citing it (layered extraction names them; timelines
names none) and its `aliases` the other names of their clusters, then the words a query
can name a multi-word name by: each capitalized word of three letters or more that is no
stopword, month, weekday, role or first-person word ("Greg Hall" gives "Greg" and
"Hall"). For the clusters, each new name is compared, one JEV question each, with up to
`linkCandidates` (8 in timelines mode) names known before it whose contexts (the name and
its first three texts) are nearest, as linking finds candidates, and joins the cluster of
its best yes (at least 0.5); the decision is kept in the state, so each name is decided
once, and a name JEV failed to decide stays alone. In date order, the records
of each event of a thread link (`entity`, weight 0.6) to those of its 4 nearest events
and (`temporal`, 0.5) to those of the previous and the next one; the records of a key's
value link (`supersedes`, 1) to those of the value before it when the two differ (a hint
that expands, never demotes); a record's `eventAt` is the earliest session date citing it,
used only when its writer gave none (`memory.write` and `memory.writeConversation`
always give one). Links bind the records' current versions. Turn a route on per read
with the `index` setting. The result adds `edgeRecords` and `edgeLinks` (rows written
and their links by type), `aliasCalls` (JEV calls, part of `linkUsage`), `aliasMerges`
and `aliasFallbacks` (names that joined a cluster, or stayed alone because JEV failed).

Native search/show/recall retain their access-count and operation-log effects.
The new service returns the full typed search/recall structures; existing CLI
projections are unchanged. `--readonly` keeps native immutable-snapshot behavior
and rejects raw writes/consolidation before model calls. A native-only old store
can serve readonly native reads without creating a records database. Use normal
serve for live visibility of independent CLI writes.

## Raw writes and evidence

`memory.write` stores the original UTF-8 bytes in an archived `memory-document`
record, plus searchable `memory-record` blocks of at most 600 code points. All
records use the existing records JSON format. No model runs on this write path.
The parent is retained for direct reads and original-text proof, rather than
entering the candidate pool. Whitespace, Unicode, dates and source are preserved.
Document and block IDs must all be unused. One records CAS commits the complete
document and blocks or none of them. Corrections append another document.

`memory.writeConversation` stores chat messages with the same guarantees, packed
the way the reference system packed conversations. Each message becomes a
`role: text` line (whitespace runs collapsed, lines over 540 UTF-16 units wrapped
at a space), and lines are grouped into chunks of about 600 UTF-16 units, each
headed `Conversation <sessionId> on YYYY/MM/DD (Mon) HH:MM:` (UTC, from the first
message that has a timestamp) or `Conversation <sessionId>:` without one. The
parent holds the chunks joined by a blank line and every `memory-record` is one
chunk, so its `start`/`end` locate it in the parent. `data.session` names the
conversation and `data.eventAt` is the first timestamp, else the write time. A
duplicate document id fails with `document id already exists`.

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
use pinned official endpoints, with redirects and implicit retries disabled. An
`openai` model uses the configured `baseURL` (HTTPS, or HTTP on loopback) plus
`/chat/completions`, requires `keyEnv`, and is priced in USD from a built-in table
(`gpt-4.1-mini`, `gpt-4o-mini`) or from `priceIn`/`priceOut` (USD per 1M tokens),
which are required for any other model. Structured completions explicitly request
JSON mode. By default free-text planning is limited to 1,024 output tokens,
structured output to 2,048; `maxTokens` overrides both (DeepSeek at most 16,384),
and DeepSeek `thinking: true` enables high-effort reasoning without a temperature.
A token-length finish rejects the response. A JEV decision accepts 1–128 questions and a
request of 96,000 UTF-16 code units of JSON (as the reference client measures it); every
other kind accepts 1–80 questions, and every completion request at most 160,000 bytes,
encoded without HTML escaping. A call times out after the model's `timeoutSeconds`
(1–1,800; 90 when unset), counted from sending the request to reading the whole reply, and
a memory CLI/RPC request has a five-minute deadline. An invalid consolidation batch is
never published. Credentials come from environment
variables and never enter request arguments, configuration files, logs or the
metering database.

Paid requests require one persistent private budget file shared across profiles
and processes. The default hard caps are USD 10 for JEV, CNY 50 for DeepSeek and
USD 5 for OpenAI; `"budgets": {"jev": 10, "deepseek": 60, "openai": 5}` in the
memory configuration replaces them. A process enforces its configured cap from the
start and writes it over the stored one; the amount spent is kept. (Builds before
configurable `budgets` kept the lower of the built-in and the stored cap.) Code reserves
USD 0.01 (JEV, OpenAI) or CNY 0.25 (DeepSeek) before each bounded request; a DeepSeek
or OpenAI completion whose body and output limit could cost more reserves that instead,
with a 20% margin (DeepSeek: CNY 2 per million body bytes plus CNY 8 per million output
tokens; OpenAI: its prices, four body bytes a token), so a body of at most 48,000 bytes
with the default limits reserves the flat amount. Successful metered calls settle usage
(an OpenAI call that costs more than its reservation is recorded at its metered price).
A call that never connected, or that the endpoint refused with a non-2xx status, cost
nothing: its reservation is released and recorded at 0. A call that failed after sending,
or whose reply is unreadable, may have been billed and retains its reservation. Missing
probabilities and missing paid metering fail explicitly. DeepSeek usage is conservatively accounted at peak non-cache rates,
rather than claiming an exact invoice amount.

Interfaces and prices were checked against the [JEV API](https://docs.typesafe.ai/api),
[JEV models](https://docs.typesafe.ai/models) and
[DeepSeek pricing](https://api-docs.deepseek.com/quick_start/pricing/).
Dataset results and compatibility/performance evidence belong in the workspace
report; this implementation does not claim reproduction of the paper's headline
accuracy from a small local pilot.
