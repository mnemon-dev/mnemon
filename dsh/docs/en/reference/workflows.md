# Lifecycle and Core Workflows

[简体中文](../../zh-CN/reference/workflows.md) | **English** | [Documentation Center](../README.md)

This page describes the **Layered strategy**, the default. The **General strategy** offers every available Source in one shared budget and leaves routing to the model: named tools serve their own Sources, and every other route is read with `mnemon_view_route` and changed with `mnemon_view_action`. It has no recall envelope beyond each route's own limits, no Runtime capacity maintenance or archival, and no idle review. See [Strategies and enhancements](../guides/capabilities.md#strategies-and-enhancements).

## Per-Turn Context

The Layered strategy provides stable routing guidance, a static Runtime Memory protocol, and a Wake snapshot message updated when needed:

- `mnemon:routing`: a system prompt section that, when `routingGuidance=true`, provides concise boundaries for tiered queries;
- `mnemon:runtime-memory-protocol`: a system prompt section containing the invariant Runtime Memory semantics and write rules. It is present only while the eager Runtime source participates in automatic projection and remains byte-identical across memory writes;
- Wake snapshot: rendered from the immutable View pinned for the current root turn, carried by an own message with `source.plugin=dsh-mnemon` and `form=recall`. Runtime Memory contributes a complete revisioned USER/MEMORY state snapshot without repeating the static protocol; Documents and Memory Spaces contribute bounded covers rather than their complete catalogs.

The Host appends a new user-role plugin message only when the Wake changes; it does not resend other plugins' shared context. Snapshots remain complete states rather than patches that depend on earlier messages. The static protocol stays in the system prefix, and Source text is transmitted literally without template interpolation. See [Architecture](../development/architecture.md#lifecycle-and-failures) for turn, child-Agent and generation ownership.

The lifecycle pins before the Host assembles the System Prompt, then keeps the same View for every model step in that turn:

```text
turn/start
  -> enter system-prompt/assemble hook
  -> beginTurn(root turn + operation scope)
  -> Source facts → Strategy ViewSpec → validation → Source projection
  -> pin Source revisions/digests and Host-only authority
  -> build bounded Wake
  -> continue the actual Host prompt assembly
  -> align the static protocol section with the pinned Runtime source
  -> append the changed Wake as this plugin's own complete snapshot message

agent/pre-step(step=1)
  -> cancel pending/running background review for a new turn
  -> mark Prime once
  -> append at most one short recall/writeback cue per session
  -> main Agent decides whether to call a memory tool
```

Source snapshotting does not run semantic recall. Prime only initializes routing state and does not run asynchronous CLI status queries.

A child captures and retains its live parent's pinned View and runtime at `agent/created`, before its driver starts. Its own turns pin that retained View, even after the parent finishes or moves to a newer generation. The delegation is released when the child activation is disposed. A cold resume receives a new delegation; an explicitly Host-created background child with no parent model turn snapshots a fresh scoped View. Children have their own turn authority without root-only cues or idle review. See [the authority lifecycle](../development/architecture.md#lifecycle-and-failures).

## Agent Recall

```text
Root or child Agent calls mnemon_recall(query, optional memoryBodyIds)
          |
          v
resolve the executing Agent's own turn pin and retained runtime
          |
          v
read the pinned Memory Space Source state on the Host
          |
          v
validate requested IDs are a subset; otherwise use every pinned active ID
          |
          v
Memory Spaces Source searches granted Provider namespaces concurrently
          |
          v
quality normalization + reciprocal-rank fusion
          |
          v
drop low relevance and admit at most 4 initial results / 3,600 characters
          |
          v
LLM judges whether the evidence is sufficient
          | yes                         | no
          v                             v
answer without more Recall       explicitly submit one different query
                                        |
                                        v
                              search once, deduplicate, and close Recall
          |
          v
both attempts share at most 6 results, 1,200 characters each,
and 4,800 total content characters for this executing Agent turn
```

This admission envelope is the Layered strategy's per-turn policy; the General strategy applies only each route's own limits and call budget.

The model-facing tool deliberately exposes no `category`, `source`, or `intent` filter: a guessed filter must not hide exact evidence. Recall is not forced: a root turn normally issues zero Provider queries. If the LLM calls it, the Host permits one initial query and at most one LLM-chosen, materially different refinement after evidence inspection. Same-query and concurrent repeats join or replay; a third distinct query replays the latest evidence without reaching a Provider. One Related traversal may follow, but only from a `memoryBodyId + id` admitted by either Recall attempt; its repeated result is replayed in the same way.

Recall, Related and the single Documents search slot are budgeted per executing Agent turn. Parallel calls share that turn's state; sibling tasks, later turns and cold-resumed activations do not share cached evidence or consume each other's budget. Replays are restricted to the requested Memory Space subset. Document search is separately bounded to four records, 2,600 query-local characters per record, and 6,000 content characters total. The model-facing Memory Space catalog is capped at 16 entries, and `mnemon_status` returns only a compact health aggregate. Full records, provider settings, paths, and per-Space statistics remain available to Web/RPC control-plane surfaces, not conversation history.

If the user has already supplied current facts, or the repository can answer directly, the Agent should not recall merely to “show memory.”

## Web Retrieval and Agent Queries

The Web “Recall” page follows a different path from model tools:

```text
Direct search
  -> RPC read channel
  -> Source-scoped management search
  -> raw evidence

Agent search
  -> the same deterministic direct search
  -> spawn a worker with no Mnemon tools
  -> answer only from supplied evidence
  -> Host filters citations to actual memoryBodyId/id pairs
```

The Entities and Content pages also use deterministic Source management reads, without a second model. Content uses the Provider browse contract, not semantic recall.

## Explicit Long-Term Writes

The long-term write flow for the root Agent or `/mnemon remember` is:

```text
durable candidate
       |
       v
spawn write worker
       |
       +-- list Memory Spaces
       +-- choose the narrowest suitable scope
       +-- recall when duplicate/conflict checking is useful
       +-- create a new scope only for a recurring distinct domain
       +-- remember / link / forget / merge as requested
       v
structured receipt
```

The first Memory Space created in an empty storage root uses Mnemon's native `default` ID; the Host generates later IDs. A successful write to an inactive target activates it. This activation affects only DSH routing, and merging a source database is non-destructive.

Runtime `add` / `replace` / `remove` and Document `create` / `update` do not need a model to perform storage I/O; they enter the deterministic control layer through the coordinator. Only capacity maintenance and archiving start dedicated workers.

## Runtime add: Normal Path

```text
request
  -> normalize content
  -> acquire in-process queue and file lock
  -> reload memories.json
  -> validate unique match / duplicate / capacity
  -> write temporary JSON and Markdown projections
  -> rename projections
  -> rename memories.json as the commit marker
  -> return compact receipt
```

`replace` and `remove` prefer a unique full-content match through `old_text` within the requested target; only when no exact match exists do they use a unique substring. Duplicate exact entries remain ambiguous. Capacity maintenance is triggered only when the requested add or size-increasing replacement would exceed the target limit.

## USER.md Capacity Maintenance

Capacity maintenance and the MEMORY.md archival below are part of the Layered strategy. With the General strategy, a write that would exceed a Runtime limit is rejected instead.

```text
USER add exceeds 4 KiB
          |
          v
snapshot revision + committed entries
          |
          v
spawn no-tool local compactor
          |
          v
return compacted entries + sourceIndexes
          |
          v
Host validates:
  - every source index appears exactly once
  - no duplicate or out-of-range index
  - importance is not lowered
  - candidate fits the Host byte budget
  - revision is still current
          |
          +-- invalid/conflict -> preserve original data
          |
          v
deterministic UTF-8 packing
          |
          v
retry pending add
```

The user profile is never sent to Memory Spaces. The worker has no tool permissions.

## MEMORY.md Archival and Compaction

```text
Layered strategy: pending MEMORY mutation exceeds the configured limit (10 KiB by default)
          |
          v
snapshot revision + committed entries eligible for archival
(exclude the pending add and the entry being replaced or removed)
          |
          v
Host selects existing active writable Memory Spaces within this operation's Source and namespace scope
          |
          v
One destination: Host routes directly
Several destinations: independent no-tool worker returns source-index routes only
  On model execution failure, Host falls back within eligible destinations
          |
          v
Host validates exact source coverage, destinations, authority, and source revision
          |
          +-- invalid/revision changed -> no Provider writes; preserve Runtime
          |
          v
Host writes each original entry exactly to its planned existing Space
  - committed receipt -> bind destination digest
  - skipped -> require exact Recall evidence
          |
          v
Host retains exact hot entries by importance within its byte budget
          |
          v
CAS compactAndMutate(revision, compaction, original mutation, lineage)
  -> persist JSON and both Markdown projections as one local commit
```

The planner has no data-plane tools and cannot create a Space. The Host owns every write, verifies one durable destination for every committed source entry, and changes Runtime only after all evidence is valid. A late cross-process revision conflict still preserves the original hot entries; because a remote Provider cannot share the local file lock, already committed durable copies are retained as safe duplicates rather than destructively rolled back.

## Document Creation, Update, and Archiving

```text
create/update request
          |
          v
capacityPlan using rendered UTF-8 bytes
          |
     +----+----+
     |         |
    fits     overflow
     |         |
     v         v
 commit    select least-recently-used active Document
               |
               v
          snapshot document + revision
               |
               v
          spawn archive worker
               |
               v
       write/verify concise Mnemon cold reference
       with title, summary, planned path, SHA-256
               |
          +----+----+
          |         |
        failed    receipt ok
          |         |
          v         v
   keep active   revision check
                    |
               +----+----+
               |         |
             conflict   current
               |         |
               v         v
          keep active  move file to archived
                             |
                             v
                    retry original mutation
```

Manual archiving uses the same “index first, move second” path. When the Mnemon index succeeds but a revision conflict occurs, the index is not rolled back, so a safe duplicate reference may remain while the active original text is never lost.

## Deterministic Activity Scoring and Background Review

Completed turns accumulate four signals:

```text
score =
  min(floor(totalUserCharacters / 50), 3)
  + completedTurnCount
  + min(floor(completedToolResults / 5), 2)
  + toolDiversityScore

toolDiversityScore:
  unique tools < 3  -> 0
  unique tools = 3  -> 1
  unique tools >= 4 -> 2

eligible when score >= 5
```

Reaching the threshold does not guarantee a write:

```text
completed turn
      |
      v
score >= 5 ? -- no --> retain activity for later turns
      |
     yes
      |
      v
Host dirty admission
  - explicit no-memory intent in current turn -> stop
  - persistence intent, >=320 user characters,
    >=600 assistant characters, or completed non-Mnemon work -> continue
      |
      v
wait idleReviewMs (default 30 s), and at least
idleReview.minIntervalMs (default 5 min) after the last attempt
      |
      +-- new turn --> cancel timer/worker, retain activity
      |
      v
confirm the Agent is idle, turn/end exists, the turn was composed
by the Layered strategy, and idleReview.maxPerSession is not reached
      |
      v
start a task Agent on the completed turn
  - spawn (default): a bounded checkpoint, idleReview.maxContextChars
  - fork: inherits the parent context
      |
      v
conservative maintenance decision, one layer per pass
  - Documents first: search; create at most one separate document
    for a project record, never update or archive an existing one
  - hot memory: only new, explicit, durable user assertions;
    MEMORY.md takes compact rules no Document covers, never
    project records (off with idleReview.runtimeMemory: false)
  - after a Document, working memory is refused, and vice versa
  - no Memory Spaces write tools
      |
      +-- completed, including skip -> clear activity
      |
      +-- failed/aborted ------------> retain activity
```

The admission check is deliberately structural rather than an LLM classification, so an eligible but ordinary checkpoint starts no background model. The review Agent's tools are exactly `mnemon_document_search`, `mnemon_runtime_memory` and `mnemon_document_create`, without the runtime tool when `idleReview.runtimeMemory` is `false`. The review guard holds each pass to one layer: the first Document creation or working-memory change it admits claims the pass, and the other is refused even if that first call fails; USER.md changes stay independent. “At most one” document or hot-memory change is still enforced by the persona, not by a Host mutation counter. Memory Spaces receive review output only later, through working-memory capacity archiving and Document cold archiving. While Agent Teams tools are installed, review pauses unless `idleReview.agentTeams: scoped`; the Status page says so while it is paused. Background watermarks are not yet persisted, so a Host restart loses accumulated signals that have not been processed.

## How Configuration Switches Interact

- `recallMode=off`: stops injecting recall cues; explicit `mnemon_recall` remains available.
- `writebackMode=off`: disables writeback cues and scored background review; explicit writes are still governed by `writeEnabled`.
- `lifecycleEnabled=false`: disables lifecycle reminders and review without removing explicit tools or Web entry points.
- `routingGuidance=false`: removes only the additional routing section; the Runtime Memory context remains registered.
- `writeEnabled=false`: removes semantic write tools and write RPC, and rejects write commands; it does not guarantee a read-only file-system mount.
