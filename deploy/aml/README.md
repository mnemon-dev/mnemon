# AML submission materials (draft)

Draft text for the Agent Memory Challenge 2026 submission, **industrial track (commercial
products), textual**. Everything in `<angle brackets>` is a placeholder to fill in or confirm
before submitting; nothing here reports a measured result yet. Install and operate the system
with [`docs/aml-adapter.md`](../../docs/aml-adapter.md).

## Kit contents

The kit runs each account as one engine instance of the memory cloud; the evaluation is the
dedicated instance `eval`.

| File | Purpose |
|---|---|
| `memory-config.industrial.json` | The memory strategy configuration, installed once per host as `/usr/local/share/mnemon-aml/memory-config.industrial.json`; `mnemon-instance new` writes each instance's copy with its caps. |
| `aml-serve@.service` | systemd template of an instance's `mnemon memory-agent aml-serve`, per instance through `%i`. |
| `aml-watch@.service`, `aml-watch@.timer` | The instance's alert watcher, run every minute. |
| `aml-inspect@.service` | The instance's inspect service, `mnemon memory-agent aml-inspect`: the console's read-only retrieval backend, off until `mnemon-instance inspect enable`. |
| `Caddyfile.dedicated` | TLS reverse proxy of a dedicated host: one instance at its domain's root, 31-minute timeouts, a 30 MiB body limit, requests held through a restart. |
| `Caddyfile.shared` | The same for a shared host: instance `<n>` at `/i/<n>`, one snippet per instance. |
| `instance.sh` | Installed as `/usr/local/sbin/mnemon-instance`: `new`, `suspend`, `resume`, `rotate-key`, `delete`, `delete-user`, `list`, `inspect enable`, `inspect disable`. |
| `preflight.sh` | Installed as `/usr/local/sbin/mnemon-preflight`: the checks before a job, for one instance. |
| `wg0.conf.example` | The WireGuard spoke of an instance host: the admin listeners' private network. |
| `../../docs/aml-adapter.md` | Runbook: install, instances, operate, alerts, capacity, troubleshooting, data deletion. |
| `../../docs/admin-api.md` | The admin listener: token, private bind, routes. |
| `../../docs/inspect-api.md` | The inspect service: its routes, their JSON, the retrieval trace. |
| `make build-linux-amd64` | Builds the static `bin/mnemon-linux-amd64`. |

## System

| Field | Value |
|---|---|
| System name | Mnemon |
| Version | v0.2.x: a v0.2.x release follows this work (the `VERSION` given to `make build-linux-amd64`), commit `<release commit>` |
| Track | Industrial (commercial products), textual |
| Team / organization | Mnemon Team / Mnemon |
| Contact | `grivn.wang@gmail.com` |
| Endpoint | `https://aml.mnemon.dev`, the instance `eval` on its own host, the dedicated 12 vCPU / 48 GB / 387 GB evaluation host (`POST /add`, `POST /search`, `GET /health`) |
| Implementation | Go, one static binary: `mnemon memory-agent aml-serve` |
| Paper | *Raw Records, Fast Judgments, Slow Thoughts*: Guangren Wang, <https://arxiv.org/abs/2609.36059> (arXiv, 2026) |
| Reference implementation | <https://github.com/Grivn/mnemon-memory-agent> (MIT; the paper author's own repository) |
| Project repository | <https://github.com/mnemon-dev/mnemon> (Apache-2.0) |

## Summary

The system stores every conversation as raw, verbatim records and keeps the model work for
reading. Storing a conversation needs no language model; only the optional consolidation layer
uses one. Reading plans a few searches with a small model, judges the hits with a fast decision
model (JEV), and returns the stored records themselves: the records JEV judged, in time order.
A consolidation job (run while conversations are added and before a user's next Search) adds a
compact layer of topics, timelines and value histories next to the evidence. Search returns
evidence only and never an answer.

## Method

The method is the one of the paper *Raw Records, Fast Judgments, Slow Thoughts* (Guangren
Wang, <https://arxiv.org/abs/2609.36059>, arXiv, 2026), whose reference implementation is
<https://github.com/Grivn/mnemon-memory-agent> (MIT; the paper author's own repository). This
system is a Go port of that implementation's final read path and consolidation, wrapped in the
AML HTTP contract, with the changes listed below.

### Write path: raw packed records, no model

Storing the records uses no model. Consolidation, when enabled, is the only model work an Add
may do (see step 6 below).

- Each Add is packed into a conversation document and chunk records. Lines are
  `<speaker>: <text>` (the message `role` is the speaker), wrapped at 540 characters at a
  space after the half, and cut into chunks of at most 600 characters. A chunk begins with
  `Conversation <session_id> on <YYYY/MM/DD (Mon) HH:MM>:` built from the first message
  timestamp of the request (UTC), or `Conversation <session_id>:` when there is none. The
  original date therefore travels inside `content`, and no date is inferred.
- Records are content-addressed and immutable. Add returns only when the records are
  persisted and searchable by words.
- An Add is idempotent by `request_id`; a different payload under the same `request_id` is
  refused. Long Adds are split into parts of at most 90,000 bytes of message text.
- Every `user_id` has its own store (`users/<hash of user_id>`), so users are isolated by
  construction.

### Read path: planned cue searches, JEV judging, need loop

1. **Plan.** A planner (DeepSeek Flash, no reasoning mode) writes up to three short searches
   and one invented note that would answer the question, and lists up to three needs
   ("NEED" a fact, "ALL" every instance). The question itself is searched too. The prompts are
   the reference implementation's, verbatim.
2. **Search.** Each cue runs a BM25 search (words only) over the user's records and is
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
   histories, directives) with the linked records before the evidence. The deployment
   consolidates in the background (`--consolidate background`): an Add returns once its
   records are persisted and searchable, and supervised workers fold every full batch (10,000
   characters) of the user's new records; an Add waits only while its user's backlog is above
   one job's window (160,000 characters). The rest of the conversation is folded before that
   user's next Search, as the reference implementation folds a conversation once it pauses.
7. **Search returns evidence only.** The response lists the consolidation blocks first, then
   the stored record texts of the View (verbatim, with the conversation heading and date) in
   time order, earlier first (`view.order: "time"`); records of one time keep the order they
   were written in. The records are selected, and cut at
   `top_k`, by rank. Each item's `score` is `1/(position+1)`. No answer is generated. If a
   model or a budget fails, or the read runs past its timeout, Search still
   answers with the plain BM25 ranking, cut at `top_k` by rank and delivered in time order,
   and records the fact in its log.

### Models and services

| Role | Model | Where it runs |
|---|---|---|
| Planner | DeepSeek Flash (`deepseek-flash`), no reasoning mode | DeepSeek API |
| Judge; also links the consolidation items | JEV `jev-1.13.0` | JEV API (api.typesafe.ai) |
| Consolidator (extracts each batch) | DeepSeek Flash, no reasoning mode | DeepSeek API |

Retrieval is BM25 only. The cue searches over the records, the consolidation layer's choice
of the topics and values nearest to a question, and the linking of new topics and facts to
stored ones all rank by shared words (BM25). Nothing is embedded and no embedding service
runs.

During a Search, the question, the planner's searches and candidate record texts go to the
DeepSeek and JEV APIs; during consolidation (in the background workers, or before a user's
next Search), the conversation text goes to the DeepSeek API, and the events and facts it
extracts from the conversation (topic titles, dated event texts, fact keys and values) go to
the JEV API. Raw records stay on the submission host.

Confirmed with the organizers: the consolidation blocks are allowed, the industrial track lets
participants choose their models, and DeepSeek and JEV retain no data.

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
   so they fold in parallel. The stored index and the way reads use it are the reference's,
   except that BM25 ranks the nearest topics and values (see the retrieval deviation below).
   The change itself is how a batch is joined to the memory, and it costs JEV calls during
   consolidation.
3. **AML adapter.** An HTTP service for the Add/Search contract: per-user stores, idempotent
   Add by `request_id`, conversation packing at write time, consolidation in supervised
   background workers (full batches after the Add returns, the rest before the user's next
   Search), authentication (`Bearer`, `Token` or `X-Api-Key`), unauthenticated health check,
   graceful degradation of Search, load shedding, bounded stores kept open, and an admin
   listener for metrics, readiness and status, on a private address behind a token.
4. **Large evidence lists.** Up to 100 records and 240,000 characters per Search (the reference
   View holds 16 records and 12,000 characters). The judged list is not capped at 16 records:
   it is bounded by the shortlist JEV judges (at most 64 records, and at most 8 more linked
   from the consolidation layer), 100 items and 240,000 characters. Every record comes from
   the judged list; none is added from the plain search ranking.
5. **Smaller deliberate deviations.**
   - Retrieval is BM25 only. The reference implementation ranks each search by BM25 and by
     embedding similarity and fuses the two rankings by reciprocal rank fusion, and it ranks
     the layer's nearest topics and values by embedding similarity when an embedder is
     configured. Here the cue searches, the layer's nearest topics and values, and the linking
     candidates (the stored topics and facts nearest to each new one) all rank by BM25, and
     nothing is embedded: no embedding model runs on the host.
   - Search pages are 20 whole records; the reference implementation's 12,000-character page cut
     is not used.
   - The records are delivered in time order, earlier first, records of one time in the order
     they were written (a document's chunks in their order), so a run repeats its order. The
     reference implementation delivers the items it selected in composition order or, in its
     stable delivery, sorted by source and resource identifier; neither follows the dates.
   - The consolidation layer asks JEV only the "needed" question per candidate, since the
     "superseded" answer is never used.
   - JEV request size is limited in UTF-16 units of the JSON body, as the reference client does.
   - Consolidation input waiting to be folded is bounded (16 batches per user); later records
     wait unread. A batch the model provider keeps failing on (three failed attempts over at
     least ten minutes) is split once and a half that fails again is left out of the layer,
     where the reference implementation keeps such a batch pending; a fold request too large to
     send is split down to single records. The records left out stay searchable.

## Capacity, timeouts and limits

Fill these from the measurements described in the runbook ("Capacity declaration"). The
values marked "local, fake providers" are not the host's: they come from
`scripts/aml/load.sh --local` on a development machine (18 cores), with the fake provider
answering every DeepSeek call in 2 s and every JEV call in 1.5 s, the dedicated limits
(16 Adds, 16 Searches, 16 open stores, 4 workers) and 16 users, at rising rates of 60 s
(Searches) or 30 s (Adds) each. The knee is the highest rate whose p95 stays within twice the
lowest rate's p95 with no non-2xx answer. The host's values come from the same script on a
scratch instance there (Task 12c).

| Field | Value |
|---|---|
| Host | 12 vCPU, 48 GB, 387 GB, Ubuntu 24.04 amd64, dedicated to the evaluation instance |
| Concurrent Add requests | `<TBD: measured>`, at most the instance's `--max-concurrent-adds` (16, provisional). Local, fake providers: 8 Adds a second of 20 messages is the knee, under 1 in flight; at 16 a second, after the lower steps, the users' backlogs reach the lag bound, Adds wait for consolidation (p95 21 s) and 8 of 480 are shed |
| Concurrent Search requests | `<TBD: measured>`, at most the instance's `--max-concurrent-searches` (16, provisional). Local, fake providers: 1.5 Searches a second fill the 16 slots with no wait; 2 a second is the knee, queueing for slots (39 in flight or waiting at the peak); 3 a second shed 30 of 180 |
| Add latency (20 messages), p50 / p95 | `<TBD>` / `<TBD>`. Local, fake providers, at 8 a second: 3 ms / 5 ms |
| Search latency (`top_k` 100), p50 / p95 | `<TBD>` / `<TBD>`. Local, fake providers: 9.6 s / 11.1 s up to 1.5 a second; 15.4 s / 21.9 s at the knee, 2 a second |
| Rate limit | `<TBD>` requests per minute. Local, fake providers: 120 Searches and 480 Adds a minute (the knees) |
| Request timeout accepted | 31 minutes (the platform's I/O timeout is 30 minutes); a Search's memory read stops after 10 minutes (`--search-timeout`) and answers the raw ranking |
| Stores kept open | 16 (`--max-open-stores`) |
| Request body limit | 30 MiB |
| Retries | Add retries with the same `request_id` are idempotent; 503 marks a retryable failure, with `Retry-After: 5` when no slot frees within 30 s. A restart gives the requests in flight 30 s to finish; those still running then are cancelled with 503, and the platform retries them. The proxy holds new requests for up to 30 s while the instance restarts |
| Model spending caps | JEV USD 60 (reads and consolidation links), DeepSeek CNY 600 (planning and extraction); `budgets` in the configuration |

## Deployment

- The evaluation runs as the dedicated instance `eval` of the memory cloud: its own host,
  process (`aml-serve@eval`), API key, data root, model budget and limits. Beta accounts are
  other instances on another host and share nothing with it.
- The data plane listens on loopback behind Caddy (TLS). The admin listener (metrics,
  readiness, status, store overview, profiler) binds the host's private WireGuard address,
  requires a bearer token on every request, and is reachable only from the ops host. The
  firewall denies everything else but ssh, HTTP and HTTPS, WireGuard's 51820/udp from the ops
  host, and the admin ports from the hub on `wg0`.
- systemd supervises the process (readiness, watchdog, restart), and a watcher posts alerts to
  the operators' Telegram chat (or a Feishu or WeCom group) every minute, independently of the
  console.
- Retrieval is BM25 only; no embedding model runs on the host.

## Data handling

- All user data (records, consolidation index) lives under the instance's data root on the
  submission host (`/var/lib/mnemon-aml/eval/data`), one directory per `user_id`; users
  cannot reach each other's records.
- Request bodies and user identifiers are not logged.
- The model budget file keeps metering and the SHA-256 hashes of model requests, never their
  text; it is deleted together with the data root.
- After the job the data root and the budget file are deleted within 30 days, with the
  procedure in the runbook ("Data deletion": `mnemon-instance delete eval`; one user with
  `mnemon-instance delete-user eval`, the user id typed at its prompt). Planned deletion date:
  2026-12-04.

## Evaluation evidence

`<Fill in only measured results: dataset, sample, model settings, and the run that produced
each number. None is reported in this draft.>`

## Open points to confirm before submitting

- The capacity numbers and the rate limit above, from the load test on the evaluation host
  (Task 12).
- The Windows CI job (`windows-memory`) has not yet run the new `cmd/memory` tests: they are
  the first Windows tests to open AML stores and the budget file, and must pass before the
  branch merges.
