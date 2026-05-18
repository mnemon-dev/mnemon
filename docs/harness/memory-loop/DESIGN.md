# Memory Loop MVP Design

Related visualization: [site.html](../../site/memory-loop/site.html)

Chinese version: [DESIGN.md](../../zh/harness/memory-loop/DESIGN.md)

Installable MVP assets: [harness/modules/memory-loop](../../../harness/modules/memory-loop/README.md)

The memory loop is the first practical slice of the self-evolution harness. It gives a host agent a prompt-facing working memory while using Mnemon as durable long-term memory. The harness stays small: it installs Markdown policy, hook prompts, protocol skills, and one maintenance subagent around an existing host agent.

## Design Goal

The MVP should answer one question: how can a host agent remember useful information across work without becoming a custom agent runtime?

The answer is a two-layer memory loop:

- `MEMORY.md` is working memory. It is small, readable by the model, and loaded into the prompt.
- Mnemon is long-term memory. It stores more information than the prompt can carry and is accessed through recall/write protocols.
- Dreaming is consolidation. It moves durable material from working memory into Mnemon, then compacts or evicts working memory.

This keeps online behavior simple while preserving a path to durable memory.

## Hot/Cold Memory Boundary

The memory loop intentionally separates LLM-native memory from system-native
memory.

`MEMORY.md` is hot memory. It is model-friendly and eagerly loaded into the
prompt, so it has the best behavioral effect. It is also expensive: it consumes
context, attention, and prompt budget, and it can become noisy if it grows
without quota and consolidation.

Mnemon is cold memory. It is system-friendly: durable, indexed, queryable,
cheap to keep, and efficient for scattered long-term recall. It is less
model-native because recalled material must be selected before entering the
prompt. That trade-off is acceptable because cold memory gives the agent much
larger capacity and lower online cost.

A computer memory analogy is useful:

```text
MEMORY.md -> RAM / cache
Mnemon    -> indexed disk / durable store
Dreaming  -> writeback + compaction + eviction
Recall    -> page-in / retrieval into context
```

The loop should keep high-frequency, high-confidence, currently useful context
in working memory. Lower-frequency history, scattered facts, decisions, and
experience should live in Mnemon until a focused recall brings them back.

This boundary is a pattern, not a fixed implementation pair. In the MVP,
`MEMORY.md` represents the hot memory implementation and Mnemon represents the
cold memory implementation. Future work can improve either side:

- model-driven filesystem memory, layered Markdown, structured prompt memory,
  or agent-maintained notes improve the hot, LLM-native side;
- RAG-enhanced storage, vector indexes, graph memory, hybrid retrieval, or
  stronger episodic/semantic stores improve the cold, system-native side;
- better dreaming, promotion, demotion, compaction, and eviction improve the
  exchange protocol between the two.

The memory-loop contract is therefore:

```text
LLM-native hot memory
  <-> consolidation / promotion / demotion
System-native cold memory
```

`MEMORY.md` and Mnemon are the first concrete choices for this contract, not the
only possible choices.

## Memory vs Search/Retrieval

Knowledge bases and external RAG corpora should not be treated as memory by
default.

Memory is accumulated agent, user, or project state: preferences, decisions,
experience, failures, conventions, and continuity created through prior work.
It can be written, consolidated, superseded, forgotten, and recalled.

Knowledge-base retrieval is closer to search. It queries external documents,
web pages, API docs, papers, company material, or code indexes. These sources
belong near `web_search`, `docs_search`, `code_search`, and other retrieval
tools.

The boundary is:

```text
Memory     -> what this agent/user/project has accumulated
Search/RAG -> external knowledge sources the agent can query
```

Search results become memory only when the agent internalizes them as durable
user, project, or task state. For example, an API documentation result is search
output; a project decision based on that result may become memory.

## Core Parts

| Part | Role | Boundary |
| --- | --- | --- |
| HostAgent | Runs tasks, receives hooks, and decides whether to load protocol skills or spawn the dreaming subagent. | It does not own the memory storage protocol. |
| `MEMORY.md` | Prompt-facing hot working memory loaded during Prime. | It is maintained by `memory_set.md` and the dreaming subagent. |
| Mnemon | Cold long-term memory binary and store used for durable recall and write. | It is accessed through `memory_get.md` and the dreaming subagent. |

Everything else is a harness asset around these three parts.

## Harness Concepts

| Concept | Memory Loop Asset | Responsibility | Boundary |
| --- | --- | --- | --- |
| GUIDE | `GUIDE.md` | Defines when to read, write, compact, and consolidate memory. | Policy only; it does not bind storage targets. |
| setup | `harness/setup` + host projection | Installs hooks, protocol skills, dreaming subagent, memory files, and environment variables. | Installation only; not a runtime decision maker. |
| hook | `prime/remind/nudge/compact` | Provides host lifecycle timing and short reminders. | No heavy reasoning or storage protocol. |
| protocol | `memory_get.md` / `memory_set.md` | Defines online recall from Mnemon and online edits to `MEMORY.md`. | Called by the host only when GUIDE says memory work is useful. |
| subagent | `dreaming` | Consolidates `MEMORY.md` into Mnemon and rewrites working memory. | Background or explicit maintenance, not every-turn online behavior. |

## Policy And Protocol Split

`GUIDE.md` must remain storage-agnostic. It should describe memory behavior in model-facing terms:

- Should the agent read memory now?
- Should the agent write memory now?
- Is this fact stable enough to keep?
- Is this a durable preference, project convention, or reusable fact?
- Is this a transient transcript item that should be ignored?
- Should working memory be compacted or consolidated?

It should not require the host agent to decide whether the storage target is `MEMORY.md` or Mnemon.

That mapping belongs to protocol assets:

- `memory_get.md` maps read-memory behavior to Mnemon recall.
- `memory_set.md` maps write-memory behavior to `$MNEMON_MEMORY_LOOP_DIR/MEMORY.md` edits.
- `dreaming` maps consolidation behavior to Mnemon write plus `MEMORY.md` compaction or eviction.

This split makes the GUIDE portable across host agents and keeps each protocol skill narrowly reusable.

## Runtime Flow

### Prime

Prime is the only direct loading path.

Inputs:

- `GUIDE.md`
- `MEMORY.md`

Action:

- Inject both into the HostAgent system prompt.

Boundary:

- Prime does not call `memory_get.md`.
- Prime does not recall Mnemon.
- Prime does not write long-term memory.

### Remind / Recall

Remind creates the opportunity to read long-term memory.

Flow:

1. Remind asks the HostAgent to judge whether memory should be read according to `GUIDE.md`.
2. If yes, the HostAgent loads `memory_get.md`.
3. `memory_get.md` explains how to call Mnemon recall.
4. Mnemon returns bounded recall context to the HostAgent.

Boundary:

- Long-term memory is not fully injected.
- Recall results are not automatically written back to `MEMORY.md`.
- `GUIDE.md` does not need Mnemon protocol details.

### Nudge / Accumulate

Nudge creates the opportunity to write working memory.

Flow:

1. Nudge asks the HostAgent to judge whether memory should be accumulated according to `GUIDE.md`.
2. If yes, the HostAgent loads `memory_set.md`.
3. `memory_set.md` explains how to add, replace, or remove entries in `MEMORY.md`.

Boundary:

- Online accumulation writes only to `MEMORY.md`.
- It does not directly write Mnemon.
- It should avoid transcripts, one-off progress, and low-confidence observations.

### Compact

Compact is a boundary-time version of Nudge.

Flow:

1. Before context compaction, Compact asks the HostAgent to judge whether important information may be lost.
2. If yes, the HostAgent loads `memory_set.md`.
3. `memory_set.md` writes the necessary final patch into `MEMORY.md`.

Boundary:

- Compact is not dreaming.
- Compact does not perform full working-memory cleanup.
- Compact does not write long-term memory directly.

### Dreaming

Dreaming is a maintenance subagent, not a normal online hook and not a protocol skill.

Flow:

1. The HostAgent spawns the dedicated dreaming subagent.
2. The subagent reads the full `MEMORY.md`.
3. The subagent writes durable material into Mnemon using the Mnemon protocol.
4. The subagent compacts, organizes, or evicts entries in `MEMORY.md`.

Possible triggers:

- `MEMORY.md` exceeds quota.
- Context compaction is about to happen.
- The user or HostAgent explicitly asks for dreaming.

Boundary:

- Dreaming owns consolidation and cleanup.
- It does not replace Remind, Nudge, or Compact.
- It should preserve prompt-facing usefulness while moving durable information into long-term memory.

## Working Memory Rules

`MEMORY.md` should stay small and model-friendly.

Good entries:

- Durable user preferences.
- Project conventions.
- Stable facts discovered through repeated work.
- Known pitfalls and their fixes.
- Current long-running goals that are still relevant.

Bad entries:

- Raw transcripts.
- One-off progress updates.
- Unverified guesses.
- Information that belongs in source code, tests, or documentation.
- Large historical detail better stored in Mnemon.

When `MEMORY.md` grows too large, dreaming should write durable content into Mnemon first, then compact or evict working-memory entries.

## Setup Expectations

The first concrete setup target is Claude Code, but the layout should remain host-agnostic.

Setup should install:

- `env.sh`, including `MNEMON_MEMORY_LOOP_DIR` and threshold variables.
- An initial `MEMORY.md`.
- A minimal `GUIDE.md`.
- Prime, Remind, Nudge, and Compact hooks.
- `memory_get.md` and `memory_set.md` protocol skills.
- The dreaming subagent spec.

Mnemon itself remains a separate binary and long-term store. The harness assumes it is installed before recall or consolidation is used.

## MVP Scope

The MVP includes:

- Markdown policy and protocol assets.
- Host hook installation.
- Working-memory read/write through `MEMORY.md`.
- Long-term recall through Mnemon.
- Dreaming-based consolidation into Mnemon.

The MVP excludes:

- A custom agent runtime.
- A complex adapter framework.
- Multiple working-memory formats.
- Direct long-term-memory writes from normal online hooks.
- An always-on daemon. Dreaming can be manual or triggered by host lifecycle boundaries in the first version.

## Risk Boundaries

- **Over-capturing transient context:** not every useful-looking task detail should become memory. GUIDE should bias against raw transcripts and low-confidence observations.
- **Sensitive data:** working memory and long-term memory should avoid secrets, credentials, and private task content unless the user explicitly asks to preserve them.
- **Recall pollution:** Mnemon recall should stay bounded and relevant. Long-term memory is capacity-friendly, but not all stored material should be loaded back into prompt.
- **Dreaming mistakes:** dreaming should preserve prompt-facing usefulness while compacting. It should not silently erase active preferences or project conventions.
- **Storage confusion:** online hooks write `MEMORY.md`; durable Mnemon writes belong to dreaming. Keeping this boundary prevents every turn from becoming a long-term write.
- **Host portability:** anything beyond short hooks, Markdown protocol skills, and a spawned subagent should be treated as host-specific setup, not the base contract.

## Loop Summary

```text
Prime loads GUIDE + MEMORY.md
Remind may call memory_get -> Mnemon recall
Nudge / Compact may call memory_set -> MEMORY.md patch
Dreaming consolidates MEMORY.md -> Mnemon and rewrites MEMORY.md
```

The loop is intentionally asymmetric: working memory is model-friendly and loaded eagerly; long-term memory is capacity-friendly and accessed through bounded recall or consolidation.
