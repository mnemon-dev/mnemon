# Mnemon Eval Loop Guide

Use the eval loop when a task needs to test whether Mnemon harness behavior
actually improves real HostAgent work.

## Policy

- Prefer scenario-driven evals over ad hoc success claims.
- Keep canonical eval assets stable, reproducible, and reviewable.
- Treat LLM-generated evals as ephemeral or candidate assets until they show
  stable value.
- Record enough evidence for another maintainer to understand the judgement:
  task, host, loop configuration, transcript reference, diff summary, state
  changes, rubric result, and proposed next action.
- Do not loosen a rubric to make a run pass.
- Do not promote an eval asset that is flaky, duplicative, too expensive for
  its value, or likely to reward harmful behavior.

## When to Plan an Eval

Plan an eval when:

- A memory, skill, setup, host adapter, or docs workflow change claims behavior
  improvement.
- A regression is suspected from real project work.
- A repeated failure suggests a missing scenario or rubric.
- An existing scenario no longer distinguishes good behavior from weak behavior.

## Asset Lifecycle

Use this lifecycle for scenarios, suites, and rubrics:

```text
ephemeral -> candidate -> promoted -> canonical -> retired
```

- Start with `ephemeral` for exploration.
- Move to `candidate` only after the asset has a clear target, rubric, and
  observed value.
- Move to `promoted` after deduplication and at least one stable run.
- Move to `canonical` only when the asset is important enough for long-term
  comparison.
- Move to `retired` when it is obsolete, flaky, or superseded.

## HostAgent Boundary

Codex app server is the primary HostAgent today. Do not overfit eval assets to
Codex unless the scenario is explicitly testing Codex projection or driver
behavior. Record Codex-specific requirements as observed HostAgent capabilities
before turning them into generic requirements.
