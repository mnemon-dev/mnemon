# Issue 267: public Settings migration fixture

This harness installs the unchanged public DSH packages into a disposable consumer, then installs all 17 Mnemon tarballs from the selected artifact set. It validates every Mnemon tarball's SHA-512 and SHA-256, checks the installed lockfile's tarball integrity, rejects mixed DSH versions, checks package resolution stays inside the consumer, and compares selected installed framework bytes with integrity-verified public tarballs.

The framework is installed with normal npm peer resolution first. Its physically installed framework and peer packages become exact consumer dependencies. The **baseline only** uses `--legacy-peer-deps`, because dsh-mnemon 0.5.12's old public peer range excludes DSH 0.1.7-alpha.1. The fixed consumer explicitly uses normal peer resolution. There are no framework edits, TypeScript path aliases, workspace package links, or substituted Cordis/Settings services.

## Prepare and launch

Run from the repository root. Use disposable paths outside the checkout, and supply the real native Mnemon executable.

```sh
node docs/pr-assets/issue-267-settings-migration/harness/prepare-framework.mjs \
  --framework-version 0.1.7-alpha.1 \
  --root /tmp/issue267/framework-alpha7

node docs/pr-assets/issue-261-dsh-slots/harness/pack-artifacts.mjs \
  --source /path/to/built/source \
  --output /tmp/issue267/artifacts/baseline

node docs/pr-assets/issue-267-settings-migration/harness/packed-e2e.mjs \
  --mode baseline \
  --framework-version 0.1.7-alpha.1 \
  --framework-root /tmp/issue267/framework-alpha7 \
  --artifacts /tmp/issue267/artifacts/baseline \
  --run-root /tmp/issue267/baseline-alpha7 \
  --native-cli /path/to/mnemon \
  --serve true
```

Pack the built fixed checkout into a separate artifact directory, then use the same launch command with `--mode fixed`, its artifact directory, and a fresh run root. The normal install must succeed without widening or altering the official package manifests. `--framework-version` also accepts `0.1.5-rc.2` and `0.1.6-alpha.2` with matching clean framework roots for the existing compatibility cohorts.

For the alpha.7 profile, settings are fields of the ordinary `mnemon` Config row in `profiles/web/cordis.patch.yml`. The harness uses the published `agent-preset-registry` and `dsh-agent-preset` contracts for a minimal persona preset. Older cohorts use their published settings.yaml and preset-directory contracts. Automatic maintenance is disabled and the only model endpoint is the deterministic loopback fixture reused from Issue 261. The model accepts `compatibility-261` to exercise real Mnemon Runtime add and status tools.

The WebUI binds only to `127.0.0.1` on an ephemeral port. The bootstrap URL is written to **`<run-root>/server.json`**, and raw startup output remains in `web.log`; neither is printed by the harness. Keep bootstrap URLs private when collecting screenshots or evidence. Use the browser to open the URL read from that file. The harness does not operate a browser.

The baseline `web.log` should contain `TypeError: ctx.settings.register is not a function`. A matching error also becomes the non-secret `activationFailure` field in `server.json`. If the official launcher's required-entry audit exits, its exit code is recorded there. The fixed run should have no Settings activation failure and should expose the Mnemon UI and tools.

Important evidence files are `install-policy.json`, `artifact-provenance.json`, `framework-pins.json`, `runtime-graph.json`, `effective-config.yml`, `cli-version.log`, `mnemon-cli-version.log`, `web.log`, and the model request/event logs. Runtime data stays below the run root. Send `SIGUSR2` to the harness PID in `server.json` to restart the Web process without reinstalling or rewriting the profile; this preserves settings edits and persistence data. Stop the harness with `SIGTERM`.

`--serve false` prepares and verifies the consumer without starting a Web server. `--reuse-install true` is only for re-running setup after an interrupted fixture preparation; it rewrites the fixture profile and is not a persistence check.

## Retained settings fixture

For the fixed alpha.7 run, add `--legacy-settings retained` to seed an unchanged `settings.yaml.imported` backup. The fixture records the exact profile hash and backup SHA-256 in `legacy-settings-fixture.json`. It covers root recovery, independent UI toggles, a current profile override, an existing Strategy config row, legacy Source enablement, and a foreign profile namespace. Verify the recorded expected values after startup and again after a `SIGUSR2` host restart.

`--legacy-settings pending` instead seeds the original `settings.yaml`. The first host leaves Mnemon-specific recovery to the next startup while DSH renames/imports the old file; the second host recovers the retained backup. This path avoids racing DSH's private asynchronous importer. The harness never evaluates YAML expressions itself.

## Repeated loopback runs

Many disposable ports can accumulate cookies on `127.0.0.1`. If the browser's long plugin-resource request returns HTTP 431 while the same public resource succeeds without those headers, retain the current loopback listener and change only the privately read bootstrap URL's hostname to `localhost` before navigating. The official Web authentication accepts that loopback authority and issues its own session. Do not pass `--host localhost`: the public CLI host schema accepts only `127.0.0.1` or `0.0.0.0`. Do not print the bootstrap URL, remove existing browser cookies, increase header limits or alter framework files for this workaround.
