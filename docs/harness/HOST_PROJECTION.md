# Host Projection

Chinese version: [HOST_PROJECTION.md](../zh/harness/HOST_PROJECTION.md)

This document defines how a Mnemon loop module is projected into a concrete
host runtime such as Claude Code, Codex, OpenClaw, or a future app-server eval
host.

The loop module standard defines the canonical package shape. Host projection
defines how that package becomes visible and executable inside a host runtime.

## Principle

Mnemon keeps canonical harness state in `.mnemon`. Host directories contain
projections that can be regenerated.

```text
.mnemon/
  canonical state, loop modules, reports, proposals, audit
      |
      | projected by setup/<host>
      v
.claude/ or .codex/
  host-readable skills, hooks, config, and pointers back to .mnemon
      |
      v
host runtime
```

The projection adapter should not create an independent copy of truth. It should
render enough host-native files for the host to discover and use the loop while
keeping durable state under `.mnemon`.

## Responsibilities

A host projection adapter owns these responsibilities:

| Responsibility | Description |
| --- | --- |
| Path resolution | Resolve project root, host config directory, canonical `.mnemon`, active store, and loop module path. |
| Asset projection | Render or copy host-readable GUIDE, hooks, protocol skills, and subagents. |
| Hook registration | Register host lifecycle hooks when the host supports them. |
| Environment injection | Make `MNEMON_DATA_DIR`, `MNEMON_STORE`, `MNEMON_HARNESS_DIR`, and loop-specific env visible to hooks and skills. |
| Manifest writing | Record what was projected and where under `.mnemon/hosts/<host>/manifest.json`. |
| Validation | Detect missing assets, stale projections, incompatible host capabilities, and path conflicts. |
| Uninstall | Remove host projection files while preserving canonical `.mnemon` state by default. |

## Non-Responsibilities

A host projection adapter should not:

- Reimplement Mnemon memory storage or retrieval.
- Move canonical state into `.claude`, `.codex`, or another host directory.
- Hide host-specific behavior inside loop module root files.
- Mutate user-owned host config outside declared projection sections.
- Delete memory, reports, proposals, or audit records unless the user explicitly
  requests destructive cleanup.

## Canonical Layout

The target canonical layout is:

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

Current MVP scripts may still place loop runtime files in host config
directories. New projection adapters should move toward this canonical layout
and keep host directories as generated views.

## Projection Layouts

### Claude Code

Claude Code projection uses the host's native skill, hook, subagent, and
settings surfaces.

```text
.claude/
├── skills/
│   └── <projected protocol skills>
├── hooks/
│   └── <projected hook entrypoints>
├── agents/
│   └── <projected subagents>
└── settings.json
```

Claude Code projection should:

- Register lifecycle hooks in `settings.json`.
- Keep generated hook entrypoints small.
- Source Mnemon env files from the canonical `.mnemon` location when possible.
- Keep policy in `GUIDE.md` and hook prompts, not in shell glue.

### Codex

Codex projection should follow the same canonical model while rendering into
Codex-native surfaces.

```text
.codex/
├── skills/
│   └── <projected protocol skills>
├── hooks/
│   └── <projected lifecycle adapters, when supported>
├── agents/
│   └── <projected maintenance agents, when supported>
└── config/
    └── <runtime or app-server config>
```

Codex projection should:

- Project protocol skills into the Codex skill surface.
- Map lifecycle events to Codex hooks when available.
- Use app-server lifecycle endpoints as a fallback when direct hooks are not
  available.
- Pass canonical `.mnemon` paths into the app server and skills through env or
  runtime config.
- Write eval artifacts under `.mnemon/reports`, `.mnemon/proposals`, and
  `.mnemon/audit`.

Exact Codex paths may evolve with Codex host capabilities. The adapter should
record its chosen paths in `.mnemon/hosts/codex/manifest.json`.

## Lifecycle Mapping

Host adapters map Mnemon lifecycle events to native host events:

| Mnemon Event | Claude Code Projection | Codex Projection | Fallback |
| --- | --- | --- | --- |
| `prime` | Session start hook. | Session init hook or app-server session start. | Explicit `/lifecycle/prime` eval call. |
| `remind` | User prompt hook. | Request or message boundary hook. | Explicit `/lifecycle/remind` eval call. |
| `nudge` | Stop or turn-end hook. | Turn-end hook or response finalization. | Explicit `/lifecycle/nudge` eval call. |
| `compact` | Pre-compact hook. | Compact, checkpoint, or context-save event. | Explicit `/lifecycle/compact` eval call. |
| `maintenance` | Subagent or manual task. | Subagent, background task, or app-server job. | Explicit maintenance command. |

The mapping is semantic, not necessarily one-to-one. If a host cannot supply an
exact lifecycle event, the adapter should choose the closest safe boundary and
document it in the host manifest.

## Host Manifest

Every projection should write a host manifest:

```text
.mnemon/hosts/<host>/manifest.json
```

Recommended shape:

```json
{
  "schema_version": 1,
  "host": "codex",
  "installed_at": "2026-05-14T00:00:00Z",
  "project_root": "/path/to/project",
  "mnemon_dir": "/path/to/project/.mnemon",
  "store": "default",
  "loops": {
    "memory-loop": {
      "module_path": ".mnemon/harness/memory-loop",
      "module_version": "0.1.0",
      "projection_path": ".codex",
      "projected_assets": {
        "skills": [".codex/skills/memory_get.md"],
        "hooks": [".codex/hooks/prime.sh"],
        "subagents": []
      },
      "lifecycle_mapping": {
        "prime": "session-init",
        "remind": "message-boundary",
        "nudge": "turn-end",
        "compact": "explicit-eval"
      }
    }
  }
}
```

The manifest is the bridge between setup, status, uninstall, and eval tooling.

## Setup Contract

All host adapters should support the same high-level operations:

```text
install
  validate loop module manifests
  resolve canonical .mnemon
  install canonical loop assets if needed
  render host projection
  register hooks/config
  write host manifest

status
  read host manifest
  validate projected files exist
  validate registered hooks/config
  report stale or missing projections

uninstall
  remove projected host files
  unregister hooks/config
  preserve canonical .mnemon state by default
  update or remove host manifest
```

The `status` operation is important for app-server evals because it lets the
orchestrator verify that a run is testing the intended projection.

## App-Server Eval Host

An app-server eval host is a disposable host runtime used for testing loop
behavior. It should use the same projection contract as real hosts:

```text
eval orchestrator
    |
    | create isolated workspace and .mnemon
    | run setup/<host>/install
    | start host app server
    v
host app server
    |
    | API-driven scenarios
    v
harness loop projection
    |
    v
Mnemon engine and canonical state
```

Eval should test host behavior under harness influence, not only Mnemon CLI
CRUD. Useful assertions include:

- The app server uses the isolated `.mnemon`.
- The expected loop module versions are installed.
- Lifecycle events are invoked through the declared mapping.
- Recall decisions affect later task behavior.
- Writeback decisions create durable memory only when justified.
- Reports, proposals, and audit records are written to canonical locations.

## Quality Rules

- Projection files should be small and generated from canonical assets.
- Host-specific behavior belongs in `setup/<host>/` or generated adapter files.
- Setup should be repeatable and idempotent where practical.
- Uninstall should be conservative and preserve canonical state.
- Manifest paths should be relative when possible and absolute when required for
  runtime execution.
- Public projection behavior must be documented in both English and Chinese.

