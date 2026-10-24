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
  order (the deployment's `view.order: "time"`; without it, in rank order). There is no
  fusion with the plain search ranking, so a record JEV did not judge is not delivered.
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
  no embedding service runs.
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
| State (mnemon 0700) | `/var/lib/mnemon-aml/<n>/` (`StateDirectory=mnemon-aml/%i`): `data/`, the data root, and `model-budget.db`, the budget file |
| Watcher state (mnemon 0700) | `/var/lib/mnemon-aml-watch/<n>/state.json` (`StateDirectory=mnemon-aml-watch/%i`) |
| Units | `aml-serve@<n>.service`, `aml-watch@<n>.service` and `aml-watch@<n>.timer` |
| Limits | `/etc/systemd/system/aml-serve@<n>.service.d/limits.conf`: `MemoryMax=` and `CPUQuota=` only. The flag values live in the engine env, so the template's `ExecStart` is the same for every instance. |
| Route | dedicated: the host's Caddyfile. Shared: `/etc/caddy/mnemon-instances/<n>.caddy` |

The engine env is the instance's record: `mnemon-instance` keeps the layout, the slot and
the status (`active` or `suspended`) there, and nothing else keeps state.

**Ports follow from a slot.** `mnemon-instance new` takes the lowest slot no instance's env
file uses.

| Layout | Slot | Data listener | Admin listener |
| --- | --- | --- | --- |
| dedicated | 0 | `127.0.0.1:8090` | `<overlay address>:8091` |
| shared | 0 to 49 | `127.0.0.1:(8100+10s)` | `<overlay address>:(8101+10s)`, so 8101 to 8591 |

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

The dedicated numbers are provisional: the load test on the evaluation host (Task 12 of the
serve-hardening plan) replaces them with measured values. The evaluation host has 48 GB, so
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
200 GB (16 open stores of up to about 744 MB each); the eval host is larger.

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
`wg-quick@wg0.service`. An instance that starts while `wg0` is down cannot bind its admin
listener, exits, and restarts every 5 seconds until the address is there (troubleshooting,
below).

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
   install -m 0644 -o root -g root /tmp/aml-serve@.service /tmp/aml-watch@.service /tmp/aml-watch@.timer /etc/systemd/system/
   systemctl daemon-reload
   systemd-analyze verify /etc/systemd/system/aml-serve@.service /etc/systemd/system/aml-watch@.service /etc/systemd/system/aml-watch@.timer
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
   the new route, the snippet is removed again, so that the host's Caddyfile stays valid), and
   runs `mnemon-preflight <n>`;
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
writes its files atomically, prints a secret only in the one-time output of `new` and
`rotate-key`, and logs one line per action to the journal (`journalctl -t mnemon-instance`),
naming the action and the instance, never a secret or a raw user id. One `mnemon-instance`
changes a host at a time: it holds `/etc/mnemon-aml/.mnemon-instance.lock`, a directory, while
it runs; if one is left behind by a killed run, remove it. The console's maintenance panel
prints these commands for an instance.

### `list`

```sh
mnemon-instance list
```

```
NAME                             LAYOUT     SLOT  DATA             ADMIN                  STATUS     PUBLIC URL
beta-alice                       shared        0  127.0.0.1:8100   10.77.0.3:8101         active     https://beta.example.com/i/beta-alice
```

It reads the env files and prints no secret. The status is the record's, not systemd's:
`systemctl status aml-serve@<n>` shows the process.

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
off). `resume` of an instance that is not suspended never stops it. The watcher's next run
reports the restart (`restart`, resolved 10 minutes later).

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

**Moving an account to its own provider keys** is manual too: replace the two lines
`DEEPSEEK_API_KEY=` and `TYPESAFE_API_KEY=` in `/etc/mnemon-aml/<n>/env` with the account's
keys, with an editor, and `systemctl restart aml-serve@<n>`. The instance's keys live only in
its own env file; `host.env` is only where `new` copies them from.

**Changing caps or limits** of an existing instance: `budgets` in
`/etc/mnemon-aml/<n>/memory-config.json` (restart; the spent amounts are kept), `MemoryMax=` or
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
- `aml-serve@<n>`, `aml-watch@<n>.timer` and `caddy` are active;
- `systemd-analyze verify` passes on the templates, and `caddy validate` on the Caddyfile, when
  the tools are installed;
- `wg0` is up and carries the address of `AML_ADMIN_LISTEN`;
- ufw is active, denies incoming by default, and has the rules of "Firewall", and no rule lets
  anyone reach 51820/udp or an admin port (8091 to 8600/tcp), over IPv4 or IPv6, on any
  interface, by one port, a range or a list: each such rule is a `FAIL` line that names it.

Every check runs; each failure prints one line that starts with `FAIL`, and the exit status is
1 if any failed. It never prints a secret: the admin token reaches curl only as a config on its
standard input, never as an argument. It needs `curl` and `jq`, and says so if they are
missing.

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
| `view` | `maxItems 100`, `maxCharacters 240000`, `order "time"` | `top_k` above `maxItems` is clamped to it, never an error. The platform's formal `top_k` is 100. There is no `fusion`: the View is the judged list (JEV's shortlist of at most 64 records and at most 8 linked from the consolidation blocks), so a normal Search stays well under both caps. `order "time"` lists the records after the consolidation blocks in time order, records of one time in the order they were written; selection and the cut at `top_k` go by rank. `"fusion": "rrf"` (not set) would fill the View up to the caps from the plain search ranking. |
| `consolidation` | `{"mode": "layered"}` | DeepSeek extracts each batch alone, then JEV links it to the memory so far (see "Background consolidation"). It needs the `judge` and a completion model (the `consolidator`, else the planner). Other fields keep their defaults: 10,000-character batches, at most 16 batches per job, 4 extractions in flight (`parallel`), 8 stored candidates per linked item (`linkCandidates`). A user's records waiting to be folded are bounded by 16 × 10,000 characters; later records wait unread, losing nothing. `{"mode": "timelines"}` is the reference's sequential fold: the state and the reads are the same. `insights` is refused in background mode. `nativeEdges`, `edgeKinds`, `view.expand` and `index` (memory-service options) are not set. |
| `planner` | DeepSeek Flash, **no thinking** | Do not add `"thinking": true`. Cue planning is capped at 400 output tokens and need planning at 200; a reasoning model can spend them on reasoning and return an empty plan, which makes Search degrade. |
| `judge` | `jev` / `jev-1.13.0` | The version is pinned; the endpoint is JEV's official one. It judges the reads and, in layered mode, links the consolidation items, so its budget pays for both. |
| `consolidator` | DeepSeek Flash, no thinking, `timeoutSeconds` 600 | Each extraction asks for at most 8,192 output tokens whatever `maxTokens` says. The extraction of a full batch can take minutes, so its calls may run 600 s (`timeoutSeconds`, 1–1,800; models without it time out after 90 s). Unset, the planner would extract. |
| `budgetFile` | `/var/lib/mnemon-aml/<n>/model-budget.db` | Absolute, private (0600), one per instance, shared by all users of the instance. Two profiles with different caps on one file would overwrite each other's stored cap. |
| `budgets` | evaluation `{"jev": 60, "deepseek": 600}`, beta `{"jev": 3, "deepseek": 30}` | **Hard caps on spending**, in USD for `jev` and `openai` and CNY for `deepseek`: check them before a job. When a cap is reached the model calls stop (see Troubleshooting). Every call holds a reservation while it runs: USD 0.01 for JEV, CNY 0.25 for DeepSeek, more for a large fold (up to about CNY 0.46 for the largest request). A call that never connected or got a non-2xx answer releases it; a call that failed after sending, returned an unreadable reply or was cut by a restart keeps it held, so the usable headroom is a little below the cap. The configured value replaces the stored cap when the service starts; the amount already spent is kept. The `openai` cap is a soft one: a call that costs more than its reservation is recorded at its real price. |

A Search can make several planner calls and up to 128 JEV decision calls (the read budget).
Layered consolidation makes one DeepSeek extraction call per batch (more when a reply must
be asked again or split) and, once the user's memory holds anything to link to, a few JEV
calls per batch: one question per candidate for each new topic (up to 8 stored topics) and
two per candidate for each new fact (up to 8 stored facts), packed 128 questions to a call.
Both happen in the background workers and before a user's next Search, so the `jev` cap pays
for consolidation as well as for Searches. Estimate a job's spend from a trial run on a
scratch instance (`mnemon-preflight` prints the budgets) before choosing the caps.

## Background consolidation

- **Adds return after persistence.** An Add writes the user's records, makes them searchable
  by words, notifies the scheduler and answers; it calls no model. The workers
  (`--consolidation-workers`, `AML_WORKERS`) fold the full batches, one job per user at a time.
- **Bounded lag.** An Add waits while its user's backlog is above the lag bound, one job's
  window: 160,000 characters with the shipped configuration (16 batches of 10,000), the
  `lagBound` of `/admin/status`. It keeps its admission slot while it waits, so declare the AML
  concurrency at or below `--max-concurrent-adds` and `--max-concurrent-searches`. An Add waits
  at most until 25 minutes after it arrived; then it answers 200 all the same (its records are
  stored and searchable), counted in `addOutran` and logged as `add outran consolidation`.
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
  searchable. A failed job backs off, from 30 s doubling up to 10 minutes; a job stopped by the
  budget reserve pauses for 10 minutes.
- **Insights (L2) are refused** in background mode: `consolidation.insights` fails the startup.
- **Two-phase shutdown.** On SIGTERM the public listener closes and the scheduler drains: no
  job starts, no Add waits for it. Requests in flight and running jobs have 30 s together;
  then the requests still running are canceled (they answer 503, which the platform retries
  with the same `request_id`) and the jobs still running are cut (their saved batches stay,
  and they resume after a restart); the canceled requests have 5 s more. The stores and models
  close within the same 35 s, inside `TimeoutStopSec=60`. A model call cut by a stop keeps
  its reservation held for good; check `held` (`mnemon-preflight` prints it) and raise the
  cap by that much if the headroom gets tight.
- **One `aml-serve` per data root.** It takes an exclusive lock on `<data-root>/.aml-serve.lock`
  and refuses to run while another process holds it.
- **`--max-open-stores`.** Every open user store keeps its query cache, which holds the
  records it searches: a BEAM-10M-scale user (about 98,500 records) holds about 744 MB when
  its store is open, measured with BM25 only. Set the limit to the memory you can spare
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

- `Type=notify` and `NotifyAccess=main`: `READY=1` once both listeners are bound and the
  background work runs (its startup scan queued every user, its heartbeat ticks); `STOPPING=1`
  when the shutdown starts. There is no "ready" log line: `systemctl status` shows the instance
  `active (running)`.
- `WatchdogSec=60`: aml-serve pings every half interval while its liveness probe passes (the
  in-process `/health` answers 200, the background work's heartbeat ticked within the interval,
  and the budget file answers); systemd restarts an instance whose pings stop. aml-serve refuses
  a watchdog interval under 2 s. The probe calls no model, so a slow provider never looks like
  a dead process; `/ready` reports those.
- `Restart=always`, `RestartSec=5`, and `StartLimitIntervalSec=0` in `[Unit]`, so a crash loop
  keeps retrying every 5 s.
- `TimeoutStopSec=60`, which outlasts the 35 s shutdown.
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
internet reaches it. Read it by hand on the host without putting the token on a command line:

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
  nanoseconds, `maxBacklog` in characters), `lagBound`, `breakers` and `breakerTrips` by provider
  kind, `budgets` (always an array of `kind`, `cap`, `spent`, `held`), `openStores` and `version`.
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
| `mnemon_aml_model_attempts_total{kind,class,outcome}` | model call attempts: class `interactive`, `flush` or `background`; outcome `ok`, `retryable`, `failed`, `breaker-open`, `bulkhead-full` or `budget` |
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
| `mnemon_aml_budget_spent`, `_held`, `_cap{account}` | each paid account, in its currency |
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

## Logs

The instance logs to the journal: `journalctl -u aml-serve@<n> -f`. The template passes
`--log-format json`, one JSON object per line (`time`, `level`, `msg`, and `request_id` on the
lines of a request); `--log-format text` writes the same as `key=value` text. The `msg` keeps
the wordings below. One line per Add and Search (`POST /search 200 83ms`); health checks are not
logged. Request bodies and user ids are never logged; a user appears as the first eight hex
digits of its store hash, `(user xxxxxxxx)`.

| Line | Meaning |
|---|---|
| `aml-serve listening on 127.0.0.1:8100`, `aml-serve admin listening on 10.77.0.3:8101` | Startup: the listeners are bound. `READY=1` follows once the background work runs. |
| `POST /add 200 41ms` | One line per request, with `request_id`. |
| `add shed: no free slot within 30s (limit 4)`, `search shed: …` | A request found no free slot for 30 s and got 503 with `Retry-After`. |
| `degraded search N (user xxxxxxxx): <error>` | Search could not use the models and answered with the raw BM25 ranking (HTTP 200). `N` counts degraded searches since the process started. `context deadline exceeded` means the read ran past `--search-timeout`. |
| `degraded search kept rank order (user xxxxxxxx): <error>` | A degraded Search could not read the write positions of its records (a store error), so it delivered the raw ranking in rank order instead of time order. |
| `partially degraded search N (user …): judge, layer` | The read answered, but the named stages fell back: `judge` (JEV's final judgement failed; its screen stood in), `scan-budget` or `loop-budget` (the read's JEV budget ran out), `layer-state`, `layer` or `expand` (the consolidation layer was left out or not judged), `order` (the write positions could not be read). |
| `status: adds A, searches S, degraded D, partially degraded P; budget deepseek spent … held … cap …, jev …` | Every 10 minutes: the Adds and Searches answered since the start, the degraded ones, and each budget account in use. |
| `records query preparation: … (user xxxxxxxx)` | A user's store failed to build its words index or its query snapshot in the background (5 minutes at most); a later write, or the store's next opening, tries again. |
| `add outran consolidation (user …)` | An Add waited out its 25-minute window at the lag bound and answered 200; its records are stored and searchable. |
| `consolidation retries in 1m0s (user …)` | A background job failed; the user backs off (30 s doubling to 10 minutes). |
| `consolidation paused: budget reserve (user …)` | The budget's reserve stopped a background job; it pauses for 10 minutes. |
| `consolidation queue full: N users have work; dropped a notification (user …)` | The scheduler tracks at most 100,000 users with work; the user's next Add or Search picks the work up. Logged at most once a minute. |
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
| `aml-serve: the 30s grace has ended with requests still running; canceling them` | Requests still ran at the end of the grace; they answer 503. |
| `aml-serve: requests still running 5s after they were canceled; closing their connections` | The canceled requests did not end. |
| `aml-serve: closing the stores and models did not finish by the end of the shutdown; exiting while model requests are in flight: their budget reservations stay held` | The shutdown's end came first. |

A failed aml-serve or aml-watch run writes its error once, as one line, with no usage block:
`journalctl -u aml-serve@<n> -n 50` shows why an instance would not start.

The watcher's lines (`journalctl -u aml-watch@<n>`): `aml-watch: posted` (with `alert` and
`event`), `aml-watch: not delivered` (with the reason), `aml-watch: AML_ALERT_WEBHOOK is empty:
not posting` (with the message), `aml-watch: another aml-watch run holds the lock; this run does
nothing`, `aml-watch: could not take the run lock; going on without it`, `aml-watch: the public
health probe is off because --public-url is empty`, and `aml-watch: the state file is unreadable
or of another version; starting afresh`.

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
| `budget-high:<kind>` | `budgets` | spent and held are above 80% of the cap |
| `budget-held:<kind>` | `budgets` | held reservations are above 2% of the cap |
| `backlog-age` | `queue.oldestQueued` | the oldest work not yet consolidated has waited more than 15 minutes |
| `backlog-lag` | `queue.maxBacklog`, `lagBound` | the largest backlog has been above the lag bound for 15 minutes |
| `consolidation-dropped` | `dropped` | a batch was dropped from the consolidation layer |
| `link-fallback` | `linkFallbacks` | a batch was linked without JEV |
| `add-outran` | `addOutran` | an Add waited out its window for consolidation |

- **Hold and repeat.** An alert about something that happens (`restart`, `degraded-search`,
  `breaker-open`, `consolidation-dropped`, `link-fallback`, `add-outran`) keeps firing for 10
  minutes after the last run that saw it happen, so a stream of events is one alert, not a
  pair of messages per event. Any firing alert is said again 30 minutes after its last
  message. The first run (no state file) takes the counters as they are and says nothing
  about them. When the status cannot be read, the alerts that come from it are neither
  repeated nor resolved: `ready` says what is wrong.
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
dedicated instance on the evaluation host, never on `eval`, before the submission; do not
submit the placeholders. The declared concurrency must stay at or below the instance's
`--max-concurrent-adds` and `--max-concurrent-searches`.

To measure Search latency at concurrency `C` over `N` users that already hold data, on the
host: set `n` (the scratch instance), `BASE` and the `aml` function as in "Health and smoke
test", which keeps the key off the command lines, then:

```sh
export -f aml; export n BASE
C=4; N=40
seq 1 "$N" | xargs -P "$C" -I{} bash -c 'aml /search "{\"query\":\"What did the user say about their dog?\",\"user_id\":\"u{}\",\"top_k\":100}" \
  -s -o /dev/null -w "%{http_code} %{time_total}\n"' > search.txt
awk '{n[$1]++} END{for (c in n) print "status", c, n[c]}' search.txt
sort -k2 -n search.txt | awk '{t[NR]=$2} END{print "p50", t[int(NR*0.5+0.5)], "p95", t[int(NR*0.95+0.5)]}'
```

Raise `C` until the p95 or the share of non-200 answers gets worse, and note the largest `C`
that stays acceptable. Watch the process memory (`systemctl status aml-serve@$n`), the 503
count, `mnemon_aml_shed_total` and `degraded search` lines in the journal. Do the same for Add
with distinct `request_id` and `user_id` values. Adds return after persistence, but at the lag
bound they wait for the workers, which call DeepSeek (up to four extractions at once per job)
and JEV (linking): measure Adds that complete full batches, and note the concurrency at which
the providers answer `model HTTP 429`.

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
| `degraded search …` in the log | The planner, JEV or a budget failed for that question, or the read ran past `--search-timeout`. The platform still got a 200 with the raw ranking. Read the error after the colon: `model HTTP 401` (a wrong or expired key), `model HTTP 429` (provider rate limit), `model endpoint unreachable` (network or DNS; nothing was sent), `request failed` (network, after sending), `context deadline exceeded` (the timeout), `spending cap reached` (below). |
| `model spending cap reached, including unresolved reservations` | A budget is exhausted. Searches degrade to the raw ranking. A spent DeepSeek budget stops consolidation (a spent budget never drops records from the index). A spent JEV budget does not: layered consolidation goes on, but links without JEV. Raise `budgets` in `/etc/mnemon-aml/<n>/memory-config.json` and `systemctl restart aml-serve@<n>`; spent amounts are kept and the new cap applies at once. Check `spent` and `held` first (`mnemon-preflight <n>`): reservations held by calls that failed after sending, or were cut by a restart, count against the cap. |
| Empty or poor plans, many degraded searches with a DeepSeek planner | The planner has `thinking` on. Remove it (`deepseek-flash` without thinking). |
| `model credential is unavailable` or a startup error naming a credential environment variable | A `keyEnv` variable is empty or absent in `/etc/mnemon-aml/<n>/env`. |
| `aml-serve@<n>` restarts in a loop | `journalctl -u aml-serve@<n> -n 50`: an unreadable config (`memory-config.json` must be readable by group `mnemon`), `aml-serve requires a nonempty API key in $AML_API_KEY`, `another aml-serve holds …/.aml-serve.lock`, a port in use, or `--admin-listen: listen tcp 10.77.0.3:8101: bind: cannot assign requested address`: `wg0` is down (`systemctl start wg-quick@wg0`; the instance binds once it is up). |
| The console shows an instance unreachable | From the ops host: `ping 10.77.0.3` (the overlay), then the ufw rule for `wg0` and port 8091:8600 on the instance host, then `mnemon-preflight <n>`. A 401 from the admin listener means the console holds another token than the instance. |
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
instance.
