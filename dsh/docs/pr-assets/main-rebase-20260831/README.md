# Latest-main rebase verification — 2026-08-31

This is an integration record, not a new LLM benchmark or a release announcement.

## Revisions and ownership

| Item | Revision |
|---|---|
| Development branch | `codex/view-concretization` |
| Pre-rebase development baseline | `7861d85c4b2636e84ae2c63ec6ffebed1935d34c` |
| Recoverable local backup | `codex/archive-view-before-main-rebase-20260831` |
| Latest fetched `origin/main` | `aa446cf598ee5928c787d340eb314d3f1803eb4c` — v0.4.3 |
| Verified implementation | `f10c2212c431919eb9de5eec4632cfba1a733448` |

The development history was rebased onto `origin/main`, not merged into or pushed over main. Other worktrees and experiment branches were not updated. The official distribution remains `0.5.0-beta.1`, with thirteen plugins and the Root Starter. Nothing was published or pushed.

Replaying historical merges alone was insufficient: their upstream parents were already ancestors of the new base, but their architecture-aware conflict resolutions still mattered in the relocated modules. Those ports were reapplied to the Host, shared Client, Source pages, CLI runner and tests. Obsolete monolithic modules were not reinstated. The final implementation, rather than a conflict-free intermediate replay, is the verified artifact.

The following comparison is empty, including the earlier default-Strategy and Source I/O optimizations:

```sh
git diff --exit-code 7861d85c4b2636e84ae2c63ec6ffebed1935d34c f10c2212c431919eb9de5eec4632cfba1a733448 -- \
  src/core src/sdk plugins .github cordis.patch.yml package.json pnpm-lock.yaml
```

## Main behavior mapped into the composable architecture

| Main change | Final owner and behavior | Regression coverage |
|---|---|---|
| Restore Builtin (`d0b114e`) | Client mounts either DSH `shell.overlay` or `conversation.view`. Both declare the same root-scoped Source child Slot and render the same Source-owned pages. Builtin uses its owning session; Sidebar retains its independent workspace picker. | `client-apply`, `client-interaction`, `client`, `client-sidebar`, `live-runtime`; real WebUI |
| Normalize legacy `buildin` (`d1bf61e`) | Host configuration and the revision-fenced DSH settings writer normalize only the preference. Existing user overrides, unrelated fields, comments and read-only behavior are preserved. No Core taxonomy or data migration is added. | 17 migration cases; Host startup/external-event/disposal test; real Headless restart and WebUI |
| Align collapsed icon (`66ce4be`) | Shared Client CSS recognizes native `data-sidebar-collapsed`, without requiring a layout plugin's frame marker. Expanded styles remain unchanged. | Sidebar CSS tests and a real native-frame measurement: 36×36 button, 20×20 SVG |
| v0.4.2/v0.4.3 release and UI documentation | Stable release records/assets remain historical evidence; current beta instructions describe the shared placements and canonical spelling. | Documentation link checks and release consistency check |

Earlier main fixes remain in their new owners: receiver-sensitive settings bindings, Sidebar subtree retention and peer-panel coordination, asynchronous memory tasks, command-name Native CLI resolution, bounded Source operations and deterministic ZIP export. They are covered by the full suite and preserved against the development baseline; this task did not introduce alternative controllers or duplicate Source pages.

## Automated verification

Environment: macOS, Node `v25.1.0`, pnpm `11.19.0`, pinned DSH `0.1.1-rc.2`.

| Command / gate | Result |
|---|---|
| `pnpm verify` | Exit 0; Root and plugin typechecks/builds/tests, Headless and package gates passed |
| Root full Vitest run | **102 files / 888 tests passed**; 2 files / 2 tests conditionally skipped |
| Deterministic Root build | All 36 generated-file hashes matched across two builds |
| Headless fixture | 37 total tools / 7 representative Mnemon tools; legacy spelling persisted canonically, unrelated comments/settings preserved, restart idempotent |
| Package contents / exports | 43 packed files; 9 Node-compatible entry imports; 21 public type dependencies |
| `publint --strict` and `attw` | Passed |
| `pnpm verify:plugins` | Exit 0; **13 standalone plugin repositories / 14 packed artifacts**, installed through declared semver dependencies outside the workspace; each plugin's typecheck/test/build and the external SDK/Client consumer passed |
| Packed Starter activation | A real isolated DSH installation of only the Root tarball resolved and activated its default plugins through the loopback registry |
| `pnpm release:check` | Exit 0; fourteen coordinated `0.5.0-beta.1` artifacts with the `beta` tag |
| Git integrity | Latest fetched main is an ancestor; development-baseline Core/SDK/plugins are unchanged; full diff whitespace checks passed |

The 888-test total already includes the plugin suites; their independent re-execution is not added again to inflate the count. The two skipped tests are the opt-in real Native binary integration and the Windows-only smoke test.

The first full verification stopped on an obsolete upgrade-test expectation that discarded `displayMode`. It was changed to require canonical `builtin`, while retaining the copied-data inventory, grant-based writes, unchanged storage versions and reopen assertions. The complete verification was then rerun successfully. No assertion or performance threshold was removed or loosened to make the suite pass.

## Real WebUI checks

The Browser skill was used to click the actual DSH WebUI served by `pnpm e2e:serve`. This used built Root/Source Client artifacts, real DSH Slots, Host RPC and temporary on-disk storage. `DSH_HOME`, memory roots, two workspaces and the model endpoint were isolated. The two test conversations received fixed replies from the loopback-only model stub; these were not paid model evaluations.

1. Before any conversation existed, Sidebar exposed all four primary-page entries, opened Status/Runtime, and created a Runtime record through the Source management contract.
2. The native collapsed Sidebar rendered the Memory button at 36×36 and its SVG at 20×20; its dark-theme foreground matched the intended primary label color. Returning to the conversation restored chat interaction.
3. Saving Builtin removed the Sidebar entry immediately. With no conversation, no unbound Builtin workbench was shown. Once a test conversation existed, its Memory tab rendered the same four Source pages without an independent workspace picker or Back button.
4. Builtin read the Runtime record created in Sidebar, edited it successfully, and created/read a managed Document with revision 1 and a content hash.
5. Memory Spaces opened Overview, Recall, Content and Entities, with the expected distinct headings and controls. Its strategy dialog opened and canceled without saving.
6. Changing from global to workspace scope removed the global records from the visible Source pages. Workspace A and B each accepted their own Runtime record; neither appeared in the other workspace. Returning to A reopened A's record, with B absent. Status showed the actual corresponding `<workspace>/.mnemon` root.
7. Switching back to Sidebar and global scope produced one Sidebar entry and zero Builtin tabs. The original global Runtime edit and the Document created in Builtin were still readable; neither workspace-specific record appeared in the global view.
8. Directly changing the temporary settings file to `buildin` caused the Host to save `builtin`, preserving `storageScope` and the unrelated onboarding setting. Reloading the Client restored the Builtin tab and all Source pages.
9. No browser console warnings/errors were captured. The missing Native binary was honestly shown as a Provider status, without breaking Runtime/Documents. No live cloud Provider was enabled.

The browser tab and test processes were closed. The pnpm Ctrl-C wrapper exited before fixture cleanup completed, so the exact task-owned temporary directory was removed after confirming its Web port and processes had stopped. No personal memory or workspace was changed, and no test server was left running.

## Retained limitations

- Settings-page saves update the Client immediately. Direct external YAML edits normalize on the Host, but an already-open Mnemon Client must be refreshed: its settings snapshot does not subscribe to external-file pushes. Apart from its relocated import, that Client implementation is identical to main's. This existing limitation is documented, not presented as a newly implemented live-push feature.
- Browser verification used the native dark frame. Existing CSS/interaction tests cover the supported layout selectors and peer-panel contracts; this run did not reinstall every third-party skin.
- Real Native/Windows execution, live cloud Provider accounts, source-only DSH alpha, LLM accuracy, paid benchmark cost and production network latency were not revalidated here. Controlled Provider suites and the existing local performance fences passed; no claim of a new benchmark improvement is made.
