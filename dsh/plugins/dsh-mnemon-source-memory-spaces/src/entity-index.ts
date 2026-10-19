import { normalizeEntityKey, type Insight } from './contracts.ts'

/**
 * The most memories one space's derived index asks list() for. Mnemon Native
 * lists a whole store; a Provider that stops earlier reports what it listed.
 */
export const ENTITY_INDEX_LIST_LIMIT = 100_000

/** The rail lists at most this many entities; the rest stay reachable by name. */
export const ENTITY_RAIL_LIMIT = 5_000

/** One Memory Space's entity index: its memories that carry entities. */
export interface SpaceEntityIndex {
  /** Memories with at least one entity, in the Provider's order. */
  memories: Insight[]
  /** Normalized entity key to positions in memories, each memory once per key. */
  byKey: Map<string, number[]>
  /** Normalized entity key to its spellings and how many memories use each. */
  spellings: Map<string, Map<string, number>>
  /** Memories the index covers, with or without entities. */
  memoryCount: number
  complete: boolean
}

export function buildSpaceEntityIndex(memories: readonly Insight[], memoryCount: number, complete: boolean): SpaceEntityIndex {
  const kept: Insight[] = []
  const byKey = new Map<string, number[]>()
  const spellings = new Map<string, Map<string, number>>()
  for (const memory of memories) {
    const seen = new Set<string>()
    let position: number | undefined
    for (const raw of memory.entities ?? []) {
      if (typeof raw !== 'string') continue
      const key = normalizeEntityKey(raw)
      if (key === '' || seen.has(key)) continue
      seen.add(key)
      if (position === undefined) {
        position = kept.length
        kept.push(memory)
      }
      const positions = byKey.get(key)
      if (positions === undefined) byKey.set(key, [position])
      else positions.push(position)
      const name = raw.normalize('NFKC').trim()
      const names = spellings.get(key)
      if (names === undefined) spellings.set(key, new Map([[name, 1]]))
      else names.set(name, (names.get(name) ?? 0) + 1)
    }
  }
  return { memories: kept, byKey, spellings, memoryCount, complete }
}

/** The spelling most memories use; the first one seen wins a tie. */
function preferredSpelling(names: ReadonlyMap<string, number>): string {
  let preferred = ''
  let most = 0
  for (const [name, count] of names) if (count > most) { preferred = name; most = count }
  return preferred
}

/** Merge per-space indexes into one entity list, most frequent first. */
export function mergeEntityCounts(indexes: readonly SpaceEntityIndex[]): { items: Array<{ entity: string; count: number }>; names: Map<string, string> } {
  const counts = new Map<string, number>()
  const spellings = new Map<string, Map<string, number>>()
  for (const index of indexes) {
    for (const [key, positions] of index.byKey) {
      counts.set(key, (counts.get(key) ?? 0) + positions.length)
      let totals = spellings.get(key)
      if (totals === undefined) spellings.set(key, totals = new Map())
      for (const [name, count] of index.spellings.get(key) ?? []) totals.set(name, (totals.get(name) ?? 0) + count)
    }
  }
  const names = new Map<string, string>()
  for (const [key, totals] of spellings) names.set(key, preferredSpelling(totals))
  const items = [...counts]
    .map(([key, count]) => ({ entity: names.get(key)!, count }))
    .sort((left, right) => right.count - left.count || left.entity.localeCompare(right.entity))
  return { items, names }
}

/** Importance first, then the newest; the order Mnemon Native lists a store in. */
export function compareEntityMemories(left: Insight, right: Insight): number {
  return (right.importance ?? 0) - (left.importance ?? 0)
    || (right.createdAt ?? '').localeCompare(left.createdAt ?? '')
    || (left.memoryBodyName ?? '').localeCompare(right.memoryBodyName ?? '')
    || left.id.localeCompare(right.id)
}

/** Every memory across the indexes that carries the entity key. */
export function memoriesWithEntity(indexes: readonly SpaceEntityIndex[], key: string): Insight[] {
  const memories: Insight[] = []
  for (const index of indexes) for (const position of index.byKey.get(key) ?? []) memories.push(index.memories[position]!)
  return memories.sort(compareEntityMemories)
}
