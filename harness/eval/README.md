# Mnemon Harness Eval

This directory documents eval modes for host-wrapped loop testing.

The canonical eval loop module lives under:

```text
harness/modules/eval-loop/
```

Use `harness/eval/` for project-local runner notes and app-server operation
details. Use `harness/modules/eval-loop/` for reusable eval-loop policy,
scenarios, suites, rubrics, protocol skills, and lifecycle guidance.

## Codex App-Server Eval

The Codex app-server eval uses the real Codex app-server protocol instead of a
mock server. It creates an isolated run directory under `.testdata`, installs
Mnemon loop modules into a generated workspace, starts:

```bash
codex app-server --listen stdio://
```

Then it sends JSON-RPC requests for `initialize`, `skills/list`, and
`thread/start`. The default path is a smoke check that does not start a model
turn:

```bash
make codex-app-eval
```

Run the real memory/skill scenario suite with:

```bash
make codex-app-eval-suite
```

Run the longer memory regression suite with:

```bash
make codex-memory-deep-eval
```

Run the longer skill-loop regression suite with:

```bash
make codex-skill-deep-eval
```

Run the eval-loop projection smoke check with:

```bash
make codex-eval-loop-smoke
```

To run an actual Codex turn, use:

```bash
python3 scripts/codex_app_server_eval.py --agent-turn
```

The real turn may use the local Codex authentication and consume model credits.
Each run writes a JSON report and app-server stderr log under:

```text
.testdata/codex-app-eval/<timestamp>/
```

## Isolation Model

Each eval run has:

- `workspace/`: a throwaway project root read by Codex
- `workspace/.codex/`: projected Codex skills
- `.mnemon/`: canonical Mnemon harness state
- `logs/`: app-server logs
- `reports/`: machine-readable eval reports

## Scenario Suite

The default suite covers:

- `memory-skip-local`: visible workspace context should not trigger recall
- `memory-focused-recall`: relevant seeded long-term memory should be recalled
- `memory-write-decision`: durable decisions should update `MEMORY.md`
- `memory-no-pollution`: transient tokens should not be stored
- `skill-observe-evidence`: reusable workflow evidence should append JSONL

The `memory-deep` suite extends memory coverage with:

- relevant recall with noisy low-value memories
- superseding stale memory entries without duplicating decisions
- rejecting uncertain preference changes
- rejecting secret-like values and generic restatements of existing safety policy
- multi-turn continuity through persisted `MEMORY.md`

The `skill-deep` suite extends skill-loop coverage with:

- skipping transient one-off workflow evidence
- recording missing-skill evidence as JSONL
- applying an explicitly approved active skill creation
- preserving the host skill surface during canonical skill changes
- producing proposal-first curation output without activating skills
- drafting reviewable skill content without activating it
