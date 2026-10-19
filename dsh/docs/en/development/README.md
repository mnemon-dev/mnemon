# Development and Verification

[简体中文](../../zh-CN/development/README.md) | **English** | [Documentation Center](../README.md)

## Environment and commands

The plugin's Node engine floor is 20. The pinned complete DSH development profile is the published 0.1.7-rc.2 release and needs Node `^22.19.0 || >=24.0.0`; use Node 24 for development. Root, Source Client tests and the external artifact consumer use that cohort. `dsh-invariants` closes its peer graph, while `dsh-client-store` owns the public selector type used by the subagent projection adapter. Public Node entries are also smoke-tested on Node 20 in CI. DSH 0.2.0-rc.1 on npm `next` is supported as well: `tests/dsh-host-compatibility.spec.ts` runs DSH's own pre-install compatibility check over all 18 package manifests for both runtimes. To check 0.2 behavior, install DSH globally in an isolated directory as [Compatibility](../reference/compatibility.md#dsh-02) describes, or move the development dependencies to 0.2.0-rc.1 temporarily.

DSH 0.1.7 UI primitives import Markdown/highlighting dependencies that its published manifest lists as development dependencies. Root, the three Source packages and the external consumer declare that complete cohort explicitly for standalone Client tests; Host artifacts still use DSH’s provided UI module. Tests use the public async Agent factory and durable `assistant/message` events. `tests/legacy-session-repair.spec.ts` runs the real released Session v0 → v3 migration over synthetic historical logs in plain and compressed form, checking explicit copy recovery, cold reopen and timed stream replay. Its audited cases cover all three old Mnemon summaries, compatible v2 descriptors, packed-placeholder expansion, null-to-empty delta names and closed tool chains with a previously recorded provider ID. It checks multi-call provenance, owner-reference refusal and original/plugin preservation. `pnpm e2e:serve --legacy-session-replay` additionally makes the actual WebUI's loopback continuation server verify the historical wire call/result IDs and payload before returning success.

```sh
pnpm install --frozen-lockfile
pnpm run verify
pnpm run verify:plugins
```

`verify` checks types, deterministic Root builds, independent plugin builds, the full test suite, a real isolated DSH Headless profile and package exports/contents. Independent plugin checks run as separate type/test phases after building all public artifacts. Do not mix `pnpm -r verify` clean builds with tests reading sibling artifacts; use `pnpm verify` for the whole workspace. `verify:plugins` repeats verification **outside** the workspace against semver-installed tarballs and an external Source/Strategy/Provider/Client consumer. It also installs only the packed Root into real DSH, resolving all seventeen official plugins from a loopback registry without workspace links or manifest rewrites, then separately verifies the three shipped enhancements moving from disabled defaults to simultaneous activation. The external consumer compiles its own Strategy extension against the owning Strategy's packed SDK.

## Repository ownership

```text
src/
  core/       contracts, View compilation, generations, turn leases
  sdk/        installMemory, validation, test fixtures
  host/       DSH lifecycle, settings, tools, RPC, worker coordination
  client/     shared workspace, settings, Source-page SDK
plugins/
  dsh-mnemon-source-runtime/
  dsh-mnemon-source-documents/
  dsh-mnemon-source-memory-spaces/
  dsh-mnemon-strategy-default-three-tier/  # the Layered strategy, the default main strategy
  dsh-mnemon-strategy-general/        # shipped, alternative main strategy
  dsh-mnemon-strategy-scoped/         # shipped, disabled selection contribution
  dsh-mnemon-strategy-light-context/  # shipped, disabled projection contribution
  dsh-mnemon-strategy-auto-capture/   # shipped, disabled in-turn capture contribution
  dsh-mnemon-provider-*/
tests/        Host/Core/UI composition and boundary tests
scripts/      reproducible build, artifacts, Headless and Web fixtures
cordis.patch.yml   default Starter composition
```

Root owns Core/SDK and the DSH Host/default Starter, not Source storage implementations. Each directory under `plugins/` is a publishable standalone project. The default distribution depends on all seventeen official plugins by public semver. The Starter installs the General strategy and the three enhancements with their Entries disabled; `memoryView.strategyTypeId` records which main strategy composes the View, and DSH's plugin manager records each component switch. Source/Strategy peers depend on Core's public SDK, Strategy extensions on their owner's public SDK, and Providers on the Memory Spaces SDK. Peer/development relationships can produce a package-manager cycle warning; production import boundaries are independently checked.

No private workspace packages, forwarding controller modules, business bindings or compatibility directory remain. Compatibility means retained user configuration, data and workflows, not retention of historical internal symbols.

Build Root before plugin clients that need its public browser artifact:

```sh
pnpm build
pnpm --workspace-concurrency=1 -r build
pnpm --filter dsh-mnemon-source-runtime verify
```

A plugin can be copied to a new repository and use its own `pnpm install && pnpm verify` once its declared peer versions are available. During unreleased development, use packed artifacts and the local-registry verification harness; do not substitute repository source paths.

## Test ownership and coverage

| Boundary | Tests |
|---|---|
| Core/SDK | Immutable Views, budgets, Strategy validation, concurrent turns, grants, leases, generation replacement, cleanup and performance |
| Strategy extension | Independent slots, combination/order, unload, conflict, read-only scope, shared quotas and actual Host activation |
| Source | Its controller/storage, revisions, snapshots, JSON operations, own Client clicks and instance isolation |
| Provider | Driver behavior, credentials, capability truth and fault responses |
| Memory Spaces | Provider child lifecycle, cross-provider conformance, merge/routing/quality and Native process serialization |
| Host | Default composition, settings/data scope, tools, supervised workflows, RPC authority, receipts and UX |
| Artifact | Every public entry, standalone install/build/test, external composition and browser artifacts |

The workspace identity, Client platform boundary and delegated workspace scope tests also run on Windows with Node 22.19 and 24. They cover real filesystem errors and junction aliases as well as a portable regression for Windows returning `ENOENT` below a file; that simulated error is not a substitute for the Windows run.

`tests/bundle-activation.spec.mjs` boots the published DSH Loader with disposable activation counters for all seventeen Starter packages. It checks the legacy `mnemon` configuration/disable target and independent component choices, then drives the published plugin manager: real component and bundle toggles must persist across reloads and restarts, the core gate must stop every active child, and no package may leave duplicate instances. Both cases run on the pinned host; set `MNEMON_BUNDLE_TEST_PROFILE=/absolute/profile` to check another installation instead. The fixture processes do not inherit the `NODE_PATH` that `pnpm exec` sets: DSH 0.2 routes plugin packages through Node's search paths, `NODE_PATH` included, so inheriting it would resolve this repository's real plugins instead of the fixture's packages. The fixture creates its own temporary profile and does not edit the supplied installation. It tests lifecycle contracts; packed Headless and WebUI checks still own real Mnemon behavior. Finally it checks that DSH's config schema collector reports only the Starter's component group, as an unrecognized tree carrier (a known trade-off, see [Compatibility](../reference/compatibility.md#installing-and-enabling-the-starter-without-a-restart)), and no other error. Run `node --expose-internals tests/fixtures/bundle-activation.mjs /absolute/profile manager --check-declared-rows` to additionally require every advertised bundle component to be manageable. That diagnostic fails on the known `0.1.7-rc.2` native-group listing defect described in [Compatibility](../reference/compatibility.md#dsh-017-bundle-component-list).

Remote Provider suites use controlled HTTP responses; Native process suites use controlled command runners plus an optional Windows binary smoke. A separate opt-in test uses an official checksum-verified Native binary to create a disposable space, write through a View, recall and forget:

```sh
MNEMON_NATIVE_TEST_CLI=/absolute/path/to/mnemon pnpm --filter dsh-mnemon-source-memory-spaces exec vitest run tests/native-integration.spec.ts
MNEMON_NATIVE_TEST_CLI=/absolute/path/to/mnemon pnpm exec vitest run tests/runtime-capacity-workflow.spec.ts -t 'two same-View Native'
```

The test never discovers a personal data root or installs a binary. These checks do not certify every live external service or every account configuration. Provider Lab is an explicit separate integration environment.

OpenViking has an opt-in loopback integration test: `MNEMON_OPENVIKING_TEST_ENDPOINT=http://127.0.0.1:1933 pnpm --filter dsh-mnemon-provider-openviking exec vitest run tests/integration.spec.ts`. Use only a disposable backend. It verifies a unique bilingual canary through create, exact readback, search, browse and exact deletion; normal CI skips it. A published v0.4.20 server with local deterministic embeddings validates the HTTP/storage/index contracts, not semantic-model quality.

The Runtime case creates and activates two Native spaces through real Host tools after the View is pinned, archives two exact checkpoints and verifies the pending add. Routing decisions are fixed locally; no model API is used.

The Agent Teams matrix loads the published DSH packages through their package exports only and runs with the ordinary test suite on the pinned host; `MNEMON_TEAM_TEST_PROFILE=/absolute/profile pnpm exec vitest run tests/agent-team-review-host.spec.ts` checks another installation of the same DSH/Teams version. It exercises real fork/spawn, native/Code Mode tools, all three Strategy extensions, restricted child execution, actual Runtime commits and parent Team tools. Reproduction profiles, the packed WebUI fixture and reviewed evidence are in the [Issue #275 record](../../pr-assets/issue-275-agent-teams/README.md).

The opt-in Flash pressure suite uses four real DSH sessions, delegated writers, independent maintenance tasks and a disposable Native store. It keeps the default 10 KiB limit and verifies exact committed content across repeated archival, namespace routing and session-free browser management. Supply a DeepSeek credential through `DEEPSEEK_API_KEY` and a verified CLI through `MNEMON_NATIVE_TEST_CLI`, then run:

```sh
MNEMON_RUN_FLASH_STRESS=1 MNEMON_FLASH_STRESS_ROUNDS=8 MNEMON_FLASH_STRESS_REPORT=/tmp/mnemon-flash-stress.json pnpm exec vitest run tests/runtime-capacity-flash-stress.spec.ts
```

Every outgoing request and returned model is checked against `deepseek-v4-flash`; the suite uses non-thinking mode and never selects Pro. It reports model input changes separately from storage changes, uses synthetic project facts, and removes its temporary stores and sessions. Destination authorization is a hard assertion; semantic topic matches are reported separately as model quality. Set `MNEMON_FLASH_STRESS_JSON_PROMPT=1` to repeat the quoted-JSON input diagnostic. It is skipped in ordinary CI and requires explicitly authorized live API usage.

The separate automatic-memory acceptance suite simulates backend, frontend, Android and operations work in four concurrent DSH sessions. Developer prompts contain accepted decisions, corrections and disposable diagnostics; the model chooses its own memory actions. Actual temporary JSON files provide bounded development tasks, while the real default lifecycle performs idle review. Fresh reader sessions have no development transcript or file access. Supply the same credential and CLI environment variables, then run:

```sh
MNEMON_RUN_FLASH_QUALITY=1 MNEMON_FLASH_QUALITY_REPORT=/tmp/mnemon-flash-quality.json pnpm exec vitest run tests/runtime-memory-flash-quality.spec.ts
MNEMON_RUN_FLASH_QUALITY=1 MNEMON_FLASH_QUALITY_WAVES=24 MNEMON_FLASH_QUALITY_REPORT=/tmp/mnemon-flash-quality-long.json pnpm exec vitest run tests/runtime-memory-flash-quality.spec.ts -t 'four preconfigured'
```

The default is 12 waves per session; the extended workload has 24. Both use Flash with thinking disabled, guided recall/writeback and the default 10,240-byte memory limit. Only the idle debounce is shortened from 30 to 5 seconds; eligibility rules stay unchanged. The suite records retained facts, transient markers, corrected answers, module attribution, no-write turns, tool errors and archival. A successful Vitest run means the experiment completed: inspect `finalEvaluation.automatedVerdict` and manually audit retained prose for stale documents, duplicates and scope errors before declaring quality acceptance. The [2026-09-09 acceptance report](../../pr-assets/runtime-memory-quality-flash-20260909/README.md) records failures as well as successful checks. Ordinary CI skips both live cases. This simulation does not substitute for full application builds, a half-day human workflow or Windows testing.

Keep generated live-run output outside the repository, as the commands above do. Follow the [evidence storage policy](../../pr-assets/README.md): commit summaries, reproduction inputs and minimal examples; attach complete sanitized run data to the PR with its revision and SHA-256.

The performance regression composes 100 three-Source Views under wall/CPU budgets. Deterministic builds compare all generated hashes. Neither check promises production network latency or LLM quality.

`pnpm bench:algorithms` measures local query, storage, compaction and recall scaling at N=1/10/100/1000 in temporary synthetic stores. It writes `.cache/algorithm-benchmark.json`; use `MNEMON_BENCH_OUTPUT` for another destination or `MNEMON_BENCH_SIZES=1,10,100` for smaller cases. Run it without concurrent tests/builds. The [algorithm audit](../../pr-assets/algorithm-performance-20261003/README.md) records the workload definitions, before/after results, operation counters, complexity bounds and external-Provider limitations. Normal CI uses deterministic equivalence/work-bound tests, not latency assertions from this benchmark.

Both the default and three-extension profiles run that performance fence. The
[2026-09-01 Strategy contribution verification](../../pr-assets/strategy-extensions-20260901/README.md)
records coexistence, independent artifacts, real Headless activation and limits.

## Real WebUI

For stylesheet placement, stable selectors, activation scope, migration from generated classes and acceptance steps, see [Skin development and Mnemon integration](./skin-integration.md).

```sh
pnpm build
pnpm --workspace-concurrency=1 -r build
pnpm e2e:serve
```

The fixture prints a temporary workspace and loopback URL. It isolates `DSH_HOME`, `MNEMON_DATA_DIR`, browser workspace and model endpoint. Select **Mnemon E2E** for a conversation; this test-owned preset omits Shell requirements while retaining Host memory tools. The model returns a fixed answer, so this fixture checks UI and transport, not real-model distillation quality. Stop with Ctrl-C to remove its synthetic data.

`pnpm e2e:serve --review-evidence --strategy-extensions` adds a per-Agent synthetic overview tool and fixed parent/reviewer calls. It verifies five inherited chunks and attempts the same foreign read before and after the guard; see the [issue #211 evidence](../../pr-assets/issue-211-20260911/README.md). `tests/review-evidence-host.spec.ts` also covers real DSH native and Code Mode dispatch before the provider's start promise returns.

Check Sidebar without a session, all primary/secondary tabs, Runtime add/edit/remove and branch clear, Documents create/search/read, Provider settings/discovery, activation, error states, dialog cancellation, Save-to-memory, layout switching, locale and restoration of chat interaction. On the `dsh-mnemon` page under Plugins, also check the main strategy selector, each component's switch and page, DSH's component rows and the pages they open, the Default/Custom storage location, and a backup import preview. Use a disposable real Provider or controlled fixture for write/read/forget; never test against personal memory.

For issue #233, run `node scripts/fixtures/openviking-protocol.mjs` and `pnpm e2e:serve --openviking-write`. Configure OpenViking at `http://127.0.0.1:19335`, account/user `default`, with no API key, then send `openviking-write-233` in Mnemon E2E. The model fixture drives the real Host and delegated writer tools exactly once. The protocol fixture deliberately reports a cosmetic extraction update with no stored candidate to expose the baseline's false receipt; its `/__fixture` endpoint labels and reports synthetic requests/files. Repeat against a disposable real OpenViking server for backend acceptance. The protocol fixture is not evidence of live extraction or semantic quality.

For an embedded Electron Host, pass `pnpm e2e:serve --electron=/absolute/path/to/electron` (on macOS, use `Electron.app/Contents/MacOS/Electron`). Supply a separately installed test Electron executable and an isolated npm prefix through `MNEMON_CLI_PATH` and `npm_config_prefix`. The fixture runs the published DSH Web stack in Electron's main process with no `ELECTRON_RUN_AS_NODE` on the Host. It exposes Node internals for the published Cordis loader, without rebuilding or modifying DSH packages. Stop with Ctrl-C as usual.

Also switch `displayMode` live: the Sidebar and the conversation tab (`builtin`) must never mount together. Both use the same Source pages; the conversation tab follows its owning session for global/workspace/workspaces/custom reads, writes and tasks, hides scope controls, and clears stale data and editors when the session changes. Check legacy `buildin` normalization and the collapsed icon under the native Sidebar skin as well as supported layout plugins.

The [2026-09-04 main-rebase verification](../../pr-assets/main-rebase-20260904/README.md) records the exact v0.4.7/DSH rc.1 revisions, full registry and source-overlay suites, independent artifacts, plugin composition persistence and real shared-placement checks, including their limits.

The [2026-08-30 npm regression record](../../pr-assets/npm-sidebar-cli/README.md#english) preserves the old Taskboard/SSH and CLI investigation. Its removed legacy harness is not a current checkout command; use `pnpm e2e:serve` for current WebUI work.

Separate upstream profile/transport warnings from Mnemon failures rather than hiding the console.

For the Documents archive regression, use `pnpm e2e:serve --document-archive`. Create and activate a disposable exact-write Memory Space, create a document and archive it from the workbench. A title containing `REJECT` deliberately proposes an invalid destination; verify that the document stays active and no index appears. Rename it and retry. Send `archive-tool-222 prepare`, `archive-tool-222 update`, and `archive-tool-222` in separate Mnemon E2E conversation turns to drive real create → update → archive tools and assert the returned lineage. Only model decisions are scripted; storage, tools, transport and the browser remain real. The same fixture can reproduce the legacy receipt-index mismatch when used with the old Host build.

For Runtime write-scope regression, use `MNEMON_CLI_PATH=/absolute/path/to/mnemon pnpm e2e:serve --runtime-write-scope --strategy-extensions` in a fresh disposable fixture. Send `archive-scope-250` in Mnemon E2E. The scripted model saves two checkpoints, creates and activates two real Native spaces within the pinned View, then adds an overflowing checkpoint. The baseline rejects this add despite the active spaces; the fixed Host archives the originals to both authorized destinations and commits the add. `archive-scope-250 retry` retries the same pending input in a new turn. The fixture bounds child calls and requires real create/update receipts before completing them.

`pnpm e2e:serve --idle-review --strategy-extensions` exercises partial failure with deterministic loopback model choices. Send two substantive synthetic user turns (at least 150 characters each), then wait five seconds. The real reviewer creates one document and one Runtime entry before the fixture returns a deliberate model error. Refresh Memory System status and verify both committed receipts, then send more turns and confirm the one-attempt session cap prevents another child. Only the debounce and minimum interval (both 5 seconds) and attempt cap (1) differ from production defaults. This fixture does not simulate the actual Agent Teams policy; the optional Agent Teams matrix above covers that composition. No personal credentials or memory are used. See the [bilingual reproduction and evidence](../../pr-assets/idle-review-agent-team/README.md).

`pnpm e2e:serve --strict-template` reproduces [#327](https://github.com/omdsh-dev/dsh-mnemon/issues/327). The loopback model behaves as Ollama 0.33 serving a model whose chat template requires a user query:
- it keeps the system prompt and the longest suffix of the other messages that fits its window, always the last message;
- it answers 500 `no user query found in messages` when no user text turn survives;
- the window is sized when the idle review sends its third request.

Send two substantive user turns (at least 150 characters each), then wait five seconds. With the fix, the fourth review request is refused once and retried with a user turn, the review creates the Document "Review checkpoint storage", and Status shows no review failure. On a build without it, Status shows the background review failure with the committed Document receipt. The fixture prints each review request as a `Strict template:` line. Only the idle review timing (5 seconds, one attempt per session) differs from production defaults. See the [bilingual reproduction and evidence](../../pr-assets/issue-327-subagent-user-turn/README.md).

`pnpm e2e:serve --general-strategy` starts with the General strategy selected and the Layered strategy disabled. Send `general-strategy-check remember`, then `general-strategy-check recall` in the next turn. The scripted model checks that the general memory protocol is in the system prompt and that the Runtime, Documents and Memory Spaces Sources are admitted, saves one fact through the named Runtime tool, and answers the second turn only if that fact is projected as resident memory. The fixture prints each check as a `General strategy:` line. Only model decisions are scripted; the Strategy, its View, the Runtime write and the browser stay real.

`pnpm e2e:serve --without-mnemon-cli` points `cliPath` at a missing file, so the Host behaves as if the Mnemon CLI were not installed even when one is on PATH. Use it to check that Status shows no Mnemon Native card, the version dialog lists the CLI as optional, the plugin page disables the embedding test, and a new Memory Space starts on a ready Provider. Connect one first, for example a Provider Lab service on the `dsh-mnemon` page under Plugins.

`pnpm e2e:serve --docs-demo` (Chinese) or `--docs-demo=en` seeds the fictional Lumen project used for the [documentation gallery](../../assets/webui-v0.5.19/README.md): profile and working memory, six project documents and three Memory Spaces, written through the Sources' own management operations before the Host starts. A message containing “checkout” is answered through a Documents search and two Memory Spaces recalls over the real View tools; a later message containing “remember”, such as a new LCP target, replaces the working-memory entry. Only the model's tool choices are scripted, and nothing personal is involved. Add `--live-model` to have the DeepSeek API answer instead, with the key read from `DEEPSEEK_API_KEY`; the questions then run through the real model, the task Agent and the Agent answer, as in the gallery. Recapture the gallery from this fixture whenever the UI changes.

`pnpm e2e:serve --trusted-host=memory.test:4331` adds that authority to DSH's browser-trust fence so the WebUI can be checked as a remote page; add `--remote-management` to set `remoteAccess: trusted-host`. Set `MNEMON_E2E_PORT=4331` so the port matches, map the name to 127.0.0.1 in the test browser (for Chrome, `--host-resolver-rules="MAP memory.test 127.0.0.1"` with `--no-proxy-server`), and open the printed launch URL with `memory.test:4331` in place of the loopback address. Without the grant the `dsh-mnemon` page under Plugins is read-only; with it, a save persists through the API Gateway.

## Releasing

Official packages use independent versions; the Starter pins a tested combination. See [Release process](./releasing.md) for changesets, frozen artifacts, Registry checks and recovery.

## Documentation, storage and historical evidence

Keep English/Chinese pages aligned and public examples executable. Code-native Mermaid diagrams describe ownership and flow; real screenshots remain under `docs/assets`. Do not retain dead directory stubs or historical wrapper files.

Retain persisted formats and existing config keys unless an explicit migration is designed and tested. Verify locking, atomic rename, revisions, corrupt inputs and copied-root upgrade/rollback before changing storage.

The v0.3 release benchmark is a frozen historical result, not a test of this architecture. Its obsolete executable harness was removed from the working tree; [release notes](../releases/v0.3.0.md) link to the versioned historical source. Current gates are the scripts above.
