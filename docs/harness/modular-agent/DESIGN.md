# Modular Agent Harness Design

Chinese version: [DESIGN.md](../../zh/harness/modular-agent/DESIGN.md)

Mnemon's main advantage is the modular agent model: self-evolution should be an
external harness that can attach to existing agents, not a new agent framework
that replaces them.

Mnemon does not own the agent runtime, but it does own a harness runtime
substrate. That substrate is the system layer that makes independent harness
modules installable, composable, scheduled, auditable, and safe to combine with a
host agent.

## Thesis

Any host agent that supports standard extension points can gain self-evolution
capabilities by installing Mnemon harness modules.

The host agent owns the ReAct loop:

```text
observe context -> reason -> call tools -> inspect results -> continue or stop
```

Mnemon attaches additional loops around that runtime:

```text
Memory Loop: experience -> working memory -> long-term memory -> recall
Skill Loop: repeated workflow -> evidence -> proposal -> skill lifecycle
Future Loops: evaluation, risk review, safety checks, benchmark feedback
```

The distinction is:

```text
Host Agent = execution runtime
Mnemon     = harness runtime substrate
Modules    = memory / skill / eval / risk / review / audit / policy
```

## Externalized Agent Capabilities

A major design insight is that many capabilities advertised as advanced agent
features do not require a new runtime. If the host already has a ReAct loop, the
behavioral layer around that loop can often be expressed with:

- skills or protocol documents for reusable actions
- hooks for lifecycle timing
- Markdown guides for policy, judgment, and procedure
- filesystem state for durable memory, proposals, reports, and indexes
- subagents or a daemon for heavier maintenance work

In other words, many behavior-level capabilities are:

```text
ReAct loop + skill/protocol + hook timing + Markdown policy + durable state
```

The host runtime still owns low-level execution: UI, permissions, tool routing,
sandboxing, model calls, and session management. Mnemon focuses on the
attachable behavioral layer that can be installed around that runtime.

This is why the architecture emphasizes harness modules instead of a new agent
framework. The goal is to turn advanced agent behavior into portable,
inspectable, installable modules.

However, skill, hook, and Markdown assets are not sufficient by themselves once
multiple modules need to cooperate. Mnemon needs its own substrate for:

- module registry and versioning
- canonical filesystem layout
- environment and configuration resolution
- hook binding and prompt injection boundaries
- skill projection into host-native skill surfaces
- proposal, report, audit, and state schemas
- locks, leases, queues, and background job status
- setup, uninstall, upgrade, and recovery paths
- cross-module protocols

This substrate is still not an agent runtime. It does not own the ReAct loop,
talk to users, or replace host tool routing.

## Memory-Centered Harness Layer

Mnemon's harness model is memory-driven. Durable agents should not only call
tools or follow prompts; they should turn experience into governed long-term
state and use that state to improve future behavior.

This separates Mnemon from a pure tool connectivity layer. Tool protocols help
agents reach external tools, data sources, and services. Mnemon organizes the
memory-centered governance layer around the host runtime:

```text
experience -> memory -> skills -> goals -> eval / risk / review / audit
```

Memory is the continuity point. Skill evolution depends on remembered evidence
and repeated workflows. Goal modules depend on durable objective state. Eval,
risk, review, and audit loops depend on records of decisions, changes, and
outcomes. Backup and replication protect that memory-centered harness state.

This does not mean every fact should be forced into memory. The distinction is
that memory stores agent-specific experience, preferences, decisions, failures,
skills, and long-running state. External knowledge bases, web search, and tool
retrieval remain retrieval surfaces unless their results become durable agent
state.

## Host And Harness Split

| Layer | Owner | Responsibility |
| --- | --- | --- |
| ReAct loop | Host agent | Task execution, planning, tool calls, verification, user interaction. |
| Prompt assembly | Host agent | Decides which context enters the model. |
| Tool routing | Host agent | Chooses and executes tools under the host permission model. |
| Native skills | Host agent | Discovers and invokes skills using the host's own runtime. |
| Evolution modules | Mnemon harness | Adds memory, skill evolution, evaluation, and review loops through attachable assets. |
| Canonical state | Mnemon harness | Stores durable memory, skill lifecycle state, evidence, proposals, and reports. |
| Harness substrate | Mnemon harness | Provides module registry, filesystem layout, environment, setup, projection, reports, proposals, locks, queues, and cross-module protocols. |
| Maintenance runner | Mnemon harness | Optionally schedules background module jobs without becoming an agent runtime. |

This split keeps Mnemon portable. A host can adopt one module without adopting a
new runtime.

It also prevents the opposite mistake: Mnemon should not be treated as only a
pile of Markdown skills. The harness substrate is what lets modules coordinate
without becoming a monolithic agent framework.

## Execution Plane And Governance Loops

The modular-agent model separates the host execution plane from harness
governance loops.

The host agent owns the execution plane: it runs the ReAct loop, interacts with
users, invokes tools, and decides how work is performed. Mnemon owns attachable
governance loops around that execution: memory, skill lifecycle, goal tracking,
evaluation, risk, review, audit, policy, and future backup or replication.

This is similar to the distinction between application logic and a control
plane in service systems. The application still performs the work, while the
control plane provides state, policy, observability, review, recovery, and
coordination. Mnemon should play that harness role for agents.

The implication is important: agent core execution and governance loops can
evolve independently. A host can improve its reasoning and tool execution while
Mnemon improves memory, skills, evaluation, review, audit, or replication
without mixing all of those concerns into one agent framework.

## Standard Integration Surface

| Primitive | Harness Use |
| --- | --- |
| Hooks | Install lifecycle nudges at Prime, Remind, Nudge, Compact, or equivalent host events. |
| Skills | Expose reusable protocol operations such as `memory_get`, `memory_set`, `skill_observe`, and `skill_manage`. |
| Subagents | Run heavier maintenance jobs such as dreaming and curator review outside the online task path. |
| Daemon | Optionally schedule background maintenance for installed modules. |
| Filesystem | Store canonical module state in predictable directories and project/user scopes. |
| Environment | Let protocol skills resolve paths without hard-coding a specific host agent. |

The minimal requirement is a hook-like lifecycle mechanism. Skills and subagents
make the integration cleaner, but a capable agent can also follow the Markdown
protocols directly.

## Harness Daemon

`mnemon-daemon` is the proposed harness daemon: a background maintenance runner
for installed Mnemon modules.

It is useful because some module work should not run inside the online ReAct
loop:

- dreaming for memory consolidation
- skill curator review
- evaluation jobs
- risk scans
- audit and report writing
- leases, locks, queues, and module status

The daemon is not a host agent and not a second runtime. It must not converse
with users, take over task execution, route tools for the host, or bypass
proposal and approval policy.

The intended boundary is:

```text
Host Agent      -> online task execution and user interaction
mnemon-daemon   -> offline harness maintenance and scheduled module jobs
Harness Modules -> memory, skills, eval, risk, review, audit, policy
```

For the MVP, modules can still run manually or through host hooks. The daemon
becomes important when multiple modules need shared scheduling, logs, reports,
locks, and status.

## Current Modules

| Module | Purpose | Current Reference Host |
| --- | --- | --- |
| Memory Loop | Adds working memory, long-term memory, and dreaming consolidation. | Claude Code setup under `harness/setup/install.sh --host claude-code --module memory-loop`. |
| Skill Loop | Adds active/stale/archived skill lifecycle, evidence capture, curator proposals, and approved lifecycle mutation. | Claude Code setup under `harness/setup/install.sh --host claude-code --module skill-loop`. |

## Relationship To Skill Packs

Mnemon is not primarily a skill collection.

Skill packs provide task or workflow capabilities to a host agent. For example,
a coding skill pack may teach planning, debugging, testing, review, release, or
skill-authoring workflows. Those skills are useful host-facing capabilities.

Mnemon sits at a different layer:

```text
Host Agent
  -> task/workflow skill packs
  -> Mnemon harness modules
```

Task skills help the agent do work. Mnemon harness modules help the agent manage
memory, skill lifecycle, evaluation, risk, audit, review, and policy around that
work.

The two layers should be compatible. Mnemon can observe, evaluate, curate,
archive, restore, or audit skill collections, but it should not be described as
only another skill pack.

## Memory Differentiator

The memory module uses a hot/cold memory model:

- Working memory is model-friendly. It is small Markdown context loaded into the
  prompt and maintained by the agent.
- Long-term memory is engineering-friendly. Mnemon stores larger durable memory
  outside the prompt and recalls it on demand.
- Dreaming consolidates between them by writing durable working memory into
  Mnemon and compacting or evicting the prompt-facing working memory.

This keeps the best part of Markdown memory while avoiding the capacity ceiling
of a single always-loaded file.

## Future Modules

The same harness pattern can support more loops:

- Eval loop: collect outcomes, run benchmarks, and feed failures into proposals.
- Risk loop: scan proposed skill or memory changes before they become active.
- Review loop: coordinate human approval, checkpoints, and release gates.
- Audit loop: record which module acted, why it acted, and what changed.
- Policy loop: maintain host-specific safety and permission guidance.
- Backup / replication loop: preserve and restore harness state across machines,
  nodes, or host-agent environments.

Each module should remain independently installable. Modules may optionally use
`mnemon-daemon` for background scheduling, but should not require it for the
basic install path.

Backup and replication should start conservatively. The first useful shape is a
primary-writer model with snapshots, restore, node identity, leases or locks,
conflict detection, merge proposals, and audit records. Multi-node active-active
coordination can remain a later design.

## Composable Module Flow

Harness modules should compose through explicit state and proposal boundaries,
not by silently calling each other.

Example:

```text
Skill Loop produces a skill proposal
  -> Risk Loop scans the proposal
  -> Review Loop requests approval
  -> Audit Loop records the decision
  -> Skill Loop applies the approved change
```

The same pattern can apply to memory consolidation, policy updates, benchmark
failures, or host setup changes. A module may create evidence or a proposal;
another module may review, scan, approve, or record it. The host agent remains
the runtime that decides when to invoke these capabilities.

## Long-Horizon Goal Modules

A future `mnemon-goal` module can use this architecture to support long-horizon
agent work without becoming a task runtime itself.

`mnemon-goal` would maintain objective state, milestones, blockers, decisions,
handoffs, and progress reports. Around a long-running goal, it can repeatedly
coordinate other harness modules:

- Memory Loop recalls context at the start and preserves durable decisions after
  milestones.
- Skill Loop observes repeated workflows and proposes reusable skills.
- Eval Loop checks milestone quality with tests, benchmarks, or checklists.
- Risk Loop scans dangerous changes before execution or application.
- Review Loop requests approval for key proposals or high-impact steps.
- Audit Loop records triggers, decisions, changes, and outcomes.
- Policy Loop keeps project constraints and user preferences visible.
- `mnemon-daemon` can detect stale, blocked, or due goals and schedule
  maintenance jobs.

This makes `mnemon-goal` an orchestrating harness module: it coordinates
memory, skills, evaluation, risk, review, audit, and policy around a durable
objective while the host agent continues to execute the actual work.

## Non-Goals

- Do not replace the host agent runtime.
- Do not let `mnemon-daemon` become an agent runtime.
- Do not reduce Mnemon to only a skill pack or prompt collection.
- Do not require one universal skill format.
- Do not inject all state into the prompt.
- Do not make self-modifying changes without explicit policy and review.

## Reference Case

Claude Code is the first modular-agent case because it currently exposes one of
the most complete combinations of hooks, skills, subagents, filesystem
configuration, and project/user scopes.

That makes Claude Code a strong experimental mount point for Mnemon harness
modules:

- hooks can carry Prime, Remind, Nudge, Compact, and future module triggers
- skills can expose portable protocol operations
- subagents can run dreaming, curator review, and other maintenance work
- project and user config can validate local and global install scopes
- settings files can make setup and uninstall repeatable

Claude Code is a reference host, not the only supported runtime. Its role is to
validate the harness attachment model. The architecture should remain portable
to any host agent with comparable extension points.
