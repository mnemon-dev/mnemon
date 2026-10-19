<h1 align="center">dsh-mnemon</h1>

<p align="center"><strong>English</strong> · <a href="https://github.com/mnemon-dev/mnemon/blob/master/dsh/README.zh-CN.md">简体中文</a></p>

<p align="center">
  <a href="https://www.npmjs.com/package/dsh-mnemon"><img alt="npm version" src="https://img.shields.io/npm/v/dsh-mnemon?label=npm" /></a>
  <a href="https://www.npmjs.com/package/dsh-mnemon"><img alt="npm downloads" src="https://img.shields.io/npm/dt/dsh-mnemon?label=downloads%20total" /></a>
  <a href="https://github.com/omdsh-dev/dsh-mnemon/releases/latest"><img alt="GitHub release" src="https://img.shields.io/github/v/release/omdsh-dev/dsh-mnemon" /></a>
  <a href="https://github.com/mnemon-dev/mnemon"><img alt="GitHub stars" src="https://img.shields.io/github/stars/mnemon-dev/mnemon" /></a>
  <a href="https://github.com/mnemon-dev/mnemon/blob/master/dsh/LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-yellow.svg" /></a>
  <a href="https://dshfind.com/en/plugins/omdsh-dev/dsh-mnemon?ref=badge"><img alt="dshfind" src="https://dshfind.com/api/badge/omdsh-dev/dsh-mnemon?lang=en" /></a>
  <a href="https://dshfind.com/en/plugins/omdsh-dev/dsh-mnemon?ref=badge"><img alt="dshfind downloads" src="https://dshfind.com/api/badge/omdsh-dev/dsh-mnemon?metric=downloads&amp;lang=en" /></a>
</p>

<p align="center"><strong>Composable, view-based memory for DeepSeek Harness.</strong></p>
<p align="center">Pluggable sources and strategies, with layered memory out of the box.</p>

<p align="center">
  <a href="https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/ui-guide.md#in-a-conversation">
    <img src="https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/webui-v0.5.19/en/recall.gif" alt="A question answered from working memory, Project Documents and Memory Spaces; the expanded turn memory bar opens the document it read in Project Documents" width="880" />
  </a>
</p>

<p align="center">
  <a href="https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/installation.md"><strong>Install and start</strong></a> ·
  <a href="https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/assets/webui-v0.5.19/README.md">Watch the demo</a> ·
  <a href="https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/README.md">Documentation</a> ·
  <a href="https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/development/extensions.md">Build a plugin</a>
</p>

## Why dsh-mnemon

An Agent that starts every session from scratch keeps asking what it was already told. Putting everything into one memory store is no better: it either floods each turn or misses what matters. dsh-mnemon gives DeepSeek Harness memory that is layered, visible and composable.

- **The right memory for each turn.** Preferences and working facts stay in context. Project documents and long-term evidence are searched only when a question needs them.
- **You can see what a turn used.** Under each reply, the turn memory bar lists the documents and memories the turn read and wrote, and one click opens each in the Memory System. The Memory System shows everything that is stored and lets you edit it.
- **Save to memory in one click.** When a conversation turns up a fact worth keeping, the brain mark under the reply hands it to a task Agent, which removes duplicates, distils it and writes it to the right Memory Space; the receipt says where it went.
- **Compose it on the Plugins page.** Choose a main strategy and optional enhancements, and switch them without moving any data. Every component has its own page for its settings.
- **Keep data where you want it.** Local by default with Mnemon Native, or one of eight third-party Providers. Storage can be global, per workspace or centralized, with ZIP backup.
- **Extend it.** Sources and Strategies are ordinary DSH plugins built on public SDKs. An installed component gets the same pages and switches as the shipped ones.

## See it in action

These recordings come from a real WebUI with the live DeepSeek model answering, on a seeded, fictional project, Lumen. Waits for the model play faster, marked in the lower right corner.

**Save a new fact to memory.** A reply reports a new measurement. Select the brain mark under it, cut the candidate down to the sentence worth keeping and send it to the task Agent; the receipt names the Memory Space it went to and opens it in one click.

<p align="center">
  <a href="https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/ui-guide.md#save-to-memory"><img src="https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/webui-v0.5.19/en/save.gif" alt="Save to memory: edit the candidate, send it to the task Agent, then view the new memory from the Saved receipt" width="880" /></a>
</p>

**See all memory in one place.** Status, runtime memory, Project Documents and Memory Spaces live in the Memory System. The graph links memories through the entities they mention, and Ask Agent answers with the memories it cites, by content.

<p align="center">
  <a href="https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/ui-guide.md#the-memory-system"><img src="https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/webui-v0.5.19/en/memory.gif" alt="The Memory System: status, runtime memory, a document, the Memory Spaces graph, then an Agent answer with citations" width="880" /></a>
</p>

**Compose memory on the Plugins page.** One main strategy, its memory sources and optional enhancements, each switch applying at once. Every component has its own page, and storage, backup and interface settings live here too.

<p align="center">
  <a href="https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/ui-guide.md#on-the-plugins-page"><img src="https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/webui-v0.5.19/en/plugins.gif" alt="The Plugins page: memory composition, the main strategy menu, the Layered strategy's and Memory Spaces' pages, then storage and interface" width="880" /></a>
</p>

Every step is explained in the [UI guide](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/ui-guide.md); all screens, recordings and the capture environment are in the [v0.5.19 gallery](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/assets/webui-v0.5.19/README.md).

## Three kinds of memory

| Memory | Keep here | How it reaches the Agent |
|---|---|---|
| **Runtime memory** | Preferences, working agreements, facts needed on the next turn | Injected every turn as compact USER.md and MEMORY.md |
| **Project Documents** | Designs, investigations, procedures and handoffs | Searched first, then read when relevant |
| **Memory Spaces** | Durable facts, decisions, entities and their relationships | Recalled on demand from the enabled Providers |

The default **Layered strategy** keeps runtime memory resident and reads the other two on demand. The **General strategy** offers every available source within one budget and lets the model decide how to use each. A **memory space** is one named, Provider-backed scope for long-term evidence; the Chinese product term is 记忆空间.

## Quick start

New to DSH? [Install and start](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/installation.md) walks from an empty machine to your first memory, with a screenshot for every step. The short version, with [Node.js](https://nodejs.org/) 22.19 or later:

```sh
npm install --global pnpm
npx @deepseek-ai/dsh web
```

<p align="center">
  <a href="https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/installation.md"><img src="https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/install-v0.5.19/en/install.gif" alt="On the Plugins page: Add plugin, type dsh-mnemon, Install, Enable now; the Memory System appears in the sidebar" width="880" /></a>
</p>

1. In the page that opens, click **Plugins → Add plugin**, type `dsh-mnemon`, click **Install**, then **Enable now**.
2. Open **Memory System** in the sidebar. **Status** shows each memory component and Provider.
3. Tell a conversation something to remember. The turn memory bar under the reply lists what the turn read and wrote, and the brain mark saves a reply to memory.
4. For Memory Spaces, install the Mnemon CLI for Mnemon Native with `npm install --global @mnemon-dev/mnemon`, or enable another Provider on the Memory Spaces page.
5. Choose the main strategy and enhancements under **Plugins → dsh-mnemon**.

dsh-mnemon supports DSH `0.2.0-rc.2` (npm `latest` and `next`) and `0.1.7-rc.2`. The same package serves the desktop app's Plugins page, `dsh plugin --profile web add dsh-mnemon` on the command line, and Headless with `dsh plugin --profile headless add dsh-mnemon`; keep `v0.5.16` on older hosts. [Getting started](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/getting-started.md) continues from here, and [compatibility and upgrades](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/reference/compatibility.md) covers existing installations.

## How it works

[![Source facts flow through a Strategy and Core validation into one View for the DSH Host](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/diagrams/en/composable-memory.png)](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/development/architecture.md)

- **Sources** own memory and its operations: Runtime memory, Project Documents and Memory Spaces, whose Providers are its children.
- A **Strategy** decides how the available Sources take part in a turn: what stays resident, what can be searched, and which tools and budgets apply. Enhancements add to it through standard slots.
- **Core** validates the result into one immutable **View** for the turn. The DSH Host pins it to that turn and controls tool access.

The same public contracts serve the shipped plugins and external repositories. [Architecture](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/development/architecture.md) explains ownership, the turn lifecycle and the UI regions components contribute to.

## Official plugins

The Starter pins a tested combination of independently versioned packages. One main strategy runs at a time; the enhancements are off until you turn them on.

| Package | Role | Default |
|---|---|---|
| [dsh-mnemon-source-runtime](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-source-runtime/README.md) | Runtime memory: USER.md, MEMORY.md, revisions and local hot storage | On |
| [dsh-mnemon-source-documents](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-source-documents/README.md) | Project Documents: Markdown, search, revisions and archiving | On |
| [dsh-mnemon-source-memory-spaces](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-source-memory-spaces/README.md) | Memory Spaces: durable evidence and their Providers | On |
| [dsh-mnemon-strategy-default-three-tier](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-strategy-default-three-tier/README.md) | Layered strategy: resident runtime memory, the rest on demand | Selected |
| [dsh-mnemon-strategy-general](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-strategy-general/README.md) | General strategy: every available source in one budget | Off |
| [dsh-mnemon-strategy-auto-capture](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-strategy-auto-capture/README.md) | Active capture: in-turn guidance to keep useful facts | Off |
| [dsh-mnemon-strategy-light-context](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-strategy-light-context/README.md) | Light context: a shared ceiling on resident context | Off |
| [dsh-mnemon-strategy-scoped](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-strategy-scoped/README.md) | Scoped composition: ordered sources and a writable subset | Off |

Memory Spaces Providers: [Mnemon Native](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-provider-mnemon-native/README.md) (default, local) · [OpenViking](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-provider-openviking/README.md) · [Honcho](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-provider-honcho/README.md) · [Mem0](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-provider-mem0/README.md) · [Hindsight](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-provider-hindsight/README.md) · [Holographic](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-provider-holographic/README.md) · [RetainDB](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-provider-retaindb/README.md) · [ByteRover](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-provider-byterover/README.md) · [Supermemory](https://github.com/mnemon-dev/mnemon/blob/master/dsh/plugins/dsh-mnemon-provider-supermemory/README.md). Third-party Providers stay off until configured; graph, deletion and enumeration capabilities differ by backend. See [Providers](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/memory-providers.md).

## Build your own

Define a Source or Strategy with `dsh-mnemon/extension-sdk`, add an enhancement through the standard slots, or write a Memory Spaces driver with `dsh-mnemon-source-memory-spaces/provider-sdk`. A component can also add its own settings and Status card to the pages dsh-mnemon shows. Your repository owns its manifest, dependencies, tests and build; DSH installs and mounts it, and choosing it as the main strategy is a separate decision.

Start with the [plugin author guide](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/development/extensions.md). Discuss new capabilities and Providers in an Issue before a pull request; see [CONTRIBUTING](https://github.com/omdsh-dev/dsh-mnemon/blob/8466e3560a3b9de4e9f4b7302cbf005c84e8e69f/CONTRIBUTING.md).

## Data and trust

- Runtime memory and Project Documents are local files; Mnemon Native is local. Third-party Providers use their own services and scopes.
- Turning a component off does not erase its memory, and changing the storage location does not move data. Use ZIP backup to carry it.
- Saved Provider credentials stay on the Host and are never exported. Backups still contain private memory; protect them.
- Sources and Strategies are trusted in-process JavaScript, not sandboxed code. Remembered history never outranks current instructions.

[Backup and recovery](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/operations.md) · [Security policy](https://github.com/omdsh-dev/dsh-mnemon/blob/8466e3560a3b9de4e9f4b7302cbf005c84e8e69f/SECURITY.md) · [Release history](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/releases/README.md) · [Roadmap](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/roadmap.md)

## Develop and verify

```sh
pnpm install --frozen-lockfile
pnpm verify
pnpm verify:plugins
```

Use Node.js `^22.19.0 || >=24.0.0` and pnpm. `node scripts/serve-e2e.mjs` starts a disposable real WebUI; add `--docs-demo` for the seeded project behind these screenshots, and `--live-model` to have the DeepSeek API answer, with the key read from `DEEPSEEK_API_KEY`. Mechanical tests are not claims about model accuracy or live cloud Providers. [Development and verification](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/development/README.md).
