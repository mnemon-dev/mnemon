# Inspect API

`mnemon memory-agent aml-inspect` is the inspect service of one engine instance: the read-only
backend of the console's 检索 (retrieval) tab. It is a process of its own, beside
`aml-serve@<n>`, and never a route of aml-serve. It reads the instance's data root and runs
retrieval on it **without side effects**: it writes no store, folds no pending record, writes no
model cache, spends nothing from the instance's budget and moves no metric of the instance. The
deployment is in the [AML runbook](aml-adapter.md) ("Inspect (检索)").

This document is the contract between the engine and the console (mnemon-fe). Its JSON may gain
fields; a field is never renamed or repurposed.

## Conventions

- Every answer is JSON (`Content-Type: application/json`) with `Cache-Control: no-store`.
  Request bodies are JSON objects of at most 65,536 bytes, which only white space may follow
  (else `400 invalid`, `field` `body`). The service reads every body to its end, and from then
  on watches the connection: a client that closes it cancels its request (a full search stops
  its model calls, and is not answered).
- **Numbers.** Counts, positions and sequence numbers are integers. Probabilities are numbers
  from 0 to 1. `ms` is milliseconds of wall time, rounded to 0.1. Costs are DeepSeek's CNY and
  JEV's USD, unrounded. Any number may be written in exponent form (`4.2e-06`).
- **Times.** `modified` is RFC 3339 UTC to the second (`2026-10-25T07:59:12Z`). `created_at` and
  memory dates are what the engine stored, unparsed: a record's `created_at` is the time its
  conversation was written for (RFC 3339, as `/search` gives it); an event's, a value's or a
  directive's `date` is the session date the consolidation wrote (`2023/05/08`).
- **Stores.** A user's store is named by its **key**, the first 32 hex digits of
  `sha256(user_id)` (lowercase), and shown by its **prefix**, the key's first 8 digits. Within
  this authenticated service the full key appears in answers and paths, so that the console can
  page and address stores by it; the service's own logs carry the prefix only. A raw user id is
  never stored by the engine and is never logged or answered by this service.
- **Excerpts.** Every text in a trace is an excerpt of at most 200 characters (Unicode code
  points): the text itself when it is that short, else its first 199 characters and `…`. The
  full texts are in `result`, `/records` and `/memory`.
- **Errors** are `{"error":"<code>"}`; a 400 adds `"field":"<name>"`. Every code is listed in
  [Status codes](#status-codes).

## Where it listens, and who may read it

The rules are aml-serve's admin listener's ([Admin API](admin-api.md)):

- `--listen` takes an IP address literal and a port. A loopback address needs no token; a private
  one (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, `fc00::/7`) needs `--token-env`; every other
  address is refused at startup, token or not.
- `--token-env NAME` names the variable holding the bearer token, at least 32 characters.
- In the memory cloud the service listens on the instance's overlay address at the slot's admin
  port + 1: 8092 on a dedicated host, 8102 + 10 × the slot on a shared one.

With a token set, **every** request, `/healthz` and unknown paths included, must carry
`Authorization: Bearer <token>` (the scheme in any case). Without it, or with another token, the
answer is `401 {"error":"unauthorized"}` with `WWW-Authenticate: Bearer realm="mnemon-inspect"`,
before anything else is done. Tokens are compared as SHA-256 digests in constant time, and are
never logged or answered.

## Limits

| Limit | Value |
| --- | --- |
| requests reading stores at once (`--max-concurrent`) | 2 by default; past it `503 busy` |
| request body | 65,536 bytes |
| `GET /v1/stores` page | `limit` 1–500, default 100 |
| `GET /v1/stores/{key}/records` page | `limit` 1–200, default 50 |
| `GET /v1/stores/{key}/memory` page | `limit` 1–200, default 50 |
| pending record ids | the first 100 |
| a lookup's `{id}` (`/records/{id}`, `/memory/{section}/{id}`) | 1–256 bytes, decoded, no control character |
| `POST /v1/search` `top_k` | 1–100; default the View's `maxItems` (100 in the shipped configuration) |
| `POST /v1/search` `query` | 1–12,000 bytes, not blank |
| trace excerpts | 200 characters |
| a full search | 10 minutes |

`/healthz` and `/v1/info` read no store and do not count against `--max-concurrent`.

## Routes

| Route | Returns |
| --- | --- |
| `GET /healthz` | `{"ok":true}` |
| `GET /v1/info` | the service: version, modes, debug budget, recent cost, limits |
| `GET /v1/stores?limit&after` | a page of the data root's stores |
| `POST /v1/resolve` | the store of a user id |
| `GET /v1/stores/{key}/records?after&limit` | a page of a store's records |
| `GET /v1/stores/{key}/records/{id}` | one record, by its id |
| `GET /v1/stores/{key}/memory?section&after&limit` | a page of a store's consolidated memory |
| `GET /v1/stores/{key}/memory/{section}/{id}` | one memory item, by its section and id |
| `POST /v1/search` | a retrieval: its result, trace and usage |

Any other path answers `404 {"error":"not_found"}`; another method on a route answers
`405 {"error":"method_not_allowed"}` with an `Allow` header.

### `GET /healthz`

`200 {"ok":true}`. It reads nothing.

### `GET /v1/info`

```json
{
  "version": "0.2.3",
  "modes": {"lexical": true, "full": true},
  "debugBudget": {
    "currency": {"deepseek": "CNY", "jev": "USD"},
    "caps": {"deepseek": 5, "jev": 1},
    "spent": {"deepseek": 0.0123, "jev": 0.0004},
    "held": {"deepseek": 0, "jev": 0},
    "remaining": {"deepseek": 4.9877, "jev": 0.9996}
  },
  "recent": {"fullSearches": 3, "avgCost": {"deepseek": 0.0041, "jev": 0.00013}},
  "limits": {
    "maxConcurrent": 2, "topKMax": 100, "topKDefault": 100, "queryMaxBytes": 12000,
    "bodyMaxBytes": 65536, "storesPageMax": 500, "storesPageDefault": 100,
    "recordsPageMax": 200, "recordsPageDefault": 50, "memoryPageMax": 200,
    "memoryPageDefault": 50, "pendingIdsMax": 100, "excerptMax": 200, "fullSearchSeconds": 600
  }
}
```

| Field | Meaning |
| --- | --- |
| `version` | the product version of the build |
| `modes.lexical` | always `true` |
| `modes.full` | `true` when full searches may run: a debug budget file is set, at least one cap is positive, and every paid model the read path calls (the planner's and the judge's kinds) has a positive cap; else `false`, and a full search answers `409 full_disabled` |
| `debugBudget.currency` | the currency of each kind: `deepseek` CNY, `jev` USD |
| `debugBudget.caps` | the cap of each kind, from `--debug-caps`; 0 for a kind with none |
| `debugBudget.spent` | what full searches of any run of this service have spent from the debug budget file, by kind; 0 without the file |
| `debugBudget.held` | what calls in flight hold now, by kind |
| `debugBudget.remaining` | `caps − spent − held`, never below 0, by kind |
| `recent.fullSearches` | full searches this process ran since it started (those refused before reading are not counted) |
| `recent.avgCost` | their mean `usage.cost` by kind, 0 before the first; the console shows it as the estimate of a full search |
| `limits` | the limits above |

The maps of `debugBudget` and `recent.avgCost` always have `deepseek` and `jev`. The instance's
own budget is never read or written.

### `GET /v1/stores?limit=1..500&after=<key>`

The data root's stores, sorted by key.

```json
{
  "total": 12,
  "stores": [
    {"store": "58e645e0", "key": "58e645e06cad8b5e2d3b842ebef7c1c5", "records": 1234,
     "bytes": 61440000, "modified": "2026-10-25T07:59:12Z",
     "consolidation": {"watermark": 5678, "pending": 12}}
  ],
  "next": "58e645e06cad8b5e2d3b842ebef7c1c5"
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `total` | int | the stores in the data root |
| `stores[].store` | string | the key's first 8 hex digits |
| `stores[].key` | string | the store's key, 32 hex digits |
| `stores[].records` | int or null | the store's records, as `/records` lists them |
| `stores[].bytes` | int | the sizes of the store's files, in bytes |
| `stores[].modified` | string | the newest modification time among them |
| `stores[].consolidation` | object or null | null when the instance does not consolidate |
| `…consolidation.watermark` | int | the write position (records version) of the newest record consolidation has read; 0 before the first job |
| `…consolidation.pending` | int | records not folded yet: read and held back, or written after the watermark. A `/search` folds them first |
| `stores[].error` | string | present only when the store could not be read: `store_busy` (retry) or `store_unreadable`; `records` and `consolidation` are then null |
| `next` | string or null | the cursor of the next page, the last store's key; null on the last page |

**Paging.** Pass `next` back as `after` (exclusive). `after` is 32 lowercase hex digits; a
malformed `after` or `limit` is `400 invalid` with `field` `after` or `limit`. Stores created
between two pages appear in a later page if their key sorts after the cursor.

### `POST /v1/resolve`

```json
{"user_id": "alice"}
```

answers

```json
{"key": "2bd806c97f0e00af1a1fc3328fa763a9", "store": "2bd806c9", "exists": true}
```

`user_id` is a JSON string, or a number taken as its decimal text, as `/search` takes it; a
missing or blank one is `400 invalid` (`field` `user_id`). `exists` says whether the data root
holds a store for that user. Neither the id nor the key is logged.

### `GET /v1/stores/{key}/records?after=<seq>&limit=1..200`

The store's records in write order: the searchable records (conversation chunks) of the default
scope, as `/search` returns them, without the archived documents they are cut from.

```json
{
  "records": [
    {"id": "aml-5c1f…-00000", "seq": 12, "created_at": "2023-05-08T13:56:00Z", "session": "s1",
     "content": "Conversation s1 on 2023/05/08 (Mon) 13:56:\nuser: I adopted a dog named Biscuit …"}
  ],
  "next": 12
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `records[].id` | string | the record's id: a `/search` item's `id` |
| `records[].seq` | int | its write position in the store, increasing |
| `records[].created_at` | string | its time, as the `/search` item's `created_at` |
| `records[].session` | string | the session id it was added under; `""` when none |
| `records[].content` | string | its whole text: the `/search` item's `content` |
| `next` | int or null | the last record's `seq` when more follow; null on the last page |

**Paging.** `after` is a `seq` (exclusive), an integer ≥ 0; omit it for the first page. A
malformed `after` or `limit` is `400 invalid`. An unknown key is `404 store_not_found`; a key that
is not 32 lowercase hex digits is `400 invalid` (`field` `key`).

### `GET /v1/stores/{key}/records/{id}`

One record, by the id a `result` item, a trace or a memory item's `records` names.

```json
{"record": {"id": "aml-5c1f…-00000", "seq": 12, "created_at": "2023-05-08T13:56:00Z", "session": "s1",
            "content": "Conversation s1 on 2023/05/08 (Mon) 13:56:\nuser: I adopted a dog named Biscuit …"}}
```

- `record` is the very object `/records` lists for that record: the same fields and bytes.
- `{id}` is the record's id as one path segment, URL-encoded. Decoded, it is 1–256 bytes with
  no control character, else `400 invalid` (`field` `id`).
- A store with no such record among those `/records` lists (an unknown id, or a document that
  records are cut from) is `404 record_not_found`; an unknown store is `404 store_not_found`.
- To show the record in its context, page `/records` with `after` = its `seq` − 1: the page
  starts with it.
- The id is never logged.

### `GET /v1/stores/{key}/memory?section=threads|events|facts|directives&after=<n>&limit=1..200`

The store's consolidated memory, one section a page, in memory order, and its consolidation
progress.

```json
{
  "counts": {"threads": 3, "events": 42, "facts": 7, "directives": 1},
  "watermark": 5678,
  "pending": {"count": 3, "ids": ["aml-…-00004", "aml-…-00005", "aml-…-00006"]},
  "section": "facts",
  "items": [
    {"position": 0, "id": "pet · name", "key": "pet · name", "values": [
      {"value": "Biscuit", "date": "2023/05/08", "records": ["aml-…-00000"], "conflict": false, "supersedes": null},
      {"value": "Max", "date": "2023/06/01", "records": ["aml-…-00031"], "conflict": true, "supersedes": 0}
    ]}
  ],
  "next": null
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `counts` | object | the items of each section |
| `watermark` | int or null | as in `/v1/stores`; null when the instance does not consolidate |
| `pending.count` | int | the records not folded yet, as `/v1/stores`'s `pending` |
| `pending.ids` | [string] | the first 100 of them, in the order consolidation reads them |
| `section` | string | the section of `items`: `threads` (the default), `events`, `facts` or `directives` |
| `items` | array | the page, each item with its `position` (0-based, in its section) and `id` |
| `next` | int or null | the last item's `position` when more follow; null on the last page |

`pending` is null when the instance does not consolidate. Items by section:

| Section | Item |
| --- | --- |
| `threads` | `{"position", "id": "T3", "title", "events": <count>, "first": <date of its first event>, "last": <date of its last>}`; `first` and `last` are `""` for a thread with no event |
| `events` | `{"position", "id": "e<position>", "thread": <thread id>, "date", "text", "records": [<record id>], "entities": [<name>]}`; `entities` only when the event names some |
| `facts` | `{"position", "id": <key>, "key", "values": [{"value", "date", "records", "conflict", "supersedes"}]}`: the key's history, oldest first. `conflict` is the consolidation's mark of a value that contradicts an earlier one without the user saying it changed; `supersedes` is the index (in `values`) of the value this one replaces, the one before it when they differ, else null. The last value is the current one |
| `directives` | `{"position", "id": "d<position>", "date", "text", "records": [<record id>]}`: the standing instructions and preferences the user stated |

Item ids are what a trace's `compose` blocks name (`items`). Record ids link to `/records` and to
`result`. Consolidation only appends items, so positions are stable from page to page.
**Paging** is `after` = a `position` (exclusive), as for records; an unknown `section` is
`400 invalid` (`field` `section`).

### `GET /v1/stores/{key}/memory/{section}/{id}`

One memory item, by its section and the id a trace's `compose` block or a `judge.layer`
candidate names.

```json
{"section": "facts",
 "item": {"position": 0, "id": "pet · name", "key": "pet · name", "values": [
   {"value": "Biscuit", "date": "2023/05/08", "records": ["aml-…-00000"], "conflict": false, "supersedes": null}]}}
```

- `item` is the very object `/memory?section=<section>` lists for that item, `position`
  included: the same fields and bytes.
- `{section}` is `threads`, `events`, `facts` or `directives`, else `400 invalid`
  (`field` `section`).
- `{id}` is the item's `id` as one path segment, URL-encoded: a fact's key holds spaces and
  `·` (`pet%20%C2%B7%20name`). Decoded, it is 1–256 bytes with no control character, else
  `400 invalid` (`field` `id`).
- A section with no item of that id is `404 item_not_found`, an id of another section
  included (`events/T1`); an unknown store is `404 store_not_found`.
- To show the item in its context, page `/memory` with its `section` and `after` = its
  `position` − 1 (no `after` for position 0).
- The id is never logged: a fact's key is content.

### `POST /v1/search`

```json
{"key": "58e645e06cad8b5e2d3b842ebef7c1c5", "query": "When did I adopt Biscuit?",
 "options": ["May", "June"], "top_k": 10, "mode": "full", "trace": true}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `key` | string | the store, 32 lowercase hex digits; or give `user_id` instead |
| `user_id` | string or number | the user, as `/search` takes it; exactly one of `key` and `user_id` |
| `query` | string | the question, as `/search`'s `query`: 1–12,000 bytes, not blank |
| `options` | [string] | optional: a multiple-choice question's choices, as `/search`'s `options` |
| `top_k` | int | 1–100; default the View's `maxItems` (`limits.topKDefault`; 100 in the shipped configuration), as `/search` without one |
| `mode` | string | `lexical` (the default) or `full` |
| `trace` | bool | default `true`; with `false`, `trace` is null |

A malformed field is `400 invalid` with its name in `field` (both `key` and `user_id`, or
neither, is `field` `key`); an unknown store is `404 store_not_found`.

```json
{
  "store": "58e645e0",
  "mode": "full",
  "result": [{"id": "index:3-timelines:9f2c…", "content": "Timelines of …", "score": 1},
             {"id": "aml-…-00000", "content": "Conversation s1 on …", "score": 0.5, "created_at": "2023-05-08T13:56:00Z"}],
  "degraded": [],
  "trace": [ … stages … ],
  "usage": {"calls": 9, "tokens": {"input": 41200, "output": 380}, "cost": {"deepseek": 0.0011, "jev": 0.0017}}
}
```

- `result` is exactly what `/search` puts in `data`: the same objects, fields and bytes, `score`
  being 1/(position + 1).
  - **lexical** calls no model and costs nothing. It is the raw ranking aml-serve answers a
    degraded `/search` with (the memory read failed: budget, breaker, provider): `result` is
    byte-identical to that `data` for the same store, `query` and `top_k` (`options` are not
    searched, as there).
  - **full** runs the read path `/search` runs, with the instance's configuration and models,
    billed to the **debug budget**. It does not first fold the pending records as `/search`
    does (stage `pending`): its `result` is what `/search` answers once nothing is pending.
- `usage` is the models' use: `calls` (int), `tokens.input` and `tokens.output` (ints, as the
  providers metered them) and `cost` by kind. It counts what the calls' replies metered; the debug
  budget also settles attempts that were retried, at their input estimate. Lexical's is all 0.
- `degraded` names the stages of a full read that fell back instead of failing it, as `/search`
  logs a partially degraded search (`judge`, `layer`, `order`, …), each once; empty when none,
  and always in lexical mode.
- `trace` is the stages, in order (below), or null when `trace` was `false`.

**A full search that fails** answers its partial trace and usage:

- `402 {"error":"debug_budget_exhausted","trace":[…],"usage":{…}}` when the debug budget refused
  a call (`trace` is null when it was refused before anything ran);
- `502 {"error":"provider_failed","result":[…],"trace":[…],"usage":{…}}` when the read failed
  otherwise (a provider, the breaker, the 10-minute bound): `result` is then what `/search`
  would answer, the raw ranking, as lexical returns it;
- `503 {"error":"store_busy","trace":[…],"usage":{…}}`, with `Retry-After`, when the store
  changed while the search read it, or SQLite was busy, once its read (the model calls) had
  run: nothing it read is answered, but what the read cost is. `trace` is null when its
  stages could not be built, or `trace` was `false`. A search that meets the same before its
  read (and any lexical one) answers `{"error":"store_busy"}` alone.

A read in which a stage fell back answers 200 and names the stages in `degraded`. A client that
closes its connection cancels its full search: the search's model calls stop, and it is not
answered (the debug budget keeps what they cost).

## The trace

Every stage has:

| Field | Type | Meaning |
| --- | --- | --- |
| `name` | string | the stage |
| `ms` | number | the wall time spent in the stage, summed over its parts |
| `calls` | int | the model calls it made |
| `tokens` | object | `{"input", "output"}`, ints |
| `cost` | object | `{"deepseek", "jev"}` |

A lexical trace is `pending`, `candidates`, `compose`; a full one is `pending`, `cues`,
`candidates`, `judge`, `need`, `compose`. The read runs them interleaved (the judge screens the
pool, the need loop reads more, the judge judges the shortlist, composing judges the layer); each
stage sums its parts.

Ids in a trace point into the rest of the answer: a record id is a `result` item's `id` when the
result shows it, and an id of `/records`; an item id (`T3`, `e7`, `pet · name`, `d0`) is the `id`
of a `/memory` item.

### `pending`

What `/search` would fold before reading. Inspect never folds it.

| Field | Type | Meaning |
| --- | --- | --- |
| `count` | int | the records not folded yet (`/memory`'s `pending.count`) |
| `ids` | [string] | the first 100 |
| `watermark` | int | the consolidation's watermark |
| `folded` | bool | always `false` |

Without consolidation `count` is 0, `ids` empty and `watermark` 0.

### `cues` (full)

The planner's plan: what to search, and what the reply needs.

| Field | Type | Meaning |
| --- | --- | --- |
| `cues` | array | `{"text", "kind"}`, in order: `kind` `message` (the question itself), `note` (the planner's invented answering note) or `search` (a search the planner wrote) |
| `needs` | array | `{"text", "all"}`: up to three information needs; `all` when every instance is needed (a count, a list) |
| `needless` | bool | the planner named no need: the one need is the question |

### `candidates`

What the searches found, before any judgement.

| Field | Type | Meaning |
| --- | --- | --- |
| `searches` | array | one per search: `{"search", "text", "kind", "source": "record", "ranked", "ids", "scores"}` |
| `pool` | [string] | full only: the records the first pages pooled (at most 48), in pool order |
| `layer` | array | full only: the consolidated layer's candidates, `{"id", "kind", "source": "layer", "lit", "near"}`; empty when the read added no layer |

- `searches[].search` is the search's number in the read (0, 1, …); in full mode the first ones
  are the cues, `search` *i* being cue *i*. Lexical has one search, of the query, `kind` `query`.
- `searches[].ranked` is how many records the search ranked (at most 200); `ids` are the ones
  read here, in rank order: full, the first page of 20; lexical, the first `top_k`.
- `scores[i]` is `ids[i]`'s words score (BM25) for the search's text over the store's records,
  the score the ranking sorted by.
- `layer[].id` is a thread id (`kind` `topic`) or a fact key (`kind` `value`). `lit` counts the
  composed records linked to it (0 when none); `near` is its rank among those nearest the
  question (0 is the nearest), or null.

### `judge` (full)

JEV's answers, per candidate.

| Field | Type | Meaning |
| --- | --- | --- |
| `records` | array | every pooled record: `{"id", "source": "record", "excerpt", "screen", "row", "needed", "superseded", "judged", "decision", "shown"}` |
| `layer` | array | the layer's candidates: `{"id", "kind", "source": "layer", "excerpt", "needed", "decision"}` |
| `linked` | array | records linked from the layer's blocks that JEV judged: `{"id", "source": "layer", "excerpt", "needed", "decision", "shown"}` |

- `screen` is the screen's probability that the reply needs the record (null when the read did
  not screen it); `row` the need table's per need (null when the record was not tabled).
- `needed` and `superseded` are the final judgement's probabilities that the reply needs it and
  that something later made it no longer current; `judged` is false when the judgement did not
  reach the record (`needed` is then its screen, and `superseded` 0).
- `decision`, for a record: `superseded` (superseded ≥ 0.5), else `needed` (needed ≥ 0.5), else
  `kept` (shown all the same: the View's fill), else `dropped`. For a layer candidate or a
  linked record: `kept` (the View shows it) or `dropped`. A topic is shown when the topics
  block lists it, or when the timelines block shows any of its events (the topics block lists
  a topic only while it has room).
- `shown` says whether `result` shows the record.

### `need` (full)

The need loop: rounds that read on until each need is met, done or given up.

| Field | Type | Meaning |
| --- | --- | --- |
| `rounds` | array | `{"round", "steps", "pages", "searches", "fresh"}`, one per round that acted |
| `states` | [string] | each need's final state: `open`, `met`, `done` or `given up` |
| `stop` | string | why the loop stopped: `covered`, `given up` or `rounds` |

- `steps` is `{"need", "step"}` per need: `none`, `met`, `done`, `given up`, `page` (read the next
  page of a search) or `search` (asked the planner for a new search).
- `pages` is `{"search", "ids", "scores"}`: the next page read from a search, `search` being its
  number.
- `searches` are the planner's new searches, as in `candidates.searches`, with their first page.
- `fresh` are the records the round pooled.

### `compose`

The View as `result` shows it, item by item.

| Field | Type | Meaning |
| --- | --- | --- |
| `blocks` | array | one per `result` item, in order: `{"position", "id", "layer", "items", "records", "excerpt"}` |
| `truncated` | bool | the View left out something for want of items or characters |
| `order` | string | how the records are listed: `time` or `rank` |

- `position` is the item's index in `result` and `id` its `id` there.
- `layer` is `instructions`, `topics`, `timelines`, `facts`, `insights` or `records`.
- `items` are the memory items a block shows: directives (`d…`), threads (`T…`), events (`e…`),
  fact keys; for a record, its own id.
- `records` are the record ids a block links to that `result` shows; for a record, its own id.
- `excerpt` is the item's `content`, excerpted.

## Status codes

| Status | `error` | When |
| --- | --- | --- |
| 200 | | success |
| 400 | `invalid` | a malformed field, named in `field`: `limit`, `after`, `key`, `section`, `id`, `user_id`, `query`, `options`, `top_k`, `mode`, `trace`, or `body` (not a JSON object, or more than white space after it) |
| 401 | `unauthorized` | no bearer token, or another one |
| 402 | `debug_budget_exhausted` | a full search the debug budget refused; partial `trace` and `usage` |
| 404 | `store_not_found` | no store with that key |
| 404 | `record_not_found` | `/records/{id}`: no record of that id among those `/records` lists |
| 404 | `item_not_found` | `/memory/{section}/{id}`: no item of that id in the section |
| 404 | `not_found` | no such route |
| 405 | `method_not_allowed` | another method on a route |
| 409 | `full_disabled` | a full search while `modes.full` is false |
| 413 | `too_large` | a body over 65,536 bytes |
| 500 | `internal` | a failure retrying will not help (for example a damaged store) |
| 502 | `provider_failed` | a full search whose read failed; `result` (the raw ranking), partial `trace` and `usage` |
| 503 | `busy` | `--max-concurrent` requests are reading, or the service is stopping; `Retry-After` says when to retry |
| 503 | `store_busy` | the store changed while it was read without a write-ahead log, SQLite was busy, or a writer's crash left a journal its writer must roll back first; `Retry-After`. A full search whose read had run adds its `trace` and `usage` |

## Side effects: none, by construction

- **Read-only files.** `records.db` and `memory-index.db` are opened read-only. A store that a
  writer has open (its `-wal` and `-shm` exist) is read through them, live (`mode=ro`). A store
  no writer has open is read from its main file alone (`mode=ro&immutable=1`), which never makes
  a `-wal` or `-shm`; the file's size, time and identity are checked when the request ends, and a
  change answers `503 store_busy`. Nothing ever checkpoints. In the memory cloud the unit mounts
  the instance's state read-only (`ReadOnlyPaths=`), so a write could not happen anyway.
- **No flush, no consolidation.** Pending records stay pending (stage `pending`).
- **No instance budget, cache or metric.** Full searches reserve and settle in the debug budget
  file (`--debug-budget-file`, which must differ from the instance's `budgetFile`) under its caps
  (`--debug-caps`); the model cache is off; the service has no metrics.
- **Logs** carry the method, the route, the status, the time and the store's 8-digit prefix,
  never a key, a user id, a query, a looked-up id or any content; `/v1/resolve` logs no store
  at all.
