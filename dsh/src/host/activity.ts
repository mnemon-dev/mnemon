import type { HostSessionEvent } from "./dsh.ts"
import type { TurnMemoryActivity, TurnMemoryActivitySnapshot } from "./protocol.ts"
import { parseMnemonActivityMeta } from './activity-presentation.ts'

export type { TurnMemoryActivity, TurnMemoryActivitySnapshot } from "./protocol.ts"

interface PendingCall {
  turn: number
  name: string
}

const RECALL_TOOLS = new Set(['mnemon_recall', 'mnemon_related'])
const INSPECTION_TOOLS = new Set(['mnemon_status', 'mnemon_memory_bodies'])
const WRITE_TOOLS = new Set([
  'mnemon_remember',
  'mnemon_forget',
  'mnemon_link',
  'mnemon_document_manage',
  'mnemon_document_create',
  'mnemon_runtime_memory',
  'mnemon_memory_body_create',
  'mnemon_memory_body_update',
  'mnemon_memory_body_merge',
])

function eventTurn(event: HostSessionEvent): number | undefined {
  return typeof event.data.turn === 'number' ? event.data.turn : undefined
}

function resultCallId(event: HostSessionEvent): string | undefined {
  const message = event.data.message as { source?: { callId?: unknown } } | undefined
  return typeof message?.source?.callId === 'string' && message.source.callId !== '' ? message.source.callId : undefined
}

function emptyActivity(turn: number): TurnMemoryActivity {
  return { turn, count: 0, names: [], recalls: 0, writes: 0, documentSearches: 0, inspections: 0, failures: 0,
    retrieved: [], writebacks: [] }
}

/**
 * Incremental durable-log projection. Repeated UI reads process only events
 * appended since the previous snapshot instead of rescanning the full session.
 */
export class TurnActivityProjection {
  private eventCount = 0
  private lastEventSeq: number | undefined
  private readonly pending = new Map<string, PendingCall>()
  private readonly byTurn = new Map<number, TurnMemoryActivity>()

  reset(): void {
    this.eventCount = 0
    this.lastEventSeq = undefined
    this.pending.clear()
    this.byTurn.clear()
  }

  snapshot(events: readonly HostSessionEvent[]): TurnMemoryActivitySnapshot {
    const currentLastSeq = events.at(-1)?.seq
    if (events.length < this.eventCount || (events.length === this.eventCount && this.lastEventSeq !== currentLastSeq)) this.reset()

    for (let index = this.eventCount; index < events.length; index += 1) this.consume(events[index]!)
    this.eventCount = events.length
    this.lastEventSeq = currentLastSeq

    return {
      cursor: typeof currentLastSeq === 'number' ? currentLastSeq : events.length,
      activities: [...this.byTurn.values()]
        .sort((left, right) => left.turn - right.turn)
        .map(activity => ({ ...activity, names: [...activity.names],
          retrieved: activity.retrieved.map(read => ({ ...read, items: read.items.map(item => ({ ...item })) })),
          writebacks: activity.writebacks.map(write => ({ ...write, item: { ...write.item } })),
        })),
    }
  }

  private consume(event: HostSessionEvent): void {
    if (event.type === 'tool/call') {
      const turn = eventTurn(event)
      const callId = event.data.callId
      const name = event.data.name
      if (turn !== undefined && typeof callId === 'string' && typeof name === 'string' && name.startsWith('mnemon_')) {
        this.pending.set(callId, { turn, name })
      }
      return
    }
    if (event.type !== 'tool/result') return
    const callId = resultCallId(event)
    if (callId === undefined) return
    const call = this.pending.get(callId)
    if (call === undefined) return
    this.pending.delete(callId)

    let activity = this.byTurn.get(call.turn)
    if (activity === undefined) {
      activity = emptyActivity(call.turn)
      this.byTurn.set(call.turn, activity)
    }
    activity.count += 1
    activity.names.push(call.name)
    if (event.data.error !== undefined) {
      activity.failures += 1
      return
    }
    const semantic = parseMnemonActivityMeta(event.data.meta, callId, call.name)
    if (semantic.read !== undefined) activity.retrieved.push(semantic.read)
    if (semantic.writeback !== undefined) activity.writebacks.push(semantic.writeback)
    if (call.name === 'mnemon_document_search') activity.documentSearches += 1
    else if (RECALL_TOOLS.has(call.name)) activity.recalls += 1
    else if (WRITE_TOOLS.has(call.name)) activity.writes += 1
    else if (INSPECTION_TOOLS.has(call.name)) activity.inspections += 1
  }
}
