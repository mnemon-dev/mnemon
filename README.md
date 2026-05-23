<p align="center">
  <img src="docs/logo/logo.svg" width="160" height="160" alt="Mnemon Logo" />
</p>

# Mnemon

**English** | [中文](docs/zh/README.md)

**LLM-supervised persistent memory for AI agents.**

[![Go 1.24+](https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![CI](https://github.com/mnemon-dev/mnemon/actions/workflows/ci.yml/badge.svg)](https://github.com/mnemon-dev/mnemon/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/mnemon-dev/mnemon)](https://goreportcard.com/report/github.com/mnemon-dev/mnemon)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)

---

LLM agents forget everything between sessions. Context compaction drops critical decisions, cross-session knowledge vanishes, and long conversations push early information out of the window.

Mnemon gives your agent persistent, cross-session memory — a four-graph knowledge store with intent-aware recall, importance decay, and automatic deduplication. Single binary, zero API keys, one setup command.

For the broader harness direction, Mnemon is an event-sourced lifecycle layer
for agents you already use. It does not replace Codex, Claude Code, OpenClaw,
or future hosts; it adds governed memory, skill, eval, proposal, and audit
lifecycles around them.

> **Claude Max / Pro subscriber?** Mnemon works entirely through your existing subscription — no separate API key required. Your LLM subscription *is* the intelligence layer. Two commands and you're done.

### Why Mnemon?

Most memory tools embed their own LLM inside the pipeline. Mnemon takes a different approach: **your host LLM is the supervisor.** The binary handles deterministic computation (storage, graph indexing, search, decay); the LLM makes judgment calls (what to remember, how to link, when to forget). No middleman, no extra inference cost.

| Pattern | LLM Role | Representative |
|---|---|---|
| **LLM-Embedded** | Executor inside the pipeline | Mem0, Letta |
| **File Injection** | None — reads file at session start | Claude Code Memory |
| **MCP Server** | Tool provider via MCP protocol | claude-mem |
| **LLM-Supervised** | External supervisor of a standalone binary | **Mnemon** |

Mnemon also addresses a gap in the protocol stack. MCP standardizes how LLMs discover and invoke tools. ODBC/JDBC standardizes how applications access databases. But how LLMs interact with databases using memory semantics — this layer has no protocol. Mnemon's three primitives — `remember`, `link`, `recall` — form an intent-native protocol: command names map to the LLM's cognitive vocabulary (`remember` not INSERT, `recall` not SELECT), and output is structured JSON with signal transparency rather than raw database rows.

<p align="center">
  <img src="docs/diagrams/llm-supervised-concept.jpg" width="720" alt="LLM-Supervised Architecture — three patterns compared, with Mnemon hooks, protocol boundary, and deterministic memory engine" />
  <br />
  <sub>The LLM-Supervised pattern: hooks drive the lifecycle, the host LLM makes judgment calls, the binary handles deterministic computation.</sub>
</p>

Memory has a **compound interest effect** — the longer it accumulates, the greater its value. LLM engines iterate constantly, skill files cost nearly nothing to write, but memory is a private asset that grows with the user. It is the only component in the agent ecosystem worth deep investment.

<p align="center">
  <img src="docs/diagrams/10-knowledge-graph.jpg" width="720" alt="Knowledge Graph — 87 insights connected by temporal, entity, semantic, and causal edges" />
  <br />
  <sub>A real knowledge graph built by Mnemon — 87 insights, 2150 edges across four graph types.</sub>
</p>

See [Design & Architecture](docs/DESIGN.md) for details.

## Quick Start

### Install

**Homebrew** (macOS / Linux):

```bash
brew install mnemon-dev/tap/mnemon
```

**Go install**:

```bash
go install github.com/mnemon-dev/mnemon@latest
```

**From source**:

```bash
git clone https://github.com/mnemon-dev/mnemon.git && cd mnemon
make install
```

**Verify installation**:

```bash
mnemon --version
```

### Claude Code

```bash
mnemon setup
```

`mnemon setup` auto-detects Claude Code, then interactively deploys skill, hooks, and behavioral guide. Start a new session — memory just works.

### [Codex](https://github.com/openai/codex)

```bash
mnemon setup --target codex --yes
```

One command deploys the mnemon skill, prompt files, and Codex lifecycle hooks
(`SessionStart`, `UserPromptSubmit`, `Stop`) in `.codex/hooks.json`.

### [OpenClaw](https://github.com/openclaw/openclaw)

```bash
mnemon setup --target openclaw --yes
```

One command deploys skill, hook, plugin, and behavioral guide to `~/.openclaw/`. Restart the OpenClaw gateway to activate.

### [NanoClaw](https://github.com/qwibitai/nanoclaw)

NanoClaw runs agents inside Linux containers. Use the `/add-mnemon` skill to integrate:

1. Install mnemon on the host (see above)
2. In your NanoClaw project, run `/add-mnemon` — Claude Code will modify the Dockerfile, add a container skill, and set up volume mounts
3. Each WhatsApp group gets its own isolated memory store, with optional global shared memory (read-only)

The skill is available at `.claude/skills/add-mnemon/` in the NanoClaw repo.

### [Nanobot](https://github.com/HKUDS/nanobot)

```bash
mnemon setup --target nanobot --global --yes
```

One command writes a skill file to `~/.nanobot/workspace/skills/mnemon/SKILL.md`. Memory is shared across all Nanobot sessions and projects. Use `--global` (recommended) because Nanobot discovers skills from the global workspace directory.

### Uninstall

```bash
mnemon setup --eject
```

## How it works

Once set up, memory operates through a lightweight harness: `SKILL.md` teaches
commands, `GUIDELINE.md` teaches judgment, hooks remind the agent at lifecycle
boundaries, and the `mnemon` binary executes deterministic memory operations.
Supported setup commands automate this, but the harness is installable from
markdown alone.

```text
Session starts
    |
    v
  Prime   -> make skill, guideline, and active store visible
    |
    v
User prompt arrives
    |
    v
  Remind  -> decide whether recall could change this task
    |
    v
Agent works and calls Mnemon only when useful
    |
    v
  Nudge   -> decide whether durable writeback is justified
    |
    v
Before context compaction
    |
    v
  Compact -> preserve only critical continuity
```

The four hook phases are reminders, not a hard workflow. **Prime** makes the
skill, guideline, and active store visible. **Remind** prompts a recall
decision. **Nudge** prompts a writeback decision. **Compact** preserves only
critical continuity before context compression.

You don't run mnemon commands yourself. The agent does when the guideline says
memory is useful.

## Features

- **Zero user-side operation** — install once; supported runtimes can use hooks, minimal runtimes can use persistent rules
- **LLM-supervised** — the host LLM decides what to remember, update, and forget; no embedded LLM, no API keys
- **Multi-framework support** — Claude Code (hooks), OpenClaw (plugins), Nanobot (skills), and more
- **Markdown-installable harness** — `SKILL.md`, `INSTALL.md`, `GUIDELINE.md`, and four lifecycle reminders
- **Four-graph architecture** — temporal, entity, causal, and semantic edges, not just vector similarity
- **Intent-native protocol** — three primitives (`remember`, `link`, `recall`) map to the LLM's cognitive vocabulary, not database syntax; structured JSON output with signal transparency
- **Intent-aware recall** — graph traversal + optional vector search (RRF fusion), enabled by default for all queries
- **Built-in deduplication** — `remember` auto-detects duplicates and conflicts; skips or auto-replaces
- **Retention lifecycle** — importance decay, access-count boosting, and garbage collection
- **Privacy-safe receipts** — export hashed operation receipts for memory-boundary audits without raw memory contents or queries
- **Optional embeddings** — works fully without Ollama; add local [Ollama](https://ollama.ai) for enhanced vector+keyword hybrid search

## Vision

All your local agentic AIs — across sessions and frameworks — sharing one pool of live memory.

```
  Claude Code ──┐
                │
  OpenClaw ─────┤
                │
  Nanobot ──────┤
                │
  NanoClaw ─────┤
                ├──▶  ~/.mnemon  ◀── shared memory
  OpenCode ─────┤
                │
  Gemini CLI ───┘
```

The foundation is in place: a single `~/.mnemon` database that any agent can
read and write. Claude Code setup automates hook installation; OpenClaw can use
plugin hooks; Nanobot integrates via skill files; NanoClaw integrates via
container skills and volume mounts. The same harness can be installed in any
LLM CLI that supports skills, rules, system prompts, or event hooks.

The longer-term direction is a **memory gateway**: protocol decoupled from storage engine. The current SQLite backend is the first adapter; the protocol surface (`remember / link / recall`) can sit on top of PostgreSQL, Neo4j, or any graph database. Agent-side optimization (when to recall, what to remember) and storage-side optimization (indexing, graph algorithms) evolve independently. See [Future Direction](docs/design/08-decisions.md#82-future-direction) for details.

## FAQ

**Do different sessions share memory?**
Yes. By default, all sessions use the same `default` store — a decision remembered in one session is available in every future session.

**Can I isolate memory per project or agent?**
Yes. Use named stores to separate memory:

```bash
mnemon store create work        # create a new store
mnemon store set work           # set as default
MNEMON_STORE=work mnemon recall "query"  # or use env var per-process
```

Different agents/processes can use different stores via the `MNEMON_STORE` environment variable — no global state contention.

**Local or global mode?**
`mnemon setup` defaults to **local** (project-scoped `.claude/`), recommended for most users. **Global** (`mnemon setup --global`, installed to `~/.claude/`) activates mnemon across all projects — convenient if you want other frameworks (e.g., OpenClaw) to share memory by forwarding requests through Claude Code CLI, but may add maintenance overhead.

**How do I customize the behavior?**
Edit the generated guideline (`~/.mnemon/prompt/guide.md` in current setup
flows) or use the installable [memory loop GUIDE](harness/loops/memory/GUIDE.md)
as the source. The skill file should stay focused on command syntax.

**What is sub-agent delegation?**
Sub-agent delegation is optional. When a runtime supports it, the main agent can
decide *what* to remember and ask a cheaper or isolated worker to execute
`mnemon remember`. It is a useful execution strategy, not a required part of the
Mnemon architecture.

## Configuration

| Environment Variable | Default | Description |
|---|---|---|
| `MNEMON_DATA_DIR` | `~/.mnemon` | Base data directory |
| `MNEMON_STORE` | *(active file or `default`)* | Named memory store for data isolation |

**Ollama-specific** (only relevant if using embeddings):

| Environment Variable | Default | Description |
|---|---|---|
| `MNEMON_EMBED_ENDPOINT` | `http://localhost:11434` | Ollama API endpoint |
| `MNEMON_EMBED_MODEL` | `nomic-embed-text` | Embedding model name |

## Development

```bash
make build          # build binary
make install        # build + install to $GOBIN
make test           # run E2E test suite
mnemon setup        # interactive setup
mnemon setup --eject  # remove all integrations
make help           # show all targets
```

**Dependencies**: Go 1.24+, `modernc.org/sqlite`, `spf13/cobra`, `google/uuid`

See [Development and Deployment](docs/DEPLOYMENT.md) for Docker, Compose, Ollama embedding, and release setup.

## Documentation

- [Modular Self-Evolution Harness](docs/harness/README.md) — formal harness docs for modular agent, memory loop, and skill loop design
- [Memory Loop Harness](harness/loops/memory/README.md) — installable memory loop assets
- [Skill Loop Harness](harness/loops/skill/README.md) — installable skill loop assets
- [Design & Architecture](docs/DESIGN.md) — current engine architecture, algorithms, integration design
- [Usage & Reference](docs/USAGE.md) — CLI commands, embedding support, architecture overview
- [Architecture Diagrams](docs/diagrams/) — system architecture, pipelines, lifecycle management

## Star History

<a href="https://www.star-history.com/?repos=mnemon-dev%2Fmnemon&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=mnemon-dev/mnemon&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=mnemon-dev/mnemon&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=mnemon-dev/mnemon&type=date&legend=top-left" />
 </picture>
</a>

## References

Mnemon combines the paradigm of one paper with the methodology of another, grounded in the structural insight that graph memory is isomorphic to LLM attention. See [Theoretical Foundations](docs/DESIGN.md#25-theoretical-foundations) for details.

- **RLM** — Zhang, Kraska & Khattab. [Recursive Language Models](https://arxiv.org/abs/2512.24601). 2025. Establishes the paradigm: LLMs are more effective as orchestrators of external environments than as direct data processors.
- **MAGMA** — Zou et al. [A Multi-Graph based Agentic Memory Architecture](https://arxiv.org/abs/2601.03236). 2025. Provides the methodology: four-graph model (temporal, entity, causal, semantic) with intent-adaptive retrieval.
- **Graph-LLM Structural Insight** — Joshi & Zhu. [Building Powerful GNNs from Transformers](https://arxiv.org/abs/2506.22084). 2025; and the Graph-based Agent Memory survey (Chang Yang et al., 2026). Confirms that LLM attention is computationally equivalent to GNN operations — graph memory is a structural match, not an engineering convenience.

## License

[Apache-2.0](LICENSE)
