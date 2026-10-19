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
uses one. Reading plans a few searches with a small model,
judges the hits with a fast decision model (JEV), and returns the stored records themselves, in
a ranked list that fuses the plain search order with the judged order. A consolidation job
(run while conversations are added and before a user's next Search) adds a compact layer of
topics, timelines and value histories next to the evidence. Search returns evidence only and never an answer.

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
5. **Compose and fuse.** The judged View is composed as in the reference implementation. For the
   long evidence list AML allows, the final order is the reciprocal-rank fusion (k = 60) of the
   composed rank and the rank of the plain message search, filled up to `top_k` records (at most
   100) and 240,000 characters.
6. **Consolidation layer.** Records are folded in batches by a model into threads of dated
   events, facts with value histories and standing directives (timelines consolidation). At
   read time JEV selects the topics that matter for the question, and the layer adds blocks
   (topics, timelines, value histories, directives) with the linked records before the
   evidence. With `--consolidate-on-add`, an Add folds every full batch (10,000 characters) of
   the user's new records before it returns, and the rest of the conversation is folded before
   that user's next Search, as the reference implementation folds a conversation once it
   pauses.
7. **Search returns evidence only.** The response is a list of stored record texts (verbatim,
   with the conversation heading and date) and the consolidation blocks, in rank order; each
   item's `score` is `1/(rank+1)`. No answer is generated. If a model, a budget or the embedder
   fails, or the read runs past its timeout, Search still answers with the plain hybrid ranking
   and records the fact in its log.

### Models and services

| Role | Model | Where it runs |
|---|---|---|
| Planner | DeepSeek Flash (`deepseek-flash`), no reasoning mode | DeepSeek API |
| Judge | JEV `jev-1.13.0` | JEV API (api.typesafe.ai) |
| Consolidator | DeepSeek Flash, no reasoning mode | DeepSeek API |
| Embeddings | `nomic-embed-text` (`search_query: ` / `search_document: ` prefixes) | Ollama on the submission host |

During a Search, the question, the planner's searches and candidate record texts go to the
DeepSeek and JEV APIs; during consolidation (in an Add, or before a user's next Search), the
conversation text goes to the DeepSeek API. Raw records and vectors stay on the submission host. `<confirm that the track
rules allow external model APIs, and add their data-retention terms if the rules require
them>`.

## Changes from the reference implementation

1. **Go port.** The reference system is a TypeScript plugin; this is a Go implementation of
   its final read path and consolidation (`internal/memoryagent`), with prompts copied
   verbatim and the reference code's numbers, thresholds and budgets (the code wins where the
   paper and the code differ). It needs no Node runtime.
2. **Fused ranking.** The final list is the reciprocal-rank fusion of the judged order and the
   plain message-search order. This is not part of the reference method; it is a deliberate
   change for long evidence lists.
3. **AML adapter.** An HTTP service for the Add/Search contract: per-user stores, idempotent
   Add by `request_id`, conversation packing at write time, optional consolidation (full
   batches during Add, the rest before the user's next Search), authentication (`Bearer`,
   `Token` or `X-Api-Key`), unauthenticated health check, graceful degradation of Search, and
   bounded stores kept open.
4. **Large evidence lists.** Up to 100 records and 240,000 characters per Search (the reference
   View holds 16 records and 12,000 characters); the records beyond the judged View come from
   the fused ranking.
5. **Smaller deliberate deviations.**
   - Search pages are 20 whole records; the reference implementation's 12,000-character page cut
     is not used.
   - The rendered order of the judged records is the composition order (the reference's stable
     delivery order depends on random identifiers).
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
| Model spending caps | JEV USD `<cap>`, DeepSeek CNY `<cap>` (`budgets` in the configuration) |

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
