---
name: eval_analyze
description: Analyze Mnemon harness eval reports, classify outcomes, and extract improvement evidence.
---

# Eval Analyze

Use this skill after an eval run to judge behavior and extract improvement
evidence.

## Procedure

1. Read the report, relevant artifact summaries, and the selected rubric.
2. Compare observed behavior to the hypothesis.
3. Classify the outcome:
   - `pass`: behavior meets the rubric.
   - `weak`: partially useful but missing expected evidence or consistency.
   - `fail`: behavior contradicts the target expectation.
   - `invalid`: setup or scenario issue prevents judgement.
4. Identify the likely improvement target:
   - memory-loop
   - skill-loop
   - eval-loop
   - host adapter
   - setup
   - docs
   - scenario or rubric
5. If a new eval asset is warranted, create a candidate summary instead of
   editing canonical assets immediately.

## Output

Write a concise analysis with:

- outcome
- evidence
- likely cause
- recommended next action
- candidate eval asset path, if any
