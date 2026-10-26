# AML adapter runbook

`mnemon memory-agent aml-serve` serves the Agent Memory Leaderboard (AML) `Add` and
`Search` HTTP contract on top of Mnemon's raw records and the "simple" memory strategy.
This runbook runs it as the engine instances of the memory cloud: one account is one
instance, with its own process, API key, admin token, data root, model budget and limits.
The evaluation instance runs alone on a dedicated host; beta instances share a host. It
covers installing a host, opening and operating instances, monitoring and alerts, and
removing the data afterwards.

The kit is in [`deploy/aml/`](../deploy/aml); the submission text is in
[`deploy/aml/README.md`](../deploy/aml/README.md); the admin listener, its token and its
routes are in [`docs/admin-api.md`](admin-api.md). The console that reads the instances is
the memory cloud's other repository, `mnemon-fe`; its design
(`docs/superpowers/specs/2026-10-24-memory-cloud-design.md` there) is the authority for how
instances, hosts and the console fit together.

Every command below runs as root on the host (`sudo -i`) unless it says otherwise.

## Architecture

```
                                            Internet :443
  ┌───────────────────────────────┬───────────────────────────────────┬─────────────────────────────┐
  │ eval host (dedicated)         │ beta host (shared)                │ ops host                    │
  │ Caddy https://eval.<domain>/  │ Caddy https://beta.<domain>/i/<n>/│ Caddy console.<domain>      │
  │   └▶ aml-serve@eval           │   └▶ aml-serve@<n>, one each      │   └▶ mnemon-console         │
  │      data  127.0.0.1:8090     │      data  127.0.0.1:(8100+10s)   │ Prometheus 127.0.0.1:9090   │
  │      admin 10.77.0.2:8091     │      admin 10.77.0.3:(8101+10s)   │   (targets from the console)│
  │ aml-watch@eval (a timer)      │ aml-watch@<n> (a timer each)      │                             │
  └───────────────┬───────────────┴─────────────────┬─────────────────┴──────────────┬──────────────┘
                  └── WireGuard 10.77.0.0/24 ───────┴────────────────────────────────┘
                     only the hub, 10.77.0.1, reaches the admin ports
```

One instance:

```
AML platform ──HTTPS──▶ Caddy :443 ──HTTP──▶ aml-serve@<n>, data 127.0.0.1:<port>
 POST /add, /search      TLS, 31 m timeouts      one process, one data root
 GET  /health            30 MiB body limit        │
                                                  ├─ <data-root>/users/<hash>/   one records store per user_id
                                                  ├─ DeepSeek Flash (HTTPS)      cue planner, consolidation extraction
                                                  └─ JEV 1.13.0 (HTTPS)          judge, consolidation links
console, Prometheus (ops host) ──WireGuard──▶ aml-serve@<n>, admin <overlay address>:<port>, bearer token
```

- **Add** packs the messages into raw records (no model on the write path) and returns once
  they are persisted and searchable by words. The instances consolidate in the background
  (`--consolidate background`): supervised workers fold every full batch (10,000
  characters) of a user's new records into the consolidation index. Layered consolidation
  folds a batch in two steps: DeepSeek extracts the batch alone (up to four batches at a
  time), then JEV links what it extracted to the memory so far, in batch order. A retry
  with the same `request_id` is idempotent.
- **Search** first folds the user's tail (the rest of the conversation so far, short of a
  full batch), as the reference system folds a conversation once it pauses; then it runs
  the planner, the cue searches, the JEV need loop and JEV's final judgement, and returns
  the judged View: the consolidation blocks first, then the records JEV judged, in time
  order (the deployment's `view.order: "time"`; without it, in rank order). Nothing is
  added from the plain search ranking, so a record JEV did not judge is not delivered.
  The View holds the current records of JEV's shortlist (at most 64): those it calls
  needed, then, when the plan named a need, the rest by its needed answer; and at most 8
  more that the blocks link to and JEV calls needed. All of them stay within `top_k`
  records and 240,000 characters, and the View is usually far shorter than `top_k`. The
  selection, and the cut at `top_k` when blocks and records pass it, go by rank. Each
  item's `score` is `1/(position+1)`. It never returns generated answers. If a model or a
  budget fails, or the read runs past `--search-timeout`, it falls back to the raw BM25
  ranking with HTTP 200, cut at `top_k` by rank and delivered in the same order, and logs
  the fact.
- **Retrieval is BM25 only.** The cue searches over a user's records, the consolidation
  layer's choice of the topics and values nearest to a question, and the linking of new
  topics and facts to stored ones all rank by shared words (BM25). Nothing is embedded and
  no embedding service runs. The cue searches rank by SQLite FTS5's own `bm25()` over the
  store's background-built index (until it covers the records, by the frozen BM25 rules);
  the raw ranking of a degraded Search, and the rest, keep the frozen rules.
- Each `user_id` has its own store: `<data-root>/users/<hex(sha256(user_id))[:32]>`. One
  user can never reach another's records. Logs show only the first 8 hex digits.

### Contract details

- Add reads a message `timestamp` as Unix milliseconds, as Unix seconds when the number is
  below 10^11, or as an RFC 3339 string (`2023-05-08T13:56:00Z`); numbers may come as strings,
  and anything else is no timestamp. Multimodal `content` keeps its plain strings and the
  `text` of parts whose `type` ends in `text` (`text`, `input_text`, `output_text`).
- Search needs only a `user_id`. A missing, zero or negative `top_k` means the View maximum
  (100 with the shipped configuration); a blank `query` answers `{"data":[]}`.
- Each Search item's `score` is `1/(position+1)` by its position in `data` (1, 0.5, 0.333…),
  layer blocks included, so a client that sorts by score keeps the delivered order.
- Every response carries `X-Request-Id`: the client's own when it sent a usable one (1 to 128
  printable ASCII characters), else a new one. The instance's log lines for the request carry
  it as `request_id`.
- A missing or wrong key gets `401 {"error":"missing or invalid API key"}`. A suspended
  shared instance answers `403 {"error":"account suspended"}` from Caddy, in the same shape.

## Instances

An instance has a name: `^[a-z][a-z0-9-]{1,30}$`, for example `eval` or `beta-alice`. The
name is the account's identifier everywhere: in paths, units, the console and this runbook.
The names `data`, `env`, `host`, `host-env`, `users`, `watch`, `instances`, `aml`, `mnemon`,
`caddy` and `test-instance` are reserved: they collide with the kit's paths or read as common
words, and every subcommand refuses them.

| Resource | Path / value |
| --- | --- |
| Engine env (root:root 0600) | `/etc/mnemon-aml/<n>/env`: the record (`AML_LAYOUT`, `AML_SLOT`, `AML_STATUS`), `AML_LISTEN`, `AML_ADMIN_LISTEN`, the flag values `AML_MAX_ADDS`, `AML_MAX_SEARCHES`, `AML_MAX_OPEN_STORES`, `AML_WORKERS`, `GOMEMLIMIT` (80% of `MemoryMax=`), and the secrets `AML_API_KEY`, `MNEMON_ADMIN_TOKEN` and the provider keys the configuration names |
| Engine config (root:mnemon 0640) | `/etc/mnemon-aml/<n>/memory-config.json`: the shipped BM25-only configuration with this instance's caps and budget file |
| Watcher env (root:root 0600) | `/etc/mnemon-aml/<n>/watch.env`: `AML_ALERT_WEBHOOK`, `AML_ALERT_KIND`, `AML_PUBLIC_URL`, `AML_ADMIN_LISTEN` (the engine's value), `MNEMON_ADMIN_TOKEN`; never a provider key or the API key |
| State (mnemon 0700) | `/var/lib/mnemon-aml/<n>/` (`StateDirectory=mnemon-aml/%i`): `data/`, the data root, `model-budget.db`, the budget file, and, once the console has set them, `provider-keys.json`, the account's own provider keys ("An account's own provider keys"), and `settings.json`, the budget caps set at run time ("Budget caps at runtime"), each mnemon 0600 |
| Watcher state (mnemon 0700) | `/var/lib/mnemon-aml-watch/<n>/state.json` (`StateDirectory=mnemon-aml-watch/%i`) |
| Inspect env (root:root 0600), while inspect is on | `/etc/mnemon-aml/<n>/inspect.env`: `AML_INSPECT_LISTEN`, `AML_INSPECT_DEBUG_CAPS`, `GOMEMLIMIT`, and the secrets `MNEMON_INSPECT_TOKEN` and the provider keys ("Inspect (检索)") |
| Inspect state (mnemon 0700), while inspect is on | `/var/lib/mnemon-aml-inspect/<n>/debug-budget.db` (`StateDirectory=mnemon-aml-inspect/%i`), and `settings.json`, its debug caps set at run time |
| Units | `aml-serve@<n>.service`, `aml-watch@<n>.service` and `aml-watch@<n>.timer`; `aml-inspect@<n>.service` while inspect is on |
| Limits | `/etc/systemd/system/aml-serve@<n>.service.d/limits.conf`: `MemoryMax=` and `CPUQuota=` only. The flag values live in the engine env, so the template's `ExecStart` is the same for every instance. |
| Route | dedicated: the host's Caddyfile. Shared: `/etc/caddy/mnemon-instances/<n>.caddy` |

The engine env is the instance's record: `mnemon-instance` keeps the layout, the slot and
the status (`active` or `suspended`) there, and nothing else keeps state.

**Ports follow from a slot.** `mnemon-instance new` takes the lowest slot no instance's env
file uses.

| Layout | Slot | Data listener | Admin listener | Inspect listener (while on) |
| --- | --- | --- | --- | --- |
| dedicated | 0 | `127.0.0.1:8090` | `<overlay address>:8091` | `<overlay address>:8092` |
| shared | 0 to 49 | `127.0.0.1:(8100+10s)` | `<overlay address>:(8101+10s)`, so 8101 to 8591 | `<overlay address>:(8102+10s)`, so 8102 to 8592 |

A host is either dedicated, with one instance, or shared; the script refuses to mix them.

**Public base URLs.** Dedicated: `https://eval.<domain>`, the domain's root, as the
evaluation was verified. Shared: `https://beta.<domain>/i/<n>`; Caddy's `handle_path /i/<n>/*`
strips the prefix, so the engine sees `/add`, `/search` and `/health` unchanged, and one
certificate covers every beta instance.

**Host secrets.** The provider keys and the alert bot are per host, in
`/etc/mnemon-aml/host.env` (root:root, 0600): `DEEPSEEK_API_KEY` and `TYPESAFE_API_KEY`,
`OPENAI_API_KEY` only with an OpenAI model, and `AML_ALERT_WEBHOOK` and `AML_ALERT_KIND`.
`mnemon-instance new` copies each line into exactly the file that needs it: the provider
keys the configuration names into `env`, the webhook and its kind into `watch.env`. Flags
never carry a secret. One alert bot serves every instance on a host; the watcher's
`--instance` names the instance in every message.

**Defaults, sized to the real hosts.** `new`'s flags override any of them.

| | Dedicated (evaluation) | Shared (beta) | `new` flag |
| --- | --- | --- | --- |
| DeepSeek cap (CNY) | 600 | 30 | `--deepseek-cap` |
| JEV cap (USD) | 60 | 3 | `--jev-cap` |
| `MemoryMax=` | `40G` | `1G` | `--memory-max` |
| `GOMEMLIMIT` (engine env) | `32768MiB` | `819MiB` | follows `--memory-max`: 80% of it, rounded down to a whole MiB |
| `CPUQuota=` | none | `100%` | `--cpu-quota` |
| `--max-concurrent-adds` (`AML_MAX_ADDS`) | 16 | 4 | `--max-concurrent-adds` |
| `--max-concurrent-searches` (`AML_MAX_SEARCHES`) | 16 | 4 | `--max-concurrent-searches` |
| `--max-open-stores` (`AML_MAX_OPEN_STORES`) | 16 | 2 | `--max-open-stores` |
| `--consolidation-workers` (`AML_WORKERS`) | 4 | 1 | `--consolidation-workers` |

The dedicated numbers are provisional: the load test on the evaluation host
(`scripts/aml/load.sh`, see "Capacity declaration") replaces them with measured values. The
evaluation host has 48 GB, so
40G leaves room for Caddy, sshd and the watcher; the 8 GB beta host holds about five beta
instances at the defaults. `GOMEMLIMIT` makes the Go runtime collect harder as the instance
nears its `MemoryMax=`, rather than be killed there.

## Hosts

| Role | Machine | Overlay address | Runs |
| --- | --- | --- | --- |
| ops | Ubuntu 24.04, 6 vCPU / 12 GB | `10.77.0.1`, the hub | the console, Prometheus (the console's kit, in `mnemon-fe`) |
| eval | Ubuntu 24.04, 12 vCPU / 48 GB / 387 GB | `10.77.0.2` | one instance, `aml-serve@eval`, dedicated |
| beta | Ubuntu 26.04, 4 vCPU / 8 GB / 145 GB | `10.77.0.3` | about five beta instances at the shared defaults |

The hosts' public addresses are in the memory cloud design. The hosts share no private
network: the WireGuard overlay below is it. A dedicated host needs at least 8 vCPU, 32 GB and
200 GB (16 open stores of up to about 744 MB each, measured on 2026-10-24); the eval host is
larger.

Bring-up drills come before go-live, as the only place where the templates, the drop-ins,
`handle_path`, WireGuard and the firewall meet a real systemd and Caddy:

- **On the beta host:** two scratch instances on the shared layout. Run `mnemon-preflight` on
  both, then `suspend`, `resume` and `delete` them.
- **On the eval host:** one scratch instance on the dedicated layout, used for the VM load
  test and then deleted. After that, `mnemon-instance new eval --layout dedicated …`.

Load tests and the soak use a scratch instance, never `eval`. `eval` is created clean, with a
fresh key, at go-live.

## Prerequisites

- Ubuntu 24.04 or 26.04, amd64, with systemd.
- `apt install curl jq wireguard-tools ufw`, and Caddy from its official apt repository
  (caddyserver.com/docs/install). The scripts need nothing else beyond coreutils.
- A DNS name for the host: `eval.<domain>` for the dedicated host, `beta.<domain>` for the
  shared one.
- Outbound HTTPS to `api.deepseek.com` and `api.typesafe.ai`. Check both from the host:
  `curl -sS -o /dev/null -w '%{http_code}\n' https://api.deepseek.com` (any HTTP status shows
  the host is reachable).
- On the build machine: Go and `make`.

## Private network

The admin listeners are reachable over a WireGuard overlay, `10.77.0.0/24`. The ops host is
its hub at `10.77.0.1`; eval is `10.77.0.2` and beta `10.77.0.3`; each listens on UDP 51820.
An instance host's spoke config is [`deploy/aml/wg0.conf.example`](../deploy/aml/wg0.conf.example):
its only peer is the hub, with `AllowedIPs = 10.77.0.1/32`, so nothing else is reachable
through it, and instance hosts do not forward. The hub's config is in the console's kit.
Neither script installs or configures WireGuard; these are one-time host steps.

```sh
umask 077
wg genkey > /etc/wireguard/wg0.key
wg pubkey < /etc/wireguard/wg0.key           # this host's public key: add it as a [Peer] on the hub,
                                             # with AllowedIPs = 10.77.0.2/32 (eval) or 10.77.0.3/32 (beta)
install -m 0600 /tmp/wg0.conf.example /etc/wireguard/wg0.conf
nano /etc/wireguard/wg0.conf                 # Address, the hub's PublicKey and Endpoint
systemctl enable --now wg-quick@wg0
wg show wg0                                  # the hub as a peer, with a latest handshake
ping -c 3 10.77.0.1
```

The overlay address is the `--admin-ip` of every instance on the host: the admin listener
binds it, which aml-serve accepts because it is private (10/8) and the instance has an admin
token ([`docs/admin-api.md`](admin-api.md)). `aml-serve@.service` starts after
`wg-quick@wg0.service` and wants it, but does not require it: the data plane never depends on
the overlay. An instance that starts while `wg0` is down cannot bind its admin listener. It
logs that once, serves its public listener all the same, and binds the admin listener again
every 5 seconds until the address is there; meanwhile the console cannot read it and the
watcher's `ready` alert fires (troubleshooting, below).

## Firewall

The eval and beta hosts run ufw: incoming traffic is denied but for ssh, HTTP and HTTPS, the
WireGuard port from the ops host, and the admin ports from the hub over `wg0`. ufw filters
traffic arriving on `wg0` like any other interface, so without the last rule the console can
never reach the admin ports. Add the ssh rule before you enable ufw.

```sh
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 443/udp                            # Caddy's HTTP/3
ufw allow from <ops address> to any port 51820 proto udp
ufw allow in on wg0 from 10.77.0.1 proto tcp to any port 8091:8600
ufw enable                                   # only after the 22/tcp rule is in place
ufw status verbose
```

`ufw status verbose` then shows `Status: active`, `Default: deny (incoming)`, and the rules,
among them `51820/udp ALLOW IN <ops address>` and `8091:8600/tcp on wg0 ALLOW IN 10.77.0.1`.
`mnemon-preflight` checks each of these. A provider firewall in front of the hosts, if any,
opens the same ports; the admin ports (8091 to 8600) never listen on a public address and are
never opened to the internet. The ops host's firewall stays as it is: Docker manages its
nftables, and live services depend on it.

## Install once per host

1. **Build the binary** on the build machine, and copy it with the kit:

   ```sh
   GOTOOLCHAIN=go1.26.10 make build-linux-amd64 VERSION=aml-2   # writes bin/mnemon-linux-amd64 (static, CGO off)
   file bin/mnemon-linux-amd64                    # ELF 64-bit LSB executable, x86-64, statically linked
   scp bin/mnemon-linux-amd64 deploy/aml/* <host>:/tmp/
   ```

   `VERSION` becomes the product version that `/admin/status` reports as `version`.

2. **Install the binary, the scripts, the shipped configuration and the unit templates.**

   ```sh
   useradd --system --no-create-home --shell /usr/sbin/nologin mnemon
   install -m 0755 -o root -g root /tmp/mnemon-linux-amd64 /usr/local/bin/mnemon
   install -m 0755 -o root -g root /tmp/instance.sh /usr/local/sbin/mnemon-instance
   install -m 0755 -o root -g root /tmp/preflight.sh /usr/local/sbin/mnemon-preflight
   install -D -m 0644 -o root -g root /tmp/memory-config.industrial.json /usr/local/share/mnemon-aml/memory-config.industrial.json
   install -m 0644 -o root -g root /tmp/aml-serve@.service /tmp/aml-watch@.service /tmp/aml-watch@.timer /tmp/aml-inspect@.service /etc/systemd/system/
   systemctl daemon-reload
   systemd-analyze verify /etc/systemd/system/aml-serve@.service /etc/systemd/system/aml-watch@.service /etc/systemd/system/aml-watch@.timer /etc/systemd/system/aml-inspect@.service
   ```

   `systemd-analyze verify` loads a template as the instance `test_instance` and prints
   nothing when the units are sound. `mnemon-instance new` writes each instance's files from
   the shipped configuration; neither script installs the binary or the templates.

3. **Write the host's secrets.** Use an editor, so that no value lands in the shell's history
   or on a command line.

   ```sh
   install -d -m 0750 -o root -g mnemon /etc/mnemon-aml
   install -m 0600 -o root -g root /dev/null /etc/mnemon-aml/host.env
   nano /etc/mnemon-aml/host.env
   ```

   ```sh
   # /etc/mnemon-aml/host.env (root:root, mode 0600): one NAME=value per line, no quotes
   DEEPSEEK_API_KEY=<DeepSeek key>
   TYPESAFE_API_KEY=<JEV key>
   #OPENAI_API_KEY=<only with an openai model>
   AML_ALERT_WEBHOOK=https://api.telegram.org/bot<token>/sendMessage?chat_id=<chat id>
   AML_ALERT_KIND=telegram
   ```

   Telegram is the recommended alert bot ("Alerts" has its setup); a Feishu or WeCom bot's
   webhook URL with `AML_ALERT_KIND=feishu` or `wecom` works too. `mnemon-instance new`
   refuses to run if the file is missing, is not root's with mode 0600, or lacks a name the
   shipped configuration or the watcher needs, and refuses a telegram webhook that is not the
   bot's `sendMessage` URL with its `chat_id`; the refusal names the variable, never its value.
   The configuration's `keyEnv` fields name the provider keys.

4. **Install Caddy** from its official apt repository, then the Caddyfile of the host's
   layout, with the host's domain:

   ```sh
   install -m 0644 /tmp/Caddyfile.dedicated /etc/caddy/Caddyfile   # a shared host: /tmp/Caddyfile.shared
   nano /etc/caddy/Caddyfile                      # replace eval.example.com (beta.example.com) with the domain
   install -d -m 0755 /etc/caddy/mnemon-instances # a shared host only
   caddy validate --config /etc/caddy/Caddyfile
   systemctl reload caddy
   ```

   Caddy obtains the certificate on its own once DNS points at the host and 80/443 are open.
   On a shared host Caddy accepts the import of `/etc/caddy/mnemon-instances/*.caddy` while
   no instance exists yet (it warns that nothing matches). For a certificate issued
   elsewhere, use the `tls <cert> <key>` line shown in `Caddyfile.dedicated`. Do not enable
   `log_credentials`: the access log must not carry the API key.

5. **Join the overlay and turn on the firewall** ("Private network" and "Firewall", above).

## Open an instance

```sh
mnemon-instance new eval --layout dedicated --public-url https://eval.<domain> --admin-ip 10.77.0.2
mnemon-instance new beta-alice --layout shared --public-url https://beta.<domain>/i/beta-alice --admin-ip 10.77.0.3
```

| Flag | Meaning |
| --- | --- |
| `--layout dedicated\|shared` | required; a dedicated host holds one instance |
| `--public-url URL` | required: the https base URL clients use, the domain's root (dedicated) or `/i/<n>` of the host's domain (shared) |
| `--admin-ip IP` | required: the host's overlay address, a private IPv4 address; aml-serve checks it again at startup |
| `--host-name NAME` | the host in the registration JSON; default `uname -n` |
| `--deepseek-cap`, `--jev-cap`, `--memory-max`, `--cpu-quota`, `--max-concurrent-adds`, `--max-concurrent-searches`, `--max-open-stores`, `--consolidation-workers` | override the defaults ("Instances", above) |

`new`:

1. checks the name, the flags, `host.env`, the shipped configuration, the installed binary,
   templates and user, and, on a shared host, that the Caddyfile imports the snippets;
2. refuses a name of which anything is left (any of its files or directories), and takes the
   lowest free slot;
3. makes the API key and the admin token: `mnm_` and 32 random bytes from `/dev/urandom` in
   base64url without padding (47 characters);
4. writes the engine env (with `GOMEMLIMIT`, 80% of `MemoryMax=` rounded down to a whole
   MiB), the watcher env, the configuration (the shipped one with `budgets` and `budgetFile`
   set for the instance), the state directories, the limits drop-in and, on a shared host,
   the snippet, each to a temporary file renamed into place. If anything fails before it is
   done, it removes what it wrote;
5. runs `systemctl daemon-reload`, `systemctl enable --now aml-serve@<n>.service`, waits up to
   120 s for `/ready`, enables `aml-watch@<n>.timer`, reloads Caddy (shared; if Caddy refuses
   the new route, the snippet is removed again, so that the host's Caddyfile stays valid, and
   `new` says to fix the Caddyfile and run `mnemon-instance resume <n>`, which writes the route
   again), and runs `mnemon-preflight <n>`;
6. prints, once, the base URL, the API key, the admin URL `http://<admin-ip>:<port>`, the admin
   token, and the registration JSON for the console:

   ```json
   {"id":"beta-alice","kind":"shared",
    "host":{"name":"beta-1","vpcIp":"10.77.0.3"},
    "publicUrl":"https://beta.example.com/i/beta-alice",
    "adminUrl":"http://10.77.0.3:8101",
    "adminToken":"mnm_…","apiKeyFingerprint":"0123456789ab"}
   ```

   The JSON holds no API key, only its fingerprint: the first 12 hex digits of the key's
   SHA-256. `vpcIp` is the overlay address.

The keys are printed even when the instance did not start or its preflight failed, so that
none is lost; `new` then exits 1 and says what to look at. The instance's files stay;
`mnemon-instance delete <n>` removes them.

Then:

- **Register it in the console:** paste the registration JSON into the console's register
  form, which adds the account fields, the region and the spec. The admin token lives in the
  instance's env files and the console's registry only.
- **Hand over** the base URL and the API key to the account holder over a private channel.
- **Smoke-test it** ("Health and smoke test", below), delete the smoke user, and run
  `mnemon-preflight <n>` again: it refuses an instance whose data root still holds the smoke
  user's store.

## Operate an instance

`mnemon-instance help` lists every subcommand. Each refuses to overwrite an existing instance,
writes its files atomically, prints a secret only in the one-time output of `new`,
`rotate-key` and `inspect enable`, and logs one line per action to the journal (`journalctl -t mnemon-instance`),
naming the action and the instance, never a secret or a raw user id. One `mnemon-instance`
changes a host at a time: it holds `/etc/mnemon-aml/.mnemon-instance.lock`, a directory, while
it runs; if one is left behind by a killed run, remove it. The console's maintenance panel
prints these commands for an instance.

### `list`

```sh
mnemon-instance list
```

```
NAME                             LAYOUT     SLOT  DATA             ADMIN                  STATUS     INSPECT  KEYS                  CAPS           PUBLIC URL
beta-alice                       shared        0  127.0.0.1:8100   10.77.0.3:8101         active     off      account:deepseek      800*/3         https://beta.example.com/i/beta-alice
```

It reads the env files and prints no secret. The status is the record's, not systemd's:
`systemctl status aml-serve@<n>` shows the process. `INSPECT` says whether the instance's
inspect service is on (its `inspect.env` exists; "Inspect (检索)"). `KEYS` says where the
instance's provider keys come from: `host`, or `account:<kinds>` once the console has set the
account's own ("An account's own provider keys"), read from the key file's key names alone, never
its keys; `invalid` for a key file that is not the format. `CAPS` is the DeepSeek and JEV caps,
`DEEPSEEK/JEV`: the configuration's, or, marked `*`, the settings file's ("Budget caps at
runtime"), `∞*` for an unlimited one; `invalid` for a settings file that is not the format.

### `suspend` and `resume`

```sh
mnemon-instance suspend beta-alice
mnemon-instance resume beta-alice
```

`suspend` on a shared host swaps the snippet for one that answers
`403 {"error":"account suspended"}`, checks it with `caddy validate` and reloads Caddy; then,
on either layout, it stops and disables `aml-watch@<n>.timer`, stops `aml-watch@<n>.service`,
stops and disables `aml-serve@<n>.service`, and records `AML_STATUS=suspended`. Disabled units
stay down through a reboot. If Caddy refuses the new snippet, the old one is put back (or the
new one removed, when there was none, so that the host's Caddyfile stays valid) and `suspend`
exits 1, having stopped nothing. A dedicated host's route stays and answers 502 while the
instance is suspended. `suspend` may be run again; it does the same again.

`resume` reverses it, in an order that never leaves it half-done: it enables and starts
`aml-serve@<n>` and waits up to 120 s for `/ready`; only then does it restore the route
(shared), record `AML_STATUS=active` and enable the watcher's timer. If the instance is not
ready within 120 s, or Caddy refuses the route, `resume` stops and disables `aml-serve@<n>`
again and exits 1: the instance stays fully suspended (its route answers 403, its watcher is
off). `resume` of an instance that is not suspended never stops it. Nor does it stop one that
is back (ready, and on a shared host its route restored): if recording the status or enabling the
watcher's timer then fails (or it is interrupted), the instance serves, `resume` exits 1 and says
what may be left (its status, or its watcher's timer), and running `mnemon-instance resume <n>`
again finishes them. The watcher's next run reports the restart (`restart`, resolved 10 minutes
later).

### `rotate-key`

```sh
mnemon-instance rotate-key beta-alice
```

It rotates the API key only: it writes a new key to the engine env (atomically, 0600),
restarts `aml-serve@<n>`, waits for `/ready`, and prints the new key once with its new
fingerprint. Update the fingerprint in the console and hand the key over. The shutdown drains
the requests in flight, and Caddy holds new ones for up to 30 s while it dials again, so a
quick restart shows clients a pause, not errors; a request held longer gets 502, which the
platform retries. A client still sending the old key gets 401. A suspended instance gets the key
without a restart; it applies when the instance resumes.

**Rotating the admin token** is a manual step: write a new token, for example
`printf 'mnm_%s\n' "$(head -c 32 /dev/urandom | base64 | tr -- '+/' '-_' | tr -d '=\n')"` into
`MNEMON_ADMIN_TOKEN` of both `/etc/mnemon-aml/<n>/env` and `/etc/mnemon-aml/<n>/watch.env`
with an editor, `systemctl restart aml-serve@<n>`, then set the token in the console
(`PATCH /api/instances/<id>`, or its edit form).

**Moving an account to its own provider keys** is no longer an edit of its env: the console
sets them at run time, and they win over the env's ("An account's own provider keys", below). The
env's `DEEPSEEK_API_KEY=` and `TYPESAFE_API_KEY=` stay the host's keys, which a kind without an
account key uses; `host.env` is only where `new` copies them from.

**Changing caps or limits** of an existing instance: the caps from the console, at run time
("Budget caps at runtime"), or `budgets` in `/etc/mnemon-aml/<n>/memory-config.json` (restart; the
spent amounts are kept; a cap of the settings file wins over it), `MemoryMax=` or
`CPUQuota=` in `aml-serve@<n>.service.d/limits.conf` (`systemctl daemon-reload`, then restart;
with a new `MemoryMax=`, set `GOMEMLIMIT` in the engine env to 80% of it, in whole MiB, as `new`
does: `2G` gives `1638MiB`), or the flag values in the engine env (restart).

### `delete`

```sh
mnemon-instance delete beta-alice
```

It asks for the instance's name and refuses unless it is typed exactly. Then it stops and
disables the units, removes the snippet (and reloads Caddy), `/etc/mnemon-aml/<n>/`,
`/var/lib/mnemon-aml/<n>/`, `/var/lib/mnemon-aml-watch/<n>/` and the limits drop-in, prints
each path it removed, and runs `systemctl daemon-reload`. It also removes what is left of an
instance whose `new` did not finish. Note the final spend first (`mnemon-preflight <n>`
prints the budgets), then mark the instance deleted in the console. Removing an instance
removes its users' data for good ("Data deletion", below).

### `delete-user`

```sh
mnemon-instance delete-user beta-alice
User id:                                     # type the user id here, then Enter
```

The per-user data deletion as one command. Type the user id at its prompt, never in a command:
an argument, a `printf … |` or a here-string would keep it in the shell's history. It reads
the one line (one trailing newline is not part of the id; anything else is), never its
arguments, and computes the store: `users/` and the first 32 hex digits of the SHA-256 of the
exact id (a numeric id is the digits as written). It refuses if there is no such store, or if
the SHA-256 tool gives no digest. Then it stops `aml-serve@<n>`, removes the store's
directory, starts the instance and waits for `/ready`. It logs and prints only the store's
first 8 hex digits. A suspended instance stays stopped. (The smoke test pipes in `smoke-user`,
the runbook's own test id, not a person's.)

## An account's own provider keys

An account may bring its own DeepSeek and JEV keys (ruling R98). For each kind, the account's key
wins when it has one; otherwise the instance sends the host's, `DEEPSEEK_API_KEY` and
`TYPESAFE_API_KEY` of its env (which `new` copied from `host.env`). The evaluation instance is
not special: its keys may be set too.

The account's keys live in one file per instance, the **key file**,
`/var/lib/mnemon-aml/<n>/provider-keys.json`: beside the data root `…/data`, never in it, so that a
snapshot of the data root never holds a key (a snapshot of the host's disk does, as it holds the
env files). It is the service user's (`mnemon`), mode 0600, and aml-serve writes it atomically;
`aml-serve@.service` and `aml-inspect@.service` pass it as `--provider-keys-file`. aml-serve
reads it at its start and writes it for the console; aml-inspect only reads it, at its start and
before each full search. Its format and rules are in [Admin API](admin-api.md) ("The key file").

**From the console.** 设置 → 模型服务 API key shows each kind's source (「主机的 key」 or
「账户自带的 key」) and fingerprint, and takes a new key in a write-only field. Saving sends the
filled fields to `PUT /admin/v1/provider-keys` through the console. The engine checks every key
with its provider first, with one free `GET …/models` (or, where a provider has no such route, a
call of `max_tokens` 1), 10 s at most each, and applies them only when every one passed: it writes
the key file and swaps the keys in. Model calls that start after the reply use them; calls in
flight finish with theirs. No restart. 「恢复使用主机的 key」 reverts a kind
(`DELETE /admin/v1/provider-keys?kind=`). A refusal changes nothing:

| Answer | Meaning |
| --- | --- |
| `400 key_rejected`, `status` 401 or 403 | the provider refused the key: wrong, expired or revoked |
| `400 key_rejected`, `status` 402 | the key's account has no balance (DeepSeek: `Insufficient Balance`) |
| `502 key_check_unavailable` | the provider said nothing about the key (a 5xx, a 429, no answer within 10 s, a broken connection): try again later |
| `409 unknown_kind` | the instance's configuration has no model of that kind |
| `409 no_key_file` | aml-serve runs without `--provider-keys-file` (not the kit's unit) |

The journal of `aml-serve@<n>` has one line for each change, `provider keys replaced: deepseek
<fingerprint>` or `provider keys reverted to the host's: <kinds>`, and a warning for each key a
provider refused. The fingerprint is the first 12 hex digits of the key's SHA-256; the key itself
is never in a log, an error, a metric or a reply. `/admin/status` shows each kind's source and
fingerprint (`providerKeys`), `mnemon-instance list` shows `KEYS`, and `mnemon-preflight` checks
the key file's mode and owner when it exists.

**The routes, by hand** (on the host, without the console; never put a key on a command line):

```sh
n=beta-alice
admin_write() { # admin_write METHOD PATH: METHOD PATH on $n's admin listener, with its token; the body on standard input
  curl -sS -X "$1" -H 'Content-Type: application/json' --data-binary @- \
    --config <(grep -E '^MNEMON_ADMIN_TOKEN=' "/etc/mnemon-aml/$n/env" | sed 's/^MNEMON_ADMIN_TOKEN=/header = "Authorization: Bearer /; s/$/"/') \
    "http://$(grep -E '^AML_ADMIN_LISTEN=' "/etc/mnemon-aml/$n/env" | cut -d= -f2-)$2"
}
IFS= read -rs -p 'DeepSeek key: ' key && echo && printf '{"deepseek":"%s"}' "$key" | admin_write PUT /admin/v1/provider-keys; key=
admin_write DELETE '/admin/v1/provider-keys?kind=deepseek' </dev/null
```

**The manual way** (no admin listener, or the engine down): write the file as the service user
with mode 0600, then restart. aml-serve checks no key at its start, so check the keys first.

```sh
n=beta-alice
install -o mnemon -g mnemon -m 0600 /dev/null /var/lib/mnemon-aml/$n/provider-keys.json.new
# with an editor, never on a command line:
#   {"version":1,"keys":{"deepseek":"<key>","jev":"<key>"},"updatedAt":"2026-10-25T08:00:00Z"}
mv /var/lib/mnemon-aml/$n/provider-keys.json.new /var/lib/mnemon-aml/$n/provider-keys.json
systemctl restart aml-serve@$n
mnemon-preflight $n
```

A start refuses a key file that its group or others may read, that another user owns, that is
malformed, or that names a kind the configuration has no model of, with a line in
`journalctl -u aml-serve@<n>` that names the file and never its content; fix it (or remove it)
and start again.

**What reverts the keys to the host's:** the console's 「恢复使用主机的 key」 (or the `DELETE`
route), at once; removing the key file, or a kind from it, then restarting; `mnemon-instance
delete <n>`, which removes the file with the instance's state; and a restore of
`/var/lib/mnemon-aml/<n>/` from a backup older than the keys. A restart, an upgrade and
`rotate-key` keep the account's keys, and a new key in `host.env` or the env changes nothing for
a kind that has an account key.

## Budget caps at runtime

An instance's DeepSeek (CNY) and JEV (USD) caps can change while it runs (rulings R99 and R100),
from the console's 设置 → 预算上限. A cap set there replaces the configuration's (`budgets` of
`/etc/mnemon-aml/<n>/memory-config.json`, which `new` writes: 600/60 on the evaluation host,
30/3 for a beta instance by default) and is kept in the instance's **settings file**,
`/var/lib/mnemon-aml/<n>/settings.json`, beside the key file, the service user's, mode 0600,
written atomically by aml-serve (`aml-serve@.service` passes `--settings-file`). Its format and
rules are in [Admin API](admin-api.md) ("The settings file").

- **Set.** The console sends `PUT /admin/v1/budgets`, a number above 0 and at most 100,000 per
  kind, or `null` for **unlimited**. The cap applies at once to the reservations made from then
  on; one already held keeps its amount. No restart.
- **Below what is spent.** Allowed, after a confirm in the console: the kind's paid calls then
  stop, as at a cap reached (Searches degrade, a spent DeepSeek budget stops consolidation), and
  the reply says `"over": true`.
- **Unlimited.** No reservation of the kind is refused for a cap; its spend is still metered
  (`spent` and `held`, in the budget file), and is bounded only by the provider account's own
  balance. `/admin/status` shows `"cap": null, "unlimited": true`, the console 「无上限」, `list`
  `∞*`; `/ready`, the watcher's `budget-high` and `budget-held` and `mnemon-preflight` measure no
  share of it.
- **Revert.** 「恢复默认上限」 sends `DELETE /admin/v1/budgets?kind=`: back to the
  configuration's cap; the settings file goes once it holds no cap.
- **What shows it.** `/admin/status`'s `budgets` (each with its `source`: `config` or `settings`),
  `mnemon-instance list`'s `CAPS` (`800*/60`: `*` for a cap of the settings file), and the
  journal of `aml-serve@<n>`: `budget caps set: deepseek 600→800 CNY` and `budget caps reverted
  to the configuration's: …`. `mnemon-preflight` checks the settings file's mode and owner when
  it exists.
- **By hand**, with `admin_write` of "An account's own provider keys":
  `printf '{"deepseek":800}' | admin_write PUT /admin/v1/budgets`, and
  `admin_write DELETE '/admin/v1/budgets?kind=deepseek' </dev/null`. Or, with the engine
  stopped, write the file as the service user, mode 0600 (as the key file), and start it.
- **The inspect service's debug caps** change the same way, from the 检索 tab: `PUT` and
  `DELETE /v1/debug-caps` on the inspect listener ([Inspect API](inspect-api.md)), kept in its
  own `/var/lib/mnemon-aml-inspect/<n>/settings.json` (`aml-inspect@.service` passes
  `--settings-file`), over `AML_INSPECT_DEBUG_CAPS`; `null` is unlimited there too.

The concurrency limits, the memory and CPU limits, `--max-open-stores` and the consolidation
workers are process flags and unit limits: changing one needs a restart, and stays a
`mnemon-instance` operation ("Changing caps or limits").

## Preflight

```sh
mnemon-preflight beta-alice
```

```
mnemon-preflight beta-alice
  version aml-2
  lagBound 160000 characters
  budget deepseek: spent 1.5 + held 0.25 of cap 30 (5.8%)
  budget jev: spent 0.1 + held 0 of cap 3 (3.3%)
mnemon-preflight beta-alice: all 15 checks passed
```

Run it before handing an instance over, before a job, and after any change. It runs as root,
reads the env files with `grep` (never sourcing them), and checks:

- `env` and `watch.env`: owned by root, mode 0600;
- `env` holds `AML_API_KEY`, the provider keys the instance's configuration names,
  `MNEMON_ADMIN_TOKEN`, `AML_LISTEN`, `AML_ADMIN_LISTEN`, `AML_MAX_ADDS`, `AML_MAX_SEARCHES`,
  `AML_MAX_OPEN_STORES` and `AML_WORKERS`; `watch.env` holds `AML_ALERT_WEBHOOK` (https; for
  telegram, the bot's `sendMessage` URL with its `chat_id`), `AML_ALERT_KIND` (`telegram`,
  `feishu` or `wecom`), `AML_PUBLIC_URL` (https), and the engine's `AML_ADMIN_LISTEN` and
  `MNEMON_ADMIN_TOKEN`;
- `$AML_PUBLIC_URL/health` answers 200 through Caddy;
- `/ready` on the admin listener answers 200 with the token;
- `/admin/status` shows a `lagBound` above 0 (background consolidation), and every budget's
  spent and held below 80% of its cap; it prints those numbers and the version;
- the data root holds no store of `smoke-user`;
- only once the console has set the account's own provider keys or budget caps: the key file,
  `/var/lib/mnemon-aml/<n>/provider-keys.json`, and the settings file,
  `/var/lib/mnemon-aml/<n>/settings.json`, are the service user's (`mnemon`), mode 0600 (one more
  check each), and so is the inspect service's settings file while it is on; an unlimited cap is
  printed as `no cap (unlimited)`, with no share measured;
- only while the inspect service is on: `inspect.env` is root's with mode 0600 and holds
  `AML_INSPECT_LISTEN`, `AML_INSPECT_DEBUG_CAPS`, `MNEMON_INSPECT_TOKEN` and the provider keys,
  and `/healthz` on the inspect listener answers 200 with the inspect token (two more checks);
- `aml-serve@<n>`, `aml-watch@<n>.timer`, `aml-inspect@<n>` while inspect is on, and `caddy`
  are active;
- `systemd-analyze verify` passes on the templates, and `caddy validate` on the Caddyfile, when
  the tools are installed;
- `wg0` is up and carries the address of `AML_ADMIN_LISTEN`;
- ufw is active, denies incoming by default, and has the rules of "Firewall", and no rule lets
  anyone reach 51820/udp or an admin port (8091 to 8600/tcp), over IPv4 or IPv6, on any
  interface, by one port, a range or a list, or by all of tcp or all of udp: each such rule is
  a `FAIL` line that names it.

Every check runs; each failure prints one line that starts with `FAIL`, and the exit status is
1 if any failed. It never prints a secret: the admin and inspect tokens reach curl only as a
config on its standard input, never as an argument, and a wrong `AML_ALERT_KIND` is named only
when it is a short lowercase word (what was pasted there by mistake may be the webhook). It needs
`curl` and `jq`, and says so if they are missing.

## Configuration

`deploy/aml/memory-config.industrial.json` is installed once as
`/usr/local/share/mnemon-aml/memory-config.industrial.json`; `new` writes each instance's
`/etc/mnemon-aml/<n>/memory-config.json` from it, with the instance's `budgets` and with
`budgetFile` set to `/var/lib/mnemon-aml/<n>/model-budget.db`. It is loaded strictly (unknown
fields are errors, and the file must be at most 1 MiB with one JSON value). JSON has no
comments, so the notes are here:

| Field | Value | Notes |
|---|---|---|
| `strategy` | `"simple"` | The read strategy of the paper. `""` would be the older memory-agent/v1 reads. |
| `view` | `maxItems 100`, `maxCharacters 240000`, `order "time"` | `top_k` above `maxItems` is clamped to it, never an error. The platform's formal `top_k` is 100. The View is the judged list (JEV's shortlist of at most 64 records and at most 8 linked from the consolidation blocks), so a normal Search stays well under both caps. `order "time"` lists the records after the consolidation blocks in time order, records of one time in the order they were written; selection and the cut at `top_k` go by rank. |
| `safety` | unset (every check off) | The safety and privacy checks, each switched on its own. `abstain`: a read that ran whole (nothing degraded) and found nothing the reply needs, JEV calling no current record of its shortlist and no block of the consolidated layer needed, answers only the user's standing instructions (the block that says how to answer, whatever the question) instead of the records nobody judged needed. With or without it, such a Search counts in `mnemon_aml_search_no_evidence_total` and its request line carries `evidence=none`. `forget` (layered consolidation): what the user asks to forget is withdrawn from every read. The extraction is asked to mark such a request as a value of the key it concerns (`"forget": true`); every value of the key up to its last mark is then withdrawn and the key shows `forgotten at the user's request`, the records those values and the mark come from are withheld from the View, and an event or directive made from them alone is not shown. A value told after the mark shows again. The prompt changes only with the switch on. `mask`: what a Search hands out has its sensitive values of a known form hidden, keeping their kind and last four characters (`[card ending 4242]`): the kinds Presidio recognizes by default that a pattern finds: card numbers that pass Luhn's check, dashed US social security numbers, IBANs whose check digits hold, email addresses, North American and E.164 phone numbers, and, after their context word, bank account numbers ("account", "acct"), passport and driver's license numbers; with API keys, tokens and private keys of known forms, and the value said after "password", "passcode" or "PIN" (`internal/safety`, patterns, checksums and context words, no model). Scanned, LoCoMo holds none of them and BEAM-128k 62 emails, 8 phone numbers and one password hash, none of them in a question or a gold answer. The store and the reads keep the values; `mnemon_aml_search_masked_total{kind}` counts what was hidden. |
| `consolidation` | `{"mode": "layered"}` | DeepSeek extracts each batch alone, then JEV links it to the memory so far (see "Background consolidation"). It needs the `judge` and a completion model (the `consolidator`, else the planner). Other fields keep their defaults: 10,000-character batches, at most 16 batches per job, 4 extractions in flight (`parallel`), 8 stored candidates per linked item (`linkCandidates`). A user's records waiting to be folded are bounded by 16 × 10,000 characters; later records wait unread, losing nothing. `{"mode": "timelines"}` is the reference's sequential fold: the state and the reads are the same. `index` (a memory-service option) is not set. |
| `planner` | DeepSeek Flash, **no thinking** | Do not add `"thinking": true`. Cue planning is capped at 400 output tokens and need planning at 200; a reasoning model can spend them on reasoning and return an empty plan, which makes Search degrade. |
| `judge` | `jev` / `jev-1.13.0` | The version is pinned; the endpoint is JEV's official one. It judges the reads and, in layered mode, links the consolidation items, so its budget pays for both. |
| `consolidator` | DeepSeek Flash, no thinking, `timeoutSeconds` 600 | Each extraction asks for at most 8,192 output tokens whatever `maxTokens` says. A background job's extraction may run 600 s (`timeoutSeconds`, 1–1,800; models without it time out after 90 s). A Search's flush gives each extraction attempt at most 180 s, then retries it: about 10× a typical extraction of about 1,900 output tokens, which DeepSeek Flash returned in 15–17 s (about 110 tokens/s) from the evaluation host on 2026-10-24. In that measurement DeepSeek sent nothing until its reply was complete, so these caps are what bound the wait. Once a reply's headers have come, an attempt of any class whose body then sends nothing for 60 s is cut and retried (`the reply stopped sending`); the blank lines DeepSeek documents sending while a request waits under load are bytes, and keep it going. Unset, the planner would extract. |
| `budgetFile` | `/var/lib/mnemon-aml/<n>/model-budget.db` | Absolute, private (0600), one per instance, shared by all users of the instance. Two profiles with different caps on one file would overwrite each other's stored cap. |
| `budgets` | evaluation `{"jev": 60, "deepseek": 600}`, beta `{"jev": 3, "deepseek": 30}` | **Hard caps on spending**, in USD for `jev` and `openai` and CNY for `deepseek`: check them before a job. When a cap is reached the model calls stop (see Troubleshooting). Every call holds a reservation while it runs: USD 0.01 for JEV, CNY 0.25 for DeepSeek, more for a large fold (up to about CNY 0.46 for the largest request). A call that never connected or got a non-2xx answer releases it; a reply settles it at its metered usage; a call that failed or was cut after sending, or whose reply is unreadable or carries no metering, settles it at its input estimate (its request's input tokens at the input price, no output). Only calls in flight hold it, so the usable headroom is a little below the cap while they run; a process that died with calls in flight leaves their reservations held until the next start, which settles them at their input estimates. The configured value replaces the stored cap when the service starts; the amount already spent is kept. The `openai` cap is a soft one: a call that costs more than its reservation is recorded at its real price. |

A Search can make several planner calls and up to 128 JEV decision calls (the read budget).
Layered consolidation makes one DeepSeek extraction call per batch (more when a reply must
be asked again or split) and, once the user's memory holds anything to link to, a few JEV
calls per batch: one question per candidate for each new topic (up to 8 stored topics) and
two per candidate for each new fact (up to 8 stored facts), packed 128 questions to a call.
Both happen in the background workers and before a user's next Search, so the `jev` cap pays
for consolidation as well as for Searches. Estimate a job's spend from a trial run on a
scratch instance (`mnemon-preflight` prints the budgets) before choosing a beta instance's
caps. The evaluation caps are fixed at 600/60: DeepSeek CNY 600 and JEV USD 60.

## Background consolidation

- **Adds return after persistence.** An Add writes the user's records, makes them searchable
  by words, notifies the scheduler and answers; it calls no model. The workers
  (`--consolidation-workers`, `AML_WORKERS`) fold the full batches, one job per user at a time.
- **Bounded lag.** An Add waits while its user's backlog is above the lag bound, one job's
  window: 160,000 characters with the shipped configuration (16 batches of 10,000), the
  `lagBound` of `/admin/status`. The scheduler knows a user's backlog from the user's first
  job or first Add since the process started: that Add measures it once, after its write and
  before it lets go of its user's write lock (reading only), so the user's other Adds, which
  write one at a time, wait for that one read and then add what they write (the Adds of other
  users are not held); characters are UTF-16 units, as a job counts them. It keeps its
  admission slot while it waits, so declare the AML concurrency at or below
  `--max-concurrent-adds` and `--max-concurrent-searches`. An Add waits at most until 25
  minutes after it arrived; then it answers 200 all the same (its records are stored and
  searchable), counted in `addOutran` and logged as `add outran consolidation`.
- **Search folds the rest.** Before it reads, a Search folds its user's tail, for at most 10
  minutes; a user with nothing pending pays only a state read. While it does, it holds its
  user: no background job of that user starts (a running one goes on). Its calls are of the
  `flush` and `interactive` classes, the workers' of the `background` class.
- **Defer, then fall back.** When JEV cannot be reached (an outage, an open breaker) or its
  budget is spent, the job stops before the batch it cannot link: the batch stays pending, and
  a later job links it with JEV. A failure of the linking request itself counts toward the
  batch's linking rule, which has the drop rule's thresholds (three failures over at least ten
  minutes); once it is due, the batch is linked without JEV from its whole extraction, never
  split or dropped (`consolidation linked N batches without JEV`).
- **The drop rule** is about extraction: when the provider fails three times on the same batch
  over at least ten minutes (not counting a spent budget), the next attempt splits it once and
  leaves a half that fails again out of the index (`consolidation dropped …`); the records stay
  searchable. A failed job backs off from 30 s, doubling with each failure in a row, to at most
  8 to 10 minutes (every wait is jittered, the cap too, so that users that keep failing do not
  retry in step); a job stopped by the budget reserve pauses for 10 minutes.
- **Two-phase shutdown.** On SIGTERM the public listener closes and the scheduler drains: no
  job starts, no Add waits for it. Requests in flight and running jobs have 30 s together;
  then the requests still running are canceled (they answer 503, which the platform retries
  with the same `request_id`) and the jobs still running are cut (their saved batches stay,
  and they resume after a restart); the canceled requests have 5 s more. The stores and models
  close within the same 35 s. At its end the model requests still in flight (an attempt
  abandoned past its timeout runs on to its hard timeout) are cut: each settles its budget
  reservation at its input estimate, or releases it if the request was not sent, within 5 s
  more. The whole shutdown is 30 + 5 + 5 s, 40 s, inside `TimeoutStopSec=60`, and leaves nothing
  held. A shutdown that had to cancel or cut anything exits 1, and says what ("Logs").
  Only a crash, or a settlement that did not finish within those last 5 s, leaves reservations
  held; the next start settles them at their input estimates and logs it (`settled N budget
  reservations a previous process left held`), so that `held` is 0 again with nothing for you to
  do. (`mnemon-preflight` prints `held`.)
- **One `aml-serve` per data root.** It takes an exclusive lock on `<data-root>/.aml-serve.lock`
  and refuses to run while another process holds it.
- **`--max-open-stores`.** Every open user store keeps its query cache, which holds the
  records it searches: a BEAM-10M-scale user (about 98,500 records) holds about 744 MB when
  its store is open, measured on 2026-10-24 with BM25 only, before the planned leaner query
  snapshot (not built yet), which would lower it. Set the limit to the memory you can spare
  divided by the largest user: 16 on the evaluation host, 2 for a 1 GB beta instance, and 64
  (the command's default) only for small users (LoCoMo-scale users hold a few hundred chunks).
  A store that is in use is never closed, so more distinct users in flight than the limit use
  more memory for a while. Too low a limit makes stores reopen with a cold cache, which slows
  the next Search of that user.
- **Layered consolidation** (the shipped `consolidation.mode`) extracts each batch alone,
  without the index so far, up to four batches at once; then JEV links the batches strictly in
  batch order, saving the index after each one. For every new topic and fact it compares the
  stored ones nearest to it (by shared words, BM25) and decides whether it continues one. A
  batch linked without JEV starts new topics and facts instead of continuing stored ones (a
  fact keeps the history of the stored key of the same name); the journal logs it as
  `consolidation linked N batches without JEV`.
- **`--search-timeout`** (default `10m`, `1m` to `18m`) bounds a Search's memory read. When it
  expires the Search answers the raw ranking (HTTP 200, counted as degraded). With the tail
  fold's own 10 minutes before it, a Search answers within the platform's 30 minutes.
- **Load shedding.** Past `--max-concurrent-adds` (or `--max-concurrent-searches`) a request
  waits up to 30 s for a slot, then gets 503 with `Retry-After: 5`, before its body is read or
  its store opened; the platform retries 503. It is counted in `mnemon_aml_shed_total` and
  logged as `<route> shed: no free slot within 30s (limit N)`. 0 means no limit.
- **Limits that match the platform.** The server accepts 30 MiB bodies, sets the write timeout to
  31 minutes, and answers `GET /health` without a key (any 2xx is healthy). Caddy holds a request
  through a restart for up to 30 s, dialing every 250 ms; it retries a POST only when its dial
  failed, which means nothing was sent, so a POST is never sent twice.

## systemd

`aml-serve@.service`:

- `Type=notify` and `NotifyAccess=main`: `READY=1` once the public listener is bound and the
  background work runs (its startup scan queued every user, its heartbeat ticks); `STOPPING=1`
  when the shutdown starts. An admin address that cannot be bound yet (`wg0` down) does not hold
  `READY=1` back: the admin listener is bound later, and the log says when. There is no "ready"
  log line: `systemctl status` shows the instance `active (running)`.
- `After=` and `Wants=` `wg-quick@wg0.service` and `network-online.target`, and no `Requires=`:
  the instance starts after the overlay and asks for it, and neither a missing overlay nor a
  stopped one stops it.
- `WatchdogSec=60`: aml-serve pings every half interval while its liveness probe passes (the
  in-process `/health` answers 200, the background work's heartbeat ticked within the interval,
  and the budget file answers); systemd restarts an instance whose pings stop. aml-serve refuses
  a watchdog interval under 2 s. The probe calls no model, so a slow provider never looks like
  a dead process; `/ready` reports those.
- `Restart=always`, `RestartSec=5`, and `StartLimitIntervalSec=0` in `[Unit]`, so a crash loop
  keeps retrying every 5 s.
- `TimeoutStopSec=60`, which outlasts the 40 s shutdown (30 s, 5 s, and 5 s for the model
  requests that were cut to settle).
- `StateDirectory=mnemon-aml/%i` (0700), `User=mnemon`, `LimitNOFILE=65536`, and sandboxing
  (`ProtectSystem=strict`, `PrivateTmp`, `PrivateDevices`, `NoNewPrivileges` and the like).
- `MemoryMax=` and `CPUQuota=` come from the instance's drop-in. `systemctl show
  aml-serve@<n> -p MemoryMax -p CPUQuotaPerSecUSec` shows what applies.

`aml-watch@.service` is `Type=oneshot`, runs as `mnemon`, has `TimeoutStartSec=90` (a run that
hangs is killed, which gives back its lock) and the same sandboxing, with
`StateDirectory=mnemon-aml-watch/%i`. It has no `[Install]` section and does not require
aml-serve: it must run, and alert, while aml-serve is down. `aml-watch@.timer` runs it with
`OnBootSec=2min`, `OnUnitActiveSec=1min` and `AccuracySec=1s`.

## Admin listener and metrics

Each instance's admin listener binds its overlay address with a token
([`docs/admin-api.md`](admin-api.md) has the address rules, the authentication and every
route). The console, Prometheus (through the console) and the watcher read it; nothing on the
internet reaches it. The admin token is a secret of its own, at least 32 bytes: `--admin-token-env`
must name another variable than `--auth-key-env` (the unit passes `MNEMON_ADMIN_TOKEN` and
`AML_API_KEY`), or startup is refused. Read it by hand on the host without putting the token on a
command line:

```sh
n=beta-alice
admin() { # admin PATH: GET PATH on instance $n's admin listener, with its token
  grep -E '^MNEMON_ADMIN_TOKEN=' "/etc/mnemon-aml/$n/env" | sed 's/^MNEMON_ADMIN_TOKEN=/header = "Authorization: Bearer /; s/$/"/' |
    curl --config - -sS "http://$(grep -E '^AML_ADMIN_LISTEN=' "/etc/mnemon-aml/$n/env" | cut -d= -f2-)$1"
}
admin /ready
admin /admin/status | jq .
admin /admin/v1/stores | jq .
admin /metrics | grep '^mnemon_aml_'
```

- **`/ready`** answers 200, or 503 with every reason: the data root is not writable, its disk
  has less than 1 GiB free, a provider's circuit breaker is open (a half-open breaker counts as
  ready: it lets calls probe the provider), a paid account has spent and held more than 95% of
  its cap, or the budget file does not answer.
- **`/admin/status`** is the status the watcher reads, in camelCase: `startedAt`, `adds`,
  `searches`, `degraded`, `partial`, `dropped`, `linkFallbacks`, `addOutran`, `partialLast10m`,
  `searchesLast10m`, `queue` (`queued`, `running`, `backingOff`, `held`, `oldestQueued` in
  nanoseconds, `maxBacklog` in characters), `lagBound`, `breakers`, `breakerTrips` and
  `rejectedByKind` (requests refused with 401, 402 or 403) by provider kind, `budgets` (always an
  array of `kind`, `cap` (`null` when unlimited), `spent`, `held`, `source` and `unlimited`),
  `openStores`, `version` and `providerKeys` (each key kind's `source`, `account`, `host` or
  `none`, and the key's `fingerprint`, never the key).
- **`PUT` and `DELETE /admin/v1/provider-keys`** set and revert the account's own provider keys
  ("An account's own provider keys"): write-only, no route answers a key.
- **`PUT` and `DELETE /admin/v1/budgets`** set and revert the budget caps at run time ("Budget
  caps at runtime").
- **`/admin/v1/stores`** is the console's store overview: each store by its 8-digit prefix, its
  bytes, last change, whether it is open and its backlog. No user id and no content.
- **`/debug/pprof/`** is Go's profiler, behind the token like every admin route.

`/metrics` (Prometheus text format; the console scrapes it for Prometheus):

| Series | Meaning |
| --- | --- |
| `mnemon_aml_requests_total{route,code}` | requests answered, by route (`add`, `search`, `health`, `other`) and status code |
| `mnemon_aml_request_duration_seconds{route}` | time to answer, the wait for a slot included |
| `mnemon_aml_in_flight{route}` | requests being served or waiting for a slot |
| `mnemon_aml_shed_total{route}` | requests shed with 503 (`add`, `search`) |
| `mnemon_aml_search_degraded_total` | Searches answered by the raw ranking |
| `mnemon_aml_search_partial_total{stage}` | stages of a read that fell back, once per Search |
| `mnemon_aml_search_masked_total{kind}` | values `safety.mask` hid in what Searches handed out: `card`, `ssn`, `iban`, `secret`, `password`, `email`, `phone`, `account`, `passport`, `license` |
| `mnemon_aml_search_no_evidence_total` | Searches whose read, run whole, found nothing the reply needs (`safety.abstain` answers them with no items) |
| `mnemon_aml_model_attempts_total{kind,class,outcome}` | model call attempts: class `interactive`, `flush` or `background`; outcome `ok`, `throttled` (HTTP 429, retried after its `Retry-After`; without one, or with one of 0, an invalid value or a past date, an `interactive` or `flush` call retries patiently: the k-th wait is a jittered floor of at most 2, 4, 8 and 16 s (75–100% of it at random), or the class's backoff when that is longer, as after earlier 5xx retries; 30 s in all at most per model call: a Search makes many calls, so under a long storm it can wait longer in all, up to its `--search-timeout` for its read and up to its own 10-minute flush window for its flush; no wait is planned past them), `invalid` (a reply that did not parse or validate, retried once), `rejected` (HTTP 401, 402 or 403: the key or the balance was refused; not retried), `retryable`, `failed`, `breaker-open`, `bulkhead-full` or `budget`. Every outcome but `ok` is a model failure the console shows |
| `mnemon_aml_model_attempt_seconds{kind,class}` | time of the attempts that went to the provider |
| `mnemon_aml_model_retries_total{kind}` | attempts after a call's first |
| `mnemon_aml_consolidation_jobs_total{outcome}` | scheduler jobs: `ok`, `failed`, `paused`, `canceled` |
| `mnemon_aml_consolidation_batches_total{outcome}` | batches `folded`, `deferred` and `dropped` |
| `mnemon_aml_consolidation_link_fallbacks_total` | batches linked without JEV |
| `mnemon_aml_add_outran_total` | Adds that waited out their window |
| `mnemon_aml_consolidation_queued`, `_running`, `_backing_off` | users in the scheduler, by state |
| `mnemon_aml_consolidation_oldest_queued_seconds` | age of the oldest work not yet consolidated |
| `mnemon_aml_consolidation_max_backlog_characters` | the largest user backlog |
| `mnemon_aml_consolidation_lag_bound_characters` | the lag bound it is measured against (0 unless background) |
| `mnemon_aml_breaker_open{kind}` | 1 while a provider's breaker refuses calls |
| `mnemon_aml_budget_spent`, `_held`, `_cap{account}` | each paid account, in its currency; the cap is `+Inf` while it is unlimited |
| `mnemon_aml_open_stores` | user stores open now |
| `mnemon_aml_stores`, `mnemon_aml_store_bytes` | stores in the data root and their bytes, from a scan at most 60 s old; left out of a scrape whose scan failed |
| `mnemon_aml_admin_auth_failures_total` | admin requests refused with 401 |

The Go and process collectors (`go_goroutines`, `process_resident_memory_bytes`,
`process_open_fds`, …) are there too. No user, request or path labels a series.

- `folded` counts the batches taken off the pending records, the ones given up on included,
  so it includes the dropped batches; and `dropped` counts drops, each a batch or a half of a
  split one. The two overlap: do not add them.
- The watchdog's in-process `/health` probe is counted: it adds about 2 a minute (one every
  30 s) to `mnemon_aml_requests_total{route="health"}`.

## Inspect (检索)

The console's 检索 tab reads an instance's memory through the instance's **inspect service**,
`aml-inspect@<n>` (`mnemon memory-agent aml-inspect`): a process of its own beside
`aml-serve@<n>`, never a route of aml-serve. Its JSON is in [Inspect API](inspect-api.md):
the stores, a user's records and consolidated memory (threads, events, facts with their
conflicts and supersedes, directives, and what is pending), and retrieval with its trace,
stage by stage. Whether it runs is the instance's **content-access switch**: it is off by
default, `new` never turns it on, and the evaluation instance keeps it off unless the
operator decides otherwise after checking the submission's rules.

**Side-effect free, by construction.**

- It opens every `records.db` and `memory-index.db` read only. A store aml-serve holds open
  (its `-wal` and `-shm` exist) is read live through them; one it does not hold is read from
  its main file alone (`immutable`), so that no `-wal` or `-shm` is ever made, and the file is
  checked again when the request ends: a store that changed meanwhile answers
  `503 store_busy`, and the console retries. Nothing checkpoints. The unit mounts
  `/var/lib/mnemon-aml/<n>` read only (`ReadOnlyPaths=`): a write could not happen anyway.
- It never folds or consolidates. A Search folds what is pending before it reads; a full
  search of the inspect service does not, and its trace names the pending records instead.
- Its full searches call the instance's models with the instance's configuration and keys
  (the account's own, read from the key file again before each full search, over the host's:
  "An account's own provider keys"), billed to a **debug budget** of their own, `/var/lib/mnemon-aml-inspect/<n>/debug-budget.db`, under
  its caps (`AML_INSPECT_DEBUG_CAPS`; DeepSeek in CNY, JEV in USD). The instance's budget file
  is never opened, the model cache is off, and the service has no metrics. Lexical searches,
  the default, call no model and cost nothing: they are the raw ranking aml-serve answers a
  degraded Search with, byte for byte.
- The debug budget has one writer. While it runs, aml-inspect holds
  `/var/lib/mnemon-aml-inspect/<n>/debug-budget.db.lock` beside it, and a second aml-inspect on
  the same debug budget refuses to start (`another aml-inspect holds …`). Whatever a previous
  run left held there (a crash, or a stop past its windows) is settled at its input estimates
  when the service starts, and logged once as `settled N budget reservations a previous process
  left held, at their input estimates: …`.
- Its shutdown has aml-serve's phases: the requests in flight have 30 s, those canceled then 5 s more,
  and the model requests of a full search that still run are cut and settle in the debug budget
  within 5 s more. That is 30 + 5 + 5 s, inside the unit's `TimeoutStopSec=45`.
- It runs at a low priority within small limits: `Nice=10`, `IOSchedulingClass=best-effort`
  at `IOSchedulingPriority=7` (the lowest), `MemoryMax=1G` (and `GOMEMLIMIT=819MiB`),
  `CPUQuota=100%`, two requests reading stores at once (`--max-concurrent`). Not the idle
  class: under sustained I/O it can starve, and on a disk whose I/O scheduler honours it (`bfq`,
  and `mq-deadline` since Linux 5.14) a starved inspect read would hold its read (SHARED) lock on
  the rollback-journal `memory-index.db` and delay aml-serve's consolidation writes, by up to
  their 10-second busy timeout. Best-effort 7 puts the reads behind aml-serve's without starving
  them; NVMe's usual scheduler, `none`, ignores the classes.
- Its log lines name the method, the route, the status, the time and the store by its
  8-digit prefix (`journalctl -u aml-inspect@<n>`), never a key, a user id, a query or any
  content; resolving a user id logs no store at all.

**Turn it on.**

```sh
mnemon-instance inspect enable beta-alice                     # debug caps: deepseek 5 CNY, jev 1 USD
mnemon-instance inspect enable beta-alice --debug-deepseek-cap 10 --debug-jev-cap 2
```

It makes the inspect token (`mnm_` and 32 random bytes in base64url, as the admin token),
writes `inspect.env` (root's, 0600) with the token, the listener, the caps, `GOMEMLIMIT` and
the provider keys the configuration names, copied from `host.env`; makes the inspect state;
enables and starts `aml-inspect@<n>`; waits up to 120 s for its `/healthz`; and prints, once,
the inspect URL, the token and the JSON for the console:

```
Inspect URL:    http://10.77.0.3:8102
Inspect token:  mnm_…

For the console (it holds the inspect token):
{"inspectUrl":"http://10.77.0.3:8102","inspectToken":"mnm_…"}
```

Register both beside the instance's admin URL and token. The token is shown only then; it
refuses to run while inspect is on already.

**Where it listens.** On the admin listener's overlay address, at its port + 1: 8092 on a
dedicated host, 8102 + 10 × the slot on a shared one. That is inside the firewall's rule for
8091 to 8600 from the hub on `wg0`: no firewall change. Its rules are the admin listener's
(loopback without a token, a private address only with one), and every request, `/healthz`
included, carries `Authorization: Bearer <inspect token>`. Unlike aml-serve it has no data plane
to keep serving: with `wg0` down it cannot bind, exits, and systemd restarts it every 5 s until
the address is there.

**Turn it off.** `mnemon-instance inspect disable beta-alice` stops and disables the unit and
removes `inspect.env` and the inspect state, the debug budget with it; remove the URL and the
token from the console. To rotate the token, disable it and enable it again. `delete` removes
the inspect state with the instance; `list` shows `INSPECT on`; `mnemon-preflight` checks
inspect only while it is on.

**When a full search is refused.** `409 full_disabled`: the service has no debug caps.
`402 debug_budget_exhausted`: the debug budget is spent; `GET /v1/info` shows what is left. Raise
the caps from the console's 检索 tab (`PUT /v1/debug-caps`, at run time), or `inspect disable`
then `inspect enable` with higher caps, which starts a new debug budget.
`502 provider_failed`: the read failed (a provider, the breaker, the 10-minute bound); the
answer carries the raw ranking `/search` would have given, and what the read did.
`503 store_busy`: the store changed under the search (aml-serve opened it and wrote), SQLite was
busy, or a crash left a journal aml-serve rolls back when it next opens the index; the console
retries, and a full search's answer still carries what it cost. An inactive user's store with a
journal like that stays busy until aml-serve opens it again, which can be long: the console
caps its retries (3, say) and shows "store recovering" after them. Closing the connection
cancels a full search.

## Logs

The instance logs to the journal: `journalctl -u aml-serve@<n> -f`. The template passes
`--log-format json`, one JSON object per line (`time`, `level`, `msg`; every line of a request
carries its `request_id`, and its `user` once the request has named one); `--log-format text`
writes the same as `key=value` text. The `msg` keeps the wordings below. One line per Add and
Search (`POST /search 200 83ms`, with the request's `request_id` and `user`); health checks are
not logged. Request bodies and user ids are never logged; a user appears as the first eight hex
digits of its store hash, `(user xxxxxxxx)` in a message and `user` beside the `request_id` of
a request's lines. A request that never named a user (a bad body, an unknown path) has none.

| Line | Meaning |
|---|---|
| `aml-serve listening on 127.0.0.1:8100`, `aml-serve admin listening on 10.77.0.3:8101` | Startup: the public listener is bound, and the admin listener too when its address can be (the next line is for when it cannot). `READY=1` follows once the public listener is bound and the background work runs; it does not wait for the admin listener. |
| `aml-serve: --admin-listen: listen tcp 10.77.0.3:8101: bind: cannot assign requested address; serving without the admin listener and binding it again every 5s` | WARN, once: the admin address could not be bound (`wg0` is not up). The instance serves its public listener, and `aml-serve admin listening on …` follows when the retry, every 5 s and quiet, succeeds. |
| `settled N budget reservations a previous process left held, at their input estimates: deepseek 2 (held 0.5000, settled 0.0030), jev 1 (held 0.0100, settled 0.0000)` | WARN, at start, in aml-serve (the instance's budget) and in aml-inspect (its debug budget): a crash, or a stop past its windows, left model requests' reservations held, and this start settled them by kind (the reservations, what they held and what they settled at, in each account's currency). One reservation reads in the singular: `settled 1 budget reservation a previous process left held, at its input estimate: deepseek 1 (held 0.2500, settled 0.0030)`. Nothing held: no line. |
| `aml-serve: settling what the budget file holds: <error>; it stays held until a start settles it` | ERROR, at start (aml-inspect: `settling what the debug budget holds: …`): the settlement failed; the reservations stay held, and the next start tries again. |
| `POST /add 200 41ms` | One line per request, with `request_id`, and `user` once the request named one. |
| `add shed: no free slot within 30s (limit 4)`, `search shed: …` | A request found no free slot for 30 s and got 503 with `Retry-After`. |
| `degraded search N (user xxxxxxxx): <error>` | Search could not use the models and answered with the raw BM25 ranking (HTTP 200). `N` counts degraded searches since the process started. `context deadline exceeded` means the read ran past `--search-timeout`. |
| `degraded search kept rank order (user xxxxxxxx): <error>` | A degraded Search could not read the write positions of its records (a store error), so it delivered the raw ranking in rank order instead of time order. |
| `partially degraded search N (user …): judge, layer` | The read answered, but the named stages fell back: `lexical-index` (the store's lexical index could not be caught up first, so the records it lacks ranked by the frozen BM25 rules), `judge` (JEV's final judgement failed; its screen stood in), `scan-budget` or `loop-budget` (the read's JEV budget ran out), `layer-state`, `layer` or `expand` (the consolidation layer was left out or not judged), `order` (the write positions could not be read). |
| `status: adds A, searches S, degraded D, partially degraded P; budget deepseek spent … held … cap …, jev …` | Every 10 minutes: the Adds and Searches answered since the start, the degraded ones, and each budget account in use. |
| `records query preparation: … (user xxxxxxxx)` | A user's store failed to build its words index or its query snapshot in the background (5 minutes at most); a later write, or the store's next opening, tries again. |
| `add outran consolidation (user …)` | An Add waited out its 25-minute window at the lag bound and answered 200; its records are stored and searchable. |
| `consolidation backlog unreadable (user …): <error>` | WARN: the first Add of a user since the start could not measure the user's backlog (a store read failed); the lag bound holds from the user's first job instead. |
| `consolidation retries in 1m0s (user …)` | A background job failed; the user backs off (30 s doubling, to at most 8 to 10 minutes, jittered). |
| `consolidation paused: budget reserve (user …)` | The budget's reserve stopped a background job; it pauses for 10 minutes. |
| `consolidation queue full: N users have work; dropped a notification (user …)` | The scheduler tracks at most 100,000 users with work; the user's next Add or Search picks the work up. Logged at most once a minute, on the line of the request that found it full (its `request_id` and `user`). |
| `pack failed`, `open store failed`, `write failed (user …)`, `degraded search failed (user …)`: `<error>` | An Add failed (`pack`, `open store`, `write`), or the raw ranking a degraded Search falls back to did. WARN, with a 503 the platform retries, when a retry can pass (the shutdown, a cancellation or deadline, a busy or locked store, a write conflict, too many open files); else ERROR, with a 500. |
| `consolidation failed (user …): <error>`, `flush failed (user …): <error>` | A job, or the fold before a Search, failed; the records are searchable, and the next job or request retries. |
| `consolidation linked N batches without JEV (user …)` | Layered consolidation linked N batches without JEV because its calls failed or the `jev` budget is spent: their topics and facts started new ones instead of continuing stored ones. Nothing is dropped. |
| `consolidation dropped N records (user …): <reason>` | The index leaves out N records whose fold the provider refused or timed out on for at least 10 minutes, whose fold request was too large, or whose reply never parsed. Outages, rate limits (429), 5xx and credential or balance refusals (401/402/403) never drop records. The records stay searchable. |
| `consolidation stopped with N records pending (user …)`, `flush stopped …` | One job or fold made its 50 calls; the next one continues. |
| `… panicked …` with a stack trace | A bug: an Add answered 500, a Search the raw ranking, a consolidation was skipped. Report it with the trace. |
| `add skipped N of M messages without text` | Messages with empty or image-only content were skipped. |
| `aml-serve watchdog: no ping: <reason>` | The liveness probe failed and the ping was skipped; systemd restarts the instance after a whole interval without pings. |
| `aml-serve: shutting down` | SIGTERM or SIGINT: the shutdown starts. |
| `aml-serve: <component>: <error>; shutting down` | A component failed (a listener, the background work): the process shuts down and systemd restarts it. |
| `aml-serve: the 30s grace has ended with <jobs>; cutting them: their saved batches stay, and they resume after a restart` | Consolidation jobs still ran at the end of the grace. |
| `aml-serve: the 30s grace has ended with 3 requests still running; canceling them` | Requests still ran at the end of the grace (the count says how many); they answer 503. The command then exits 1: `canceled at the end of the 30s grace: 3 requests still running`. |
| `aml-serve: requests still running 5s after they were canceled; closing their connections` | The canceled requests did not end. |
| `aml-serve: deepseek: cut 2 model requests still in flight at the close's deadline: their reservations settled at their input estimates, or were released if they were not sent` | WARN, at the end of the shutdown, one clause per provider (`; jev: cut …`): model requests still running (abandoned attempts) were cut, and settled or released within 5 s. The count is how many were cut, and one reads `cut 1 model request`. After a Search whose planner timed out it is 1 or 2: the Search's two planner calls cancel each other when one fails, so the other may already have settled before the shutdown. With a failed settlement a provider's clause reads `…at the close's deadline; 1 reservation stays held, their settlement failed: the next start settles them`. |
| `aml-serve: closing the stores and models did not finish 5s after the end of the shutdown; exiting while model requests settle: their budget reservations stay held until the next start settles them` | ERROR: even the 5 s to settle the cut requests ran out. The process exits; the next start settles the holds (`settled N budget reservations …`). |

A failed aml-serve or aml-watch run writes its error once, as one line, with no usage block:
`journalctl -u aml-serve@<n> -n 50` shows why an instance would not start.

The watcher's lines (`journalctl -u aml-watch@<n>`): `aml-watch: posted` (with `alert` and
`event`), `aml-watch: not delivered` (with the reason), `aml-watch: AML_ALERT_WEBHOOK is empty:
not posting` (with the message), `aml-watch: another aml-watch run holds the lock; this run does
nothing`, `aml-watch: could not take the run lock; going on without it`, `aml-watch: the public
health probe is off because --public-url is empty`, and `aml-watch: the state file is unreadable
or of another version; moved it aside and starting afresh` (with `path`, `aside` and `kept`).
That last one moves the file to `state.json.bad-<UTC time>` (`state.json.bad-20261025T120301Z`)
in the watcher's state directory, keeps the alerts it owed messages for when they decode (`kept`
counts them), so that their resolutions still go out, and goes on with a fresh state; its
variant `…; starting afresh, and it could not be moved aside` says the file stayed where it
was, and the run's own save replaces it. Remove the `.bad-` files when you have looked at them.

The instance script's lines (`journalctl -t mnemon-instance`), one per action:

```
new beta-alice: shared, slot 0, data 127.0.0.1:8100, admin 10.77.0.3:8101, API key fingerprint 0123456789ab
suspend beta-alice
resume beta-alice
rotate-key beta-alice: new API key, fingerprint 0123456789ab
delete-user beta-alice: removed store 58e645e0
delete beta-alice
```

## Alerts

Every instance has its watcher: `aml-watch@<n>.timer` runs `mnemon memory-agent aml-watch`
every minute. One run reads the admin listener's `/ready` and `/admin/status` (with the token)
and the public `/health`, applies the rules below, posts each alert that fires, each firing
alert that is due again and each alert that clears to the host's bot, and saves its state in
`/var/lib/mnemon-aml-watch/<n>/state.json`. Its environment is `/etc/mnemon-aml/<n>/watch.env`.
Every message starts with `[mnemon-aml]`, and its header names the host and the instance:
`[mnemon-aml] ALERT <alert> on <host>/<n>`, `[mnemon-aml] RESOLVED <alert> on <host>/<n>`. The
watcher keeps alerting when the ops host is down.

| Alert | Reads | Fires when |
| --- | --- | --- |
| `ready` | `/ready`, `/admin/status` | `/ready` does not answer 200, or the status cannot be read |
| `health` | `$AML_PUBLIC_URL/health` | it does not answer 200 |
| `restart` | `startedAt` | the server started at another time than at the last run |
| `degraded-search` | `degraded` | a Search was answered by the raw ranking since the last run |
| `partial-search` | `partialLast10m`, `searchesLast10m` | more than 5% of the last 10 minutes' Searches were partially degraded, once there were 20 |
| `breaker-open:<kind>` | `breakers`, `breakerTrips` | a provider's breaker is open, or opened since the last run |
| `provider-rejected:<kind>` | `rejectedByKind` | the provider rejected requests with 401, 402 or 403 since the last run: a wrong or revoked key, or an empty balance (see Troubleshooting) |
| `budget-high:<kind>` | `budgets` | spent and held are above 80% of the cap |
| `budget-held:<kind>` | `budgets` | held reservations are above 2% of the cap |
| `backlog-age` | `queue.oldestQueued` | the oldest work not yet consolidated has waited more than 15 minutes |
| `backlog-lag` | `queue.maxBacklog`, `lagBound` | the largest backlog has been above the lag bound for 15 minutes |
| `consolidation-dropped` | `dropped` | a batch was dropped from the consolidation layer |
| `link-fallback` | `linkFallbacks` | a batch was linked without JEV |
| `add-outran` | `addOutran` | an Add waited out its window for consolidation |

- **Hold and repeat.** An alert about something that happens (`restart`, `degraded-search`,
  `breaker-open`, `provider-rejected`, `consolidation-dropped`, `link-fallback`, `add-outran`)
  keeps firing for 10 minutes after the last run that saw it happen, so a stream of events is
  one alert, not a pair of messages per event. Any firing alert is said again 30 minutes after
  its last message. The first run (no state file) takes the counters as they are and says
  nothing about them. When the status cannot be read, the alerts that come from it are neither
  repeated nor resolved: `ready` says what is wrong. A server too old to report
  `rejectedByKind` gets no `provider-rejected` alert.
- **At-least-once delivery.** A message that did not go out stays unsent in the state, and the
  next run sends it again, every minute, with no cap on the tries. An alert that clears before
  its own message was ever delivered still owes its resolution, which says
  `(after <d>, alert not delivered)`; `<d>` is how long the alert fired, measured to when the
  message is built, so after a long outage of the webhook it can read
  `after 73h0m0s, alert not delivered`. A run with no webhook logs its messages, keeps what it
  owes and exits 0; the first run with a webhook posts it.
- **A state that cannot be saved** fails the run (non-zero); its firing alerts are then said
  again every minute until the state can be saved.
- **One run at a time.** A run takes a non-blocking lock on `state.json.lock`. A run that finds
  it held logs one WARN line and exits 0, posting and saving nothing. A run that hangs is
  killed after 90 s, which gives the lock back.
- **A quiet journal.** A failed run writes its error once, as one line; an empty `--public-url`
  logs one WARN line per run.

**The alert bot.** One bot serves every instance on a host (`AML_ALERT_WEBHOOK` and
`AML_ALERT_KIND` in `host.env`). Telegram is the recommended kind: it needs no keyword and no
signature. `AML_ALERT_WEBHOOK` must be https (http only to a loopback address), and its URL is a
secret, as it holds the bot's token or the hook's key: it is never printed or logged.

**Telegram** (`AML_ALERT_KIND=telegram`):

1. In Telegram, message @BotFather: `/newbot`, a name, then a username that ends in `bot`. It
   answers with the bot's token, `<digits>:<letters>`. Whoever holds the token can post as the
   bot: keep it like a password.
2. Open a chat with the bot and press **Start**, or send it a message. For a group, add the bot
   to the group and post `/start@<bot username>` there (in a group, a bot sees only commands
   and mentions).
3. Read the chat id from `getUpdates`, on a host, without putting the token on a command line:

   ```sh
   read -rs TOKEN                                # paste the token, then Enter; it is not echoed
   printf 'url = "https://api.telegram.org/bot%s/getUpdates"\n' "$TOKEN" | curl -sS --config - |
     jq '[.result[] | (.message // .channel_post // .my_chat_member).chat | {id, type, title}] | unique'
   unset TOKEN
   ```

   A private chat's id is a positive number and a group's a negative one (a supergroup's
   starts with `-100`); a public channel may also be named `@<channel username>`. `getUpdates`
   lists the last 24 hours only.
4. Build `https://api.telegram.org/bot<token>/sendMessage?chat_id=<chat id>` and write it into
   `host.env` with an editor, with `AML_ALERT_KIND=telegram`.

The watcher posts each message to that URL without its query, with the chat in the body, as
plain text (no `parse_mode`, no link preview); `chat_id` must be the URL's only parameter. Any
https host that serves the Bot API will do, a self-hosted Bot API server too. A message is
delivered when Telegram answers 2xx with `"ok":true`; any other answer, a 429 included, is a
failed delivery, logged with the HTTP status and Telegram's `error_code` (never its
description), and the next run sends the message again.

**Feishu** (`feishu`). Create a custom bot in the alert group and register the custom keyword
`mnemon-aml` (every message contains it), or an IP allowlist of the instance hosts. Signature
verification is not supported: a bot that requires it rejects every message. **WeCom**
(`wecom`) needs nothing extra.

**Alert drill, per instance**, before go-live: run the watcher once by hand with the instance's
`watch.env` loaded, a scratch state and `--admin` pointed at a closed port; the `ready` alert
must arrive. Run it again against the real admin listener; the resolution must arrive.
`systemd-run` loads the file the way the unit does, so the webhook is never printed or put on a
command line:

```sh
n=beta-alice
drill() { # drill ADMIN: one watcher run of instance $n against ADMIN, on a scratch state
  systemd-run --quiet --pipe --wait --collect -p User=mnemon -p EnvironmentFile="/etc/mnemon-aml/$n/watch.env" \
    /usr/local/bin/mnemon memory-agent aml-watch --instance "$n" --admin "$1" --admin-token-env MNEMON_ADMIN_TOKEN \
    --public-url '${AML_PUBLIC_URL}' --state "/var/lib/mnemon-aml-watch/$n/drill.json"
}
drill http://127.0.0.1:9                        # the ready alert arrives in the group
drill 'http://${AML_ADMIN_LISTEN}'              # its resolution arrives
rm -f "/var/lib/mnemon-aml-watch/$n/drill.json" "/var/lib/mnemon-aml-watch/$n/drill.json.lock"
```

The single quotes keep `${AML_PUBLIC_URL}` and `${AML_ADMIN_LISTEN}` for systemd, which expands
them from the file.

## Capacity declaration

The capacity table to submit is "Capacity, timeouts and limits" in
[`deploy/aml/README.md`](../deploy/aml/README.md). Fill it in from measurements on a scratch
dedicated instance on the evaluation host, never on `eval`, before the submission. Until then it
holds the values of the final run on a development machine with fake providers, marked as such
and not the host's: do not submit them as the host's. The unit limits (16 Adds, 16 Searches)
stay provisional until that load test. The declared concurrency must stay at or below the
instance's `--max-concurrent-adds` and `--max-concurrent-searches`.

Measure with the load test, [`scripts/aml/load.sh`](../scripts/aml/README.md), run as root on
the host against the scratch instance `n`. It reads the API key and the admin token from the
instance's env file (its `AML_API_KEY=` and `MNEMON_ADMIN_TOKEN=` lines), never from a command
line, and it settles the consolidation scheduler through the admin listener before every step,
the first included. The host has no Go: copy `scripts/aml/` there, with a vegeta v12.13.0
binary that `VEGETA` names, built on the build machine (`GOOS=linux GOARCH=amd64 go install
github.com/tsenart/vegeta/v12@v12.13.0`, with `GOBIN` unset, puts it in
`$(go env GOPATH)/bin/linux_amd64/`). Every request reaches the real providers and spends the
instance's budget, so start at low rates:

```sh
env=/etc/mnemon-aml/$n/env
BASE=$(grep -E '^AML_PUBLIC_URL=' "/etc/mnemon-aml/$n/watch.env" | cut -d= -f2-)
ADMIN=http://$(grep -E '^AML_ADMIN_LISTEN=' "$env" | cut -d= -f2-)
VEGETA=/usr/local/bin/vegeta scripts/aml/load.sh --base-url "$BASE" --key-file "$env" \
  --admin-url "$ADMIN" --admin-token-file "$env" --mode search --rates 0.25,0.5,1
VEGETA=/usr/local/bin/vegeta scripts/aml/load.sh --base-url "$BASE" --key-file "$env" \
  --admin-url "$ADMIN" --admin-token-file "$env" --mode add --rates 1,2,4
```

Each run adds users of its own (`load-<run>-u<k>`, named in its `report.json`): delete them, or
the scratch instance, afterwards. The report gives, per step, the p50, p95 and p99, the non-2xx
answers and the peak of requests in flight, and per route the knee: the highest rate whose p95
and p50 stay within twice the lowest rate's, with no non-2xx answer. Watch the process memory
(`systemctl status aml-serve@$n`), the 503 count, `mnemon_aml_shed_total` and `degraded search`
lines in the journal while it runs. Adds return after persistence, but at the lag bound they
wait for the workers, which call DeepSeek (up to four extractions at once per job) and JEV
(linking): note the rate at which the providers answer `model HTTP 429`.

The values marked "local machine, fake providers" come from the final verification run,
`scripts/aml/final.sh` on a quiet development machine: the whole fault drill and both knees of
one commit (`bc18487b`, run on 2026-10-25), built from its `git archive`, against the fake
providers.

## Health and smoke test

On the host, with the instance's key kept off the command lines:

```sh
n=beta-alice
BASE=$(grep -E '^AML_PUBLIC_URL=' "/etc/mnemon-aml/$n/watch.env" | cut -d= -f2-)
aml() { # aml PATH JSON [CURL OPTION...]: POST JSON to instance $n with its API key
  grep -E '^AML_API_KEY=' "/etc/mnemon-aml/$n/env" | sed 's/^AML_API_KEY=/header = "Authorization: Bearer /; s/$/"/' |
    curl --config - -sS -X POST "$BASE$1" -H 'Content-Type: application/json' -d "$2" "${@:3}"
}

curl -fsS "$BASE/health"                           # {"status":"ok"}

aml /add '{
  "request_id": "smoke-1", "user_id": "smoke-user", "session_id": "s1",
  "messages": [
    {"role": "user", "content": "I adopted a dog named Biscuit last week.", "timestamp": 1683554160000},
    {"role": "assistant", "content": "Congratulations! What breed is Biscuit?", "timestamp": 1683554220000}]}'
# {"success":true,"request_id":"smoke-1","user_id":"smoke-user","session_id":"s1"}
# Run it again: the same 200 (idempotent). Change a word under the same request_id: 400.

aml /search '{"query": "What is the name of the dog?", "user_id": "smoke-user", "top_k": 100}'
# {"data":[ ... {"id":"aml-...-00000","content":"Conversation s1 on 2023/05/08 (Mon) 13:56:\nuser: I adopted a dog named Biscuit ...","score":...,"created_at":"2023-05-08T13:56:00Z"}]}

aml /search '{"query":"x","user_id":"nobody","top_k":5}'
# {"data":[]}      an unknown user gets an empty list and no store is created
curl -sS -o /dev/null -w '%{http_code}\n' -X POST "$BASE/search" -d '{}'   # 401 without a key

printf 'smoke-user\n' | mnemon-instance delete-user "$n"
mnemon-preflight "$n"
```

The Search calls the paid models, and folds the smoke Add (short of a full batch) before it
reads, which spends DeepSeek (and JEV, once the user's memory holds something to link to).
Each item's `score` is its score by position (1 for the first, then 0.5, …). The smoke user
lives in the real data root: **delete it before the job**, or the platform's own users are
mixed with it; `mnemon-preflight` fails while its store is there.

## Backups

The default is a crash-consistent snapshot of the host's disk, by the provider, on a schedule:
every database is SQLite with its journal, so a snapshot restores to a consistent state of each
file, losing at most the writes since the snapshot (a retried Add rewrites them).

To restore one instance from a snapshot:

```sh
systemctl stop aml-watch@<n>.timer aml-serve@<n>
# restore /var/lib/mnemon-aml/<n>/ from the snapshot, owned by mnemon, mode 0700
systemctl start aml-serve@<n> aml-watch@<n>.timer
mnemon-preflight <n>
```

Litestream 0.5.17 can follow the databases created at runtime under a directory (a `dir` entry
with `pattern: "*.db"`, `recursive: true` and `watch: true`), but it is not enabled: it switches
every database it replicates to WAL mode, and `memory-index.db` and `model-budget.db` run in
SQLite's rollback-journal mode, which is what the engine has been tested with. Try it on a
scratch instance first if continuous replication is needed.

## Troubleshooting

| Symptom | Cause and action |
|---|---|
| **503** on `/add` or `/search` | Retryable: the platform retries 5xx with the same `request_id`. Causes: no admission slot within 30 s (`shed` in the log; raise the limits or lower the declared concurrency), a store is busy (`database is locked`), too many open files, or the instance is shutting down. |
| **400** | A contract error, not retried by the platform. Add: `request_id`, `user_id` or `session_id` missing or blank, a body that is not a JSON object, or `request_id was already used for a different payload`. Search: a missing or blank `user_id`, or a body that is not a JSON object. The message is in the `error` field. Content shape never causes a 400: empty messages are skipped, and an Add with no text returns 200 without writing; a blank query returns `{"data":[]}` and a missing `top_k` means the View maximum. |
| **401** | The key is wrong or missing: a client still uses a rotated key, or the key was mistyped. `mnemon-instance rotate-key <n>` issues a new one. |
| **403** `{"error":"account suspended"}` | The shared instance is suspended: `mnemon-instance resume <n>`. |
| **404** `{"error":"not found"}` from a shared host | No instance has that `/i/<n>` prefix: check the base URL and `mnemon-instance list`. |
| **413** | The body is over 30 MiB (the platform splits at 20 messages or 2,000 words, so this means a client bug). |
| **502** or **504** from Caddy | The instance is down for longer than Caddy's 30 s of dialing (`systemctl status aml-serve@<n>`), a dedicated instance is suspended, or a proxy timeout is below the platform's 31 minutes: compare the timeouts with `deploy/aml/Caddyfile.dedicated` or the snippet. |
| `degraded search …` in the log | The planner, JEV or a budget failed for that question, or the read ran past `--search-timeout`. The platform still got a 200 with the raw ranking. Read the error after the colon: `model HTTP 401` or `model HTTP 403` (a wrong, expired or revoked key) and `model HTTP 402` (an empty balance; DeepSeek says `Insufficient Balance`), which also page as `provider-rejected:<kind>` (below); `model HTTP 429` (the provider's rate limit outlasted the call's retries: up to its `Retry-After`, or without one (or with one of 0, an invalid value or a past date) a jittered floor of at most 2, 4, 8 and 16 s, down by up to a quarter, or the class's backoff when that is longer, as after earlier 5xx retries; 30 s in all per model call; a Search makes many calls, so under a long storm it can wait longer in all, up to its `--search-timeout` for the read and up to its own 10-minute flush window for the flush, and no wait is planned past them); `the provider's retry budget is spent` (the provider's retries, patient ones included, used up their budget: 20% of a minute's calls, and always 5 retries starting in any second; a retry is taken from the budget when it starts, after its wait); `model endpoint unreachable` (network or DNS; nothing was sent), `request failed` (network, after sending), `attempt timed out` (an attempt waited past its timeout: 45 s for the planner, 20 s for JEV, 180 s for a flush's extraction), `the reply stopped sending` (a reply sent nothing for 60 s after its headers came), `context deadline exceeded` (the timeout), `spending cap reached` (below). |
| `provider-rejected:<kind>` alert | The provider refuses the instance's requests: 401 or 403, a wrong, expired or revoked key; 402, an empty balance (DeepSeek: `Insufficient Balance`). They are not retried: Searches degrade or partially degrade, and consolidation backs off with its batches pending (no record is dropped). Check the account's balance in the provider's console and top it up: the next call goes through, with no restart. For a new key of the account, set it in the console (设置 → 模型服务 API key): it is checked and applied without a restart ("An account's own provider keys"). For a new host key, put it into `DEEPSEEK_API_KEY=` or `TYPESAFE_API_KEY=` of `/etc/mnemon-aml/<n>/env` (and of `/etc/mnemon-aml/host.env`, which `new` copies from) with an editor, then `systemctl restart aml-serve@<n>`; it does nothing for a kind that has an account key. `mnemon_aml_model_attempts_total{outcome="rejected"}` shows the call classes affected; the alert resolves once no new rejection has been seen for 10 minutes. |
| `model spending cap reached, including unresolved reservations` | A budget is exhausted. Searches degrade to the raw ranking. A spent DeepSeek budget stops consolidation (a spent budget never drops records from the index). A spent JEV budget does not: layered consolidation goes on, but links without JEV. Raise the cap from the console (设置 → 预算上限; "Budget caps at runtime"), which applies at once with no restart, or raise `budgets` in `/etc/mnemon-aml/<n>/memory-config.json` and `systemctl restart aml-serve@<n>` (a cap of the settings file wins over it); spent amounts are kept and the new cap applies at once. Check `spent` and `held` first (`mnemon-preflight <n>`): reservations held by calls in flight count against the cap. Those a previous process left held (a crash) are settled at the next start (`settled N budget reservations a previous process left held` in the journal), so a restart clears them; there is nothing to raise the cap by. |
| Empty or poor plans, many degraded searches with a DeepSeek planner | The planner has `thinking` on. Remove it (`deepseek-flash` without thinking). |
| `model credential is unavailable` or a startup error naming a credential environment variable | A `keyEnv` variable is empty or absent in `/etc/mnemon-aml/<n>/env`. |
| `aml-serve@<n>` restarts in a loop | `journalctl -u aml-serve@<n> -n 50`: an unreadable config (`memory-config.json` must be readable by group `mnemon`), `aml-serve requires a nonempty API key in $AML_API_KEY`, `another aml-serve holds …/.aml-serve.lock`, or the public port in use. An admin address that cannot be bound is not one of them: the instance serves without it ("The admin listener is down" below). |
| The admin listener is down, and the instance serves | The log has `aml-serve: --admin-listen: listen tcp 10.77.0.3:8101: bind: cannot assign requested address; serving without the admin listener and binding it again every 5s`, once: `wg0` is down (`systemctl start wg-quick@wg0`). Searches and Adds are answered meanwhile. The instance binds the admin listener within 5 s of the address coming up and logs `aml-serve admin listening on …`. Until then the console cannot read it, `mnemon-instance new` and `resume` cannot see `/ready`, and the watcher's `ready` alert fires; it resolves by itself. Another bind error (the port in use) behaves the same, and is logged once as well. |
| The console shows an instance unreachable | From the ops host: `ping 10.77.0.3` (the overlay), then the ufw rule for `wg0` and port 8091:8600 on the instance host, then `mnemon-preflight <n>`, then the journal for a `--admin-listen` line (the instance may be up without its admin listener, above). A 401 from the admin listener means the console holds another token than the instance. |
| Alerts do not arrive | `journalctl -u aml-watch@<n>`: `not delivered` with the webhook's answer, or `AML_ALERT_WEBHOOK is empty`. Telegram answers `HTTP <status> with error_code <code>`: 400 is mostly a wrong `chat_id`, or a chat where the bot was never started; 401 a wrong token; 403 a bot that was blocked or removed from the group; 429 its rate limit (the next run sends the message again). A Feishu bot with a keyword needs `mnemon-aml`; one with signature verification rejects every message. Run the alert drill. |
| Memory grows | Lower `AML_MAX_OPEN_STORES` in the engine env and restart; `MemoryMax=` in the drop-in caps the instance (systemd kills it above, and it restarts in 5 s), and `GOMEMLIMIT`, 80% of it, makes the Go runtime collect harder before that. |

## Data deletion

An instance's data must be deleted within 30 days after its job ends. All its user data is
under its data root (records and the consolidation index). Its budget file holds metering and
the SHA-256 hashes of the model requests (no text); it goes with the instance.

```sh
mnemon-preflight <n>              # note the final spend: the budget lines
mnemon-instance delete <n>        # type the name: every user's records and indexes, the budget file,
                                  # the keys, the watcher's state and the route
find /var/lib/mnemon-aml/<n> /etc/mnemon-aml/<n> 2>/dev/null   # prints nothing
```

One user's data, with the instance running: type the user id at the script's prompt, never in a
command, so that it stays out of the shell's history ("`delete-user`", above):

```sh
mnemon-instance delete-user <n>   # User id: <type it, then Enter>
```

The journal holds no request bodies and no user ids (only eight-digit hash prefixes), so it
needs no deletion; `journalctl --rotate && journalctl --vacuum-time=1s` clears all archived
journal files if you want to. Also delete disk snapshots of the host taken during the job, or
release the host and its disks. Record the date and the commands you ran.

## Upgrade

Build the new binary, replace `/usr/local/bin/mnemon` between jobs (never in the middle of
one), then restart the instances one at a time, each after the one before is ready:

```sh
install -m 0755 -o root -g root /tmp/mnemon-linux-amd64 /usr/local/bin/mnemon
systemctl restart aml-serve@<n>
mnemon-preflight <n>              # prints the version the instance now runs
```

A data root written by one build is read by the next. A new template or script is installed
as in "Install once per host", followed by `systemctl daemon-reload` and a restart of each
instance. Install the binary before templates that pass a new flag: the templates that pass
`--provider-keys-file` need a binary that has it, and an older one refuses to start.
