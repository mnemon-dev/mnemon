# Capability map

[简体中文](../../zh-CN/guides/capabilities.md) | **English** | [Documentation hub](../README.md)

dsh-mnemon is the memory system for DeepSeek Harness (DSH). It does not force every kind of knowledge into one database. It keeps frequent context, complete project narratives and long-term evidence apart, lets a main strategy decide how they reach each turn, and brings nine long-term memory Providers into one workflow.

The short rule: **keep every-turn context in runtime memory, complete narratives in Project Documents, and evidence you need across tasks in Memory Spaces.**

## What you can do

| Goal | Where | How it runs | Changes data? |
|---|---|---|---|
| Keep preferences, conventions and environment facts in every turn | **Runtime memory** | The Host maintains `USER.md` and `MEMORY.md` projections | Yes, after you confirm |
| Keep complete designs, investigations, procedures and handoffs | **Project Documents** | The Host manages Markdown, search, capacity and revisions | Creating and editing do; reading and searching do not |
| Create a durable space on one of nine backends | **Memory Spaces → Overview → Create Memory Space** | You choose an enabled Provider | Yes |
| Decide the Provider for spaces a task Agent creates | **Memory Spaces → Provider for new spaces** | One fixed Provider, or Host rules first and then the task Agent among the eligible ones | Saving the setting does |
| Find raw durable evidence | **Memory Spaces → Recall → Direct search** | Active spaces answer concurrently with their native recall | No |
| Turn evidence into an answer | **Memory Spaces → Recall → Ask Agent** | A clean task Agent receives only the bounded evidence | No |
| Qualify, deduplicate, distil and write a candidate | **Save to memory** under a reply or on Memory Spaces | A clean task Agent behind Host-enforced tools, paths, locks and receipts, with a receipt for you | Only if the Agent decides to write |
| Title and describe several spaces | **Memory Spaces → Overview → Tidy names and descriptions** | One isolated task per space | Local catalog metadata only |
| Move a document out of hot capacity | **Project Documents → Archive** | A task Agent indexes a cold reference before the Host moves the original | Yes |
| See what memory a turn used | **Turn memory** under the reply | The documents and memories each tool read or wrote, each opening where it lives | No |
| Change how memory is composed | **Plugins → dsh-mnemon → Memory composition** | Switches apply to future turns | Configuration only |

## Three kinds of memory

| Memory | Best for | How it reaches context | Source of truth |
|---|---|---|---|
| **Runtime memory** | Frequent preferences, collaboration rules and project facts | Compact injection on every turn | `runtime/memories.json`; the Markdown files are projections |
| **Project Documents** | Long-form knowledge that must keep structure and provenance | Search first, then full text on demand | `documents/index.json` plus managed Markdown |
| **Memory Spaces** | Facts, decisions, entities and relationships across sessions | Bounded evidence recalled from active spaces | A Mnemon Native store or the selected external Provider |

Each is its own Source plugin. Runtime memory and Project Documents keep local storage; Memory Spaces swaps backends through Providers without touching the other two. External Sources can join through the same public contracts. See [Architecture](../development/architecture.md).

## Strategies and enhancements

One **main strategy** composes each turn. **Enhancements** add to whichever main strategy runs, through standard slots, and can be combined. All are chosen under **Plugins → dsh-mnemon**.

| Component | What it does | Options |
|---|---|---|
| **Layered strategy** (default) | Keeps runtime memory resident; searches Documents and recalls Memory Spaces on demand; runs idle review and capacity maintenance | Task Agent model and idle review on its page |
| **General strategy** | Offers every available source in one budget; the model decides how to use each; no automatic review or capacity maintenance | Resident Sources; Additional guidance |
| **Active capture** | In-turn guidance to keep useful facts. It is guidance, not an autonomous recorder | Recording targets, instruction and operations |
| **Light context** | A shared ceiling on resident context. It is not token accounting | Resident character ceiling |
| **Scoped composition** | Orders the sources that take part and limits which may be written. It creates no storage | Sources in priority order; writable Sources |

Switching strategies never moves data. A turn keeps the composition it started with. See [Configuration](../reference/configuration.md) for how these choices are saved.

## Nine long-term memory Providers

| Provider | Form | Best fit | Scope |
|---|---|---|---|
| **Mnemon Native** | Official local CLI and SQLite | Full graph, exact writes, local-first default | Global, workspace or custom root |
| **OpenViking** | HTTP and `viking://` | Existing resource trees, verified exact writes and semantic retrieval | Target URI and user identity |
| **Honcho** | HTTP workspace and peers | Team and Agent-peer conclusions | Provider workspace |
| **Mem0** | Platform or self-hosted HTTP | Existing Mem0 user and Agent memories | User and agent identity |
| **Hindsight** | HTTP memory bank | Banks, entities and a native graph | Bank id |
| **Holographic** | Local structured fact files | Auditable local entity and semantic facts | Follows the workspace; path override allowed |
| **RetainDB** | HTTP project and user | Project- and user-scoped memory | Project and user identity |
| **ByteRover** | Local `brv` CLI | Code knowledge and curation workflows | Follows the workspace; directory override allowed |
| **Supermemory** | HTTP container | Document ingestion and container sharing | Container tag |

Memory Spaces' page under **Plugins → dsh-mnemon** holds each Provider's reusable service settings and switch; the Memory System's Memory Spaces page holds the spaces themselves. Providers are off until enabled. Only Mnemon Native needs the Mnemon CLI; when it is missing, another ready Provider can serve Memory Spaces. See [Providers](./memory-providers.md).

## Who does the work

**The Host, deterministically.** Status, direct search, content and entity browsing, activation, ordinary runtime edits and document reading or editing need no model. They pass through the Host's schema, path, permission, lock, revision, capacity, timeout and cancellation checks.

**Independent task Agents.** These never reuse the main conversation's history or context window:

- **Save to memory** qualifies, routes, deduplicates, distils, writes and reports a receipt;
- **Ask Agent** answers from bounded recalled evidence;
- **Tidy names and descriptions** runs one title and description task per selected space;
- **Document archive** indexes a cold reference before the Host moves the original;
- **Provider for new spaces**, with smart selection, calls a model only when rules leave several candidates.

Task Agents follow DSH's default route for new sessions. **Task Agent model**, under Background tasks on the Layered strategy's page, can choose a separate Provider and model. Tasks are isolated: a failure shows on its own space or operation and never blocks the page.

## Where it shows up

| Surface | What it offers |
|---|---|
| **Memory System** | Status, Runtime memory, Project Documents and Memory Spaces, with every confirmation step |
| **Plugins page** | Memory composition, component pages with their settings (including Providers), storage and interface |
| **Conversation** | The turn memory bar, Save to memory and links into the Memory System |
| **Headless** | The same runtime injection, document search, memory space tools, workspace routing and supervised writes, without a WebUI |
| **Commands and tools** | `/mnemon` commands and the least-privilege tools Agents use |

## Storage scope

**Global** uses `MNEMON_DATA_DIR` or `~/.mnemon`, shared by local workspaces. **Workspace** uses `<workspace>/.mnemon`; local backends such as Mnemon Native, Holographic and ByteRover can follow it. **Custom** is a global scope at a path you choose. **Centralized** keeps each workspace in its own subdirectory of one root, with an optional global `USER.md`. Switching never moves, merges or deletes a previous root. Remote Provider namespaces (workspaces, users, banks, projects, containers, URIs) never change with the DSH workspace. See the [storage model](../reference/storage-model.md).

## What it does not do

- Remembered history never outranks current instructions, live tool results or repository facts.
- Provider differences stay visible; missing graph, delete or exact-write support is never faked.
- Provider credentials never reach the browser, a task Agent or a backup.
- Turning a Provider off never deletes remote data; it clears the local catalog, which reconnecting rebuilds.
- Changing storage scope never migrates, merges or deletes an old root.
- There are no distributed transactions across local files and remote Providers.

## Continue

1. [Get started](./getting-started.md)
2. [Walk through the UI](./ui-guide.md)
3. [Compare the Providers](./memory-providers.md)
4. [Lifecycle, concurrency and failure boundaries](../reference/workflows.md)
5. [Compatibility and upgrades](../reference/compatibility.md)
