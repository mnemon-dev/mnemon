# Mnemon CLI as an optional Provider backend

[简体中文](./README.zh-CN.md)

Tested implementation: `c45680308ad74be9963f9e20e41897da5af2f9a8`, stacked on the main Strategy choice (`533b67fe78470fb08488a379346313f26e80ec21`). macOS 15.6, Node 25.1.0, pnpm 11.19.0, published DSH 0.1.7-rc.2 and Mnemon CLI 0.2.7. Third-party Providers ran from the repository's [Provider Lab](../../../provider-lab/README.md) in Docker, bound to `127.0.0.1`, with Ollama 0.34.4 in a container serving `qwen2.5:3b` and `nomic-embed-text` on the CPU. No personal memory or credentials were used.

## Without the Mnemon CLI

`pnpm e2e:serve --without-mnemon-cli` hides the installed CLI. Mem0 was then connected under Settings → Memory System as a self-hosted service.

| Surface | Result | Capture |
|---|---|---|
| Status | No Mnemon Native card; the Provider list shows Mem0 ready | [Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/nocli-status.png) |
| Versions | dsh-mnemon first; Mnemon CLI "Not installed (optional)" with its install command | [Versions](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/nocli-versions.png) |
| Settings | Native tagged "CLI not installed (optional)"; the embedding test, which runs the CLI, is disabled | [Settings](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/nocli-settings.png) |
| Create Memory Space | Starts on Mem0; Mnemon Native disabled with "Needs the Mnemon CLI on this machine" | [Create](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/nocli-create.png) |

Creating the space from that dialog produced a Mem0 space with healthy storage. A Headless run against the same Mem0 container then followed the default persistence strategy: before any Provider was ready, persisting failed with "No memory provider is ready: install the Mnemon CLI to use Mnemon Native, or connect another provider in Settings". After Mem0 was connected, the strategy reported Mem0, the persisted space was created on Mem0, a fact was stored and a natural-language query recalled it. See [evidence.json](./evidence.json).

## With the Mnemon CLI and the Provider Lab

A WebUI fixture with the CLI installed was seeded through the public plugin composition (`scripts/seed-provider-lab.mjs`): Mnemon Native, OpenViking, Honcho, Mem0, Hindsight, Holographic, RetainDB and Supermemory each received a space and five facts. See the [Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/lab-status.png) capture.

- Status shows the Mnemon Native card as healthy, with Mnemon 0.2.7 and its space, beside the enabled third-party Providers.
- The create dialog still starts on Mnemon Native while seven other Providers are ready, including four that sort before it. The first version of this change picked the alphabetically first ready Provider; the WebUI check found it, and Native now leads whenever it is ready.
- Recall lists one source per active space and attributes every result to its Provider. With Ollama idle, “记忆系统的三层结构是什么？” (what is the memory system's three-tier structure?) returned the matching fact from OpenViking, Mem0, Honcho and RetainDB, and the fact in the Mem0 space created without the CLI.
- Content lists each enumerable Provider's facts. It showed 43 facts: five each from Mnemon Native, OpenViking, Honcho, Holographic, RetainDB and Supermemory, eleven across three Mem0 spaces, and Hindsight's first two units while its extraction was still running ([Content](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/lab-content.png)).
- Entities offers only Mnemon Native, Hindsight and Holographic as entity indexes; every other Provider is marked unsupported. It reported 24 active entities from Mnemon Native (9), Hindsight (15) and Holographic (13) ([Entities](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/lab-entities.png)).

## Validation

- `pnpm verify`: documentation (2,122 local links, 81 anchors), deterministic build (42 files), build, typecheck and tests for 17 workspace packages (Memory Spaces 174 passed with 2 conditional skips, Runtime 61, Documents 39, general Strategy 6), root suite 1,417 passed with 6 conditional skips and one timing-sensitive Session repair test that timed out while Docker extraction loaded the machine and passed alone (132/132), real Headless activation (37 tools), legacy settings import and restart, the root disable gate, 52 packed files, publint and attw.
- `pnpm verify:plugins --skip-build`: 17 independent plugin repositories and 18 packed artifacts, including the standalone Memory Spaces suite and a real DSH upgrade from published 0.5.15.
- `pnpm release:intent`: covered for dsh-mnemon, dsh-mnemon-provider-mnemon-native and dsh-mnemon-source-memory-spaces.
- New regression tests fail on the previous code: persisting without the CLI, Native staying first in an alphabetical catalog, provider stats after a failed version probe, and the create dialog without Native.

## Limits

Ollama ran on the CPU inside Docker. Extraction queues (Hindsight retain, Supermemory ingestion, OpenViking semantic writes) saturated it, and while they ran, Providers that embed each query timed out and were shown as unavailable; they answered once the queue drained. One OpenViking write reported a 10 s timeout although the fact landed. ByteRover was not exercised because its `brv` CLI is not installed. The scripted checks show that the paths work end to end, not how well each Provider extracts or ranks.
