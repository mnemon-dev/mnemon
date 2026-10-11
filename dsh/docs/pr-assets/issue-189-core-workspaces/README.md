# Built-in centralized workspaces — issue #189

[中文](./README.zh-CN.md) · [Evidence index](../README.md)

Tested on 2026-09-09 with published DSH 0.1.2-rc.1, Node 22.19.0, pnpm 10.13.1 and Mnemon Native 0.2.7 on macOS. This is a fresh implementation based on `main` at `820b0b092fe68eb7ce6fbf1eaa12e9cf39087e31`, replacing the separate storage-plugin approach in PR #215. The original checkout and old branch were retained.

The functional run used `2314d6a44ee21b74f1e3bb22a0533d90285c5eea`; final layout verification and the complete automated checks used `51446b51ed636eace6f6ca4d206969561ada194d`. Screenshots 01–03 and 05–07 record the functional revision; 04, 08 and 09 record the final layout. None were copied from the old plugin PR.

## Implementation boundary

The root package's Host resolves all four layouts and canonical workspace identities; Core carries the operation scope and retains View authority. Existing Sources receive their directory through the current configuration contract. There is no new package, dependency, storage contribution API or SDK export. The existing Memory Spaces package has a presentation-only fix for long paths.

## Automated verification

```bash
MNEMON_NATIVE_TEST_CLI=/opt/homebrew/bin/mnemon npx --yes --package=node@22.19.0 --package=pnpm@10.13.1 --call 'pnpm run verify'
npx --yes --package=node@22.19.0 --package=pnpm@10.13.1 --call 'node scripts/verify-plugin-artifacts.mjs --skip-build'
CHANGESET_BASE_REVISION=820b0b092fe68eb7ce6fbf1eaa12e9cf39087e31 npx --yes --package=node@22.19.0 --package=pnpm@10.13.1 --call 'pnpm run release:intent'
```

| Check | Result |
|---|---|
| Root tests | 852 passed; 5 opt-in Flash checks skipped |
| Independent plugin tests in `verify` | 324 passed; 1 Windows-only smoke check skipped |
| Deterministic build, TypeScript, package exports and package lint | Passed |
| Real Headless profile | 39 tools; 8 representative Mnemon tools; restart and legacy-disable checks passed |
| Real Native CLI | Temporary View write, recall and deletion passed |
| Packed artifacts | 16 independent plugin repositories and 17 tarballs passed, including root-only Starter installation into real DSH |

Coverage includes all four storage scopes, legacy `dataDir`, registry rejection, session-owned Headless/Builtin routing, real Source writes to all four areas, global versus storage USER.md, symlink aliases, missing path descendants, Unicode, same-named directories, rename behavior and non-destructive switching. The latest main's 23 capacity-workflow tests also passed.

The initial package-size check exposed a tight pre-existing ceiling. A separate build of the base revision measured 1,249,360 unpacked bytes; the final package is 1,256,247 bytes (+6,887), still 46 files. Growth is confined to Host/UI, public scope types and README copy. The bounded ceiling is now 1,260,000 bytes; no Source or Provider implementation was bundled into the root.

## Real WebUI workflow

Run `pnpm e2e:serve` after building. The existing harness uses a disposable Profile, global data root, two temporary workspaces, an explicit temporary central root and a loopback-only scripted model. The browser exercised the real DSH settings, RPC and Source stack.

1. Select centralized workspace storage and global USER.md. A relative root blocks Save; an absolute root saves and takes effect live.
2. In A, add a project fact and a global preference, create a Document, enable Holographic and create a real Native space (88 KB database).
3. In B, confirm zero A project facts, Documents or Provider mappings. Only the global preference is visible. Add B's distinct project fact.
4. Clear the root and save: the workspace uses the default global-root subtree, with the global preference still visible. Restore the custom root: the original facts return. Five control-file SHA-256 values remain identical across these switches and Sidebar/Builtin changes.
5. Create real A/B DSH sessions with the scripted model. Builtin follows each owning session and omits the workspace picker. Restart the Host: both facts, the global preference, A's Document and its Provider mappings survive.
6. Inspect A while the active conversation belongs to B. The difference indicator appears; Align returns to B. Test Chinese Dark, English Light and a 390×844 Sidebar.

Disk assertions found `runtime`, `data`, `documents` and `state` under A's single hashed subtree, and no `.mnemon` directory in either project. Desktop Provider canvas measurements were 992/992 pixels (client/scroll width); narrow Runtime and Memory Spaces were 326/326. Final browser console inspection returned no errors.

Screenshot review caught and fixed two layout defects: long directory paths caused horizontal overflow, and the English alignment notice squeezed out the workspace selector. The final screenshots show constrained directory text and wrapping header controls; the existing presentation fingerprint was updated after visual verification.

## Screenshots

| Evidence | Shows |
|---|---|
| [01 — Chinese Dark settings](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/01-settings-zh-dark.png) | Scope, optional root and independent global profile |
| [02 — A Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/02-workspace-a-runtime.png) | A project fact plus global preference |
| [03 — A Document](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/03-workspace-a-document.png) | Successful managed Document creation |
| [04 — A Providers](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/04-workspace-a-providers.png) | Real Native database and Holographic mapping; corrected long-path layout |
| [05 — B isolation](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/05-workspace-b-isolation.png) | Global preference present; A project fact absent |
| [06 — English Light settings](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/06-settings-en-light.png) | Full centralized label and root control |
| [07 — Builtin after restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/07-builtin-b-after-restart.png) | B's recovered fact in its owning session |
| [08 — Workspace alignment](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/08-workspace-alignment.png) | A inspection versus B execution, with visible selector and alignment action |
| [09 — 390px Sidebar](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/09-sidebar-390px.png) | Narrow Memory Spaces, long path and alignment controls |

## Limits

This run validates local persistence and UI/transport behavior, not live model distillation quality or remote Provider accounts. Five real-Flash checks were not enabled; the Windows-only binary smoke check requires Windows. Moving or renaming a workspace selects a new path-derived ID; old data is preserved without migration. Remote Provider namespaces keep their own sharing semantics. Test data and browser tabs were removed after verification; screenshots contain only synthetic content and temporary paths.
