# AML adapter runbook

`mnemon memory-agent aml-serve` serves the Agent Memory Leaderboard (AML) `Add` and
`Search` HTTP contract on top of Mnemon's raw records and the "simple" memory strategy.
This runbook installs it on one Ubuntu (amd64) VM behind a TLS reverse proxy, runs it for
a job, and removes the data afterwards. The files of the deployment kit are in
[`deploy/aml/`](../deploy/aml); the submission text is in
[`deploy/aml/README.md`](../deploy/aml/README.md).

## Architecture

```
AML platform ──HTTPS──▶ Caddy :443 ──HTTP──▶ aml-serve 127.0.0.1:8090
 POST /add, /search      TLS, 31 m timeouts      one process, one data root
 GET  /health            30 MiB body limit        │
                                                  ├─ <data-root>/users/<hash>/   one records store per user_id
                                                  ├─ Ollama 127.0.0.1:11434      nomic-embed-text embeddings
                                                  ├─ DeepSeek Flash (HTTPS)      cue planner, consolidation
                                                  └─ JEV 1.13.0 (HTTPS)          judge
```

- **Add** packs the messages into raw records (no model on the write path), embeds the new
  chunks through Ollama, and, with `--consolidate-on-add`, folds every full batch (10,000
  characters) of the user's new records into the timelines index with DeepSeek before
  answering 200. The rest, the tail of the conversation so far, is folded before that user's
  next Search, as the reference system folds a conversation once it pauses. A retry with the
  same `request_id` is idempotent.
- **Search** (with `--consolidate-on-add`) first folds the user's tail, then runs the planner,
  the cue searches, the JEV need loop and the RRF fusion of the raw search rank with the JEV
  rank, and returns stored records (and the consolidation blocks) in rank order. It never
  returns generated answers. If a model, a budget or the embedder fails, or the read runs past
  `--search-timeout`, it falls back to the raw hybrid ranking with HTTP 200 and logs the fact.
- Each `user_id` has its own store: `<data-root>/users/<hex(sha256(user_id))[:32]>`. One user
  can never reach another's records.

### Contract details

- Add reads a message `timestamp` as Unix milliseconds, as Unix seconds when the number is
  below 10^11, or as an RFC 3339 string (`2023-05-08T13:56:00Z`); numbers may come as strings,
  and anything else is no timestamp. Multimodal `content` keeps its plain strings and the
  `text` of parts whose `type` ends in `text` (`text`, `input_text`, `output_text`).
- Search needs only a `user_id`. A missing, zero or negative `top_k` means the View maximum
  (100 with the shipped configuration); a blank `query` answers `{"data":[]}`.
- Each Search item's `score` is `1/(rank+1)` by its position in `data` (1, 0.5, 0.333…), layer
  blocks included, so a client that sorts by score keeps the order.

## Prerequisites

- An Ubuntu 22.04 or 24.04 VM, amd64, with `systemd`.
- Inbound 80 and 443 (Caddy, and the certificate challenge). Port 8090 stays on loopback.
- Outbound HTTPS to `api.deepseek.com` and `api.typesafe.ai`. Check both from the VM before
  the job: `curl -sS -o /dev/null -w '%{http_code}\n' https://api.deepseek.com` (any HTTP
  status shows the host is reachable).
- A DNS name for the VM, and the three secrets below.
- On the build machine: Go 1.24 and `make`.

## Secrets and environment variables

Secrets are read only from the environment, never from the configuration file, and are not
logged. Put them in `/etc/mnemon-aml/env`:

| Variable | Needed | Used for |
|---|---|---|
| `AML_API_KEY` | yes | The key the platform sends (`Authorization: Bearer <key>`, `Token <key>` or `X-Api-Key: <key>`). `aml-serve` refuses to start without it. |
| `TYPESAFE_API_KEY` | yes | JEV judge (`judge.keyEnv`). |
| `DEEPSEEK_API_KEY` | yes | DeepSeek Flash planner and consolidator (`planner.keyEnv`, `consolidator.keyEnv`). |
| `OPENAI_API_KEY` | optional | Only when a model uses `"kind": "openai"` (for example `gpt-4.1-mini` as the planner, with `"baseURL": "https://api.openai.com/v1"` and `"keyEnv": "OPENAI_API_KEY"`). Models outside the built-in price table (`gpt-4.1-mini`, `gpt-4o-mini`) need `priceIn` and `priceOut` (USD per 1M tokens). Not used by the shipped configuration. |

```sh
# /etc/mnemon-aml/env  (root:root, mode 0600; systemd reads it before dropping to User=mnemon)
AML_API_KEY=<the key you register on the platform>
TYPESAFE_API_KEY=<JEV key>
DEEPSEEK_API_KEY=<DeepSeek key>
#OPENAI_API_KEY=<only with an openai model>
```

The `keyEnv` fields of the configuration name these variables, so the names can be changed
in both places. A paid model whose variable is empty makes `aml-serve` fail at startup.

## Install

1. **Build the binary** on the build machine and copy it:

   ```sh
   make build-linux-amd64 VERSION=aml-1          # writes bin/mnemon-linux-amd64 (static, CGO off)
   file bin/mnemon-linux-amd64                   # ELF 64-bit LSB executable, x86-64, statically linked
   scp bin/mnemon-linux-amd64 deploy/aml/* <vm>:/tmp/
   ```

   On the VM:

   ```sh
   sudo install -m 0755 -o root -g root /tmp/mnemon-linux-amd64 /usr/local/bin/mnemon
   ```

2. **Create the service user and the configuration directory.**

   ```sh
   sudo useradd --system --no-create-home --shell /usr/sbin/nologin mnemon
   sudo install -d -m 0750 -o root -g mnemon /etc/mnemon-aml
   sudo install -m 0640 -o root -g mnemon /tmp/memory-config.industrial.json /etc/mnemon-aml/memory-config.json
   sudoedit /etc/mnemon-aml/memory-config.json   # set "budgets": the shipped caps are placeholders
   sudoedit /etc/mnemon-aml/env            # the variables above
   sudo chmod 0600 /etc/mnemon-aml/env && sudo chown root:root /etc/mnemon-aml/env
   ```

   Set `budgets` (the spending caps, see Configuration) before the first start: they are
   placeholders, and the service enforces whatever the file says.

   The systemd unit creates `/var/lib/mnemon-aml` (mode 0700, owner `mnemon`) when it starts.
   The data root is `/var/lib/mnemon-aml/data` and the model budget file is
   `/var/lib/mnemon-aml/model-budget.db`.

3. **Install Ollama and the embedding model.** Embeddings run on the same host.

   ```sh
   curl -fsSL https://ollama.com/install.sh | sh      # review the script first if you prefer
   ollama pull nomic-embed-text
   curl -s http://127.0.0.1:11434/api/embed \
     -d '{"model":"nomic-embed-text","input":"search_document: hello"}' | head -c 120
   ```

   The last command must print an `embeddings` array. Ollama unloads an idle model after five
   minutes by default; if the first request after a pause is slow, set `OLLAMA_KEEP_ALIVE`
   (and, for concurrent Adds, `OLLAMA_NUM_PARALLEL`) with `sudo systemctl edit ollama`.

4. **Install the service.**

   ```sh
   sudo install -m 0644 /tmp/aml-serve.service /etc/systemd/system/aml-serve.service
   sudo systemctl daemon-reload
   sudo systemctl enable --now aml-serve
   sudo systemctl status aml-serve --no-pager
   curl -s http://127.0.0.1:8090/health            # {"status":"ok"}
   ```

   The unit runs exactly:

   ```
   /usr/local/bin/mnemon memory-agent aml-serve --listen 127.0.0.1:8090 \
     --data-root /var/lib/mnemon-aml/data --memory-config /etc/mnemon-aml/memory-config.json \
     --consolidate-on-add --max-open-stores 16
   ```

   If it exits at once, read `journalctl -u aml-serve -n 50`: the usual causes are a missing key
   in the env file, a config the strict loader rejects, or a relative path.

5. **Install Caddy** (v2, from the official apt repository: caddyserver.com/docs/install), then:

   ```sh
   sudo install -m 0644 /tmp/Caddyfile /etc/caddy/Caddyfile
   sudoedit /etc/caddy/Caddyfile                   # replace aml.example.com with your domain
   caddy validate --config /etc/caddy/Caddyfile
   sudo systemctl reload caddy
   ```

   Caddy obtains the certificate for the domain on its own once DNS points at the VM and 80/443
   are open. For a certificate issued elsewhere, use the `tls <cert> <key>` line shown in the
   Caddyfile. Do not enable `log_credentials`: the access log must not carry the API key.

6. **Register** `https://<domain>` as the endpoint, and the same `AML_API_KEY` as the key, on
   the platform. The platform calls `POST /add`, `POST /search` and `GET /health`.

## Configuration

`deploy/aml/memory-config.industrial.json` is loaded strictly (unknown fields are errors, and
the file must be at most 1 MiB with one JSON value). JSON has no comments, so the notes are
here:

| Field | Value | Notes |
|---|---|---|
| `strategy` | `"simple"` | The read strategy of the paper. `""` would be the older memory-agent/v1 reads. |
| `view` | `maxItems 100`, `maxCharacters 240000`, `fusion "rrf"` | `top_k` above `maxItems` is clamped to it, never an error. The platform's formal `top_k` is 100. |
| `consolidation` | `{"mode": "timelines"}` | Other fields keep the author's defaults (10,000-character batches, at most 16 batches per job). A user's records waiting to be folded are bounded by 16 × 10,000 characters; later records wait unread, losing nothing. |
| `planner` | DeepSeek Flash, **no thinking** | Do not add `"thinking": true`. Cue planning is capped at 400 output tokens and need planning at 200; a reasoning model can spend them on reasoning and return an empty plan, which makes Search degrade. |
| `judge` | `jev` / `jev-1.13.0` | The version is pinned; the endpoint is JEV's official one. |
| `consolidator` | DeepSeek Flash, no thinking, `timeoutSeconds` 600 | Each fold asks for at most 8,192 output tokens whatever `maxTokens` says. A fold of a full batch can take minutes, so its calls may run 600 s (`timeoutSeconds`, 1–1,800; models without it time out after 90 s). Unset, the planner would fold. |
| `embedding` | Ollama at `http://127.0.0.1:11434`, `nomic-embed-text` | Prefixes `search_query: ` and `search_document: ` (note the trailing spaces). Changing the model or prefixes after data exists means new vectors: use a fresh data root. |
| `budgetFile` | `/var/lib/mnemon-aml/model-budget.db` | Absolute, private (0600), shared by all users of the process. Keep one budget file per deployment: two profiles with different caps would overwrite each other's stored cap. |
| `budgets` | `{"jev": 20, "deepseek": 200}` | **Placeholders: set them before the job.** They are hard caps on spending, in USD for `jev` and `openai` and CNY for `deepseek`. When a cap is reached the model calls stop (see Troubleshooting). Every call holds a reservation while it runs: USD 0.01 for JEV, CNY 0.25 for DeepSeek, more for a large fold (up to about CNY 0.46 for the largest request). A call that never connected or got a non-2xx answer releases it; a call that failed after sending, returned an unreadable reply or was cut by a restart keeps it held, so the usable headroom is a little below the cap. The configured value replaces the stored cap when the service starts (builds before configurable budgets kept the lower of the two); the amount already spent is kept. The `openai` cap is a soft one: a call that costs more than its reservation is recorded at its real price. |

A Search can make several planner calls and up to 128 JEV decision calls (the read budget).
Consolidation makes one DeepSeek call per fold (more when a reply must be asked again or
split): during the Add that completes a batch, and before the next Search for the rest.
Estimate the job's spend from a trial run (read the budget file, below) before choosing the
caps.

## Operating notes

- **One `aml-serve` process per data root.** There is no lock file. Two processes on the same
  data root would break the per-user write serialization and the idempotency checks. For
  another job or another run, use another `--data-root` and another budget file (and another
  port).
- **`--max-open-stores`.** Every open user store keeps its query cache, and the cache keeps
  each vector it loads: about 3 kB per chunk with `nomic-embed-text` (768 dimensions), so
  roughly 200 MB for a user with 70,000 chunks. Set the limit to the memory you can spare
  divided by the largest user: use 8 to 16 for BEAM-scale users (the unit uses 16), and the
  default 64 only for small users (LoCoMo-scale users hold a few hundred chunks). A store
  that is in use is never closed, so more distinct users in flight than the limit use more
  memory for a while. Too low a limit makes stores reopen with a cold cache, which slows the
  next Search of that user.
- **`--consolidate-on-add`.** With it, consolidation runs during requests. An Add folds every
  full batch (10,000 characters) of the user's new records into the topics, timelines and
  values index, and stores the index embeddings, before it returns; it consolidates until 25
  minutes after it arrived at most, and leaves the work to an Add of the same user queued
  behind it, which catches up. The short rest, the conversation's tail, is folded before that
  user's next Search, for at most 10 minutes; a user with nothing pending pays only a state
  read. Consolidation failures never fail the Add or the Search: the records are already
  searchable, the failure is logged, and the next request of that user carries on. When the
  provider fails three times on the same batch over at least ten minutes (not counting a
  spent budget), the next attempt splits it once and leaves a half that fails again out of
  the index (`consolidation dropped …` in the log; the records stay searchable). With
  `consolidation.nativeEdges` the jobs of an Add, and of a Search's flush, defer the
  records' native edges, which are rebuilt once after them when any job left them pending.
  Without the flag the adapter never consolidates (there is no other trigger), and Search
  returns evidence records only.
- **`--search-timeout`** (default `10m`, `1m` to `18m`) bounds a Search's memory read. When it
  expires the Search answers the raw ranking (HTTP 200, counted as degraded). With the tail
  fold's own 10 minutes before it, a Search answers within the platform's 30 minutes.
- **Restarts and retries.** On SIGTERM the server lets requests finish for 30 seconds. A longer
  request is cut, and the platform retries it with the same `request_id`; the retry completes
  the missing steps (write, embedding, consolidation) without duplicating the document. A
  model call cut by a stop keeps its reservation held for good (one reservation per call in
  flight); check `held` in the budget file (Logs and monitoring) and raise the cap by that
  much if the headroom gets tight. Avoid replacing the binary while a job runs.
- **Limits that match the platform.** The server accepts 30 MiB bodies, sets the write timeout to
  31 minutes, and answers `GET /health` without a key (any 2xx is healthy).

## Capacity declaration

The capacity table to submit is "Capacity, timeouts and limits" in
[`deploy/aml/README.md`](../deploy/aml/README.md). Fill it in from measurements on the target
VM, with the recipe below, before the submission; do not submit the placeholders. Keep
`--max-open-stores` at 16 unless a measurement says otherwise.

To measure Search latency at concurrency `C` over `N` users that already hold data:

```sh
export BASE=https://aml.example.com KEY=<AML_API_KEY>
C=4; N=40
seq 1 "$N" | xargs -P "$C" -I{} sh -c \
  'curl -s -o /dev/null -w "%{http_code} %{time_total}\n" -X POST "$BASE/search" \
     -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
     -d "{\"query\":\"What did the user say about their dog?\",\"user_id\":\"u{}\",\"top_k\":100}"' > search.txt
awk '{n[$1]++} END{for (c in n) print "status", c, n[c]}' search.txt
sort -k2 -n search.txt | awk '{t[NR]=$2} END{print "p50", t[int(NR*0.5+0.5)], "p95", t[int(NR*0.95+0.5)]}'
```

Raise `C` until the p95 or the share of non-200 answers gets worse, and note the largest `C`
that stays acceptable. Watch the process memory (`systemctl status aml-serve`), the 503 count
and `degraded search` lines in the journal, and Ollama's CPU. Do the same for Add with
distinct `request_id` and `user_id` values.

## Health and smoke test

```sh
BASE=https://aml.example.com
KEY=<AML_API_KEY>

curl -fsS "$BASE/health"                           # {"status":"ok"}

curl -sS -X POST "$BASE/add" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{
  "request_id": "smoke-1", "user_id": "smoke-user", "session_id": "s1",
  "messages": [
    {"role": "user", "content": "I adopted a dog named Biscuit last week.", "timestamp": 1683554160000},
    {"role": "assistant", "content": "Congratulations! What breed is Biscuit?", "timestamp": 1683554220000}]}'
# {"success":true,"request_id":"smoke-1","user_id":"smoke-user","session_id":"s1"}
# Run it again: the same 200 (idempotent). Change a word under the same request_id: 400.

curl -sS -X POST "$BASE/search" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"query": "What is the name of the dog?", "user_id": "smoke-user", "top_k": 100}'
# {"data":[ ... {"id":"aml-...-00000","content":"Conversation s1 on 2023/05/08 (Mon) 13:56:\nuser: I adopted a dog named Biscuit ...","score":...,"created_at":"2023-05-08T13:56:00Z"}]}

curl -sS -X POST "$BASE/search" -H "Authorization: Bearer $KEY" -d '{"query":"x","user_id":"nobody","top_k":5}'
# {"data":[]}      an unknown user gets an empty list and no store is created
curl -sS -o /dev/null -w '%{http_code}\n' -X POST "$BASE/search" -d '{}'   # 401 without a key
```

The Search calls the paid models. With `--consolidate-on-add` the smoke test also spends
DeepSeek on consolidation: the smoke Add is short of a full batch, so the Search folds it
before it reads. Each item's `score` is its rank score (1 for the first, then 0.5, …). The
smoke user lives in the real data root: **delete it before the job** (see "User deletion"
below), or the platform's own users are mixed with it.

## Logs and monitoring

The service logs to the journal (`journalctl -u aml-serve -f`), one line per Add and Search
(`POST /search 200 83ms`; health checks are not logged). Request bodies and user ids are never
logged; a user appears as the first eight hex digits of its store hash.

| Line | Meaning |
|---|---|
| `degraded search N (user xxxxxxxx): <error>` | Search could not use the models and answered with the raw hybrid ranking (HTTP 200). `N` counts degraded searches since the process started. `context deadline exceeded` means the read ran past `--search-timeout`. |
| `partially degraded search N (user …): judge, layer` | The read answered, but the named stages fell back: `judge` (JEV's final judgement failed; its screen stood in), `scan-budget` or `loop-budget` (the read's JEV budget ran out), `layer-state`, `layer-lexical`, `layer` or `expand` (the consolidation layer was left out, ranked by words, or not judged). |
| `status: adds A, searches S, degraded D, partially degraded P; budget deepseek spent … held … cap …, jev …` | Every 10 minutes: the Adds and Searches answered since the start, the degraded ones, and each budget account in use. |
| `hybrid search failed, searching words (user …)` | The embedder was down during a degraded search. |
| `consolidation failed (user …): <error>` | Consolidation stopped during an Add; the Add still succeeded and the next request of the user retries. |
| `flush failed (user …): <error>` | Folding the tail before a Search failed or ran past 10 minutes; the Search read what was consolidated. |
| `consolidation native edges failed (user …): <error>`, `flush native edges failed …` | Rebuilding the records' native edges (`nativeEdges`) after the jobs failed; reads use the edges built before, and the next request retries. |
| `consolidation left native edges pending (user …)`, `flush left …` | The request's window ended before the edges were rebuilt; the next request rebuilds them. |
| `consolidation native edges left <n> pairs undecided (user …)`, `flush native edges left …` | With `edgeKinds`, JEV did not answer that many event pairs in the rebuild (failed calls, a spent budget or an unavailable provider); the edges are built without them, and the next rebuild asks them again. |
| `consolidation native edges gave up <n> pairs whose calls failed 3 times (user …)`, `flush native edges gave up …` | Those event pairs failed a third call in a rebuild in which other calls succeeded; they are never asked again and give no edge. |
| `consolidation dropped N records (user …): <reason>` | The index leaves out N records whose fold the provider refused or timed out on for at least 10 minutes, whose fold request was too large, or whose reply never parsed. Outages, rate limits (429), 5xx and credential or balance refusals (401/402/403) never drop records. The records stay searchable. |
| `consolidation left to a queued Add (user …)` | Another Add of the user was waiting; it consolidates instead. |
| `consolidation skipped: the Add's window has passed (user …)` | The Add waited 25 minutes for its user; the next request consolidates. |
| `consolidation stopped with N records pending (user …)`, `flush stopped …` | The 50-job cap of one request was reached; the next request continues. |
| `… panicked …` with a stack trace | A bug: an Add answered 500, a Search the raw ranking, a consolidation was skipped. Report it with the trace. |
| `layer embedding failed (user …)` | The index embeddings were not stored; the first Search embeds them. |
| `embedding failed (user …)` | Ollama failed during an Add (the Add got a 503 and will be retried). |
| `add skipped N of M messages without text` | Messages with empty or image-only content were skipped. |

Budget state (read-only, as the service user; `sudo apt install sqlite3` first):

```sh
sudo -u mnemon sqlite3 -readonly /var/lib/mnemon-aml/model-budget.db \
  'select kind, cap, spent, held from accounts;'
```

`held` is what calls in flight reserve, plus calls that failed after sending or were cut by
a stop; it counts against the cap like `spent`. The status line shows the same numbers with
the cap the running process enforces.

## Troubleshooting

| Symptom | Cause and action |
|---|---|
| **503** on `/add` or `/search` | Retryable: the platform retries 5xx with the same `request_id`. Causes: the embedder (Ollama) is down or slow (`embedding failed` in the log), a store is busy (`database is locked`), too many open files, or the server is shutting down. Check `systemctl status ollama`, the journal, and `LimitNOFILE`. |
| **400** | A contract error, not retried by the platform. Add: `request_id`, `user_id` or `session_id` missing or blank, a body that is not a JSON object, or `request_id was already used for a different payload`. Search: a missing or blank `user_id`, or a body that is not a JSON object. The message is in the `error` field. Content shape never causes a 400: empty messages are skipped, and an Add with no text returns 200 without writing; a blank query returns `{"data":[]}` and a missing `top_k` means the View maximum. |
| **401** | The key is wrong or missing. Compare `AML_API_KEY` in `/etc/mnemon-aml/env` with the platform's; restart the service after changing it. |
| **413** | The body is over 30 MiB (the platform splits at 20 messages or 2,000 words, so this means a client bug). |
| `degraded search …` in the log | The planner, JEV, a budget or the embedder failed for that question, or the read ran past `--search-timeout`. The platform still got a 200 with the raw ranking. Read the error after the colon: `model HTTP 401` (a wrong or expired key), `model HTTP 429` (provider rate limit), `model endpoint unreachable` (network or DNS; nothing was sent), `request failed` (network, after sending), `context deadline exceeded` (the timeout), `spending cap reached` (below). |
| `model spending cap reached, including unresolved reservations` | A budget is exhausted. Searches degrade to the raw ranking and consolidation stops (a spent budget never drops records from the index). Raise `budgets` in `/etc/mnemon-aml/memory-config.json` and `sudo systemctl restart aml-serve`; spent amounts are kept and the new cap applies at once. Check `spent` and `held` in the budget file first: reservations held by calls that failed after sending, or were cut by a restart, count against the cap. |
| Empty or poor plans, many degraded searches with a DeepSeek planner | The planner has `thinking` on. Remove it (`deepseek-flash` without thinking). |
| `model credential is unavailable` or a startup error naming a credential environment variable | A `keyEnv` variable is empty or absent in `/etc/mnemon-aml/env`. |
| Service restarts in a loop | `journalctl -u aml-serve`: a relative path, an unreadable config (`memory-config.json` must be readable by group `mnemon`), `aml-serve requires a nonempty API key in $AML_API_KEY`, or the port 8090 already in use (a second process on the same VM). |
| Memory grows | Lower `--max-open-stores` (edit the unit, `daemon-reload`, restart). |
| Caddy returns 502 or 504 | `aml-serve` is down or a proxy timeout is below the platform's 31 minutes. Compare `read_timeout` and `write_timeout` in the Caddyfile with the values in `deploy/aml/Caddyfile`. |

## Data deletion

The job's data must be deleted within 30 days after the job ends. All user data is under the
data root (records, vectors and the consolidation index). The budget file holds metering and
the SHA-256 hashes of the model requests (no text); delete it with the data, after noting the
final spend.

```sh
sudo systemctl stop aml-serve
sudo -u mnemon sqlite3 -readonly /var/lib/mnemon-aml/model-budget.db \
  'select kind, spent, held from accounts;'       # note the final spend
sudo rm -rf /var/lib/mnemon-aml/data               # every user's records, vectors and indexes
sudo rm -f /var/lib/mnemon-aml/model-budget.db*    # metering and request hashes
sudo find /var/lib/mnemon-aml -type f              # prints nothing
```

The journal holds no request bodies and no user ids (only eight-digit hash prefixes), so it needs
no deletion; `sudo journalctl --rotate && sudo journalctl --vacuum-time=1s` clears all archived
journal files if you want to.

Also delete disk snapshots or images of the VM taken during the job, or release the VM and its
disks. Ollama keeps model files, not requests (do not run it with `OLLAMA_DEBUG` during the
job). Record the date and the commands you ran.

**User deletion** (for example the smoke user), with the service stopped:

```sh
sudo systemctl stop aml-serve
sudo rm -rf "/var/lib/mnemon-aml/data/users/$(printf %s 'smoke-user' | sha256sum | cut -c1-32)"
sudo systemctl start aml-serve
```

The directory name is the first 32 hex digits of the SHA-256 of the exact `user_id` string
(a numeric id is the digits as written).

## Upgrade

Build a new binary, stop the service between jobs (never in the middle of one), replace
`/usr/local/bin/mnemon`, and start it. A data root written by one build is read by the next;
if you change the embedding model or its prefixes, start a new data root.
