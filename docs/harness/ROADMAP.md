# Mnemon Harness Roadmap

Chinese version: [ROADMAP.md](../zh/harness/ROADMAP.md)

This roadmap describes how Mnemon Harness should grow from the current MVP
loops into a broader modular-agent governance layer. It is directional, not a
fixed release schedule.

The principle is simple: build one loop at a time, keep each module useful on
its own, and avoid turning Mnemon into a replacement agent runtime.

The roadmap is memory-driven rather than module-driven. Memory is the continuity
point that lets agent experience become durable state. Other loops should
strengthen, govern, or operate around that state instead of becoming isolated
features.

## Current MVP Loops

Mnemon already has two installable MVP harness loops.

| Loop | Status | Purpose |
| --- | --- | --- |
| Memory Loop | Implemented MVP | Connects prompt-facing working memory, Mnemon long-term memory, and dreaming consolidation. |
| Skill Loop | Implemented MVP | Manages active, stale, and archived skills through evidence, curator review, and approved lifecycle changes. |

Both MVP loops use the same harness vocabulary:

- GUIDE files define loop policy.
- setup scripts mount the loop into a host agent.
- hooks inject lifecycle prompts at host-defined moments.
- protocol skills expose reusable operations.
- subagents run heavier maintenance work.
- Mnemon-owned state keeps module data outside the host runtime.

Claude Code is the first reference host because it exposes hooks, skills,
subagents, and project/user configuration. The architecture should remain
portable to other host agents with comparable extension points.

## Phase 1: Stabilize The Core Loops

Focus: make the current Memory Loop and Skill Loop dependable.

- Harden setup, uninstall, and upgrade paths.
- Improve path and environment resolution.
- Keep hook prompts short and move policy into GUIDE files.
- Add clearer reports for what each loop observed or changed.
- Validate local and project-level installation scopes.
- Keep the loops independently installable.

Success means a host agent can install memory or skill evolution separately and
understand what changed.

## Phase 2: Harness Runtime Substrate

Focus: make multiple loops easier to operate together.

This phase should introduce the minimum shared substrate needed by modules:

- module registry and version metadata
- canonical filesystem layout
- shared state, reports, proposals, and audit records
- locks, leases, queues, and background job status
- setup, uninstall, upgrade, and recovery conventions
- optional `mnemon-daemon` for scheduled maintenance

`mnemon-daemon` should be a harness maintenance runner, not an agent runtime. It
can run dreaming, curator review, eval jobs, risk scans, audit writing, and
other offline module work.

## Phase 3: Goal Loop

Focus: support long-horizon work without replacing the host agent.

A future `mnemon-goal` module should maintain durable goal state:

- objectives
- milestones
- blockers
- decisions
- handoffs
- progress reports
- stale or due goal detection

The host agent still executes the work. `mnemon-goal` coordinates surrounding
harness loops: memory recall and consolidation, skill proposal, evaluation,
risk review, human review, audit, and policy reminders.

## Phase 4: Governance Loops

Focus: add control, quality, and accountability around self-evolution.

Likely modules:

- Eval Loop: tests, benchmarks, checklists, and outcome feedback.
- Risk Loop: scan proposed memory, skill, policy, or setup changes.
- Review Loop: coordinate human approval and release gates.
- Audit Loop: record triggers, decisions, actors, changes, and outcomes.
- Policy Loop: keep host-specific constraints and permission guidance visible.

These loops should compose through explicit proposals, reports, and approval
boundaries instead of silently mutating each other's state.

## Phase 5: Portability And Replication

Focus: make harness state portable across agents, projects, and machines.

Portability work includes:

- additional host-agent setup targets
- host capability detection
- adapter-light installation guides
- import and export of harness state
- backup and restore
- replication of memory, skills, goals, proposals, reports, audit logs, and
  policy state

Replication should start conservatively with a primary-writer model, snapshots,
restore, node identity, leases or locks, conflict detection, merge proposals,
and audit records. Multi-node active-active coordination is a later design.

## Non-Goals For The Near Term

- Do not build a new general-purpose agent runtime.
- Do not implement every future loop before the core loops are stable.
- Do not require every host agent to use the same skill format.
- Do not hide self-modifying changes from review and audit.
- Do not over-engineer distributed replication before local harness state is
  solid.

Mnemon should grow loop by loop. The long-term goal is a modular harness layer
where memory, skills, goals, evaluation, risk, review, audit, policy, and
replication can evolve independently around a host agent's execution loop.
