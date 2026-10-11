# Additive Strategy plugins — 2026-09-01 verification

This records implementation verification, not a release or an LLM-quality benchmark.

## Revisions and scope

| Item | Revision |
| --- | --- |
| Development branch | `codex/view-concretization` |
| Starting baseline | `721d30651d43fb9942c4bf09349a4a61816780b7` |
| Core contribution lifecycle | `e92eaff` |
| Three-tier slots and Source-qualified retrieval | `ec297c1` |
| Optional plugins and Host write-scope enforcement | `874f8a8` |
| Verified test/artifact harness | `c4c50c9214cd144cf722442c8b697e09877b012c` |

One selected complete Strategy still owns the View. Optional plugins register
pure, bounded contributions through the existing Fiber-owned `installMemory`
service. Slot semantics belong to the target Strategy, not Core. There is no
second Context service, background scheduler, Source storage migration or default
Starter activation change. Main and experiment branches were not changed; no
push or publication was performed.

| Optional plugin | Exclusive slot | Effect |
| --- | --- | --- |
| `dsh-mnemon-strategy-scoped` | `selection` | Ordered Source instances and an optional writable subset |
| `dsh-mnemon-strategy-light-context` | `projection` | One shared projection-character ceiling within the Host budget |
| `dsh-mnemon-strategy-auto-capture` | `capture` | Current-turn recording guidance for explicitly named, offered Actions |

Enabling all three does not change `strategyId`. Conflicting owners of the same
target/slot are rejected atomically. Other complete Strategies do not inherit
these contributions. Disabling one removes its contribution from new turns;
running turns retain their immutable View and lease. Removing selection while
duplicate-role Sources remain restores the normal ambiguity check, not a guess.

## Verification results

Environment: macOS, Node `v25.1.0`, pnpm `11.19.0`, pinned DSH `0.1.1-rc.2`.

| Gate | Observed result |
| --- | --- |
| `pnpm verify` | Exit 0: typechecks, deterministic Root build, plugin builds/tests, full suite, real Headless and package checks |
| Full Root Vitest run | **108 files / 929 tests passed**, 2 files / 2 tests conditionally skipped |
| Deterministic Root build | All 36 generated-file hashes matched |
| Local performance fences | Default and additive profiles each composed 100 three-Source Views within CPU < 2,000 ms and wall < 5,000 ms; unchanged thresholds |
| Real Headless, default | 37 total tools / 7 representative Mnemon tools; preserved settings and idempotent restart |
| Real Headless, additive | All three optional Entries activated; scoped/capture guidance reached the actual DSH model request without changing the selected Strategy |
| Package/public-entry gates | 43 Root packed files, 11 Node-compatible entries, 23 public type dependencies; `publint --strict` and `attw` passed |
| `pnpm verify:plugins` | Exit 0: **16 independent plugin repositories / 17 packed artifacts** |
| Packed real DSH activation | Default-only Starter and a separate install with all three optional packages both passed |
| Independent authoring | Each plugin installed/typechecked/tested/built outside the workspace; an external consumer compiled a new contribution against the packed three-tier SDK, exercised conflict/replacement, and performed local Provider write/recall |
| `pnpm release:check` | Exit 0: 17 coordinated `0.5.0-beta.1` artifacts, `beta` tag; optional plugins are not Starter runtime dependencies |

The 929-test count already includes plugin tests; independent re-execution is
not added again. Skips are the opt-in real Native binary test and Windows-only
smoke. Upstream UI-primitives missing-source-map warnings remained visible.

The first full gate exposed old assertions for one Strategy package, two
contribution kinds and a Core-only dependency whitelist. These now require the
four explicit Strategy packages, third contribution kind and only the declared
owner's public SDK (whole-plugin mounting remains test-only). The complete gate
was rerun successfully. Private imports and performance thresholds were not relaxed.

## Boundary regressions

- Real Cordis/SDK tests cover load order, mounting before the owner, deterministic
  JSON, payload limits, atomic slot conflict, unsupported/inactive targets,
  individual unload, failed replacement and pinned old turns.
- Owner tests cover all three contributions, invalid fields, missing Sources,
  write narrowing and allocation matrices from tiny budgets through 32 Sources.
  Without contributions, original default composition remains intact.
- Identical namespace/query ids across Sources cannot share evidence or Related
  admission. Recall still has a shared two-query envelope; Documents and Related
  retain their shared quotas. Same-Source empty-read diagnostics survive replay.
- A real Host regression reproduced Runtime capacity archival writing into a
  View-read-only Source. Maintenance now checks the offered Action before the
  Provider write. Denial leaves Runtime data unchanged; a positive test still
  archives when both Sources are writable. Read-only Documents are rejected
  before archive preflight starts a worker. Operator management stays separate.
- The Host combination test rejects an unoffered Runtime write, commits a
  permitted Provider write, disables only light-context, then recalls the saved
  fact with selection/capture still active.
- Packed consumption mounts two Runtime instances and a local Provider, rejects
  a competing projection-slot owner, unloads light-context, installs the externally
  compiled contribution and preserves the other two contributions.

After building, repeat the additional real Headless profile with:

```sh
node scripts/verify-headless-profile.mjs --strategy-extensions true
```

Headless uses isolated temporary homes/storage and a loopback model stub. The
artifact runner uses a loopback registry with exact semver manifests, not source
aliases or workspace links. Successful runners remove their temporary test
directories. Personal memory, active profiles and remote accounts were not used.

## Limits

- Activation means enabling a DSH Entry, not merely downloading npm files.
  Arbitrary complete Strategies cannot be stacked indiscriminately: compatible
  distinct slots compose; same-slot conflicts are explicit.
- Capture is guidance, not guaranteed LLM behavior, a hard write counter or a
  new autonomous maintenance service. No model-quality improvement is claimed.
- A projection cap is not a token/full-prompt limit, delta injection or
  summarization. Runtime has no expansion route; small caps can omit useful hot
  memory. Bounded/scoped guidance does not claim a complete store.
- Client and Source storage implementations were unchanged. Automated Client
  interaction, copied-data compatibility and local Provider tests passed. This
  task did not repeat manual browser clicks or certify live cloud accounts,
  real Native/Windows execution, source-only DSH alpha or production latency.

Usage and author contracts: [English](../../en/development/extensions.md),
[简体中文](../../zh-CN/development/extensions.md).
