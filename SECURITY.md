# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Mnemon, please report it responsibly:

1. **Do NOT open a public GitHub issue.**
2. Use [GitHub Security Advisories](https://github.com/mnemon-dev/mnemon/security/advisories/new) to report privately.
3. Include steps to reproduce, affected versions, and potential impact.

We will acknowledge receipt within 48 hours and aim to release a fix within 7 days for critical issues.

## Scope

The Memory CLI runs locally and stores data in `~/.mnemon/`. The AML adapter is a network
service; its model is under "Network service" below. Key security considerations for local use:

- **SQLite database** — contains all stored insights; protected by filesystem permissions (`0644`).
- **Hook scripts** — shell scripts executed by the LLM CLI at lifecycle events; written with `0755` permissions.
- **Embedding provider connection** — optional requests send insight or query text to the configured Ollama or OpenAI-compatible server. The default local Ollama endpoint does not use TLS. If `MNEMON_EMBED_ENDPOINT` points outside a trusted local network, use HTTPS to protect content and any `MNEMON_EMBED_API_KEY` bearer token in transit.

### Network service (memory cloud instances)

`mnemon memory-agent aml-serve` is a network service: one engine instance per account of the
memory cloud ([runbook](docs/aml-adapter.md)). Its security model:

- **Data plane.** `POST /add` and `POST /search` require the instance's API key
  (`Authorization: Bearer`, `Token`, or `X-Api-Key`), compared in constant time. The data
  listener binds loopback; Caddy terminates TLS in front of it. `GET /health` needs no key and
  says nothing but `{"status":"ok"}`. Each user has a separate store; one user cannot reach
  another's records.
- **Admin plane.** Metrics, readiness, status, the store overview and the profiler are served
  on a separate admin listener, never on the data listener. It binds a loopback address, or a
  private one only together with an admin token, which every admin request must carry as a
  bearer token ([Admin API](docs/admin-api.md)); any other address is refused at startup.
- **Private network.** The instances' admin listeners bind their hosts' WireGuard overlay
  addresses. The console on the ops host reaches them over that overlay only, and the instance
  hosts' firewalls admit the admin ports only from the hub, on the overlay interface. No admin
  port listens on a public address.
- **Secrets.** API keys, admin tokens, provider keys and alert webhooks live in root-only
  (0600) environment files on the instance host; the console keeps the admin tokens in its own
  0600 registry and only a fingerprint of each API key. An API key, an admin token or a provider
  key never appears in a URL, a log line, a metric label or an error. An alert webhook is a URL
  by design (a Telegram bot's token, or a Feishu or WeCom hook's key, is in its path or its
  query), so it is never printed, logged or passed as an argument. The host scripts pass the
  other secrets to programs only on standard input, never as arguments.
- **Data handling.** Request bodies and user ids are never logged; a user appears only as the
  first 8 hex digits of its store's hash. An instance's data is removed with
  `mnemon-instance delete`, one user's with `mnemon-instance delete-user`.

## Supported Versions

| Version | Supported |
|---|---|
| Latest release | Yes |
| Older releases | Best effort |
