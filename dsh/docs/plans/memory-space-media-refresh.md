# Memory space media refresh

## Goal and scope

Audit repository media and current documentation after the memory space terminology change, recapture current UI on v0.5.4, replace stale current references, and submit a PR. Preserve the provenance of versioned release, Issue, and PR evidence.

## Plan

1. Inventory every local image and recording with its references. Inspect old screenshots for terminology and distinguish current guidance from historical evidence.
2. Run the real v0.5.4 DSH WebUI in a disposable environment with the user-supplied Mnemon Pack imported first. Use Light appearance for all media. Capture matching English and Chinese desktop surfaces, key dialogs, version maintenance, configuration, disabled states, and narrow memory-space flows.
3. Build one documented current gallery with dimensions, source revision, capture conditions and file hashes. Update README, getting started, operations, configuration and UI guides; replace the current entry point to the old demo with current media.
4. Verify screenshot readability and terminology, media hashes, current-reference coverage and bilingual links. Run repository checks and submit a complete PR with evidence.

## Acceptance

- Every media reference in current usage guides points to current evidence or has an explicit historical purpose.
- Product screenshots show 记忆空间 / memory space, the real v0.5.4 interface, and meaningful state from the supplied backup.
- No screenshot labels or application state are painted over; original historical evidence keeps its identity.
- Chinese and English receive matching coverage, including important narrow layouts.
- The PR records validation results and any environment limits.

## User steering

Import the supplied Mnemon Pack before capturing screenshots and a new recording. Keep the original archive and personal memory directories unchanged. Inspect visible imported content for publication suitability; never commit the backup, databases, credentials or raw private records.

Recapture every current screenshot and recording in Light mode, following the user’s latest visual preference. Discard the uncommitted dark drafts.

## Completed capture and validation

- Imported the provided format-1 Mnemon Pack through the real safe-import UI before capture. Its original hash remains unchanged.
- Captured 60 Light JPEGs: 26 desktop and four narrow surfaces in each language. Recorded two continuous Light demos with 333 source frames in total.
- Checked every current screenshot and source video frame for legacy product labels; zero OCR matches and errors. Both encoded videos decode successfully.
- Audited all 142 existing media files and retained their original hashes. Current guide media references resolve to the new Light gallery or current architecture diagrams.
- Updated matching bilingual README, onboarding, operations, configuration, UI and compatibility guidance. Added a root-only patch changeset because README ships in the npm package.
- `npx --yes pnpm@10.13.1 run verify` passed: 1083 tests passed; Windows smoke and opt-in native integration were skipped under their existing environment guards. Type checks, 39-file deterministic builds, Headless activation, package contents and exports, publint and attw passed.
- Documentation verification passed with 939 local links and 35 Markdown anchors. Source-map warnings come from the published DSH primitives package's missing map, without test failures.
- Original ZIP and personal roots were not modified. The disposable imported copy and server were removed, and the capture browser tab was closed.
- Known limits remain documented: some narrow card fields truncate, the Chinese disabled Documents page exposes its English Source title, and model/cloud-provider behavior was not evaluated.

Delivery scope: submit a reviewed GitHub PR. Merging and release belong to a separate maintainer action.
