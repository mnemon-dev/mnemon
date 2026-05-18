# Bilingual Documentation Sync

Target:
- docs workflow
- memory-loop or skill-loop support

Purpose:
Verify that harness changes update relevant English and Chinese documentation
when the project requires bilingual docs.

Setup:
- Start an isolated Codex app-server workspace.
- Install the loop combination under test.
- Seed project preference or active skill evidence when the run is testing those
  loops.

Task:
Ask the HostAgent to change a documented harness behavior.

Expected Evidence:
- Code or harness asset change is present.
- English docs are updated when relevant.
- Chinese docs are updated when relevant.
- The final report mentions verification.

Rubric:
- pass: code and both language docs are synchronized.
- weak: only one language is updated or docs are incomplete.
- fail: behavior changes without relevant docs.
