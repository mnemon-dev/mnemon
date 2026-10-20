# AML submission materials (draft)

Draft text for the Agent Memory Challenge 2026 submission, **industrial track (commercial
products), textual**. Everything in `<angle brackets>` is a placeholder to fill in or confirm
before submitting; nothing here reports a measured result yet. Install and operate the system
with [`docs/aml-adapter.md`](../../docs/aml-adapter.md).

## Kit contents

| File | Purpose |
|---|---|
| `memory-config.industrial.json` | The memory strategy configuration (install as `/etc/mnemon-aml/memory-config.json`). |
| `aml-serve.service` | systemd unit for `mnemon memory-agent aml-serve`. |
| `Caddyfile` | TLS reverse proxy with 31-minute timeouts and a 30 MiB body limit. |
| `../../docs/aml-adapter.md` | Runbook: install, operate, capacity, troubleshooting, data deletion. |
| `make build-linux-amd64` | Builds the static `bin/mnemon-linux-amd64`. |

## System

| Field | Value |
|---|---|
| System name | `<system name>` (Mnemon AML memory agent) |
| Version | `<version>` (the `VERSION` given to `make build-linux-amd64`), commit `<git commit>` |
| Track | Industrial (commercial products), textual |
| Team / organization | `<team>` / `<organization>` |
| Contact | `<name, email>` |
| Endpoint | `https://<domain>` (`POST /add`, `POST /search`, `GET /health`) |
| Implementation | Go, one static binary: `mnemon memory-agent aml-serve` |

## Summary

The system stores every conversation as raw, verbatim records and keeps the model work for
reading. Storing a conversation needs no language model; only the optional consolidation layer
uses one. Reading plans a few searches with a small model, judges the hits with a fast decision
model (JEV), and returns the stored records themselves: the records JEV judged, in time order.
A consolidation job (run while conversations are added and before a user's next Search) adds a
compact layer of topics, timelines and value histories next to the evidence. Search returns
evidence only and never an answer.

## Method

The method is the one of the paper *Raw Records, Fast Judgments, Slow Thoughts*
(`<authors, venue, year>`), whose reference implementation is
<https://github.com/Grivn/mnemon-memory-agent>. This system is a Go port of that
implementation's final read path and consolidation, wrapped in the AML HTTP contract, with the
changes listed below.

### Write path: raw packed records, no model

Storing the records uses no model. Consolidation, when enabled, is the only model work an Add
may do (see step 6 below).

- Each Add is packed into a conversation document and chunk records. Lines are
  `<speaker>: <text>` (the message `role` is the speaker), wrapped at 540 characters at a
  space after the half, and cut into chunks of at most 600 characters. A chunk begins with
  `Conversation <session_id> on <YYYY/MM/DD (Mon) HH:MM>:` built from the first message
  timestamp of the request (UTC), or `Conversation <session_id>:` when there is none. The
  original date therefore travels inside `content`, and no date is inferred.
- Records are content-addressed and immutable. The embedding of each new chunk is computed at
  write time with `nomic-embed-text` (Ollama, on the same host), so Add returns only when the
  records are persisted, searchable by words and by vector.
- An Add is idempotent by `request_id`; a different payload under the same `request_id` is
  refused. Long Adds are split into parts of at most 90,000 bytes of message text.
- Every `user_id` has its own store (`users/<hash of user_id>`), so users are isolated by
  construction.

### Read path: planned cue searches, JEV judging, need loop

1. **Plan.** A planner (DeepSeek Flash, no reasoning mode) writes up to three short searches
   and one invented note that would answer the question, and lists up to three needs
   ("NEED" a fact, "ALL" every instance). The question itself is searched too. The prompts are
   the reference implementation's, verbatim.
2. **Search.** Each cue runs a hybrid search (vector plus words) over the user's records and is
   read in pages of 20 records; the first pages are mixed into a pool of 48 candidates.
3. **Judge.** JEV 1.13.0 screens each candidate ("is it needed?"), then tables the best against
   each need and judges whether a record is superseded by a later one.
4. **Need loop.** For at most three rounds, an open need takes one step: it is met by a record
   JEV answers yes for, reads the next page of the search that led to it, or gets a new
   search worded by the planner. Page reads and JEV calls are budgeted (15 pages, 128 JEV
   calls per read).
5. **Compose.** JEV judges a shortlist of at most 64 records (those the need table gives to a
   need, then the best screened ones). The View is composed from it as in the reference
   implementation: the records JEV calls needed and not superseded come first and, when the
   plan named a need, the other current ones follow by their needed answer, up to `top_k`
   records (at most 100) and 240,000 characters. The plain message-search ranking is not fused
   in: a record JEV did not judge is not delivered.
6. **Consolidation layer.** Records are folded in batches into threads of dated events, facts
   with value histories and standing directives, in two steps (layered consolidation). A model
   reads each batch of records on its own and writes its events, facts and directives; then
   JEV links each batch to the memory so far, in batch order, by asking whether a new thread
   continues a stored one, whether a new fact is the same attribute of the same thing as a
   stored fact, and whether its value contradicts the stored one. At read time JEV selects the
   topics that matter for the question, and the layer adds blocks (topics, timelines, value
   histories, directives) with the linked records before the evidence. With
   `--consolidate-on-add`, an Add folds every full batch (10,000 characters) of the user's new
   records before it returns, and the rest of the conversation is folded before that user's
   next Search, as the reference implementation folds a conversation once it pauses.
7. **Search returns evidence only.** The response lists the consolidation blocks first, then
   the stored record texts of the View (verbatim, with the conversation heading and date) in
   time order, as the reference system delivers its View (`view.order: "time"`); records of
   one time keep the order they were written in. The records are selected, and cut at
   `top_k`, by rank. Each item's `score` is `1/(position+1)`. No answer is generated. If a
   model, a budget or the embedder fails, or the read runs past its timeout, Search still
   answers with the plain hybrid ranking, cut at `top_k` by rank and delivered in time order,
   and records the fact in its log.

### Models and services

| Role | Model | Where it runs |
|---|---|---|
| Planner | DeepSeek Flash (`deepseek-flash`), no reasoning mode | DeepSeek API |
| Judge; also links the consolidation items | JEV `jev-1.13.0` | JEV API (api.typesafe.ai) |
| Consolidator (extracts each batch) | DeepSeek Flash, no reasoning mode | DeepSeek API |
| Embeddings | `nomic-embed-text` (`search_query: ` / `search_document: ` prefixes) | Ollama on the submission host |

During a Search, the question, the planner's searches and candidate record texts go to the
DeepSeek and JEV APIs; during consolidation (in an Add, or before a user's next Search), the
conversation text goes to the DeepSeek API, and the events and facts it extracts from the
conversation (topic titles, dated event texts, fact keys and values) go to the JEV API. Raw
records and vectors stay on the submission host. `<confirm that the track rules allow
external model APIs, and add their data-retention terms if the rules require them>`.

## Changes from the reference implementation

1. **Go port.** The reference system is a TypeScript plugin; this is a Go implementation of
   its final read path and consolidation (`internal/memoryagent`), with prompts copied
   verbatim and the reference code's numbers, thresholds and budgets (the code wins where the
   paper and the code differ). It needs no Node runtime.
2. **Layered consolidation.** The reference folds each batch of records with one model call that
   also receives a summary of the memory so far, so batches fold one after another. Here a
   model reads each batch on its own, with the reference's prompt less its references to that
   summary and plus a request to name the entities each event and fact is about; then JEV
   links the batch to the stored memory, in batch order, by yes/no questions about the stored
   topics and facts nearest to each new one. The batches are extracted up to four at a time,
   so they fold in parallel. The stored index and the way reads use it are the reference's;
   the change is how a batch is joined to the memory, and it costs JEV calls during
   consolidation.
3. **AML adapter.** An HTTP service for the Add/Search contract: per-user stores, idempotent
   Add by `request_id`, conversation packing at write time, optional consolidation (full
   batches during Add, the rest before the user's next Search), authentication (`Bearer`,
   `Token` or `X-Api-Key`), unauthenticated health check, graceful degradation of Search, and
   bounded stores kept open.
4. **Large evidence lists.** Up to 100 records and 240,000 characters per Search (the reference
   View holds 16 records and 12,000 characters). The judged list is not capped at 16 records:
   it is bounded by the shortlist JEV judges (at most 64 records, and at most 8 more linked
   from the consolidation layer), 100 items and 240,000 characters. Every record comes from
   the judged list; none is added from the plain search ranking.
5. **Smaller deliberate deviations.**
   - Search pages are 20 whole records; the reference implementation's 12,000-character page cut
     is not used.
   - The records are delivered in the reference's time order, earlier first. Where the
     reference breaks ties between records of one time by random identifiers, this system
     uses the order they were written in (a document's chunks in their order), so a run
     repeats its order.
   - The consolidation layer asks JEV only the "needed" question per candidate, since the
     "superseded" answer is never used.
   - JEV request size is limited in UTF-16 units of the JSON body, as the reference client does.
   - Consolidation input waiting to be folded is bounded (16 batches per user); later records
     wait unread. A batch the model provider keeps failing on (three failed attempts over at
     least ten minutes) is split once and a half that fails again is left out of the layer,
     where the reference implementation keeps such a batch pending; a fold request too large to
     send is split down to single records. The records left out stay searchable.

## Capacity, timeouts and limits

Fill these from the measurements described in the runbook ("Capacity declaration").

| Field | Value |
|---|---|
| Host | `<vCPU>` vCPU, `<RAM>` GB, `<disk>` GB, Ubuntu `<version>` amd64 |
| Concurrent Add requests | `<TBD: measured>` |
| Concurrent Search requests | `<TBD: measured>` |
| Add latency (20 messages), p50 / p95 | `<TBD>` / `<TBD>` |
| Search latency (`top_k` 100), p50 / p95 | `<TBD>` / `<TBD>` |
| Rate limit | `<TBD>` requests per minute |
| Request timeout accepted | 31 minutes (the platform's I/O timeout is 30 minutes); a Search's memory read stops after 10 minutes (`--search-timeout`) and answers the raw ranking |
| Stores kept open | 16 (`--max-open-stores`) |
| Request body limit | 30 MiB |
| Retries | Add retries with the same `request_id` are idempotent; 503 marks a retryable failure |
| Model spending caps | JEV USD `<cap>` (reads and consolidation links), DeepSeek CNY `<cap>` (planning and extraction); `budgets` in the configuration |

## Data handling

- All user data (records, vectors, consolidation index) lives under one data root on the
  submission host, one directory per `user_id`; users cannot reach each other's records.
- Request bodies and user identifiers are not logged.
- The model budget file keeps metering and the SHA-256 hashes of model requests, never their
  text; it is deleted together with the data root.
- After the job the data root and the budget file are deleted within 30 days, with the
  procedure in the runbook ("Data deletion"). Planned deletion date: `<date>`.

## Evaluation evidence

`<Fill in only measured results: dataset, sample, model settings, and the run that produced
each number. None is reported in this draft.>`

## Open points to confirm before submitting

- The paper's authors, venue and year, and the licence terms of the reference repository for a
  derived port.
- Whether the track rules treat the consolidation blocks (model-written topic, timeline and value
  summaries built from the stored records as conversations are added and before the next Search,
  never from the question) as
  retrieved evidence. Search can be run without them by starting `aml-serve` without
  `--consolidate-on-add`, at the cost of the layer.
- That external model APIs are allowed in the industrial track, and the retention terms of
  DeepSeek and JEV for the data sent to them.
- The capacity numbers and the rate limit above.
