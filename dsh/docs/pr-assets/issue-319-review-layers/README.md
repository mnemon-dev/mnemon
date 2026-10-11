# Idle review writes one layer — issue #319

[简体中文](./README.zh-CN.md) | [Issue #319](https://github.com/omdsh-dev/dsh-mnemon/issues/319) | [Verification data](./verification.json)

On main, one idle review pass records a project fact as a Document and again as a working-memory entry. With the fix, the same scripted pass creates the Document, and the host refuses the working-memory copy, so MEMORY.md stays empty. With **Write runtime memory** off, review is not offered the runtime memory tool at all.

Baseline: `dfb3196cbcea58fb9b36b7283ccac4662d366858` (main, the root package as published in 0.5.20). Fix: `3eb6eb7dd30fd47bfcfebb9e361479c1e58653ad`. The fixture and the `--review-layers` flag come from the fix branch and are identical in all three runs.

The 2026-10-01 runs use macOS 15.6 arm64, Node 24.19.0, pnpm 11.19.0 and the repository's DSH 0.1.7-rc.2. Each run owns a disposable `DSH_HOME`, data directory, workspace and loopback model stub. No model is called and no personal memory is read.

## Setup

```sh
pnpm build
pnpm --workspace-concurrency=4 -r build
pnpm e2e:serve --review-layers
```

In a new conversation, send the two turns recorded in the [verification data](./verification.json). Together they pass review admission. The flag shortens the idle window to 5 s and allows one review per conversation. The [fixture](../../../scripts/fixtures/review-layers-model.mjs) scripts only the review's choices:
1. search Documents;
2. create one Document;
3. when the runtime memory tool is offered, add the same fact to working memory (`target=memory`);
4. finish.

Tool dispatch, the review guard, the Sources and the WebUI are real.

![The two turns before idle review](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-319-review-layers/conversation.jpg)

## Baseline

| Working memory | Documents |
|---|---|
| ![MEMORY.md holds the same rule](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-319-review-layers/before-runtime.jpg) | ![The Document Boot account resolution](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-319-review-layers/before-documents.jpg) |

The pass creates **Boot account resolution**, then adds the same rule to MEMORY.md (`Entry added.`). The project fact now sits in both layers, and MEMORY.md, which every turn loads, holds a repository detail.

## Cause and fix

The review persona sent stable project facts, decisions and conventions to `target=memory`, while the routing guidance keeps hot memory for new user-supplied facts. The Document branch searched first; the hot-memory branch had no such check, so nothing stopped the same knowledge from landing in both layers.

- **Persona.** Review considers a Document first. MEMORY.md takes only a compact rule the user stated, such as a convention, a correction, an environment fact or a tool quirk, that no Document or entry already covers. Project records never qualify, such as a design, implementation details, paths, ports, accounts, scope agreements or handoffs.
- **Guard.** The review guard holds each pass to one layer. The first Document creation or working-memory change it admits claims the pass, and the other layer is refused even if that first call fails. USER.md changes stay independent.
- **Switch.** `idleReview.runtimeMemory` (**Write runtime memory** under Idle review, on by default) withholds `mnemon_runtime_memory` from review.
- **Memory Spaces.** Review never writes them directly. Working memory reaches them through capacity archiving and Documents through cold archiving; the docs now say so.

## Fixed build

| Working memory | Documents |
|---|---|
| ![MEMORY.md stays empty](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-319-review-layers/after-runtime.jpg) | ![The same Document](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-319-review-layers/after-documents.jpg) |

The same pass creates the Document. The working-memory add then returns:

```text
This idle review already created a Document, so it cannot also change working memory (target=memory). Finish with the result tool.
```

MEMORY.md stays empty, and the review finishes with the Document's id.

## Write runtime memory off

![Write runtime memory switched off on the Layered strategy's page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-319-review-layers/switch-off.jpg)

The switch sits under **Idle review** on the Layered strategy's page (**Plugins → dsh-mnemon**). With it off, the review's tool list has no `mnemon_runtime_memory`. The fixture creates the Document and finishes, and MEMORY.md stays empty. None of the three runs logged a console error.

## Automated checks

- `pnpm run verify` passes. It covers docs links, typecheck, the deterministic build, build, typecheck and tests for all 17 plugins, and root tests (113 files, 1,559 passed, 6 skipped). It also covers Headless activation, package contents, public entries, publint and attw.
- The package measures 1,498,810 bytes, and its budget moves to 1,501,000.
- New tests cover:
  - the layer policy: both orders, all three actions, independent USER.md changes, retries within the claimed layer, calls without `target=memory`, PTC sub-dispatch, and the allowlist checked first;
  - the coordinator attaching the policy to the review child's guard;
  - the persona's rules and order;
  - the switch withholding the tool;
  - the config default and validation;
  - the settings switch writing `idleReview.runtimeMemory` at once and hiding with review.

## Limits

- Model choices are scripted, so these runs show what the host allows, not how often a real model repeats itself. The persona steers real models; the guard holds either way.
- A pass that produces both a Document and an unrelated rule the user stated keeps only the first write. A later review can record the other.
- The runs use the repository's DSH 0.1.7-rc.2. The guard relies on the same `agent.ctx.tools.guard` support that review already requires.
- The screenshots contain synthetic content only.
