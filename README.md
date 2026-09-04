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

Mnemon gives your agent persistent, cross-session memory — a four-graph knowledge store with intent-aware recall, importance decay, and automatic deduplication. The `mnemon` memory path remains one local binary with zero API keys and one setup command.

Mnemon ships one executable with two separate surfaces. Memory stays at the
`mnemon` root; [Agency Preview](docs/AGENCY.md) lives at `mnemon agency ...` and adds
durable, project-local responsibility and effect admission to an existing Pi
agent. Agency does not replace Memory or the Agent Runtime.

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

**npm** (recommended; macOS / Linux / Windows, Node.js 22+):

```bash
npm install --global @mnemon-dev/mnemon
```

Upgrade the npm-managed CLI at any time:

```bash
mnemon update
```

The npm package installs the matching native Go executable for the host OS and
CPU. Mnemon's engine remains a single native binary; Node.js is used only by
the npm launcher and package manager.

**Alternative installers**:

```bash
brew install --cask mnemon-dev/tap/mnemon
go install github.com/mnemon-dev/mnemon@latest
```

Homebrew, `go install`, source builds, and other Node package managers must
continue to use their original installation method. To migrate one of these
installations, run the npm install command once and ensure the npm global bin
directory precedes the old executable on `PATH`; subsequent `mnemon update`
calls are npm-managed.

Windows supports the core Memory commands. Agency remains unavailable on
Windows until its local authority boundary has native Windows security.

**From source** (macOS / Linux):

```bash
git clone https://github.com/mnemon-dev/mnemon.git && cd mnemon
make install
```

**Verify installation**:

```bash
mnemon --version
mnemon agency --version
```

### Agency (Preview · Pi-first)

```bash
mnemon agency setup --runtime pi --project-root .
```

Set up each project once, then use Pi normally. Agency is available on macOS
and Linux and remains independent from Memory: `mnemon setup --target pi --yes`
enables Memory, while the command above enables Agency. See the
[Agency guide](docs/AGENCY.md) for its operating model, Preview compatibility
boundary, and optional peers.

### [Claude Code](https://github.com/anthropics/claude-code)

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

### [Cursor](https://cursor.com/)

```bash
mnemon setup --target cursor --yes
```

One command deploys the mnemon skill, prompt files, and Cursor lifecycle hooks
to `.cursor/`. The integration primes new agent sessions with Mnemon guidance
and memory status, then nudges for durable-memory writeback after responses.

### [ZCode](https://zcode.z.ai/)

```bash
mnemon setup --target zcode --global --yes
```

ZCode installs the Mnemon skill under `~/.zcode/skills/` and registers
user-level lifecycle hooks in `~/.zcode/cli/config.json`. The hooks prime new
sessions, add recall guidance before model calls, and prompt for durable-memory
writeback at stop. Without `--global`, setup installs only the project skill;
ZCode currently ignores project-level hook configuration.

### [MiniMax Code](https://github.com/MiniMax-AI/minimax-code)

```bash
mnemon setup --target minimax-code --yes
```

One command deploys the Mnemon skill to
`.minimax/skills/mnemon/SKILL.md`. Add `--global` to use
`~/.minimax/skills/mnemon/SKILL.md` across projects. Current MiniMax Code
releases discover both roots natively. The integration is intentionally
skill-only: in MiniMax Code 3.0.65, the local Agent V2 path does not dispatch
the user-prompt lifecycle hook required for dependable automatic recall.

### [TRAE](https://www.trae.ai/) (TRAE Work)

```bash
mnemon setup --target trae --yes
```

One command deploys the mnemon skill, prompt files, and TRAE native hooks for
both TRAE IDE and TRAE Work to `.trae/`. The integration uses `SessionStart`,
`UserPromptSubmit`, and `Stop` hooks in `.trae/hooks.json`.

### [Qoder](https://qoder.com/) (QoderWork)

```bash
mnemon setup --target qoder --yes
mnemon setup --target qoderwork --yes
```

Qoder deploys the mnemon skill, prompt files, and native hooks to `.qoder/`
or `~/.qoder/`. QoderWork uses its native user config at `~/.qoderwork/`.
Both integrations register `SessionStart`, `UserPromptSubmit`, and `Stop`
hooks in `settings.json`.

### [CodeBuddy](https://www.codebuddy.cn/)

```bash
mnemon setup --target codebuddy --yes
```

CodeBuddy deploys the mnemon skill, prompt files, and native hooks to
`.codebuddy/` or `~/.codebuddy/`. The integration registers `SessionStart`,
`UserPromptSubmit`, and `Stop` hooks in `settings.json`.

### [WorkBuddy](https://www.codebuddy.cn/work/)

```bash
mnemon setup --target workbuddy --yes
```

WorkBuddy deploys the mnemon skill, prompt files, and native hooks to
`.workbuddy/` or `~/.workbuddy/`. The integration registers `SessionStart`,
`UserPromptSubmit`, and `Stop` hooks in `settings.json`.

### [Kimi Code](https://github.com/MoonshotAI/kimi-code)

```bash
mnemon setup --target kimi --yes
```

Kimi Code deploys the mnemon skill, prompt files, and native lifecycle hooks to
`~/.kimi-code/` or `$KIMI_CODE_HOME/`. The integration registers
`SessionStart`, `UserPromptSubmit`, and `Stop` hooks in `config.toml`.

### [OpenCode](https://opencode.ai/)

```bash
mnemon setup --target opencode --yes
```

OpenCode deploys the mnemon skill to `.opencode/skills/`, registers the
generated guide through `opencode.json` instructions, and installs a native
plugin in `.opencode/plugins/`. The plugin injects recall context before chat
requests and adds Mnemon guidance to session compaction.

### [OpenClaw](https://github.com/openclaw/openclaw)

```bash
mnemon setup --target openclaw --yes
```

One command deploys skill, hook, plugin, and behavioral guide to `~/.openclaw/`. Restart the OpenClaw gateway to activate.

### [Pi](https://pi.dev)

```bash
mnemon setup --target pi --yes
```

One command deploys the mnemon skill, prompt files, and a Pi TypeScript extension
to `.pi/`. The extension maps Mnemon's lifecycle reminders onto Pi events
(`resources_discover`, `before_agent_start`, `agent_end`,
`session_before_compact`). Start a new Pi session or run `/reload` to activate.

### [Hermes Agent](https://github.com/NousResearch/hermes-agent)

```bash
mnemon setup --target hermes --yes
```

One command deploys the mnemon skill, prompt files, and Hermes shell hooks to
`~/.hermes/`. The integration uses Hermes' native lifecycle hooks:
`on_session_start`, `pre_llm_call`, `post_llm_call`, and optional
`on_session_finalize`. Hermes may prompt once to approve the installed shell
hooks.

### [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness)

DeepSeek Harness (DSH) integrates through the [dsh-mnemon](https://github.com/omdsh-dev/dsh-mnemon) plugin, which layers DSH's runtime memory, managed project documents, and Mnemon's long-term memory spaces into one supervised three-tier memory system.

With `mnemon` installed on the host (see [Install](#install)), add the plugin and restart your DSH Web profile:

```bash
dsh plugin --profile web add dsh-mnemon
dsh --profile web
```

The Mnemon repository is also a direct GitHub installation source. Unreleased
plugin builds can still be installed from the dedicated repository, and local
development checkouts use an absolute path:

```bash
dsh plugin --profile web add github:mnemon-dev/mnemon
dsh plugin --profile web add "github:omdsh-dev/dsh-mnemon"
dsh plugin --profile web add "link:/absolute/path/to/dsh-mnemon"
```

New installations from the Mnemon repository resolve the `latest` npm release
of `dsh-mnemon`, so publishing a new plugin release does not require a matching
change in this repository. Existing installations remain on their resolved
version until the plugin is reinstalled or updated.

Then open DSH's Settings → Plugin Config → Mnemon to pick a storage scope, and use the Memory System tab in a session to create or activate memory spaces. Recall reads only from active memory spaces; durable writes go through supervised sub-agents.

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

Once set up, Memory operates through lightweight runtime projections: a
runtime-specific `SKILL.md` teaches commands, a shared `guide.md` (by default
`~/.mnemon/prompt/guide.md`) carries judgment guidance, and native hooks or
extensions surface reminders at supported lifecycle boundaries. The `mnemon`
binary executes deterministic memory operations, while `mnemon setup` installs
the closest native mapping for each supported runtime.

```text
Session starts
    |
    v
  Prime   -> make skill, guide, and active store visible
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
skill, guide, and active store visible. **Remind** prompts a recall
decision. **Nudge** prompts a writeback decision. **Compact** preserves only
critical continuity before context compression.

You don't run mnemon commands yourself. The agent does when the guide says
memory is useful.

## Features

- **Zero user-side operation** — install once; supported runtimes can use hooks, minimal runtimes can use persistent rules
- **LLM-supervised** — the host LLM decides what to remember, update, and forget; no embedded LLM, no API keys
- **Multi-framework support** — Claude Code, Codex, Cursor, ZCode, TRAE/TRAE Work, Qoder/QoderWork, CodeBuddy, WorkBuddy, Kimi Code, OpenCode, and Hermes Agent (hooks/plugins), OpenClaw (plugins), Pi (extensions), MiniMax Code and Nanobot (skills), DeepSeek Harness (via the dsh-mnemon plugin), and more
- **Runtime-native integration** — runtime-specific `SKILL.md`, shared `guide.md`, and supported hooks or extensions
- **Four-graph architecture** — temporal, entity, causal, and semantic edges, not just vector similarity
- **Intent-native protocol** — three primitives (`remember`, `link`, `recall`) map to the LLM's cognitive vocabulary, not database syntax; structured JSON output with signal transparency
- **Intent-aware recall** — graph traversal + optional vector search (RRF fusion), enabled by default for all queries
- **Built-in deduplication** — `remember` auto-detects duplicates and conflicts; skips or auto-replaces
- **Retention lifecycle** — importance decay, access-count boosting, and garbage collection
- **Privacy-safe receipts** — export hashed operation receipts for memory-boundary audits without raw memory contents or queries
- **Optional embeddings** — works fully without an embedding provider; add local [Ollama](https://ollama.ai) or an OpenAI-compatible server for enhanced vector+keyword hybrid search

## Vision

All your local agentic AIs — across sessions and frameworks — sharing one pool of live memory.

```
  Claude Code ───────┐
                     │
  Codex ─────────────┤
                     │
  Cursor ────────────┤
                     │
  ZCode ─────────────┤
                     │
  MiniMax Code ──────┤
                     │
  TRAE ──────────────┤
                     │
  TRAE Work ─────────┤
                     │
  Qoder ─────────────┤
                     │
  QoderWork ─────────┤
                     │
  CodeBuddy ─────────┤
                     │
  WorkBuddy ─────────┤
                     │
  Kimi Code ─────────┤
                     │
  Hermes Agent ──────┤
                     │
  DeepSeek Harness ──┤
                     │
  OpenClaw ──────────┤
                     │
  Pi ────────────────┤
                     │
  Nanobot ───────────┤
                     │
  NanoClaw ──────────┤
                     ├──▶  ~/.mnemon  ◀── shared memory
  OpenCode ──────────┤
                     │
  Gemini CLI ────────┘
```

The foundation is in place: a single `~/.mnemon` database that any agent can
read and write. Claude Code, Codex, Cursor, ZCode, TRAE/TRAE Work, Qoder/QoderWork,
CodeBuddy, WorkBuddy, Kimi Code, OpenCode, and Hermes Agent setup automate hook/plugin installation;
OpenClaw can use plugin hooks; Pi integrates via native skills and TypeScript
lifecycle extensions; MiniMax Code and Nanobot integrate via skill files; NanoClaw integrates
via container skills and volume mounts. The same integration bundle can be installed in any
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
flows). Skill files should stay focused on command syntax.

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

**Retention**:

| Environment Variable | Default | Description |
|---|---|---|
| `MNEMON_MAX_INSIGHTS` | `1000` | Active-insight ceiling; `0` disables automatic pruning |
| `MNEMON_AUTO_PRUNE_MIN_AGE` | `24h` | Grace period before an insight can be auto-pruned; accepts `24h`, `7d`, or `0` |

Each automatic deletion is soft, appears in the oplog as a `prune` operation, and is
reported by ID in the triggering command's `auto_pruned_ids` field.

**Embedding** (only relevant if using embeddings):

| Environment Variable | Default | Description |
|---|---|---|
| `MNEMON_EMBED_ENDPOINT` | `http://localhost:11434` | Embedding API endpoint |
| `MNEMON_EMBED_MODEL` | `nomic-embed-text` | Embedding model name |
| `MNEMON_EMBED_PROTOCOL` | *(auto-detect)* | `ollama` or `openai`; auto-detected from an endpoint ending in `/v1` |
| `MNEMON_EMBED_API_KEY` | *(none)* | Bearer token for OpenAI-compatible servers (oMLX, vLLM, etc.) |
| `MNEMON_EMBED_DIMENSIONS` | *(native)* | Optional Matryoshka dimension truncation |

The embedding client speaks the Ollama API by default and the
OpenAI-compatible embeddings API when the endpoint ends in `/v1` (or when
`MNEMON_EMBED_PROTOCOL=openai` is set). OpenAI-compatible servers are
normally probed via their `models` route; servers that do not serve that
route (e.g. [Voyage AI](https://docs.voyageai.com)) are detected via an
embeddings round-trip instead. For example, a local server such as
[oMLX](https://omlx.dev) can be configured with:

```bash
export MNEMON_EMBED_ENDPOINT=http://127.0.0.1:18000/v1
export MNEMON_EMBED_MODEL=bge-m3-mlx-8bit
export MNEMON_EMBED_API_KEY=sk-... # omit for keyless local servers
mnemon embed --status
```

A hosted provider such as Voyage AI needs only the endpoint, model, and key:

```bash
export MNEMON_EMBED_ENDPOINT=https://api.voyageai.com/v1
export MNEMON_EMBED_MODEL=voyage-3.5
export MNEMON_EMBED_API_KEY=pa-...
mnemon embed --status
```

## Development

```bash
make build          # build the single mnemon executable
make install        # build + install to $GOBIN
make test           # run deterministic CI tests
make test-integration  # opt-in CLI E2E and Agency boundary tests
mnemon setup        # interactive setup
mnemon setup --eject  # remove all integrations
make help           # show all targets
```

**Dependencies**: Go 1.24+, `modernc.org/sqlite`, `spf13/cobra`, `google/uuid`

See [Development and Deployment](docs/DEPLOYMENT.md) for Docker, Compose, Ollama embedding, and release setup.

## Documentation

- [Agency Preview](docs/AGENCY.md) — maturity boundary, Pi setup, operating model, completion semantics, and optional peers
- [Go Engineering Standard](docs/development/go-engineering-standard.md) — maintainability, concurrency, persistence, testing, and review thresholds
- [Design & Architecture](docs/DESIGN.md) — current engine architecture, algorithms, integration design
- [Memory Usage & Reference](docs/USAGE.md) — root Memory commands, import, receipts, and embedding support
- [Memory Import Guide](docs/IMPORT.md) — schema and LLM prompt for importing historical chats
- [Architecture Diagrams](docs/diagrams/) — system architecture, pipelines, lifecycle management

## Star History

<a href="https://star-history.dera.page/#mnemon-dev/mnemon">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://star-history.dera.page/svg?repos=mnemon-dev/mnemon&theme=dark" />
   <source media="(prefers-color-scheme: light)" srcset="https://star-history.dera.page/svg?repos=mnemon-dev/mnemon" />
   <img alt="Star History Chart" src="https://star-history.dera.page/svg?repos=mnemon-dev/mnemon" />
 </picture>
</a>

## References

Mnemon combines the paradigm of one paper with the methodology of another, grounded in the structural insight that graph memory is isomorphic to LLM attention. See [Theoretical Foundations](docs/DESIGN.md#25-theoretical-foundations) for details.

- **RLM** — Zhang, Kraska & Khattab. [Recursive Language Models](https://arxiv.org/abs/2512.24601). 2025. Establishes the paradigm: LLMs are more effective as orchestrators of external environments than as direct data processors.
- **MAGMA** — Zou et al. [A Multi-Graph based Agentic Memory Architecture](https://arxiv.org/abs/2601.03236). 2025. Provides the methodology: four-graph model (temporal, entity, causal, semantic) with intent-adaptive retrieval.
- **Graph-LLM Structural Insight** — Joshi & Zhu. [Building Powerful GNNs from Transformers](https://arxiv.org/abs/2506.22084). 2025; and the Graph-based Agent Memory survey (Chang Yang et al., 2026). Confirms that LLM attention is computationally equivalent to GNN operations — graph memory is a structural match, not an engineering convenience.

## License

Copyright 2026 Grivn and Mnemon contributors.

[Apache-2.0](LICENSE)

The bracketed copyright example near the end of `LICENSE` is part of Apache
2.0's standard application appendix; this section carries the project's actual
copyright notice.
