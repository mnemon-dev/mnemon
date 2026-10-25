# Admin API

`mnemon memory-agent aml-serve --admin-listen ADDR` serves the operator's side of one engine
instance on a listener of its own. The public listener (`--listen`, the Add/Search contract and
`/health`) serves none of the routes below. The alert watcher (`aml-watch`), Prometheus and the
memory cloud console read this listener; the deployment is in the
[AML runbook](aml-adapter.md).

The admin listener is plain HTTP. Its routes read, but for the pair that replaces and reverts
the account's own provider keys (`/admin/v1/provider-keys`, below): the one other thing a
request makes is `/ready`'s probe of the data root, a file it creates there and removes; none
touches a user's records.

## Where it listens, and who may read it

`--admin-listen` is empty by default: there is no admin listener. When it is set it takes an IP
address literal and a port, such as `127.0.0.1:8091`. A host name is not accepted: what it
resolves to is not the command's to check.

| Address | Admin token |
| --- | --- |
| Loopback: 127.0.0.0/8, `::1` | optional |
| Private: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, `fc00::/7` (`netip.Addr.IsPrivate`) | required |
| Anything else | refused, token or not |

"Anything else" includes the unspecified addresses (`0.0.0.0`, `::`, or no host), link-local
ones (169.254.0.0/16, `fe80::/10`), shared address space (100.64.0.0/10) and every public
address. So is an address with a zone (`[fe80::1%eth0]:8091`) or without a port. An IPv4 address
spelled as an IPv6 one (`[::ffff:10.0.0.5]:8091`) is judged as the IPv4 address. A refusal is a
startup error that names the flag and the rule; the command has not locked the data root or
opened anything by then. An address that passes the rules but cannot be bound (the overlay
address before `wg0` is up) is not a startup error: the data plane never depends on the admin
listener, so the process logs the failure once, serves its public listener, and binds this one
again every 5 seconds until it can ([runbook](aml-adapter.md), "Private network").

`--admin-token-env NAME` names the environment variable that holds the admin token. It is empty
by default: no token. When it is set:

- The variable must hold at least 32 bytes (not characters: a token of multi-byte characters
  counts each byte). If it is unset, empty or shorter, startup fails with an error that names
  `--admin-token-env` and the variable, never its value. This holds whatever the address, and
  with no `--admin-listen` at all.
- It must be another variable than the one `--auth-key-env` names (`AML_API_KEY` by default): the
  admin token and the API key are two secrets. Startup is refused otherwise.
- Every request to the admin listener must carry the token (below).

Make a token from a source of randomness, for example `openssl rand -hex 32`, and keep it in the
instance's environment file (root-owned, mode 0600) and nowhere else a flag, a URL or a log could
show it. Bind a private address only on a network whose firewall lets just the hosts that read
the listener reach it: the token is the second lock, not the first, and it crosses that network
in clear text.

### In the memory cloud

Every engine instance has its admin listener on its host's WireGuard address, with a token
([runbook](aml-adapter.md), "Instances" and "Private network"):

- `aml-serve@.service` passes `--admin-listen ${AML_ADMIN_LISTEN} --admin-token-env MNEMON_ADMIN_TOKEN`.
  `mnemon-instance new` sets `AML_ADMIN_LISTEN` to the host's overlay address (`--admin-ip`) and
  the instance's admin port: 8091 on a dedicated host, 8101 + 10 × the slot on a shared one.
- It makes the token, `mnm_` and 32 random bytes in base64url without padding (47 characters),
  and writes it as `MNEMON_ADMIN_TOKEN` into the instance's two environment files, `env` and
  `watch.env`, and into the one-time registration JSON for the console. Nowhere else holds it
  but the console's registry.
- The instance's watcher (`aml-watch@<n>`, `--admin-token-env MNEMON_ADMIN_TOKEN`), the console
  and, through the console, Prometheus read the listener. The instance hosts' firewalls admit
  the admin ports (8091 to 8600) only from the overlay's hub, `10.77.0.1`, on `wg0`.
- Rotating the token is a manual step: write the new one into both environment files, restart
  `aml-serve@<n>`, and set it in the console.

## Authentication

With an admin token set, **every** request to the admin listener must carry

```
Authorization: Bearer <token>
```

The scheme is read in any case (`Bearer`, `bearer`). Nothing else stands in for it: not an
`X-Api-Key` header, not the `Token` scheme the public listener also accepts, and never a
token in the URL's query. The rule covers all the routes below, the profiler and a path that no
route serves, which is answered 401 before it can be answered 404. A request is checked before
anything else is done for it.

The token and the configured one are compared as SHA-256 digests with `crypto/subtle`'s
constant-time comparison, so neither the token nor its length leaks through timing.

A request without the token, or with a wrong one, is answered:

```
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer realm="mnemon-admin"
Content-Type: application/json

{"error":"missing or invalid admin token"}
```

Each such answer is counted in `mnemon_aml_admin_auth_failures_total` (a counter with no labels)
and **no line is logged**: the counter is the signal. The token, what a client sent in its
place, and anything made from either never reach a log, an error, a metric or an answer.

Without an admin token (a loopback listener only) the admin listener asks nothing of a request
and works as it always did; `mnemon_aml_admin_auth_failures_total` stays 0.

## Routes

| Route | Answer |
| --- | --- |
| `GET /metrics` | the Prometheus text format |
| `GET /ready` | readiness: 200, or 503 with the reasons |
| `GET /admin/status` | the status the watcher reads, as JSON |
| `GET /admin/v1/stores` | the store overview, as JSON |
| `PUT /admin/v1/provider-keys` | the account's own provider keys: checked, written, swapped in |
| `DELETE /admin/v1/provider-keys?kind=` | back to the host's keys |
| `/debug/pprof/` and below | Go's profiler |
| anything else | 404 `{"error":"not found"}` |

`/metrics`, `/ready`, `/admin/status` and `/admin/v1/stores` are for GET, and
`/admin/v1/provider-keys` is for PUT and DELETE only (write-only: no route answers the keys).
Any other method on them is answered by the 404 route. The answers of `/ready`,
`/admin/status`, `/admin/v1/provider-keys` and (when it succeeds) `/admin/v1/stores` are
`Cache-Control: no-store`.

### `GET /metrics`

The server's own registry, with the Go and process collectors, in Prometheus's text format.
Every series of the server is named `mnemon_aml_*` and has a `HELP` line; the Go and process
collectors' (`go_*`, `process_*`) come beside them. No user, request or path ever labels a
series. Beside the series of the Add and Search paths, the models, the budgets and the
consolidation queue, it carries these, which the memory cloud console reads:

| Series | Meaning |
| --- | --- |
| `mnemon_aml_admin_auth_failures_total` | admin requests refused with 401 (no labels) |
| `mnemon_aml_model_attempts_total{kind,class,outcome}` | attempts of model calls. `outcome` is `ok`, `throttled` (refused with HTTP 429), `invalid` (a 200 reply that did not parse or failed validation), `rejected` (refused with HTTP 401, 402 or 403: the key or the balance), `retryable`, `failed`, `breaker-open`, `bulkhead-full` or `budget`; the console counts every outcome but `ok` as a model failure |
| `mnemon_aml_open_stores` | user stores the server holds open now |
| `mnemon_aml_stores` | user stores in the data root, from the latest scan (at most 60 s old) |
| `mnemon_aml_store_bytes` | bytes of the regular files of those stores, from the same scan |
| `mnemon_aml_consolidation_lag_bound_characters` | the backlog, in characters, above which an Add waits for consolidation; 0 unless `--consolidate background`. `mnemon_aml_consolidation_max_backlog_characters` is measured against it |

`mnemon_aml_stores` and `mnemon_aml_store_bytes` come from the scan `GET /admin/v1/stores`
reads (below): a scrape makes a new scan only when the latest is older than 60 seconds, and
shares it with a request that asks at the same time. A scan that fails leaves the two series out
of that scrape rather than report 0, and the failure is logged once per streak of failures. The
scrape waits for a scan for 5 seconds at most, also when the scan it joined was started by a
`GET /admin/v1/stores` request (which has no such deadline): it leaves at its own, so a data root
whose scan takes longer is not measured here. They carry no store or user label.

### `GET /ready`

200 with `{"ready":true}`. Otherwise 503 with `{"ready":false,"reasons":["…"]}`, every reason
given: the data root is not writable, its disk has less than 1 GiB free, a provider's circuit
breaker is open, or a paid account has spent and held more than 95% of its cap.

### `GET /admin/status`

The server's counters and state, as JSON. The watcher reads it, so its field names are a contract:
fields may be added, never renamed or repurposed. Counters count from `startedAt`. Users appear as
counts only.

| Field | Meaning |
| --- | --- |
| `startedAt` | when the server was made (RFC 3339); differs after every restart |
| `adds`, `searches` | Adds and Searches answered 200 |
| `degraded` | Searches answered by the raw ranking because the memory read failed |
| `partial` | Searches answered by a read in which a stage fell back |
| `dropped` | drops of records from the consolidation layer |
| `linkFallbacks` | batches linked without JEV because JEV failed |
| `addOutran` | Adds that waited out their window for consolidation and answered anyway |
| `partialLast10m`, `searchesLast10m` | partial Searches, and Searches, in the last ten one-minute buckets |
| `queue` | the consolidation scheduler: `queued`, `running`, `backingOff`, `held`, `oldestQueued` (nanoseconds) and `maxBacklog` (characters) |
| `lagBound` | the backlog, in characters, above which an Add waits (0 unless `--consolidate background`) |
| `breakers`, `breakerTrips` | by provider kind, the breaker's state, and how many times it has opened |
| `rejectedByKind` | by provider kind, the requests the provider rejected with 401, 402 or 403 (a wrong or revoked key, an empty balance); it only grows, from 0 for every kind the models have. The watcher's `provider-rejected:<kind>` alert reads it |
| `budgets` | each paid account that has been used: `kind`, `cap`, `spent`, `held`; always an array |
| `openStores` | user stores open now |
| `version` | the product version string of the build (`dev` for a build made without one) |
| `providerKeys` | by key kind the configuration's models use (`deepseek`, `jev`): `{"source","fingerprint"}`. `source` is `account` (the key file's key), `host` (the environment's) or `none` (neither: the variable is empty, and the kind's calls fail); `fingerprint` is the key's (below), `""` with none. Never a key. An empty object when the configuration has neither kind |

### `GET /admin/v1/stores`

A read-only overview of the user stores in the data root, for the console. Query:

- `limit`: 1 to 500; 100 when absent.
- `after`: a cursor, exclusive: the `next` of the previous page. Exactly 32 lowercase hex digits.

A `limit` or `after` that is malformed, an empty one included, is answered
`400 {"error":"…"}`, and so is a query string that cannot be read: one whose percent-encoding is
malformed (`after=%zz`), or whose pairs are separated by `;` (only `&` separates them). It is not
read as the query without that pair, and the error says which:
`the query string is malformed: its percent-encoding is invalid` or
`the query string is malformed: pairs are separated by '&', not ';'`.

```json
{
  "total": 12,
  "bytes": 734003200,
  "scannedAt": "2026-10-25T08:00:00Z",
  "stores": [
    {"store": "58e645e0", "bytes": 61440000, "modified": "2026-10-25T07:59:12Z",
     "open": true, "backlog": 12345}
  ],
  "next": "58e645e06cad8b5e2d3b842ebef7c1c5"
}
```

- `total`, `bytes` and `scannedAt` describe the whole data root: the number of stores, the sum of
  their bytes, and when the scan ended (RFC 3339, UTC).
- `stores` is a page, in the order of the stores' keys. Each is named by `store`, the first 8 hex
  digits of its key, as the logs name a user. Two stores may share it. The key is the first 32 hex
  digits of `sha256(user_id)`, and the store is the directory `<data-root>/users/<key>`. The user
  id is not stored anywhere, so no id can be shown, and neither is any record's content.
- `bytes` is the sum of the sizes of the regular files directly in the store's directory
  (`records.db`, its `-wal` and `-shm` files, `memory-index.db` and the like). A subdirectory or a
  link is not a file of the store. `modified` is the newest modification time among those files
  (the directory's own when it holds none), in RFC 3339, UTC, to the second.
- `open` says the server holds the store open now: a read of the registry of open stores, with
  no I/O. A store that is not open costs nothing until a request needs it.
- `backlog` is the scheduler's estimate of the characters (UTF-16 units, as consolidation counts
  them) of the store's records not yet consolidated, the unit of `queue.maxBacklog` and
  `lagBound`. It is known from the user's first Add, or first consolidation job, since the process
  started: that Add measures the backlog once, after its write and before it lets go of the user's
  write lock (reading only: the user's other Adds wait for that one read, other users' Adds do
  not), and the estimate then counts what the Adds after it write. It is `null` when the
  scheduler has no estimate: outside `--consolidate background`; before that first Add or job; and
  for a store with nothing pending, which the scheduler stops tracking.
- `next` is the last store of the page, as a cursor, to pass back as `after`. It is `null` on the
  last page. A cursor that names no store is where the order resumes, so a store removed
  between two pages does not end the walk.

The scan that makes `bytes` and `modified` lists the directories under `<data-root>/users/` whose
names are user keys, reads each store's directory and nothing else, and honors the request's
context between stores. It is kept for 60 seconds, the first request after that makes the next,
and the requests that come meanwhile wait for it and share it (each leaves when its own
deadline or its client does), so a console that polls cannot make a storm of directory reads.
`open` and `backlog` are read for each page when it is asked for, not kept. If the scan fails the
answer is 500 `{"error":"the store scan failed"}` (503 when it was interrupted), nothing is kept,
and the server logs the failure once, naming a store by its 8-digit prefix, until a scan goes well
again.

### `PUT /admin/v1/provider-keys`

The account's own DeepSeek and JEV keys (BYOK, ruling R98): the console's 设置 → 模型服务 API key
sends them here (through its `PUT /api/instances/:id/provider-keys`). For each kind, the account's
key, when it has one, is sent in place of the host's.

```json
{"deepseek": "sk-…", "jev": "…"}
```

- The body is a JSON object of at most 4 KiB (else `413 {"error":"too_large"}`); not an object,
  empty, or followed by more than white space: `400 {"error":"invalid","field":"body"}`.
- At least one of `deepseek` and `jev`, each a string of 8 to 512 printable ASCII characters
  without spaces; one that is not: `400 {"error":"invalid","field":"<kind>"}`.
- Another field is `400 {"error":"invalid","field":"<name>"}`. A field whose name is not a plain
  name (1 to 32 letters, digits and `_`, starting with a letter) is named `body` instead: what
  stands there may be a key in the wrong place.
- A kind the configuration has no model of: `409 {"error":"unknown_kind","kind":"<kind>"}`.
- Without `--provider-keys-file`: `409 {"error":"no_key_file"}`. The keys cannot change at run
  time without a file to keep them in.

What it does, in order:

1. **Checks every key given before any is applied,** DeepSeek's first, with one request to its
   provider: `GET {base}/models` with `Authorization: Bearer <key>`, where `{base}` is the model's
   endpoint without its route (`https://api.deepseek.com`, `https://api.typesafe.ai/v1`; a
   model's `testEndpoint` likewise), through the provider's own transport (HTTP/1.1). It is not an
   attempt of the resilience layer: no retry, breaker, bulkhead, metric or rejection count sees
   it, and it spends no budget. When that route answers 404 or 405, one minimal call of the
   model of that kind takes its place: a chat completion of `max_tokens` 1 (for JEV, whose API
   decides rather than completes, a decision of one question), likewise outside every budget;
   its few tokens are the provider's to bill, and `checked` says `completion`. A kind's check,
   its fallback included, has 10 seconds.
   - a 2xx: the key is good;
   - 401, 402 (an empty balance) or 403: `400 {"error":"key_rejected","kind":"<kind>","status":<status>}`;
   - anything else (a timeout, a broken connection, a 5xx, a 429, a redirect):
     `502 {"error":"key_check_unavailable","kind":"<kind>"}`.

   The first kind that fails answers; a kind after it is not checked.
2. **Writes and swaps,** only when every key given passed: the keys given are merged into the
   key file, over the other kind's account key if it has one (which stays); the file is written
   atomically; the keys in memory are swapped. A key replaced applies to the model calls that
   start once the reply is sent; calls in flight finish, retries included, with the key they
   started with. No restart. A write that fails changes nothing and answers
   `500 {"error":"internal"}` (the server logs why).
3. **Logs one INFO line,** naming the kinds given: `provider keys replaced: deepseek
   0123456789ab, jev ba9876543210`. A key's **fingerprint** is the first 12 hex digits of its
   SHA-256. A refusal by the provider is a WARN line, `provider keys not replaced: the deepseek
   key was rejected (HTTP 401)`, or `…: the jev key: the key could not be checked: HTTP 503`.

The reply is `200`, every configured kind's status, and how each key given was checked:

```json
{"providerKeys": {"deepseek": {"source": "account", "fingerprint": "0123456789ab"},
                  "jev": {"source": "host", "fingerprint": "ba9876543210"}},
 "checked": {"deepseek": "models", "jev": "completion"}}
```

No error changes anything: neither the file nor the keys in memory. The key itself never
appears in a log, an error, a metric or a reply, of this route or any other; only its
fingerprint does.

### `DELETE /admin/v1/provider-keys?kind=deepseek|jev|all`

Reverts the account's keys of a kind, or of every kind (`all`), to the host's: they leave the key
file, which is removed when no key is left, and the kinds' calls send the host's keys at once
(those in flight keep theirs). Reverting a kind that has no account key changes nothing, and
answers the same. The reply is `200 {"providerKeys": {…}}`, the shape above, and one INFO line is
logged: `provider keys reverted to the host's: deepseek, jev`.

- A missing, empty, unknown or repeated `kind`, or a query string that does not parse:
  `400 {"error":"invalid","field":"kind"}`.
- A kind the configuration has no model of: `409 {"error":"unknown_kind","kind":"<kind>"}`.
- Without `--provider-keys-file`: `409 {"error":"no_key_file"}`.

### The key file

`--provider-keys-file PATH` names it: in the memory cloud
`/var/lib/mnemon-aml/<n>/provider-keys.json`, in aml-serve's StateDirectory, beside the data root
(`…/data`) and never in it, so that a snapshot of the data root never holds a key. aml-serve
refuses a path inside its data root. Its format:

```json
{"version":1,"keys":{"deepseek":"<key>","jev":"<key>"},"updatedAt":"<RFC 3339>"}
```

Either key may be absent. aml-serve writes it atomically (a temporary file in the same
directory, fsync, rename, then the directory's fsync), mode 0600, as the service user. A missing
file means the host's keys only. A start is refused, with an error that names the file and never
its content, when the file:

- is not a regular file (a directory, a symbolic link);
- may be read or written by its group or others (POSIX; not checked on Windows, R89);
- is not owned by the process's user (POSIX);
- is larger than 64 KiB, or malformed: not JSON, another `version`, another field, no `keys`, a
  key that is not 8 to 512 printable ASCII characters without spaces, an `updatedAt` that is not
  an RFC 3339 time;
- names a kind other than `deepseek` and `jev`, or one the configuration has no model of.

A write that a crash cut short leaves a temporary file beside it (`.provider-keys.json.tmp-…`),
never a partial key file; the next start of aml-serve, the file's one writer, removes it and logs
`removed what an interrupted write of the provider key file left: …`.

`aml-inspect --provider-keys-file PATH` reads the same file, never writes it: at its start, by
the same rules, and again before each full search, so a key the console replaces reaches the
console's full searches without a restart. A read that fails then keeps the keys it has, and is
logged once (a WARN line naming the file) until a read succeeds again. Lexical searches need no
key.

### `/debug/pprof/`

Go's `net/http/pprof`: the index and the named profiles under it (`heap`, `goroutine`, …),
`/debug/pprof/cmdline`, `/debug/pprof/profile`, `/debug/pprof/symbol` and
`/debug/pprof/trace`. A profile reads the process's memory and stacks, which is why this listener
is not public. Its write timeout (5 minutes) outlasts a profile of up to 4 minutes.

## `aml-watch`

`mnemon memory-agent aml-watch` reads `/ready` and `/admin/status` on this listener, and the
public `/health`, once per run (see the runbook for its rules).

- **`--admin-token-env NAME`** names the variable that holds the admin token (the same token
  as aml-serve's). The watcher reads it once, at the start, and the run is refused, naming the
  variable, if it is unset or empty. It sends `Authorization: Bearer <token>` on every admin
  read, and only there: the public `/health` probe carries no token, and an admin read is not
  redirected, since the `Location` of an answer is not the operator's. Without the flag the watcher
  sends none. The token is never printed, logged or put in an error, a message or the state; a
  server that refuses it is an alert about readiness (`/ready answered 401`).
- **`--instance NAME`** names the instance the watcher serves, so that instances on one host can
  be told apart. The name is 2 to 31 characters: lowercase letters, digits and hyphens, starting
  with a letter (`^[a-z][a-z0-9-]{1,30}$`); the run is refused otherwise. With it, the header of
  every message reads `[mnemon-aml] ALERT <alert> on <host>/<name>`, and `RESOLVED` likewise,
  where it reads `on <host>` without it.
