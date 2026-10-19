# Latest-main rebase verification — 2026-09-03

This is an integration record for the Composable View development branch. It
does not announce a release or a new memory-quality benchmark.

## Revisions

| Item | Revision |
|---|---|
| Development branch | `codex/view-concretization` |
| Pre-rebase development baseline | `5e4aac5c3f53be99d84c5741e6068ce4e68d3d09` |
| Recoverable local backup | `codex/archive-view-before-main-rebase-20260903` |
| Latest fetched `origin/main` and local `main` | `b2b589ee95ab6987af08a4630f4e95c0c3f6f537` — v0.4.6 |
| Verified integration code | `239ca355bc705f189dbefceffa8c5dae0261fcd5` |

The development history was rebased onto the fetched main with merge topology
preserved. Main was not merged into the feature branch, and the feature branch
was not pushed or published. The local `main` reference was fast-forwarded to
the same fetched revision. Experiment branches and their worktrees were not
changed.

## Main behavior mapped into the composable architecture

| Main addition | Final owner and boundary | Verification |
|---|---|---|
| Both DSH session-projection contracts (#147) | The Host adapter accepts both projection shapes. Core, Source and Strategy contracts remain DSH-independent. | Projection fixtures, Host lifecycle and full suite |
| Stable `events[]` and alpha `snapshotEvents()` / `eventAt()` (#156) | `src/host/session-events.ts` performs capability detection and exposes one Host-local snapshot/read path. Callers no longer assume that a DSH session is directly iterable. | Four compatibility cases, alpha.5 source build, full suite |
| Optional Better Sidebar placement (#153) | Better Sidebar contributes only a DOM seat and `{ sessionId, cwd, visible }` scope. DSH's `shell.overlay` renderer still owns every Source child Slot; the shared workspace is portalled into the optional seat from that owning React tree. No Source receives a Better Sidebar dependency. | Unit lifecycle/context tests and real WebUI clicks |
| v0.4.4–v0.4.6 release/docs changes | Historical stable release material is retained. The development distribution remains the coordinated `0.5.0-beta.1` set. | Release consistency and package gates |
| DSH alpha.5 source verification | The temporary linker overlays the full Root DSH graph and only the Cordis identity inside installed plugin workspaces. Plugin Client dependencies stay workspace-local, preserving their independent Vite/test boundary. Every original pnpm link is recorded and restored. | 37-path link/restore cycle and complete alpha.5 verification |

The first Better Sidebar bridge attempted to pass a Source renderer through the
optional service. Real WebUI navigation rejected that design because the Slot
was rendered outside its installed DSH renderer tree. The DOM-seat/portal model
replaced it, preserving the actual ownership boundary instead of suppressing
the error. Likewise, linking every plugin Client dependency to the external DSH
checkout crossed each plugin's Vite filesystem root; limiting plugin overlays
to the shared Cordis identity fixed the boundary without weakening tests.

## Automated verification

Environment: macOS, Node `v25.1.0`, pnpm `11.19.0`. Normal dependencies use
DSH `0.1.1-rc.2`; the source-only compatibility run used a built DSH
`0.1.2-alpha.5` checkout at `db6bdc3576c2d4e7c965e8e3ed0c2a731eed87f5`.

| Gate | Result |
|---|---|
| Complete `pnpm verify` against DSH alpha.5 source | Exit 0; Root and 16 plugin builds/typechecks/tests, Headless and package gates passed |
| Root Vitest portion | **113 files passed / 1005 tests passed**; 2 files / 2 tests conditionally skipped |
| Deterministic build | 38 generated files matched across both builds |
| Headless fixture | 38 total tools / 7 representative Mnemon tools |
| Package/export checks | 45 packed files; 11 Node-compatible entries; 25 public type dependencies; `publint` and `attw` passed |
| Source link restoration | All 37 Root/workspace dependency paths restored to their original registry links; normal Root typecheck passed afterward |
| `pnpm verify:plugins` on restored registry graph | Exit 0; **16 independent plugin repositories / 17 packed artifacts**; standalone install/typecheck/test/build and external consumer passed |
| Packed Starter and Strategies | Real DSH activated the packed Starter alone and three optional packed Strategies together |
| `pnpm release:check` | Exit 0; all 17 coordinated `0.5.0-beta.1` artifacts remain on the `beta` tag |
| Git integrity | Freshly fetched main is an ancestor; local main matches origin; whitespace checks passed |

The two skipped tests are the opt-in real Native binary integration and the
Windows-only smoke test. Registry UI primitives emit an existing missing source
map warning during Client tests; tests and builds complete successfully.

## Real WebUI verification

The Browser skill drove the actual DSH rc.2 WebUI using built Mnemon artifacts,
an isolated profile/workspace/storage root and a loopback-only model stub.
`dsh-better-sidebar` `0.19.0-alpha.0` at
`dde0923980dc01798c09696bcd5987c280bff927` was loaded as the optional service.

- The Better Sidebar menu contained exactly one **Memory System** item.
- Opening it rendered the shared Mnemon workspace and successfully navigated
  Status, Runtime, Documents and Memory Spaces.
- Collapsing Better Sidebar and opening the native Mnemon Sidebar still rendered
  Runtime, demonstrating that optional placement did not replace DSH placement.
- A fresh browser tab restored the persistent Memory System tab and all three
  Source pages tested there.
- The fresh tab reported zero browser console errors and zero warnings.

The first browser run intentionally retained the real Slot ownership failure as
diagnostic evidence before the portal correction. The successful rerun used a
fresh disposable fixture. Both browser tabs, the server and the fixture were
closed or removed; no personal profile or remote model was used.

## Retained limits

- This run does not claim improved LLM accuracy, token cost or Provider network
  latency; it verifies integration and behavioral preservation.
- Native execution, Windows execution and live cloud Provider accounts were not
  rerun here.
- No package, branch or tag was pushed or published.
