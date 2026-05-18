---
name: eval_plan
description: Design a scenario-driven Mnemon harness eval with target, hypothesis, HostAgent, loop configuration, evidence, and rubric.
---

# Eval Plan

Use this skill to design a scenario-driven eval before running a HostAgent.

## Procedure

1. Identify the target: loop, setup behavior, host projection, docs workflow, or
   eval-loop itself.
2. Choose an existing scenario and suite when one fits.
3. If no scenario fits, draft an ephemeral plan first. Do not promote it during
   the same step.
4. State the hypothesis in observable terms.
5. Select the HostAgent and loop combination. Codex app server is the default
   HostAgent for current Mnemon evals.
6. Define the evidence to collect:
   - transcript or response reference
   - git diff
   - `.mnemon` state changes
   - projected host surface
   - report path
   - logs or timeout reason
7. Attach a rubric or mark the run exploratory.

## Output

Return a short eval plan with:

- target
- scenario
- suite
- host
- loops
- hypothesis
- evidence
- expected report path
