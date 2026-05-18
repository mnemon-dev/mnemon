# Loop Module Standard

Chinese version: [LOOP_MODULE_STANDARD.md](../zh/harness/LOOP_MODULE_STANDARD.md)

This document defines the standard structure for Mnemon harness loop modules.
The standard is host-agnostic. Concrete hosts such as Claude Code, Codex,
OpenClaw, or future runtimes consume the same loop module through host-specific
projection adapters.

## Core Model

Mnemon separates canonical harness state from host runtime projection:

```text
.mnemon canonical state
    |
    | projected by a host adapter
    v
.claude / .codex / other host config
    |
    v
host runtime
```

The loop module owns policy, lifecycle reminders, protocol skills, maintenance
agents, environment contracts, and setup adapters. The host runtime owns the
conversation loop, prompt assembly, tool routing, native skill discovery,
permission model, and UI.

## Standard Directory

Every installable loop module should follow this shape:

```text
harness/modules/<loop-name>/
├── README.md
├── module.json
├── env.sh
├── GUIDE.md
├── hooks/
│   ├── prime.md
│   ├── remind.md
│   ├── nudge.md
│   └── compact.md
├── skills/
│   └── <protocol-skill>.md
├── subagents/
│   └── <maintenance-agent>.md
```

Host-specific projection logic lives outside modules:

```text
harness/hosts/<host>/
├── projector.sh
├── templates/
└── scripts/
```

Shared setup entrypoints compose modules and hosts:

```text
harness/setup/
├── install.sh
├── status.sh
└── uninstall.sh
```

Loop-specific runtime files may be added when they are part of the loop
contract, such as `MEMORY.md` for the Memory Loop.

## Concepts

| Concept | Required | Role |
| --- | --- | --- |
| `module.json` | Yes | Machine-readable loop identity, declared assets, state directories, lifecycle events, and supported host adapters. |
| `GUIDE.md` | Yes | Policy for when the loop should act, what the host agent should consider, and what remains out of scope. |
| `env.sh` | Yes | Runtime path contract for scripts, hooks, protocol skills, and maintenance agents. |
| `hooks/*.md` | Yes | Host-agnostic lifecycle reminders. They describe what the agent should consider at a lifecycle boundary. |
| `skills/*.md` | Usually | Protocol skills for reusable online operations. These define procedures, not host-specific installation. |
| `subagents/*.md` | Optional | Maintenance roles for heavier review, consolidation, or proposal generation. Hosts without native subagents may run them as manual or scheduled jobs. |
| `harness/hosts/<host>/` | At least one host overall | Host-specific projection adapter that installs or removes modules from a host runtime. |

## Lifecycle Events

Mnemon standardizes lifecycle vocabulary so different hosts can map their native
extension points to the same loop semantics.

| Event | Meaning | Typical Use |
| --- | --- | --- |
| `prime` | Session or runtime start. | Make loop policy, important state, and active surfaces visible. |
| `remind` | User request or task boundary. | Decide whether recall, observation, or other loop action could change the task. |
| `nudge` | Turn end or work completion. | Decide whether durable writeback, evidence capture, or report generation is justified. |
| `compact` | Context compaction or checkpoint boundary. | Preserve critical continuity and trigger maintenance when state is oversized or stale. |
| `maintenance` | Offline or explicit maintenance job. | Run heavier consolidation, curator review, evaluation, audit, or proposal work. |

Adapters may degrade gracefully. If a host lacks an exact hook, it can map the
event to the closest lifecycle boundary or expose it through an app-server eval
API.

## Host Projection

A host projection adapter renders the canonical loop module into a host-native
surface. Projection must not create a second source of truth.

```text
canonical loop module
    |
    | install / project
    v
host-native files
```

Typical responsibilities:

- Resolve canonical `.mnemon` and project-local paths.
- Copy or reference loop assets.
- Render host-readable skills, hooks, and configuration.
- Register native lifecycle hooks when the host supports them.
- Write a host manifest under `.mnemon/hosts/<host>/`.
- Preserve canonical state during uninstall unless explicitly requested.

## Canonical State

The canonical state belongs under `.mnemon`, not under a host-specific directory.
Host directories such as `.claude` or `.codex` contain projections only.

Recommended layout:

```text
.mnemon/
├── data/
│   └── <store>/mnemon.db
├── harness/
│   ├── memory-loop/
│   └── skill-loop/
├── reports/
├── proposals/
├── audit/
├── hosts/
│   ├── claude-code/
│   │   └── manifest.json
│   └── codex/
│       └── manifest.json
└── manifest.json
```

Current MVP setup scripts may still place runtime files inside host config
directories. New adapters should move toward the canonical `.mnemon` layout and
use host directories only as projection surfaces.

## Manifest Schema

Each loop module should include a `module.json` file with this stable shape:

```json
{
  "schema_version": 1,
  "name": "memory-loop",
  "version": "0.1.0",
  "description": "Connects prompt-facing working memory with Mnemon long-term memory.",
  "lifecycle_events": ["prime", "remind", "nudge", "compact"],
  "assets": {
    "guide": "GUIDE.md",
    "env": "env.sh",
    "hooks": {
      "prime": "hooks/prime.md",
      "remind": "hooks/remind.md",
      "nudge": "hooks/nudge.md",
      "compact": "hooks/compact.md"
    },
    "skills": ["skills/memory_get.md", "skills/memory_set.md"],
    "subagents": ["subagents/dreaming.md"]
  },
  "state": {
    "canonical": [".mnemon/data", ".mnemon/reports", ".mnemon/proposals", ".mnemon/audit"],
    "loop_runtime": []
  },
  "host_adapters": {
    "claude-code": "../../hosts/claude-code"
  }
}
```

The manifest is descriptive in the MVP. Later setup tooling can validate loop
modules, generate projections, and drive app-server evals from this metadata.

## Adapter Mapping

The same standard concepts map differently across hosts:

| Loop Standard | Claude Code Projection | Codex Projection |
| --- | --- | --- |
| `GUIDE.md` | Prompt guide or skill guidance visible to Claude Code. | Codex instruction or skill guidance visible to Codex. |
| `hooks/prime.md` | Session-start hook. | Session init hook or app-server lifecycle endpoint. |
| `hooks/remind.md` | User-prompt hook. | Request or message boundary hook. |
| `hooks/nudge.md` | Stop or turn-end hook. | Turn-end hook or app-server lifecycle endpoint. |
| `hooks/compact.md` | Pre-compact hook. | Compact, checkpoint, or explicit eval lifecycle endpoint. |
| `skills/*.md` | `.claude/skills` projection. | `.codex/skills` or Codex skill surface projection. |
| `subagents/*.md` | Native subagent projection when available. | Codex subagent, task adapter, or maintenance job. |
| `env.sh` | Sourced by hook scripts and injected into context. | Sourced by Codex adapter and app-server eval runtime. |

## Quality Rules

- Keep loop modules host-agnostic by default.
- Keep host-specific code under `setup/<host>/`.
- Do not duplicate canonical state into host directories.
- Treat host directories as projections that can be regenerated.
- Keep setup, status, and uninstall behavior explicit and auditable.
- Preserve user state on uninstall unless a destructive flag is explicit.
- Document English and Chinese behavior together when adding or changing public
  harness concepts.
