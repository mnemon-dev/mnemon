# Issue 261: published DSH RC and alpha compatibility

**English** | [简体中文](./README.zh-CN.md) | [Evidence index](../README.md)

Recorded on 2026-09-18, macOS arm64, Node 25.1.0, Mnemon Native CLI 0.2.8. Issue: [#261](https://github.com/omdsh-dev/dsh-mnemon/issues/261).

The baseline is the v0.5.11 release tree at `02ce994c8f05b6bf83a6b5872bd018bbfc8970ce`, merged into main as `245c9e2f6a50e6a77e7ba3c679ce07ba80458925`. The isolated fix branch started from that main commit. Production code tested here is `2a593c9a6f6510afc0e12704ff2103abc1a1f597` (including `60a481f`). The patched package still has development version 0.5.11; its integrity identifies the tested artifact, not a new npm release. The root-only patch changeset controls the next publication.

## Reproduction and change

On published DSH `0.1.6-alpha.2`, v0.5.11 completes actual Runtime add/status tools, but the browser reports `list slot "conversation.chat.turnTail" requires options.id`. Neither the turn memory bar nor the later-registered save action appears. See [original browser error](./baseline-browser-error.json) and the screenshots below.

The fix supplies stable ID `dsh-mnemon/turn-tail`, retains the RC chain selector, and guards activity reads/rendering inside the component for the alpha list renderer. A full production type check also found three accesses to the removed `SessionListState.current`. Sidebar and reactive Settings now use the public default/main UI Session binding; Builtin and Better Sidebar preserve their explicit owning session, including an explicitly absent session. Source, Strategy and Provider implementations remain in their own packages.

The two root DSH peer ranges now require `^0.1.5-rc.1 || ^0.1.6-alpha.2`. Older DSH releases without the required public UI Session service are not claimed compatible. Rolling back such a host requires its previously verified Mnemon release. No stored-data or configuration migration is introduced.

## Observed results

| Check | Release baseline | Fixed RC `0.1.5-rc.2` | Fixed alpha `0.1.6-alpha.2` |
|---|---|---|---|
| Full production public type graph | RC: 196 files, 0 errors; alpha: 196 files, 4 errors | 198 files, 0 errors | 198 files, 0 errors |
| Normal packed npm install / `npm ls --all` | Alpha needs the documented baseline-only bypass | Pass; 234 exact-cohort DSH records | Pass; 251 exact-cohort DSH records |
| Real WebUI Runtime add and status | Alpha tools succeed; bar/save action missing | Pass | Pass |
| Turn bar off / restored / after page reload | Missing on alpha | 0 / 1 / 1 | 0 / 1 / 1 |
| Other turn-tail contributions | No peer fixture | Two chain selectors decline, as configured | A and B each remain present while Mnemon is off, and each appears once after reload |
| Sidebar and Builtin tool-chip navigation | Not accepted on baseline | Correct Runtime record | Correct Runtime record |
| Save action | Absent on alpha | Correct final-answer candidate; cancelled | Correct final-answer candidate; cancelled |
| Packed Headless add/status, new-process persistence, Root disable | Alpha passes after protocol correction | 17 enabled tools, persisted bytes, 0 disabled tools | 17 enabled tools, persisted bytes, 0 disabled tools |

Alpha also passed an actual session-scope check: after selecting workspace storage, a second session wrote one record in `workspace-b`; switching back to the original session showed zero records in `workspace`. The earlier global record remains in its original global store. The new-session screen had no Builtin tab. Scoped/absent session, pending cwd, stale activity responses, and ambiguous/foreign Builtin anchors have focused component regression tests; this browser record does not claim an exhaustive third-party split-pane test.

`pnpm run verify` passed, including 1,183 root tests, the actual Native CLI create/write/read/forget integration, actual same-View Native archival through Host tools, deterministic builds, public entries and the RC1 Headless profile. `pnpm run verify:plugins --skip-build` passed all 16 independent plugins, 17 packed artifacts, external consumers, Starter upgrade from 0.4.7 and the three enabled Strategy extensions. Five opt-in live Flash tests, one live OpenViking integration and one Windows-only smoke test were skipped in this macOS run. Root package contents: 47 files, 299,146 packed bytes, 1,338,363 unpacked bytes; the checked budget is 1,339,000 bytes.

## Screenshots and retained evidence

Screenshots are original browser captures, without editing. All visible memory is synthetic test data.

| Before: alpha registration failure | After: alpha, both peer contributions and Mnemon |
|---|---|
| ![Alpha before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-261-dsh-slots/01-alpha-before-missing-turn-bar.png) | ![Alpha after](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-261-dsh-slots/02-alpha-after-peer-turn-bar.png) |

Additional captures: [Sidebar Runtime record](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-261-dsh-slots/03-alpha-runtime-navigation.png), [Mnemon off with peers retained](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-261-dsh-slots/04-alpha-bar-off-peers-retained.png), [Builtin navigation](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-261-dsh-slots/05-alpha-builtin-navigation.png), [workspace B record](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-261-dsh-slots/06-alpha-workspace-b-memory.png), [workspace A isolated](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-261-dsh-slots/07-alpha-workspace-a-isolated.png), [RC completed turn](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-261-dsh-slots/08-rc-after-turn-bar.png).

- [Browser observations](./browser-results.json), [type-check summary](./typecheck-results.json), and [verification/provenance summary](./verification.json).
- [Packed harness and reproduction commands](./harness/HARNESS.md), [artifact integrity and cohort results](./harness/evidence-summary.json).
- [Production type-check script](./check-published-types.mjs): run against a normal, exact-cohort public SDK installation. Install matching `dsh-client-store` and `dsh-client-ui-slots` declarations too. The check includes root and all plugin production sources and rejects foreign DSH declarations; no DSH source checkout is used.

The loopback model fixture speaks OpenAI and Anthropic protocols and requests actual installed Mnemon tools. No external model API was called. The UI's model label is a fixture setting, not evidence of live Flash inference. Runtime add writes real Runtime Source JSON; it is distinct from the separately passed Native memory-space CLI integration. Saving through a distillation Agent was not exercised beyond opening/cancelling its confirmation.

Only baseline alpha uses `--legacy-peer-deps`, because released 0.5.11 excludes alpha in its peers. Fixed installations use ordinary npm resolution. The first OpenAI-only alpha attempt failed because the fixture lacked Anthropic support; it is not classified as a product failure. Original public SlotCore, Chat, Renderer and Conversation bytes were verified unchanged. Unrelated subprocess services were disabled in the test profile; alpha consequently shows a terminal-restore warning. Extension-origin console messages were excluded explicitly; neither fixed cohort produced an application warning/error in the observed runs. This evidence covers mechanism compatibility, not model quality or live third-party Provider conformance.
