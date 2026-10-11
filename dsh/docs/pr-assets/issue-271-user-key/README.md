# OpenViking user-key discovery — issue 271

[简体中文](./README.zh-CN.md)

The baseline is `84d469ffa838a36fa579295d94029fcac8ac058e`. A user key can use OpenViking's data API while every `/api/v1/admin/*` route is denied. Baseline discovery still requests admin users when an account is supplied, so configuring an account alone does not allow saving the service.

The regression first failed on the baseline driver with `API is currently unavailable due to access restrictions`. The fixed driver opts into one-owner data-plane discovery only when `discoveryUser` is nonempty. It requires an account and API key, validates the selected root with read-only `fs/ls`, and leaves configuration unchanged on failure. Subsequent reads, exact writes and deletion omit the trusted account/user headers and reject a mismatched memory owner. The existing admin path remains the default.

## Public contract

The reviewed upstream revision is [OpenViking `4edc30b0`](https://github.com/volcengine/OpenViking/tree/4edc30b068934893bc94a4e1b8e87bab2100bce5). Its [authentication guide](https://github.com/volcengine/OpenViking/blob/4edc30b068934893bc94a4e1b8e87bab2100bce5/docs/en/guides/04-authentication.md) says `api_key` mode derives the tenant identity from the key and rejects account/user identity headers. Its [filesystem router](https://github.com/volcengine/OpenViking/blob/4edc30b068934893bc94a4e1b8e87bab2100bce5/openviking/server/routers/filesystem.py) exposes scoped `fs/ls` with the request context. The configured account names the local projection; it does not prove or override the key's account.

## Automated checks

- The standalone Provider regression covers denied admin routes, invalid/incomplete configuration, malformed discovery responses, denied data access, cancellation, timeout, scoped subsequent operations, owner mismatch and unchanged default enumeration.
- The composed Host regression mounts two instances of the real OpenViking plugin alongside Native, Holographic, Runtime and Documents. It verifies secret redaction, independent accounts with the same user name, exact write/search/delete and atomic rejection of a replacement user. Separate real-CLI checks cover Native Source create/write/recall/forget and exact archival to two Native namespaces in one View; see the [CLI composition record](./cli-composition.json).
- The actual generic settings form submits the field to its owning instance and renders success or rejection. The bilingual presentation fingerprint records the single added field label in each locale.
- Full `verify` passed with Node 24.20.0, pnpm 10.13.1 and official Mnemon CLI 0.2.9 in disposable stores: 1,352 root tests and 402 plugin tests passed, with 8 documented opt-in/platform skips. OpenViking includes 69 passing tests, of which 20 cover the new path. The subsequent `verify:plugins --skip-build` passed for all 16 independent plugins, 17 artifacts, the external SDK consumer and real DSH composition with package concurrency set to one. See the [aggregate verification record](./verification.json).

Initial concurrent verification attempts exceeded existing timing fences/timeouts. The complete unchanged suite passed in an exclusive window with the documented Vitest worker limits set to one; no test timeout or performance budget changed. The [baseline](./artifacts-before.json) and [fixed](./artifacts-after.json) manifests identify all 17 tarballs by SHA-256, with hashes of the changed production sources.

```sh
export VITEST_MAX_FORKS=1 VITEST_MIN_FORKS=1
export VITEST_MAX_THREADS=1 VITEST_MIN_THREADS=1
export MNEMON_PLUGIN_VERIFY_CONCURRENCY=1
export MNEMON_NATIVE_TEST_CLI=/absolute/path/to/mnemon
pnpm run verify
pnpm run verify:plugins --skip-build
```

## Reproduce the browser comparison

The [harness](./harness/serve.mjs) composes a deterministic loopback OpenViking fixture with the existing [packed DSH harness](../issue-267-settings-migration/harness/packed-e2e.mjs). Pack the baseline and fixed revisions separately with [pack-artifacts.mjs](../issue-261-dsh-slots/harness/pack-artifacts.mjs). For each artifact set, use a separate disposable run directory:

```sh
node docs/pr-assets/issue-271-user-key/harness/serve.mjs \
  --artifacts /tmp/issue271/artifacts \
  --run-root /tmp/issue271/browser \
  --framework-root /tmp/issue271/public-dsh \
  --native-cli /tmp/issue271/mnemon
```

The framework must be an unchanged, normal-peer install of published DSH `0.1.7-alpha.1`. The harness normal-installs all 17 Mnemon tarballs into another isolated profile and uses the supplied real CLI. Both fresh profiles passed checks for the 267-package DSH cohort and 10 selected official package files, recorded in the [runtime proof](./browser-runtime.json). It writes the bootstrap URL and generated fixture credential only to local mode-`0600` JSON files; do not commit or screenshot those values. `provider-evidence.json` records synthetic data and redacted request metadata.

1. In baseline **Settings → Memory System → OpenViking**, save with the fixture endpoint/key and an empty account, then with its account. Both attempts fail on admin routes.
2. In the fixed profile, fill the account and **User key owner (skip admin)** with the fixture's supplied user. Saving succeeds and creates one active mapping. Browse/search the synthetic canary in Memory Spaces.
3. Change the user to `bob`. Saving is rejected; the original `alice` mapping remains. Browse and search its canary, then create an independent Runtime memory.

The helper's `fixed` mode selects normal peer installation for both artifact revisions; it does not alter either artifact set. The accepted fixed run uses the production sources in implementation commit `4ed532540a6353e87be08f744934d9c51f829c5b`.

The real in-app browser reproduced both baseline failures using official DSH `0.1.7-alpha.1`, the 17 baseline tarballs and Mnemon CLI `0.2.9`. The [browser record](./browser-before.json) and [redacted provider trace](./provider-before.json) distinguish `GET /api/v1/admin/accounts` with an empty account from `GET /api/v1/admin/accounts/fixture/users` with the account filled. Both return the controlled access-restriction error. The `account`/`user` values in those request records describe the fixture identity bound to the generated key; `accountField` records the actual form input. Screenshots keep the key masked and omit the bootstrap URL.

![Baseline save rejected with account empty](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-271-user-key/before-account-empty.png)

![Baseline save still rejected with account supplied](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-271-user-key/before-account-filled.png)

The fixed GUI saved `alice` successfully. A subsequent save for `bob` was rejected, after which browsing and direct search still returned the exact `alice` canary. Runtime independently stored one composition canary. The [browser acceptance record](./browser-after.json) verifies the persisted original key, endpoint, account, enabled service and single active `alice` projection using booleans only; `bob` was not persisted and the registry retained mode `0600`. The [provider trace](./provider-after.json) contains 10 requests for `alice` and one rejected `bob` probe, with zero Admin calls and zero account/user identity headers. The synthetic remote content was unchanged. All disposable WebUI/fixture processes were stopped after acceptance.

![Fixed user-key service saved](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-271-user-key/after-user-key-saved.png)

![Unauthorized replacement user rejected](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-271-user-key/after-invalid-user-rejected.png)

![Original alice content remains readable after rejection](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-271-user-key/after-user-memory-content.png)

![Direct search returns the exact original canary](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-271-user-key/after-user-memory-search.png)

![Runtime remains independently writable](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-271-user-key/after-runtime-coexistence.png)

## Limits and data effects

This is a deterministic provider protocol fixture, not validation of an actual Volcengine account, external model, embedding service or live cloud availability. A readable existing root does not establish write permission. Missing roots and unavailable data APIs are rejected; no remote namespace is created during discovery. Existing persistence formats and RPC permissions are unchanged. Disable/remove the new field before downgrading to a compatible admin setup; no remote content is deleted by discovery or downgrade. The general getting-started guides were reviewed and retain their existing link to the updated provider setup guide.
