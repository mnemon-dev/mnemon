# Eval Loop Nudge

At turn completion, if eval work happened:

- Write or update a report under `$MNEMON_EVAL_LOOP_REPORTS_DIR` when a run
  produced evidence.
- Keep raw artifacts under `$MNEMON_EVAL_LOOP_ARTIFACTS_DIR`.
- Place newly proposed scenarios, suites, or rubrics under
  `$MNEMON_EVAL_LOOP_CANDIDATES_DIR` unless they were explicitly reviewed.
- Summarize whether the result suggests a code change, loop policy change,
  host adapter change, docs update, or eval asset change.
