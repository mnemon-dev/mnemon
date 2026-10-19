# Latest-main rebase verification — 2026-09-04

This is the integration record for rebasing the Composable View development
branch onto the v0.4.7 main line. It verifies compatibility and behavior; it is
not a release announcement or a memory-quality benchmark.

## Revisions

| Item | Revision |
|---|---|
| Development branch | `codex/view-concretization` |
| Pre-rebase development baseline | `73c23c837c0af3d9eea1c6cbc4e3040bc5bf768d` |
| Recoverable local backup | `codex/archive-view-before-main-rebase-20260904` |
| Latest fetched `origin/main` and local `main` | `47993c55c78ddb2c0ff1eb8e18859b4f5c35a14f` — v0.4.7 |
| Rebased feature graph before semantic integration | `f65bdc9` |
| Verified semantic integration | `60112e8ca5353395ed9bd4365e0e767f699dfd1d` |

The development history was rebased with merge topology preserved. Main was
not merged into the feature branch, and neither branch nor packages were
pushed or published. The local `main` reference was fast-forwarded to the same
freshly fetched revision after confirming it was not checked out elsewhere.

## Main behavior mapped into the composable architecture

| Main addition | Final owner and boundary | Verification |
|---|---|---|
| Stable DSH `0.1.2-rc.1` baseline from v0.4.7 | Root build/test dependencies and Source Client development fixtures use one exact rc.1 cohort. The public Cordis peer remains `^4.0.1`, which accepts rc.1's Cordis 4.0.2 without unnecessarily rejecting compatible Hosts. | Frozen install, dependency assertions, registry and source-overlay full suites |
| Store and Invariants packages | `SnapshotSelectorHook` is imported from its public owner, `@deepseek-ai/dsh-client-store`; `StoredEntry` remains owned by Slots. `@deepseek-ai/dsh-invariants` closes the published peer graph. Core, Source and Strategy contracts gain no DSH dependency. | Typecheck, Client projection tests, real WebUI |
| Architecture Client layout dependency | Root explicitly owns `@deepseek-ai/dsh-client-ui-layout`; independent Source plugins keep only their declared Client primitives and their own workspace-local build boundary. | Root/plugin builds and 16 standalone repository checks |
| Exact prerelease quarantine | Every direct rc.1 package represented in the lockfile is named explicitly in `minimumReleaseAgeExclude`; wildcard exemptions are rejected. The retained legacy projection alias is audited separately. | Plugin dependency/lock assertions and frozen install |
| DSH alpha.5 source compatibility | The linker overlays 23 Root DSH packages plus Root Cordis and only the shared Cordis identity in each of 16 plugin workspaces. It also includes Store, Invariants and UI Layout. All 40 original registry links are recorded and restored. | Full alpha.5 suite, restore check and post-restore typecheck |
| Cloud WebUI documentation from main | The rc.1 launch-token/cookie transport, HTTPS/access-control requirements, `trustedHosts` boundary and explicit rc.2 rollback procedure remain in the English and Simplified Chinese guides. | Documentation and release-link audit |
| v0.4.7 release metadata | Historical stable installation guidance remains v0.4.7. The development distribution remains the coordinated 17-package `0.5.0-beta.1` set. | `release:check` |

The rebase initially retained the feature branch's old rc.2 package/import
choices while accepting main's newer workspace and linker structure. That
state compiled only accidentally through transitive packages. The semantic
integration commit removes that split state: package ownership, direct
dependencies, source overlays, tests and documentation now describe the same
rc.1 graph without moving DSH concerns into the Composable Core or plugin SDK.

## Automated verification

Environment: macOS, Node `v25.1.0`, pnpm `11.19.0`. Registry verification used
DSH `0.1.2-rc.1`; source verification used the built DSH `0.1.2-alpha.5`
checkout at `db6bdc3576c2d4e7c965e8e3ed0c2a731eed87f5`.

| Gate | Result |
|---|---|
| Frozen dependency installation | Exit 0; the lockfile resolves the direct rc.1/Cordis 4.0.2 cohort |
| Complete `pnpm verify` against registry rc.1 | Exit 0; Root and all 16 plugin builds, typechecks and tests passed |
| Complete `pnpm verify` against alpha.5 source overlay | Exit 0; the same build/test/package chain passed |
| Root Vitest portion in each full run | **114 files passed / 1000 tests passed**; 2 files / 2 tests conditionally skipped |
| Deterministic build | 38 generated files matched across both builds |
| Headless fixture | 38 total tools / 7 representative Mnemon tools |
| Package/export checks | 45 packed files; 11 Node-compatible entries; 25 public type dependencies; `publint` and `attw` passed |
| Source-link restoration | All 40 temporary links restored; restore record absent and Root typecheck passed |
| `pnpm verify:plugins` | Exit 0; **16 independent plugin repositories / 17 packed artifacts** installed, typechecked, tested, built and consumed without workspace links |
| Packed DSH activation | The packed Starter activated alone; three optional packed Strategies activated together |
| `pnpm release:check` | Exit 0; all 17 coordinated artifacts remain `0.5.0-beta.1` on the `beta` tag |

The full suites emit the already known missing source-map warning from the
published rc.1 UI primitives. It does not affect compilation or execution. The
two skipped tests are the opt-in Native CLI integration and Windows-only smoke
test.

## Real WebUI verification

The Browser skill drove the actual DSH rc.1 WebUI with built repository
artifacts, an isolated profile/workspace/storage root and a loopback-only model
stub. Better Sidebar `0.19.0-alpha.0` at
`dde0923980dc01798c09696bcd5987c280bff927` and all three optional Strategy
bundles were installed.

- Status reported `dsh-mnemon 0.5.0-beta.1`, all three Sources running and the
  expected disabled state for eight unconfigured third-party Providers.
- Plugin management discovered three Sources and four Strategies. Enabling the
  Light Context Strategy changed the composition from 1/4 to 2/4, saved
  successfully and remained enabled after a complete WebUI restart.
- Runtime created a branch-scoped critical memory and then edited it through
  the real public management path.
- After binding the disposable workspace, Documents created and read a managed
  Markdown document with its revision, hash, source path and rendered body.
- Memory Spaces navigated Overview, Search, Content and Entities. Creating a
  native space without a CLI produced the expected actionable `ENOENT` alert
  and kept the dialog and application responsive.
- DSH Settings loaded the complete Mnemon scope, Source, Provider, background
  Agent and conversation controls under the rc.1 Client Store boundary.
- Better Sidebar offered one Memory System tab; opening it rendered the same
  shared workspace and reflected the Runtime and Documents writes.
- One actual conversation completed through the loopback model while the
  composed default plus Light Context Strategies were active.

The final browser page reported zero console errors. Five warnings came only
from the deliberately terminated first WebUI port during the persistence
restart; the replacement page completed all checks without a script error. Two
model requests stayed on localhost. The browser tab and server were stopped,
and the explicit disposable fixture was moved to Trash after verifying the
server no longer listened. No personal profile, credential, memory or remote
model was used.

## Retained limits

- This run verifies integration and behavioral preservation; it does not claim
  improved LLM accuracy, token cost or Provider network latency.
- Native success was not rerun because no Native CLI was installed; its
  user-facing failure path, isolated provider tests and package boundary were
  verified instead.
- Windows execution and live third-party Provider accounts were not available.
- The rc.2 backward WebUI regression was not repeated in this run; v0.4.7's
  retained compatibility evidence and branch-level compatibility fixtures
  remain present.
- No package, branch or tag was pushed or published.
