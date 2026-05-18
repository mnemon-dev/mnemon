# Mnemon Eval Loop Harness

This directory is the canonical eval loop module. It is a feedback-facing loop:
it designs and runs realistic harness scenarios, collects evidence, and turns
stable failures into curated improvement candidates.

The eval loop is not a parent of memory-loop or skill-loop. It is a peer module
that can evaluate interface-facing loops, host projection, setup, documentation
workflow, commit discipline, and its own eval assets.

## File Tree

```text
harness/modules/eval-loop/
├── README.md
├── module.json
├── env.sh
├── GUIDE.md
├── hooks/
├── skills/
├── subagents/
├── scenarios/
├── suites/
└── rubrics/
```

## Core Parts

| Part | Role |
| --- | --- |
| Scenario | A reproducible task pressure case with target, setup, prompt, evidence, and expected observations. |
| Suite | A named group of scenarios with host and loop configuration. |
| Rubric | Review criteria used to judge behavior, stability, and improvement value. |
| Runner | Host-specific machinery that starts isolated workspaces and drives a HostAgent. Codex app server is the current primary runner. |
| Report | Durable output containing transcript references, diffs, loop state, judgement, and next actions. |

## Eval Asset Lifecycle

Eval assets are stricter than skill assets because they define how the project
judges improvement. New assets should not become canonical immediately.

```text
ephemeral -> candidate -> promoted -> canonical -> retired
```

- `ephemeral`: one-off exploration in `scratch`; no review required.
- `candidate`: generated or proposed asset with initial evidence.
- `promoted`: curated asset suitable for local regression.
- `canonical`: stable asset suitable for long-term comparison or gates.
- `retired`: obsolete, flaky, or superseded asset kept for audit.

## Runtime Directory Protocol

Installed runtime state resolves through one environment config:

```text
$MNEMON_EVAL_LOOP_DIR/
├── env.sh
├── GUIDE.md
├── scratch/
├── candidates/
├── reports/
├── artifacts/
└── retired/
```

`env.sh` defines:

```bash
MNEMON_EVAL_LOOP_ENV=<canonical-state>/harness/eval-loop/env.sh
MNEMON_EVAL_LOOP_DIR=<canonical-state>/harness/eval-loop
MNEMON_EVAL_LOOP_SCRATCH_DIR=$MNEMON_EVAL_LOOP_DIR/scratch
MNEMON_EVAL_LOOP_CANDIDATES_DIR=$MNEMON_EVAL_LOOP_DIR/candidates
MNEMON_EVAL_LOOP_REPORTS_DIR=$MNEMON_EVAL_LOOP_DIR/reports
MNEMON_EVAL_LOOP_ARTIFACTS_DIR=$MNEMON_EVAL_LOOP_DIR/artifacts
MNEMON_EVAL_LOOP_RETIRED_DIR=$MNEMON_EVAL_LOOP_DIR/retired
```

## Codex Install

Install into the current project:

```bash
bash harness/setup/install.sh --host codex --module eval-loop
```

Check status:

```bash
bash harness/setup/status.sh --host codex --module eval-loop
```

Remove the installed Codex integration while preserving reports and candidates:

```bash
bash harness/setup/uninstall.sh --host codex --module eval-loop
```

Existing project-local Codex app-server eval commands remain available through
`make codex-app-eval-suite`, `make codex-memory-deep-eval`, and
`make codex-skill-deep-eval`.
