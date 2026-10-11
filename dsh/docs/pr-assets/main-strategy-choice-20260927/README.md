# Main Strategy choice and the general Strategy

[简体中文](./README.zh-CN.md)

Tested implementation: `4a42eb33567461ed7f4b890cd014373956e2f0d6`, stacked on the DSH 0.1.7 cleanup (`6200e087a37f8be6c252c951aeabea6c7efa0806`). macOS 15.6, Node 25.1.0, pnpm 11.19.0 and published DSH 0.1.7-rc.2. The WebUI ran from `pnpm e2e:serve` with an isolated profile and a scripted loopback model; no personal memory, credentials or external Provider was used.

## Visible result

The `dsh-mnemon` page under Plugins shows the main Strategy choice and the memory enhancements above DSH's own component list, with a pointer to Settings for detailed options. Settings → Memory System shows the same controls. A switch applies through one View transaction and changes the DSH component switches to match.

| State | Plugins page | Settings |
|---|---|---|
| Default (three-tier, enhancements off) | [Chinese](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/plugins-zh-default.png) | |
| General with light context | [Chinese](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/plugins-zh-general.png), [English](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/plugins-en-general.png) | [Chinese](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/settings-zh-general.png), [English](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/settings-en-general.png) |

The captures were taken with Settings opened over the Plugins page. That also exposed duplicate element ids between the two mounted copies of the controls; each copy now has its own ids, a change in one reloads the other, and a rejected change shows the Host's current state.

## General Strategy conversation

`pnpm e2e:serve --general-strategy` selects the general Strategy and scripts only the model's decisions. Turn 1 (`general-strategy-check remember`) checked that the general memory protocol was in the system prompt, that the Runtime, Documents and Memory Spaces Sources were admitted, and that the named Runtime tool and the Route envelope were offered; it then saved one fact, which returned a committed receipt. Turn 2 (`general-strategy-check recall`) found that fact in the resident projection. See [the conversation](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/general-conversation.png) and [evidence.json](./evidence.json).

## Validation

- `pnpm verify`: documentation (2,104 local links, 81 anchors), deterministic build (42 files), build, typecheck and tests for 17 workspace packages (general Strategy 6, three-tier 17, Runtime 61, Documents 39, Memory Spaces 168 with 2 conditional skips), root suite 1,417 passed with 6 conditional skips, real Headless activation (37 tools, 8 representative Mnemon tools), legacy settings import and restart, the root disable gate, 52 packed files, publint and attw.
- `pnpm verify:plugins`: 17 independent plugin repositories and 18 packed artifacts, including the external consumer applying a packed enhancement to the packed general Strategy, three optional Strategies in real DSH, and a real DSH upgrade from published 0.5.15.
- `pnpm release:intent` and `pnpm release:check`: passed; nine packages carry patch intents, including the introduced general Strategy.

## Limits

The scripted model shows that the protocol, Sources, tools and projection reach a real turn; it says nothing about how a live model chooses among Sources. The Headless upgrade in `verify:plugins` starts from published 0.5.15, whose pinned plugin versions mostly equal the packed ones, so pnpm may keep the published copy of an unchanged plugin version there; the independent-install, external-consumer and Strategy-composition checks cover the packed plugin code. The Plugins page still shows DSH's `cordis:group` row as closed ([dsh-external/issues#649](https://github.com/dsh-external/issues/issues/649)).
