# Entities page: counts and lists from every memory

[简体中文](./README.zh-CN.md) | [Verification data](./verification.json)

On **Memory System → Memory Spaces → Entities**, the number next to an entity did not match what selecting it listed. On the export behind the report, `dsh-mnemon` counted 22 and listed 5, while `UI` counted 5 and listed 9. Both numbers came from different reads:
- **The rail** added up each space's top 20 entities from `mnemon status`. An entity outside a space's top 20 lost that space's count or vanished: the export has 129 distinct entities, and the rail showed 62.
- **A selection** ran Agent recall (`intent: ENTITY`) through the strict quality policy, which keeps every result scoring 0.6 or more but at most four between 0.25 and 0.6. Recall scored most `dsh-mnemon` memories near 0.5, so 17 of 22 were cut. For `UI`, memories that do not carry it filled the free places.

Baseline: `main` `2729237a`, the published dsh-mnemon 0.5.23. Fix: `a0ded7ec`. The runs took place on 2026-10-04 (Asia/Shanghai):
- macOS 15.6 arm64, Node 24.19.0 and Mnemon CLI 0.2.9;
- headless Chrome 154 at 1280×800, zh-CN, light;
- the repository's disposable WebUI fixture on DSH 0.2.0-rc.2 and on 0.1.7-rc.2.

No model is called, and all memories in the screenshots are synthetic.

## Reproduction

Four Native spaces about a fictional project, seeded with explicit entities:
- `Atlas` is carried by 22 memories in three spaces;
- `WebUI` is carried by 4, one of them in a space with 23 entities, where it falls outside the top 20;
- 平台实验室 mentions Atlas in three memories without the tag.

| Before: Atlas | Before: WebUI |
|---|---|
| ![Atlas counted 22, listed 20 including memories without the entity](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/entities-complete-index/before-atlas.jpg) | ![WebUI counted 3, listed 8](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/entities-complete-index/before-webui.jpg) |

Each space showed "20 observable", and the rail held 52 of the 63 entities. Selecting `Atlas` listed 20 results: the recall limit cut two memories that carry it, and three that only mention it took their places. `WebUI` counted 3 and listed 8.

## Fix

- **One index per space.** The Source builds each space's entity index from the memories its Provider lists, so an entity's count is the number of memories that carry it, and the rail holds every entity. Spellings are merged by the key the Overview graph already uses, and each memory counts once. The same key now serves both, so the graph and the rail agree.
- **The selection lists those memories.** Every memory that carries the entity is listed, most important first, six at a time, with further pages read as the list is revealed.
- **Related memories on demand.** Below the list, **Find related memories** runs recall for the entity, with the carrying memories left out before the quality policy selects, so they cannot take its places. Recall is the slow part, about 0.5 s per space, so it stays collapsed until asked for. Once opened, it stays open for the next entities until hidden, for the rest of the browser session. Hiding and opening it again shows what was found without another recall. When no memory carries a name the user entered, related memories open by themselves, since they are all the page can offer.
- **Progressive loading.** Pressing an entity highlights it and shows its count from the rail at once, and the carrying memories come from the index. A placeholder appears only when a read outlasts 160 ms. The spaces show from the directory while their indexes are read, and a late answer for an earlier selection is dropped.
- **Freshness.** An index stays while its Provider statistics stay the same; Mnemon counts every write in its operation log. A write through the Source drops the index at once. The rail checks every space again; a selection reuses that check for ten seconds. When the same page selects another entity, the related recall it was still waiting for is cancelled. Mnemon runs one CLI call at a time per data directory, so this keeps a new selection from queueing behind the old one.
- **Providers.** A Provider whose `list()` stops short of a space, or cannot list, can implement the optional `entityIndex(body)` and return `{ memories, complete }`. An incomplete index is marked partial. An entity Provider with neither appears as query-only and contributes only related memories. The first-party Providers need no change: Mnemon Native lists a whole store, and Holographic and Hindsight list up to 1,000 memories.

| After: Atlas | After: related memories, collapsed | After: related memories, found |
|---|---|---|
| ![Atlas counted 22 and listed 22](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/entities-complete-index/after-atlas.jpg) | ![WebUI 4 of 4, related memories collapsed with Find related memories](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/entities-complete-index/after-collapsed.jpg) | ![Related memories found for WebUI](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/entities-complete-index/after-related.jpg) |

On both hosts:

| | Before | After |
|---|---|---|
| Entities on the rail | 52, each space "20 observable" | 63, each space's own count (29, 23, 8, 7) |
| Atlas: count / listed | 22 / 20, with 3 that do not carry it | 22 / 22; related memories collapsed, no recall run |
| WebUI: count / listed | 3 / 8 | 4 / 4; **Find related memories** shows 4 after 0.7–0.8 s |
| The next entity after related memories were opened | — | Atlas loads its 5 related memories by itself |
| First card after a click | 400–1,891 ms | 91–99 ms |
| Switching entities at once | settles on the second | settles on the second |

There were no console errors.

## Scale

Four synthetic Native spaces with 500 memories each and 139 entities, read through the real Source and Mnemon Native Provider with Mnemon 0.2.9 (medians of three clicks):

| | Before | After |
|---|---|---|
| Rail, first / again | 94 / 79 ms, 24 entities | 158 / 92 ms, 139 entities |
| `Release`, carried by 855 | 20 results after 1,718 ms | all 855 (paged) after 7 ms; related memories, when asked, 1.8 s |
| `Atlas`, carried by 211 | 20 results after 1,608 ms | all 211 after 7 ms; related memories, when asked, 1.5 s |
| `Component010`, carried by 86 | 20 results after 1,547 ms | all 86 after 10 ms; related memories, when asked, 1.7 s |
| With related memories open: `Atlas`, then `Component010` 0.3 s later | — | 2 ms to the second list; the first related recall cancelled; related after 1.3 s |

The rail now reads each space once, which costs about 60 ms more on first open. Recall, about 0.5 s per space, runs only for **Find related memories**; the related timings above were read through the Source directly.

## The export behind the report

The same build, on a copy of that export (4 active Native spaces, 31 memories; counts only, with no memory content in this record):
- the rail holds 126 entities instead of 62, and every count equals the Overview graph's;
- `dsh-mnemon` counts 22 and lists 22; **Find related memories** finds 4 more;
- `UI` counts 6 instead of 5: one space's top 20 had left it out. It lists 6; **Find related memories** finds 4 more.

## Tests

- Memory Spaces: the entity index (spelling merge, per-memory counting, order), every carrying memory listed with its count, related memories without carrying ones, one status and one listing for a rail and a selection together, index reuse and rebuild after statistics change or a Source write, the selection's ten-second reuse, cancellation per page view, a Provider `entityIndex` marked partial, unavailable spaces, input limits; and on the page, the carrying list with related memories found only when asked, the choice carried to the next entity until hidden, related memories by themselves when nothing carries a name, a forgotten memory leaving at once while the others stay until the refresh replaces them, directory-first spaces, a late answer dropped, paging, placeholders only for slow reads, and a related retry that keeps the list.
- Root: the reviewed presentation baseline takes the new entity classes and copy.
