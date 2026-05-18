# Skill Loop MVP Design

Related visualization: [site.html](../../site/skill-loop/site.html)

Installable MVP assets: [harness/modules/skill-loop](../../../harness/modules/skill-loop/README.md)

The skill loop gives a host agent a self-evolving skill library without replacing the host's native skill runtime. It treats skills as host-native assets, while `.mnemon` owns the canonical lifecycle state and the evidence used to evolve that state.

The MVP is intentionally a visibility and lifecycle harness. It decides which skills should be discoverable now, which should be kept for maintenance, and which should remain as history. It does not inject all skills into the prompt, and it does not require the host agent to reload newly-created or patched skills in the current session.

## Goals

- Keep the host agent in control of execution, native skill discovery, subagent calls, and tool routing.
- Store canonical skill state under `.mnemon`, separated into `active`, `stale`, and `archived` lifecycle states.
- Use GUIDE, hooks, protocol skills, and a curator subagent as the common self-evolution harness vocabulary.
- Record lightweight evidence online, then review and modify skills through explicit proposals.
- Make new active skills visible at the next Prime boundary, rather than forcing current-session reload.

## Three Core Parts

| Part | Runtime Role | Boundary |
| --- | --- | --- |
| HostAgent | Runs the task, owns the ReAct loop, receives hooks, assembles prompts, routes tools, and invokes host-native skills or subagents. | Does not own canonical skill state. It decides when to load protocol skills, but `.mnemon` remains the source of truth. |
| Host Skill Surface | The host-native skill discovery location, such as `.claude/skills`. The host runtime reads this surface using its normal skill mechanism. | Generated or mounted from `.mnemon/skills/active` by Prime. It is a view, not the canonical store. |
| `.mnemon` Skill Library | Canonical filesystem for skills and usage state: `skills/active`, `skills/stale`, `skills/archived`, plus usage sidecars or signal reports. | All lifecycle mutations happen here through `skill_manage`. Host-native directories should be treated as generated output. |

The important distinction is that HostAgent owns behavior, while `.mnemon` owns durable skill state. The harness connects the two by projecting active skills into the host-facing surface at Prime time.

## Harness Concepts

| Concept | Skill Loop Asset | Role | Boundary |
| --- | --- | --- | --- |
| GUIDE | `GUIDE.md` | Defines what counts as skill evidence, reusable workflow signal, review trigger, protected or pinned skill, and proposal-first policy. | Policy only. It does not generate, patch, move, or archive skills. |
| setup | setup scripts and bindings | Installs hooks, protocol skills, the curator subagent, and host-native skill-surface bindings. | Installation only. It does not participate in every runtime decision. |
| hook | `prime`, `remind`, `nudge`, `compact` | Provides timing: Prime syncs active skills, Nudge reminds the model to observe evidence, Compact can mark a low-frequency review boundary, and Remind is usually a no-op. | Hooks should stay short. The rules live in GUIDE and the actions live in protocol skills. |
| protocol | `skill_observe.md`, `skill_curate.md`, `skill_manage.md` | Defines portable procedures the HostAgent can load for observation, review startup, and lifecycle mutation. | Protocol skills locate `.mnemon` through the harness environment, such as `MNEMON_HARNESS_DIR`. |
| subagent | `curator` | Performs low-frequency review over evidence and the skill library, then proposes create, patch, consolidate, stale, archive, or restore actions. | Proposal-first by default. Approved changes are applied through `skill_manage`. |

## Lifecycle Model

| State | Meaning | Host Visibility |
| --- | --- | --- |
| `active` | Skills that should be discoverable by the host. | Prime syncs or mounts only this state into the Host Skill Surface. |
| `stale` | Skills that are not currently useful enough to expose, but may be reviewed, patched, restored, or consolidated later. | Not visible by default. Available to curator review and explicit restore workflows. |
| `archived` | Historical skills retained for audit, recovery, and design memory. | Not visible by default. Prefer archive over delete in the MVP. |

Lifecycle movement is conservative:

- `active -> stale` when evidence shows low use, supersession, duplication, poor fit, or high confusion risk.
- `stale -> active` when review finds the skill is still useful, has been repaired, or should be restored.
- `stale -> archived` when the skill is obsolete and should no longer be considered for normal restoration.
- `archived -> stale` or `archived -> active` only through an explicit restore proposal.

Protected or pinned skills should be skipped by automated lifecycle moves unless the proposal explicitly explains the exception and receives approval.

## Runtime Flow

```text
Prime exposes active skills
  -> host uses native skill discovery
  -> Nudge asks whether this turn produced evidence
  -> skill_observe records evidence only
  -> curator reviews evidence and drafts proposals
  -> skill_manage applies approved canonical changes
  -> next Prime exposes the new active set
```

### 1. Prime

Prime is the synchronization boundary between `.mnemon` and the host-native skill surface.

Inputs:

- GUIDE policy.
- `.mnemon/skills/active`.
- setup-created bindings for the host runtime.

Actions:

- Sync, mount, or generate host-native skill files from `.mnemon/skills/active`.
- Keep `stale` and `archived` out of the normal host discovery path.
- Leave the HostAgent to discover and invoke skills through its native mechanism.

Boundaries:

- Prime does not inject every skill body into the prompt.
- Prime does not decide which skills should be created, patched, or archived.
- The host-native skill directory is a generated view; `.mnemon` is canonical.

### 2. Remind

Remind is usually a no-op in the skill loop because host agents already have native skill discovery. In the memory loop, Remind can ask whether recall is needed. In the skill loop, repeating discovery instructions every turn would add noise without improving correctness.

If a host lacks native skill discovery or needs a lightweight reminder, Remind may be configured as an optional host-specific fast path. That is outside the MVP default.

### 3. Nudge

Nudge runs at the agent-loop stop boundary as a short reminder.

Actions:

- Ask the model to follow GUIDE.
- Ask whether this turn produced skill usage evidence or a reusable workflow signal.
- If yes, the HostAgent should load `skill_observe.md`.

Boundaries:

- Nudge does not write `.usage.json`.
- Nudge does not generate or patch skills.
- Nudge does not run curator review.
- Nudge only triggers the decision to observe.

This keeps online overhead low: the normal task path is not interrupted unless there is evidence worth recording.

### 4. `skill_observe`

`skill_observe.md` is the lightweight online protocol skill. It records evidence; it does not interpret evidence into lifecycle decisions.

Possible inputs:

- A skill was viewed, selected, or used.
- A skill helped complete a task.
- A skill was missing, misleading, outdated, or caused a failed path.
- A user gave feedback about a workflow.
- The agent repeated a workflow that may deserve a skill.
- A patch was applied manually and should be recorded as evidence.

Actions:

- Write a usage sidecar such as `.mnemon/skills/.usage.json`, or a signal report if the implementation chooses report files.
- Preserve enough context for later curator review: skill id, event type, task context, outcome, and optional evidence note.

Boundaries:

- `skill_observe` records evidence only.
- It does not decide whether a new skill should exist.
- It does not change `active`, `stale`, or `archived`.
- It should avoid storing sensitive task data unless GUIDE allows it and the evidence truly needs it.

### 5. Curator Review

The curator is a low-frequency maintenance subagent. It may run manually, at a compact or dreaming-like boundary, through a HostAgent scheduler, or after sufficiently strong signals.

Inputs:

- GUIDE review policy.
- Existing skills in `.mnemon/skills/active`, `.mnemon/skills/stale`, and `.mnemon/skills/archived`.
- Usage sidecars and signal reports.
- Optional host-specific constraints, such as skill format or naming rules.

Actions:

- Review whether evidence supports creating a skill, patching a skill, consolidating duplicates, moving a skill to stale, archiving a stale skill, or restoring a stale or archived skill.
- Draft `SKILL.md` content or patch proposals when appropriate.
- Produce a proposal or report for review.

Boundaries:

- Curator is not an online step for every task.
- Curator is proposal-first by default.
- Curator should not directly enable a new active skill.
- Curator should call out uncertainty, missing evidence, and risks instead of hiding them in the patch.

### 6. `skill_manage`

`skill_manage.md` applies approved lifecycle and content changes to `.mnemon`.

Allowed MVP operations:

- Create a proposed skill in `active` after approval.
- Patch an existing skill.
- Consolidate duplicated skills.
- Move `active -> stale`.
- Move `stale -> archived`.
- Restore `stale -> active`.
- Restore `archived -> stale` or `archived -> active` when explicitly approved.
- Update metadata and usage bookkeeping needed by the lifecycle.

Boundaries:

- `skill_manage` modifies canonical `.mnemon` state, not the host runtime directly.
- It should not bypass proposal-first review for non-trivial changes.
- It should skip protected or pinned skills unless the approved proposal explicitly covers them.
- It should prefer archive over delete in the MVP.
- The new active set becomes host-visible only after the next Prime sync.

## Current-Session Boundary

The MVP does not force current-session reload after creating or patching skills. This is a deliberate boundary.

Reasons:

- Host runtimes may cache skill discovery differently.
- Forced reload APIs are host-specific and can make the harness less portable.
- A current session may already have prompt and tool state built around the previous skill set.
- The next Prime boundary gives a clear, deterministic point where the generated Host Skill Surface can be refreshed.

If a host supports cache invalidation or immediate reload, setup can add it later as an optional fast path. The portable contract remains: `skill_manage` updates `.mnemon`; the next Prime projects the active set to the host.

## MVP Scope

In scope:

- Canonical `.mnemon/skills/{active,stale,archived}` layout.
- Prime synchronization from `active` to the Host Skill Surface.
- GUIDE policy for evidence, review triggers, lifecycle states, and proposal-first rules.
- Nudge reminder to decide whether to observe.
- `skill_observe` evidence recording.
- Curator proposal generation.
- `skill_manage` approved lifecycle mutation.
- Conservative restore and archive flows.

Out of scope for MVP:

- Replacing the host's native skill runtime.
- Prompt-injecting all skill content.
- Guaranteed current-session skill reload.
- Fully automatic skill creation without proposal review.
- Deleting archived skills as a normal lifecycle action.
- Global marketplace publishing or cross-user skill sharing.
- Complex ranking, embedding search, or adaptive skill selection beyond host-native discovery.
- Treating the skill loop as memory storage. Durable task facts belong to the memory loop, not skill state.

## Risk Boundaries

- **Prompt or discovery noise:** too many active skills can degrade host behavior. Curator should stale low-value or duplicate skills.
- **Evidence pollution:** `skill_observe` should record structured, reviewable signals and avoid turning every task detail into skill evidence.
- **Premature automation:** creating or patching skills directly from a single weak signal risks encoding bad workflows. Curator should require evidence and propose first.
- **State drift:** host-native skill directories must be treated as generated views. Manual edits should be migrated back through `.mnemon` or overwritten by Prime.
- **Protected skills:** pinned, built-in, or safety-critical skills need explicit handling and should not be silently moved.
- **Sensitive data:** skills should describe reusable procedure, not private task content. Evidence sidecars should keep only the minimum context needed for review.
- **Host portability:** anything beyond sync/mount, short hooks, and protocol skills should be host-specific extension, not the base contract.

## Responsibility Matrix

| Concept | Asset | Runtime Role | Boundary |
| --- | --- | --- | --- |
| Host runtime | HostAgent | Runs the ReAct loop, receives hooks, and decides whether to load protocol skills or the curator subagent. | Does not own canonical skill state. |
| Host-facing surface | Host Skill Surface | Location read by host-native skill discovery. | Generated or mounted by Prime from `.mnemon/skills/active`. |
| Canonical store | `.mnemon` Skill Library | Stores active, stale, archived skills and usage evidence. | Source of truth; host-native directories are views. |
| GUIDE | `GUIDE.md` | Defines evidence, review triggers, protected/pinned rules, and proposal-first policy. | Policy only; no migration. |
| setup | setup + bindings | Installs hooks, protocol skills, curator subagent, and host-native skill-surface binding. | Installation and mounting only. |
| hook | `prime/remind/nudge/compact` | Provides sync, observation reminders, and low-frequency review boundaries. | Timing only; rules stay in GUIDE. |
| protocol | `skill_observe` / `skill_curate` / `skill_manage` | Defines observe, curate, and manage procedures. | Uses harness environment to locate `.mnemon`. |
| subagent | curator | Performs low-frequency review, consolidation, proposals, and reports. | Proposal-first; approved changes flow through `skill_manage`. |
