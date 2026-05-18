# Eval Loop Compact

Before context compaction, preserve:

- Active eval goal and hypothesis.
- Scenario and suite names.
- HostAgent configuration and loop combination.
- Report and artifact paths.
- Rubric outcome and open questions.
- Any candidate eval assets that still need curation.

Do not carry large transcripts forward in prompt context. Reference artifact
paths instead.
