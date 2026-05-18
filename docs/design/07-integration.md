# 7. LLM CLI Integration

[< Back to Design Overview](../DESIGN.md)

---

![Integration Architecture](../diagrams/08-three-layer-integration.jpg)

Mnemon integrates with LLM CLIs as a markdown-installable memory harness, not as
a runtime-specific agent framework. The target runtime remains responsible for
conversation, planning, file edits, tool use, and semantic judgment. Mnemon
provides a durable memory protocol, a skill surface, a memory guideline, and
four lifecycle reminders.

The integration layer follows the **Hook-native, LLM-led, Protocol-constrained**
principle:

- **Hook-native**: lifecycle events are useful places to remind the agent about
  memory, but hooks should stay lightweight.
- **LLM-led**: the host agent decides whether recall or writeback is useful.
- **Protocol-constrained**: Mnemon owns deterministic commands, structured
  output, provenance, linking, deduplication, and lifecycle operations.

## 7.1 Installable Artifact Model

The preferred integration is three markdown artifacts plus the Mnemon binary:

| Artifact | Role |
|---|---|
| `SKILL.md` | Teaches command syntax, output interpretation, and hard guardrails |
| `INSTALL.md` | Tells the target agent how to install the skill, guideline, and hook phases in its own runtime |
| `GUIDELINE.md` | Defines recall/writeback/link/supersede/no-op judgment policy |
| `mnemon` binary | Executes deterministic memory operations |

`mnemon setup` can still automate these steps for known runtimes, but the
architecture should not depend on a custom adapter. A capable agent should be
able to read `INSTALL.md` and install Mnemon using the closest native mechanism
available in its runtime.

## 7.2 Four Hook Phases

Four hook phases define the lifecycle contract:

```text
Session starts
    |
    v
  Prime   -> load skill/guideline stance and active store info
    |
    v
User prompt arrives
    |
    v
  Remind  -> ask whether recall could change the task
    |
    v
Agent works with Mnemon only when useful
    |
    v
  Nudge   -> ask whether durable writeback is justified
    |
    v
Before context compaction
    |
    v
  Compact -> preserve only critical continuity
```

The hook contract is behavioral. The script body is runtime-specific and should
be treated as an implementation detail.

| Phase | Typical Event | Required Behavior | Should Avoid |
|---|---|---|---|
| Prime | Session start / bootstrap | Make the Mnemon skill, guideline, and active store visible | Bulk injecting historical memory |
| Remind | User prompt submit / before planning | Prompt a recall decision for memory-sensitive tasks | Auto-recalling every prompt |
| Nudge | Stop / after response | Prompt a writeback decision for durable insights | Saving ordinary chat logs |
| Compact | Before compaction | Preserve critical continuity before context is lost | Storing the full transcript |

When hooks are unavailable, encode the same checks as persistent rules. The
agent can self-check at task start, task end, and compaction boundaries.

## 7.3 Runtime Mapping

The same harness maps differently across runtimes:

| Runtime | Natural Installation Mechanism |
|---|---|
| Codex | `AGENTS.md`, skills, local instructions, and hooks when enabled |
| Claude Code | `CLAUDE.md`, skills, slash commands, settings hooks, and project/user memory files |
| OpenClaw | Plugin hooks and skills, without requiring a Mnemon-specific memory engine |
| Skill-first agents | Skills, memory guidance, and lightweight reminders |
| Minimal CLIs | A rules file or system instruction that references `SKILL.md` and `GUIDELINE.md` |

Mnemon should document these mappings as examples in `INSTALL.md`. They are not
separate product architectures.

## 7.4 Agent-Led Memory Work

The agent should treat memory as a decision, not a reflex:

1. At task start, decide whether prior experience could change the work.
2. If yes, run a focused `mnemon recall` query and treat results as evidence.
3. Do the task using current user instructions and repository facts as higher
   authority than stale memory.
4. At task end, decide whether the session produced durable knowledge.
5. If yes, write a concise memory with provenance and link/supersede related
   memories when the relationship is useful.
6. If no, do nothing.

Delegation to a sub-agent can be useful when a runtime supports it, especially
for expensive writeback review or long sessions. It is an execution strategy,
not a required part of the architecture. A single capable agent may perform the
same memory decisions directly.

## 7.5 Markdown Self-Evolution

The integration layer should evolve primarily through reviewed markdown
patches:

```text
repeated experience
  -> Mnemon recall/writeback evidence
  -> LLM reflection
  -> candidate patch to SKILL.md / GUIDELINE.md / INSTALL.md / project rule
  -> review
  -> installed behavior
```

This keeps self-evolution inspectable and reversible. Stable workflows become
skills. Stable judgment changes become guideline edits. Stable runtime setup
knowledge becomes install notes. Code, database schema, or runtime internals
should evolve only after the markdown loop proves that the behavior is valuable.

## 7.6 Verification

An integration is acceptable when the target agent can:

1. Locate the Mnemon skill and explain command syntax.
2. Locate the memory guideline and explain recall/writeback skip conditions.
3. Run `mnemon recall` for a task where memory is relevant.
4. Write one durable memory with provenance.
5. Skip memory for a trivial task.
6. Preserve only critical continuity before compaction when the runtime exposes
   that lifecycle point.

The integration is failing if hooks force memory use on every prompt, if memory
turns into a transcript dump, or if stale memory overrides current user
instructions and repository evidence.
