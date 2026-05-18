# Eval Loop MVP Design

Chinese version: [DESIGN.md](../../zh/harness/eval-loop/DESIGN.md)

Installable MVP assets: [harness/modules/eval-loop](../../../harness/modules/eval-loop/README.md)

The eval loop is Mnemon's feedback-facing harness module. It defines how a
HostAgent is tested through realistic scenarios, how evidence is collected, and
how stable failures become curated improvement candidates.

## Positioning

The eval loop is a peer of memory-loop and skill-loop. It is not their parent
module. Memory-loop and skill-loop directly affect the HostAgent interface by
changing remembered context and reusable working methods. Eval-loop observes
those effects through scenario execution and feeds findings back into the
project.

```text
harness/modules/
├── memory-loop
├── skill-loop
└── eval-loop
```

## Core Model

```text
scenario
   |
   v
isolated workspace + .mnemon + host projection
   |
   v
Codex app server HostAgent
   |
   v
artifacts: transcript, diff, memory state, skill evidence, logs
   |
   v
rubric judgement
   |
   v
report and improvement candidate
```

Codex app server is the current primary HostAgent. Generic HostAgent
requirements should be extracted from repeated Codex-first scenarios rather
than designed upfront.

## Assets

| Asset | Purpose |
| --- | --- |
| Scenario | A reproducible task pressure case with target, setup, prompt, evidence, and expected observations. |
| Suite | A named set of scenarios and loop configuration. |
| Rubric | Criteria for judging behavior and eval asset quality. |
| Skill | Protocol methods for planning, running, analyzing, and improving evals. |
| Evaluator | Background curation worker for deduping candidates and summarizing trends. |

## Lifecycle

Eval assets have a stricter lifecycle than skills because they define how the
project judges improvement.

```text
ephemeral -> candidate -> promoted -> canonical -> retired
```

- `ephemeral`: temporary exploration, no review required.
- `candidate`: proposed asset with initial evidence.
- `promoted`: curated asset for local regression.
- `canonical`: stable asset for long-term comparison or gates.
- `retired`: obsolete, flaky, or superseded asset.

This reduces review pressure: the agent can explore freely, but only stable and
useful assets are reviewed for promotion.

## First Scope

The first scenarios focus on Mnemon's current self-evolution work:

- memory preference recall
- skill creation and reuse
- bilingual documentation synchronization
- host projection smoke checks

These scenarios evaluate memory-loop and skill-loop today, but the eval-loop
framework is intentionally broader. It can also evaluate setup, host adapters,
docs workflow, commit discipline, and eval-loop itself.
