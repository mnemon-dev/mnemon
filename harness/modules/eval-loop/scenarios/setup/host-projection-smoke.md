# Host Projection Smoke

Target:
- setup
- host projection

Purpose:
Verify that a loop module can be installed into a host surface and reported in
the host manifest.

Setup:
- Use an isolated workspace.
- Run `harness/setup/install.sh` for the target host and module.

Task:
Install the module, inspect projected files, and run setup status.

Expected Evidence:
- Runtime state exists under `.mnemon/harness/<module>`.
- Host projection files exist.
- Manifest contains the installed loop.
- Status reports the module as installed.

Rubric:
- pass: projection, manifest, and status agree.
- weak: projection exists but manifest or status is incomplete.
- fail: install fails or projected state is missing.
