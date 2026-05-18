---
name: eval_run
description: Execute or supervise a planned Mnemon harness eval run in an isolated HostAgent workspace.
---

# Eval Run

Use this skill to execute or supervise a planned eval run.

## Procedure

1. Confirm the plan names a host, suite or scenario, and evidence targets.
2. Create or use an isolated workspace. Do not run scenario state in the
   developer's active workspace unless the eval explicitly requires it.
3. Install the requested loop modules with `harness/setup`.
4. For Codex app-server evals, use the project runner when available:

   ```bash
   python3 scripts/codex_app_server_eval.py --suite
   ```

   Use a specific suite option when the scenario requires it.
5. Collect artifacts and logs before cleanup.
6. Record timeouts, setup failures, and HostAgent readiness failures as eval
   evidence, not as silent skips.

## Boundaries

- Do not change canonical scenarios, suites, or rubrics while running an eval.
- Do not delete artifacts needed for report review.
- Do not treat an exploratory run as a regression result.
