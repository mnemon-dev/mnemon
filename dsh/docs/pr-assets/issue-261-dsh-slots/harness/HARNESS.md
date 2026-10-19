# Issue 261 published-package compatibility harness

[English](HARNESS.md) | [简体中文](HARNESS.zh-CN.md)

This directory contains external verification scripts. They never alter a DSH package or alias a package to a source checkout. Supply built Mnemon source only to the pack command; the installed test consumers resolve all 17 Mnemon packages from a test-owned loopback registry with checked SHA-512 integrity. The Root bundle composes all 16 companions and enables scoped, light-context, and auto-capture strategy extensions.

Requirements: Node and npm, tar, Git, and the actual Mnemon Native CLI. Node v25.1.0 and Native v0.2.8 were used for the recorded run. Run these commands from this harness directory. Set `MNEMON_SOURCE` to a built Mnemon checkout, `NATIVE_CLI` to the Native executable, and `VERIFICATION_ROOT` to an empty external directory. Keep each run root unique for initial installs.

```sh
node pack-artifacts.mjs --source "$MNEMON_SOURCE" --output "$VERIFICATION_ROOT/artifacts/fixed"
node pack-peer-fixture.mjs --output "$VERIFICATION_ROOT/artifacts/peer-fixture"
node packed-e2e.mjs --mode fixed --cohort alpha --artifacts "$VERIFICATION_ROOT/artifacts/fixed" --peer-artifact "$VERIFICATION_ROOT/artifacts/peer-fixture/artifact.json" --peers true --run-root "$VERIFICATION_ROOT/fixed-alpha" --native-cli "$NATIVE_CLI" --serve true
```

Use `--cohort rc` for published DSH 0.1.5-rc.2; `alpha` pins 0.1.6-alpha.2. Fixed consumers use ordinary npm peer resolution. More than 200 DSH package records must all have the exact cohort version. Installed public SlotCore, Chat, Renderer, and Conversation bytes must match unmodified public tarballs; missing reference tarballs are fetched from the public npm registry with their public integrity checked.

For either mode, `--framework-root /path/to/previously-verified-consumer` pins its physically installed public framework cohort and supporting peers as exact consumer dependencies. The selected DSH version must match; the pins are recorded in `framework-pins.json`. This keeps later prereleases from entering a historical reproduction through upstream peer ranges, while retaining ordinary npm resolution for fixed consumers. No public package metadata or bytes are patched. Issue #265 used this option after a fresh unpinned RC2 install selected an incomplete RC3 transitive release.

`--serve false` prepares and verifies a consumer without starting DSH. `--reuse-install true` restarts a previously prepared consumer without npm installation, preserving its home, workspace, and Mnemon data. It still checks the lock graph, local package integrity, dependency resolution, and original public module bytes. This option is for a previously verified test run, never initial installation. The loopback registry may get a new port; historical resolved tarball URLs remain in its lockfile as provenance.

The WebUI bootstrap URL, harness PID, DSH PID, CLI paths, and isolated directories are recorded in `server.json`. A bootstrap URL may be consumed by the browser; do not prefetch it as an HTTP readiness check. `SIGUSR2` to the harness PID restarts its DSH child and preserves the isolated data; it generates a new bootstrap URL. `SIGTERM` stops the DSH child and the local registry/model services. No background service is installed.

## Real model/tool sequence

Send the exact human message `compatibility-261` in the WebUI or Headless CLI. The local model fixture calls the actual `mnemon_runtime_memory` add tool, requires its successful receipt, calls actual `mnemon_status`, then emits the fixed completion text. Memory is written by the real installed Runtime Source into its JSON storage. The real Native CLI is configured and version-checked; this Runtime add does not claim a Native memory-space backend write. Title-generation requests are answered as text and cannot initiate memory writes. Child maintenance requests use their offered completion tool to report a skipped action. Other human turns receive a fixed readiness response. The fixture accepts OpenAI and Anthropic request formats and emits the matching SSE wire protocol. It has a request budget and performs no network model requests.

`model-requests.jsonl` retains request paths, detected protocols, real tool inventories, prompts, and tool receipts. `model-events.jsonl` retains the bounded fixture decisions. These are disposable test inputs and outputs.

The optional peer plugin is ordinary source under `e2e/peer-fixture`, packed by `pack-peer-fixture.mjs` and loaded through DSH's official bundle/client loader. It contributes distinct IDs `issue261-peer-a:tail` and `issue261-peer-b:tail`, with text “Compatibility peer A” and “Compatibility peer B”. Its selector declines both RC chain entries; the alpha list renderer renders both. No registry internals or framework modules are patched. Toggle Mnemon's turn-bar control off/on and reload to check that the peers coexist independently.

## Headless checks against the same packed graph

```sh
node headless-check.mjs --consumer "$VERIFICATION_ROOT/fixed-alpha/dsh-home/profiles/web" --output "$VERIFICATION_ROOT/headless/fixed-alpha" --native-cli "$NATIVE_CLI"
```

The Headless profile has its own home/data/workspace. Its only `node_modules` link points to the test-owned packed consumer above. The script asserts the exact DSH cohort, all 17 local artifact records, the full Root composition, all three enabled strategies, the real add/status sequence, persisted Runtime memory across a new CLI process, and zero `mnemon_*` tools after disabling Root. Each command has a 90-second bound. The result and logs remain under the output directory. Use a new output directory for each run.

## Baseline alpha caveat

Released Root 0.5.11 excludes alpha2 in its two published framework peer ranges. Only `--mode baseline --cohort alpha` uses `--legacy-peer-deps` to reach the original browser registration failure. Because this option also suppresses DSH's automatic peer installation, baseline reproduction uses exact public framework pins. The bundled `e2e/framework-peers-alpha.json` and `e2e/framework-peers-rc.json` snapshots were captured on macOS ARM64. The implicit alpha snapshot fallback is rejected on other platforms. For a fresh host, set `FRAMEWORK_ROOT` to a normal-installed, exact-alpha public profile and derive its physically installed platform packages. Set `BASELINE_SOURCE` to the built released Root 0.5.11 checkout before packing the baseline:

```sh
node pack-artifacts.mjs --source "$BASELINE_SOURCE" --output "$VERIFICATION_ROOT/artifacts/baseline"
node packed-e2e.mjs --mode baseline --cohort alpha --artifacts "$VERIFICATION_ROOT/artifacts/baseline" --framework-root "$FRAMEWORK_ROOT" --run-root "$VERIFICATION_ROOT/baseline-alpha" --native-cli "$NATIVE_CLI" --serve true
```

`framework-pins.mjs` derives exact pins from physically installed public packages, so foreign-platform optional package records are not pinned. The snapshot is retained in `baseline-framework-pins.json`. `install-policy.json` records this baseline-only bypass; it is not evidence of a normal compatible install. Both fixed cohorts must install without the bypass.

The original alpha browser missing-ID failure was independently reproduced. Prior OpenAI-only fixture failures in the first browser/Headless attempts were fixture protocol mismatches, not product failures. Corrected dual-protocol runs were recorded separately. `evidence-summary.json` preserves the result and artifact identities. Raw run logs, dependency trees, model requests, bootstrap URLs, and generated tarballs remain outside the repository and are intentionally excluded from this bundle.

## Artifact reuse during a long verification run

`pack-artifacts.mjs` packs all artifacts from a built source tree. For the recorded fix, Root was packed before a concurrent full clean build; `assemble-fixed-artifacts.mjs` then verified that every companion's tracked source and manifest matched the release baseline and reused its byte-identical tarball. It accepts `--source`, `--baseline`, and `--output`; the output must already contain Root's tarball and `npm pack --ignore-scripts --json` result as `root-pack.json`. All 17 tarball identities and source provenance are retained in `artifacts.json`.

## Reuse limits and copy audit

- The bundle starts local model/registry services and the official DSH CLI; it does not automate a browser. Browser screenshots and observations are documented in the surrounding evidence report.
- Build the selected Mnemon source before packing. The 17 generated tarballs and optional peer tarball are inputs generated by the commands above, not missing checked-in fixtures. `assemble-fixed-artifacts.mjs` is optional and requires the baseline Git commit to be available locally.
- Fresh installs require public npm access. DSH versions and selected Mnemon artifact bytes are checked exactly; unrelated transitive dependencies still use their public semver ranges, and their resolved versions may change. Retain each generated lockfile when repeating an exact historical run.
- The baseline alpha legacy-peer bypass is deliberately confined to reproducing released Root 0.5.11. Both fixed cohorts use ordinary peer resolution. Platform-specific public packages and Native executables must be available on the host; other operating systems were not exercised in this evidence.
- Headless output must be a new directory. Reusing WebUI installs requires the original verified consumer and artifact set; it is not an installer migration path.
- The bounded model fixture implements the named add/status sequence, title text, and skipped child maintenance. It is not a general language-model simulator or a Native memory-space backend-write test.
- The harness executes official npm package install scripts and inherits the caller's environment. It is not an OS sandbox. Its DeepSeek key is explicitly fake and its model endpoint binds to loopback. The reusable copy sets the supported `DSH_TELEMETRY_MODE=DISABLED` for both public cohorts.

`bundle-audit.json` records SHA-256 hashes of every copied source file and its committed counterpart. The two executable changes from the exported bundle add the baseline platform guard and explicit telemetry mode; documentation and the sanitized summary explain portability and excluded runtime files. The recorded artifact/result hashes remain unchanged. Repository-authored translations are recorded separately without an export-source hash.
