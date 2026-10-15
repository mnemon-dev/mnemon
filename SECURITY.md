# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Mnemon, please report it responsibly:

1. **Do NOT open a public GitHub issue.**
2. Use [GitHub Security Advisories](https://github.com/mnemon-dev/mnemon/security/advisories/new) to report privately.
3. Include steps to reproduce, affected versions, and potential impact.

We will acknowledge receipt within 48 hours and aim to release a fix within 7 days for critical issues.

## Scope

Mnemon runs locally and stores data in `~/.mnemon/`. Key security considerations:

- **SQLite database** — contains all stored insights; protected by filesystem permissions (`0644`).
- **Hook scripts** — shell scripts executed by the LLM CLI at lifecycle events; written with `0755` permissions.
- **Embedding provider connection** — optional requests send insight or query text to the configured Ollama or OpenAI-compatible server. The default local Ollama endpoint does not use TLS. If `MNEMON_EMBED_ENDPOINT` points outside a trusted local network, use HTTPS to protect content and any `MNEMON_EMBED_API_KEY` bearer token in transit.
- **Decision-model judge connection** — only when `--judge` is passed and `MNEMON_JUDGE_MODEL` is set, `recall` and `remember` send the query and the text of the judged memories to `MNEMON_JUDGE_ENDPOINT`. A cloud endpoint therefore receives that text. The default local Ollama endpoint does not use TLS; use HTTPS for any other endpoint to protect content and the `MNEMON_JUDGE_API_KEY` bearer token.

## Supported Versions

| Version | Supported |
|---|---|
| Latest release | Yes |
| Older releases | Best effort |
