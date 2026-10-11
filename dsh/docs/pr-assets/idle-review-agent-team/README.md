# Idle review limits and Agent Teams compatibility

[简体中文](./README.zh-CN.md) | **English**

Issues [#100](https://github.com/omdsh-dev/dsh-mnemon/issues/100) and [#255](https://github.com/omdsh-dev/dsh-mnemon/issues/255), reproduced from `1363ebffaf19c9ab4badf0137f6fe87acacaf989` on 2026-09-16. The implementation is committed as `2ca95ee1953f1cadd589a17255f21f87375e6038`; this evidence commit changes documentation and images only. Browser checks use the real DSH WebUI, published Host packages, disposable local storage, and all three Strategy enhancements. Model replies are deterministic loopback fixtures; these checks do not measure model judgment or external API reliability.

## Before and after

| Check | Before | With this change |
| --- | --- | --- |
| 30 eligible turns, successful review, configured cap 2 | 15 child reviews | 2 child reviews; clearing/compacting context does not reset the loaded Agent's budget |
| 30 turns over 150 seconds, failed review | 29 attempts | 1 attempt within the configured five-minute minimum interval |
| Missing fork provider | Review rejects | Default bounded spawn works; explicit fork can fall back to spawn or skip before startup |
| Published Agent Teams service and tools | Both fork and spawn fail with `TEAM_NOT_MEMBER` after the first child model step | Review pauses before child creation and explains the incompatible composition |
| Document and Runtime writes followed by a review failure | Failure does not carry both committed write receipts to the workspace | Failure remains a failure; the workspace lists both receipts for reconciliation and does not replay the run |

The two cadence regressions were run against the baseline implementation before the fix. The matching assertions now pass in `tests/lifecycle.spec.ts`. The production defaults are five minutes between attempts and at most 20 attempts per loaded parent Agent; the smaller test cap makes the bound observable.

| Actual Team error on the baseline | Compatibility pause on the changed build | Partial failure with committed receipts |
| --- | --- | --- |
| [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/before-team-error.png) | [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/after-team-paused.png) | [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/after-final-partial-receipts.png) |

The Team WebUI workload has two completed parent turns and five complete overview chunks containing 335 synthetic entries. Its normal eligibility threshold remains 5; the second substantive turn raises the accumulated score to 6. The baseline creates one review child, hits the Team error, and disposes the resident child while retaining its inactive catalog row. The changed build creates no child and makes no review model request during the recorded 190-second quiet interval. [Sanitized WebUI observations](./webui-evidence.json) include package versions, workload, build hashes, disposal evidence, and the telemetry differences between runs.

The recorded Team build predates the final Settings layout refinement and the cleanup-success counter correction. The public compatibility predicate and its scheduling gate are unchanged; the standalone guarded matrix was rerun against the final source. Screenshots contain only synthetic memory and opaque test identifiers.

## Published Agent Teams reproduction

The optional fixture uses only npm DSH `0.1.5-rc.2` and the two Agent Teams `0.1.5-alpha.2` packages. It does not modify DSH sources or add those experimental packages to this repository's dependencies. Use Node with TypeScript stripping (verified with Node 25.1.0):

```sh
team_profile=$(mktemp -d)
npm install --prefix "$team_profile" --save-exact \
  @deepseek-ai/dsh@0.1.5-rc.2 \
  @deepseek-ai/dsh-experimental-agent-team@0.1.5-alpha.2 \
  @deepseek-ai/dsh-experimental-tool-agent-team@0.1.5-alpha.2

node --experimental-strip-types scripts/fixtures/reproduce-published-team.mjs \
  --profile "$team_profile" --output "$team_profile/baseline.json"
node --experimental-strip-types scripts/fixtures/reproduce-published-team.mjs \
  --profile "$team_profile" --guard --output "$team_profile/guarded.json"
```

The explicit profile path is required. The script asserts resolved package versions and removes each case's temporary sessions. `--guard` imports the real `idleReviewBlockReason` from this repository; it does not duplicate its predicate. Both commands exit zero when all six expected outcomes match. An asserted baseline error is evidence of the defect, not a successful review.

| Composition | Fork | Spawn | Child model calls / probe executions |
| --- | --- | --- | --- |
| No Team plugins | Completed | Completed | 2 / 1 |
| Team service only | Completed | Completed | 2 / 1 |
| Team service and tools | Failed | Failed | 1 / 1 |
| Team service and tools with the guard | Skipped before child creation | Skipped before child creation | 0 / 0 |

Each parent completes a turn. The child uses a bounded persona, `maxDepth: 1`, the idle-review label, and an allowlist containing one synthetic probe. With either provider, the child is first published without its descriptor. Team tools install their policy while membership reports lead; the shared driver later appends the one-shot descriptor and membership disappears. The first probe still executes, but the next prompt's Team policy throws `TEAM_NOT_MEMBER`.

The guard checks only public `ctx.get('agentTeams')` and `tools.get('spawn_teammate', parent)` capabilities. The explicit parent scope matters. Service-only controls still work. Guarded Team cases create no child or catalog row, then successfully execute the parent's official `team_task_create` tool. Results: [baseline matrix](./published-team-baseline.json), [guarded matrix](./published-team-guarded.json).

This pauses automatic review for the affected composition. It does not repair the upstream Team policy or claim that manual delegation under that composition works. Removing Team tools allows the next eligible completed turn to schedule a review.

## Partial failure and Settings reproduction

Build the repository, point `MNEMON_CLI_PATH` to an official Mnemon 0.2.8 binary, and run:

```sh
pnpm run build
pnpm --workspace-concurrency=4 -r build
MNEMON_CLI_PATH=/path/to/mnemon pnpm e2e:serve --idle-review --strategy-extensions
```

Choose the Mnemon E2E preset. Send two substantive user messages of at least 150 characters to pass the ordinary activity threshold, then wait at least five seconds. The fixture has `idleReview.minIntervalMs: 5000` and `idleReview.maxPerSession: 1`. It performs a real Document create and a real project Runtime add, verifies both receipts, then returns the intentional HTTP 400 error `SYNTHETIC_REVIEW_FAILURE_AFTER_COMMIT`. Refresh the Memory System status to see the failed run and both receipts. Additional eligible turns do not create another review child while that loaded Agent has exhausted its budget.

The first recorded partial-failure run used the default five-minute minimum interval; its later quiet turn proves cooldown and no immediate replay. After loading the final build, the browser changed the interval to 5,000 ms and opened a fresh parent session. Two eligible turns produced one failed review. A third eligible turn followed by more than 30 seconds produced no second review, isolating the one-attempt cap. The browser then disabled review and raised the cap to 2; a fourth eligible turn followed by more than 10 seconds still created no child, isolating the independent switch. These are two sessions in one fixture log, not one child across the entire log. [Sanitized partial-failure observations](./partial-failure-evidence.json) distinguish them. The final status shows two stored Documents because the earlier session's committed Document is retained.

| Enabled with a five-second interval and cap 1 | Disabled with available budget (cap 2) | Four completed turns and one child |
| --- | --- | --- |
| [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/after-review-budget.png) | [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/after-review-disabled.png) | [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/after-bounded-conversation.png) |

In Settings, the independent automatic-review switch leaves recall and writeback settings unchanged. Numeric limits are validated before saving; the existing authorized Settings RPC enforces the same bounds and rejects writes in read-only mode. Automated coverage checks those rejection paths, cancellation, missing guard support, foreign child receipts, and denied own-scope plugin tools.

## Validation and limits

`MNEMON_NATIVE_TEST_CLI=/path/to/mnemon pnpm run verify` passed on macOS arm64, Node 25.1.0 and pnpm 11.19.0: 1,156 root tests passed, five real-model opt-in checks skipped in two files, the Windows-only plugin smoke check skipped on macOS, all standard plugin suites passed, and the real Native Source create/write/recall/forget integration passed. Types, deterministic builds, Headless activation with 39 tools, public entries, publint and attw passed. The root package contains 47 files, 298,457 packed bytes and 1,335,573 unpacked bytes; the intentional checkpoint, receipt and Settings additions fit the reviewed 1,338,000-byte ceiling without adding package files or exposing implementation sources.

The real Native CLI reports 0.2.8 and SHA-256 `f8f21151ce9777983b8d2dfb9cbea1a00b223de6063659af232d04aa57cedad0`. Real Host evidence tests cover fork/spawn with native tools and Code Mode, including complete 19,112-character overview evidence and blocked foreign tools.

The attempt budget is in memory per loaded Agent and resets on Agent unload/reopen or Host restart. Public disposal removes resident children, but the published Host exposes no plugin-scoped archive/TTL API for retained session history. Existing history is preserved, and no private persistence files or unrelated agents are deleted. Bounded spawn may omit oversized or older messages; fork is explicit and inherits the complete parent context. `maxTokens` limits each model response, not total multi-step usage.
