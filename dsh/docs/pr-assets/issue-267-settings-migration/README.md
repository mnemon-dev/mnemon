# DSH Settings compatibility and recovery — issue #267

[简体中文](./README.zh-CN.md)

## Baseline reproduction

On 2026-09-22, unmodified main `65c0e23ba410993e16c00c8b3adf92d59d853425` reproduced the activation failure with published DSH `0.1.7-alpha.1` in a disposable Web profile. The Mnemon root reported:

```text
mnemon (dsh-mnemon): TypeError: ctx.settings.register is not a function
```

The plugin page showed the root in an error state and dependent Sources and Strategies waiting for their dependency. The local activation record identifies this result as `reproduced`; selected installed DSH files were compared with integrity-verified public npm tarballs.

![Baseline: Mnemon activation fails and dependent plugins wait](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/before-alpha7-activation.png)

See the [packed-artifact harness](./harness/HARNESS.md) for preparation and launch instructions. Baseline and fixed artifacts use separate disposable profiles. Only the baseline requires `--legacy-peer-deps` to get past its outdated peer range and expose the runtime failure; the fixed consumer uses normal peer resolution.

## Implementation and public contracts

DSH `0.1.7-alpha.1` replaces dynamic Settings registrations with forms derived from each plugin's static `Config`. The implementation uses published `SettingsForms.configure/describe/mutate`, `ConfigEditor.edit`, the Cordis `internal/config` waterfall, and the Loader's `loader/volatile-update` event. Official DSH packages, manifests and source are unchanged. No DSH source checkout, workspace alias or replacement Settings service is used by the artifact harness.

- [Live Config](../../../src/host/live-config.ts) uses real volatile references from the published DeepSeek Schemastery/Cosmokit packages. It preserves the object schema for native form discovery and performs pure cross-field validation. `remoteAccess` remains an ordinary field with the host's normal remount behavior.
- A private static ESM bridge imports the unchanged published fork through the npm alias `schemastery-live`. Its narrow declarations prevent the fork's new global generic from merging into legacy schema builders. The canonical development dependency stays at `3.18.2` for the unchanged RC.1 type cohort, including an upstream declaration that omits this runtime dependency. The alias does not appear in Mnemon's emitted public types; consumer Hosts keep their own framework dependency versions. No official package, TypeScript source path or import order is patched.
- The [settings adapter](../../../src/host/settings-service.ts) retains Mnemon's client namespaces while binding reads, validation and writes to the owning Entry/Fiber. Existing hosts with `settings.register` retain their original service. Profile writes go through DSH's locked editor; the owner-scoped preflight validates the candidate runtime before persistence, and committed volatile updates refresh the runtime.
- Core, UI and View settings share one native revision. A bounded retry is allowed only when that namespace's unredacted effective, inherited and explicit values still match the observed snapshot. Same-namespace conflicts, missing snapshots, read-only state, disposal and Entry replacement are rejected. Remote descriptors remain redacted.
- [Plugin management](../../../src/host/plugin-management.ts) reapplies selected Entry state after whole-profile reconciliation, validates the resulting composition, and compensates failed transactions.
- [Client icon aliases](../../../src/client/ui-icons.ts) support both the old size-suffixed exports and alpha7's weight-suffixed exports. The settings footer now describes DSH-managed live saving in both languages without hardcoding the removed settings file.

- [Session messages](../../../src/host/lifecycle.ts) use the producer-owned `dsh-mnemon` source kind required by alpha7's V4 codec. Reading and deduplication still recognize both historical wrappers and the official `plugin:dsh-mnemon` migration. A real WebUI turn exposed this additional compatibility failure after activation was repaired.

## Retained settings and existing profile choices

Recovery reads `settings.yaml.imported` without modifying its bytes and writes only through `ConfigEditor.edit`. Its [pure planner](../../../src/host/legacy-settings-import.ts) maps the canonical root's retained sections as follows:

| Retained section | Profile Config destination |
| --- | --- |
| `mnemon` | Root fields missing from the current explicit override |
| `mnemon-ui` | `conversationInteraction` |
| Exact `mnemon-view[-hash]` | `memoryView` |
| Exact `mnemon-plugins[-hash]` | Source activation in `memoryView.entries`, only for confirmed Source Entries |

Current explicit root/UI choices win. Explicit `memoryView` fields, including an empty `entries` reset, are preserved. Existing Strategy rows take precedence over older saved activation/configuration; Source configuration remains on its native Entry. Local patch IDs are matched to their unambiguous full Loader IDs. Unrelated rows are not replaced. The plan is recomputed inside the editor callback so an intervening explicit edit wins, and `legacySettingsImported` prevents later resets from resurrecting old preferences.

Native `!!js` expressions are retained as expression data, never evaluated or converted into literal strings by recovery. An expression-controlled activation or Strategy configuration causes that Entry's old View overlay to be skipped. Source configuration expressions and unrelated expression rows stay on their native rows. Expressions in relevant legacy backup sections, malformed data and unsupported YAML are rejected without completing recovery or changing the backup.

## Migration and rollback limits

- If the original `settings.yaml` exists at startup, DSH performs its own asynchronous rename/import. Mnemon defers supplemental recovery until the next cold Host start; a page refresh or plugin remount does not clear this gate.
- Recovery accepts only the canonical root Entry `mnemon` and the exact owning profile's historical namespace suffix. It does not guess custom-root ownership, foreign profile hashes or an unsuffixed fallback. Ambiguous or rejected input remains available in the retained backup for manual review.
- Back up the DSH profile configuration, legacy settings and relevant Mnemon data before upgrading. New profile settings are not mirrored back to `settings.yaml`. A downgrade requires the corresponding configuration backup and manual reconciliation of later changes; there is no automatic reverse export.
- This changes preference storage and recovery, not the Runtime, Documents or Memory Spaces data formats.

## Verification scope

Focused regressions cover [schema/reference behavior](../../../tests/live-config.spec.ts), [owner isolation and redaction](../../../tests/profile-settings.spec.ts), [revision conflicts and retired owners](../../../tests/profile-settings-revisions.spec.ts), [pure migration planning](../../../tests/legacy-settings-import.spec.ts), [retained-file recovery and expressions](../../../tests/profile-settings-import.spec.ts), and [both client icon generations](https://github.com/omdsh-dev/dsh-mnemon/blob/65a14ddd2d83bd0850948dc915bc410552b545d9/tests/client-primitives-compat.spec.tsx).

The artifact harness uses isolated test data and a deterministic model endpoint bound to `127.0.0.1`; it requires no external model API key and calls no external model provider. Bootstrap URLs, `server.json`, raw `web.log` and model request logs remain outside the repository. Public evidence must exclude credentials and private data.

## Measured settings and migration results

Commit `407eac75ee46e677f694e6c318ca9380357fae27` passed the following browser operations with 17 packed Mnemon artifacts. The later Session source-format follow-up is tracked separately; these runs verify the settings implementation. Machine-readable hashes and results are in [settings-verification.json](./settings-verification.json).

| Published DSH | Uniform DSH packages | Official installed files checked | Actual WebUI operations |
| --- | ---: | ---: | --- |
| `0.1.5-rc.2` | 234 | 8 | Workbench; disable Light while Auto Capture/Scoped remain enabled; Core/UI save; reload |
| `0.1.6-alpha.2` | 251 | 8 | Same operations on the alpha Settings service |
| `0.1.7-alpha.1` | 267 | 10 | Root and all Sources/Strategies activate; Core/UI consecutive saves; separate UI save; reload and full Host restart |

On alpha7, the interval remained `310000`, Light stayed off, Auto Capture/Scoped stayed on, and both UI toggles stayed off after restart. On RC.2 and alpha.2 the saved intervals were `320000` and `330000`, with Light/turn bar off and the other toggles on. No false revision conflict occurred.

The retained-backup fixture restored recall limit `7`, turn bar on/save action off, and Auto Capture on. Current Light activation/configuration (`1200`) and Documents activation won over legacy values; foreign-profile Scoped preferences were ignored. After a full Host restart, the profile patch, root Config, all 21 non-root rows and original backup hashes were unchanged. Before that restart, 20 initial non-root rows matched the harness initialization semantically; the extra row was the browser's welcome-notice acknowledgement.

The real Mnemon CLI `0.2.8` also answered the WebUI status operation and created/enabled a native memory space with normal storage. The optional default Ollama embedding endpoint was not running; no external embedding or model key was used.

![Fixed alpha7 root and Source activation](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/after-alpha7-activation.png)
![Fixed alpha7 memory workbench](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/after-alpha7-status.png)
![Core/UI settings saved without a false revision conflict](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/after-alpha7-settings-saved.png)
![Strategy choices survive a complete Host restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/after-alpha7-host-restart.png)
![Retained UI preferences after recovery](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/after-alpha7-legacy-settings.png)
![Retained preferences survive restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/after-alpha7-legacy-restart.png)
![Native CLI space created and enabled through WebUI](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/after-alpha7-native-cli.png)
![RC.2 settings save](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/after-rc2-settings-saved.png)
![Alpha.2 settings save](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/after-alpha2-settings-saved.png)

## Session fix: packed verification before type isolation

The Session fix at `dc10b5b6a2b39bd3093823741e286592dc640cd6` was packed from a clean checkout and installed into three fresh consumers using normal npm peer resolution. All three used the same 17 Mnemon archives; the root archive SHA-256 is `b2204a284db063a128dce845cd115ecac33b5c91e91071d050d2a9d5daf1db16`. [final-verification.json](./final-verification.json) records this stage's artifact hashes, public-package checks, persisted tool receipts and test results. The final type-isolated artifacts are verified below.

Actual WebUI turns on `0.1.5-rc.2`, `0.1.6-alpha.2` and `0.1.7-alpha.1` called `mnemon_runtime_memory` with `action: add`, then `mnemon_status`. Each turn wrote exactly one fixture memory and returned a healthy status with the native CLI found and writes enabled. Each compressed Session decoded completely and retained two messages with source kind `dsh-mnemon`. RC.2 and alpha.2 wrote Session V3; alpha.7 wrote V4. The fixture prompt and title retain `compatibility-261` / `Issue 261 compatibility` because the deterministic model fixture is reused; the artifacts, profiles and screenshots in this directory belong to Issue 267.

Before the Session fix, the real alpha.7 turn failed with `format v4 message requires a producer-owned source kind`. The regression uses the actual published alpha.7 `encodeEvent` and `restoreReleasedV4Artifact` contracts, including rejection of the old wrapper. The completed lifecycle suite passed 56 tests.

On these alpha.7 artifacts, the browser selected Builtin display mode, disabled Light, saved interval `340000`, disabled the turn bar and retained the save action. After a complete Host process restart, the browser confirmed all settings, the restored conversation and its one Runtime memory. Auto Capture and Scoped remained enabled through their native Entries without unwanted View overrides.

The separate original-file fixture also passed both migration stages: DSH first renamed/imported `settings.yaml`, with Mnemon's supplemental marker and View absent; one full Host restart then restored the expected UI/View preferences and wrote the marker. The retained backup remained byte-for-byte unchanged. That settings-only run used `407eac75`; the final Session commit does not change its settings implementation.

![Final alpha.7 turn completes both Mnemon tool calls](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/final-alpha7-session-tools.png)
![The Runtime tool writes one visible fixture memory](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/final-alpha7-runtime-write.png)
![RC.2 completes the same real tool calls](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/final-rc2-session-tools.png)
![Alpha.2 completes the same real tool calls](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/final-alpha2-session-tools.png)
![Final alpha.7 settings after a complete Host restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/final-alpha7-settings-restart.png)
![Builtin workbench retains the Runtime memory after restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/final-alpha7-builtin-restart.png)
![Original-file migration completes on the next Host start](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/after-alpha7-native-import-restart.png)

## Final type-isolated artifacts and WebUI verification

Clean CI exposed a global generic declaration collision between legacy Schemastery and the new live fork. The private bridge described above isolates the runtime import and preserves the existing main lockfile package/snapshot values. Implementation commit `d57e14054a4c523097b382ff36ca6bd3a993ff03` was packed from a clean checkout and installed with normal peers into three new consumers. The final root archive SHA-256 is `552eb9c23846e23e958100974888351a74666c471d614d2f098f2426d5a81cb3`; all 17 artifact hashes and installation checks are recorded in [type-isolation-verification.json](./type-isolation-verification.json).

All three public DSH cohorts completed fresh WebUI turns with real Runtime writes and native CLI status calls. Each installed `schemastery-live` package matched all eight files of the unchanged official `@deepseek-ai/schemastery@3.18.3` archive. The DSH package counts and selected official-file checks remained 234/8, 251/8 and 267/10 for RC.2, alpha.2 and alpha.7 respectively.

On alpha.7, the browser prepared Core/UI edits, switched Light off through the separate immediate plugin write, and saved the remaining draft without a false conflict. After a complete Host restart, Builtin mode, interval `350000`, turn bar off/save action on and Light off/Auto Capture and Scoped on all persisted. The restored conversation completed loading, and its Builtin workbench displayed the single Runtime memory. The profile and compressed Session checks are retained alongside the browser evidence.

[Source verification on Node 22.19](https://github.com/omdsh-dev/dsh-mnemon/actions/runs/35733033143/job/106763140386) and [packed plugin verification on Node 24](https://github.com/omdsh-dev/dsh-mnemon/actions/runs/35733033143/job/106763140029) passed for this implementation commit.

![Type-isolated alpha.7 artifacts complete both tools](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/v4-alpha7-session-tools.png)
![Type-isolated RC.2 artifacts complete both tools](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/v4-rc2-session-tools.png)
![Type-isolated alpha.2 artifacts complete both tools](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/v4-alpha2-session-tools.png)
![Builtin setting persists after full Host restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/v4-alpha7-settings-restart.png)
![Interval and composed Strategy choices persist](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/v4-alpha7-strategies-restart.png)
![Final Builtin Runtime memory survives full Host restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-267-settings-migration/v4-alpha7-builtin-restart.png)

## Complete local checks and limits

The following commands passed, with the plugin check run after `verify` completed:

```sh
MNEMON_NATIVE_TEST_CLI=/path/to/mnemon \
MNEMON_DSH_V4_CONTRACT_ROOT=/path/to/alpha7-consumer/node_modules \
  npx --yes --package=node@22.19.0 --package=pnpm@10.13.1 pnpm run verify
npx --yes --package=node@24.20.0 --package=pnpm@10.13.1 pnpm run verify:plugins --skip-build
```

- Root tests: 1,342 passed, 5 skipped; plugin tests: 382 passed, 2 skipped. The skipped cases require an external Flash model, Windows, or a live OpenViking service. The real native CLI and published V4 codec cases were enabled.
- Documentation, type checks, deterministic build, package-content budget, public entry imports, declaration dependencies, `publint` and `attw` passed. The final root package contains 49 files and 1,370,296 unpacked bytes; 12 Node-compatible entries, 28 public type dependencies and the repair CLI help were verified.
- The Headless check passed with 39 tools and 8 representative Mnemon tools. Independent-consumer verification passed for all 16 plugin repositories and 17 tarballs, including an SDK-only external consumer, all three optional Strategies together, and Starter-only activation.
- The browser fixtures ran on macOS with Node `25.1.0`; final source and packed checks ran on Node `22.19.0` and `24.20.0`. This is Mnemon compatibility evidence, not an end-to-end check of every DSH feature. The minimal fixture omits terminal/deliverables/workspace-change services; their waiting entries, general permission-preset request failure and alpha.2 terminal retry button do not represent Mnemon failures. External model quality, external Providers and embeddings are outside this run.
- Repeated loopback fixtures produced a browser HTTP 431 during the final restart check. A synthetic 14 KB cookie reproduced a short request succeeding and the longer plugin-resource request failing. Navigating the same running listener through `localhost` completed the browser check without changing profile/data, framework files, server limits or existing browser cookies. See the [harness note](./harness/HARNESS.md#repeated-loopback-runs) for repeating this setup safely.
