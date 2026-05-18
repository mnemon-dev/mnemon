---
name: eval_improve
description: Turn stable Mnemon harness eval findings into scoped project, loop, adapter, docs, or eval asset improvements.
---

# Eval Improve

Use this skill to turn stable eval findings into project changes.

## Procedure

1. Confirm the finding is backed by a report or repeated observation.
2. Pick one improvement target. Avoid mixing loop policy changes, runner changes,
   docs changes, and scenario promotion in one patch unless they are tightly
   coupled.
3. For eval asset changes:
   - keep exploratory ideas in scratch
   - add candidate assets under runtime candidates
   - promote canonical repo assets only after curation
4. For code or harness changes, run the narrowest relevant eval or validation.
5. Summarize what changed, which evidence motivated it, and what remains
   unproven.

## Promotion Checklist

Before making an eval asset canonical, verify:

- It has a clear target and hypothesis.
- It has an explicit rubric.
- It produces reviewable artifacts.
- It is not duplicative.
- It is stable enough for its intended suite.
- It does not reward weak or unsafe behavior.
