---
name: mnemon-project-memory
description: Recall and save verified project lessons with Mnemon during development, debugging, reviews, and iteration in projects using the Mnemon memory panel. Keeps project-specific memory separate from transferable global experience.
---

# Project memory loop

Use this workflow when developing a project with the Mnemon memory panel plugin
enabled. Memory is supporting evidence; current instructions, source and observed
results take precedence.

Before substantial work, call `mnemon_context` with the actual current project
directory and a concise query about the task. It recalls the matching project's
memory and the separate global experience library. Inspect the returned project
path and store, especially with multiple checkouts. An unassociated directory is
not permission to put its project-specific records into global memory: associate
it with `mnemon_associate_project`, or ask the user to select it in the panel if
the intended project is unclear. Do not switch Mnemon's global active store.

After a meaningful fix or verified decision, call `mnemon_remember` with
`scope: "@project:<id>"` using the returned project id, and `source: "agent"`.
Capture the trigger, root cause, effective fix, evidence and limitations, and a
source path/commit when relevant. Include failed attempts only if they prevent a
repeat mistake. Do not claim unperformed tests or user acceptance. Routine edits
and repeated observations do not need new records.

Use `scope: "@global"` for lessons that actually transfer between projects,
such as a demonstrated tool incompatibility or an explicit general preference.
Write a concise general lesson without project-specific payloads. Global memory
is a separate library, not a copy of all project records; `@all` is read-only.

Read `action` in the write result: `skipped` means a duplicate was not added.
An uncertain write error must be checked by recall before retrying. An unavailable
memory service does not justify inventing a successful write or blocking unrelated
development. Briefly report the relevant limitation and continue the task.

These tools run locally and use the project's saved directory association.
The panel reflects later changes automatically; it does not make model calls
or guarantee that every host selects this skill on every turn. Existing project
hooks are separate and must use the same store when enabled; do not replace or
reconfigure them without an explicit project setup request.
