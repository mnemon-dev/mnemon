import { describe, expect, it } from 'vitest'
import { TurnActivityProjection } from "../src/host/activity.ts"
import { memoryReadPresentation, memoryWritePresentation } from '../src/host/activity-presentation.ts'
import type { HostSessionEvent } from "../src/host/dsh.ts"

function call(seq: number, turn: number, callId: string, name: string): HostSessionEvent {
  return { type: 'tool/call', seq, data: { turn, step: 1, callId, name, arguments: '{}' } }
}

function result(seq: number, turn: number, callId: string, failed = false, meta?: unknown): HostSessionEvent {
  return {
    type: 'tool/result',
    seq,
    data: {
      turn,
      step: 1,
      message: { source: { callId } },
      ...(failed ? { error: { name: 'Error', code: 'failed' } } : {}),
      ...(meta === undefined ? {} : { meta }),
    },
  }
}

describe('TurnActivityProjection', () => {
  it('counts only settled successes by semantic category and reports failures separately', () => {
    const projection = new TurnActivityProjection()
    const events = [
      call(1, 1, 'recall', 'mnemon_recall'), result(2, 1, 'recall'),
      call(3, 1, 'documents', 'mnemon_document_search'), result(4, 1, 'documents'),
      call(5, 1, 'write-failed', 'mnemon_remember'), result(6, 1, 'write-failed', true),
      call(7, 1, 'status', 'mnemon_status'), result(8, 1, 'status'),
      call(9, 1, 'write-pending', 'mnemon_runtime_memory'),
    ]

    expect(projection.snapshot(events)).toEqual({
      cursor: 9,
      activities: [{
        turn: 1,
        count: 4,
        names: ['mnemon_recall', 'mnemon_document_search', 'mnemon_remember', 'mnemon_status'],
        recalls: 1,
        writes: 0,
        documentSearches: 1,
        inspections: 1,
        failures: 1,
        retrieved: [],
        writebacks: [],
      }],
    })

    events.push(result(10, 1, 'write-pending'))
    expect(projection.snapshot(events).activities[0]).toMatchObject({ count: 5, writes: 1, failures: 1 })
    expect(projection.snapshot(events).activities[0]).toMatchObject({ count: 5, writes: 1, failures: 1 })
  })

  it('projects only tool-authored bounded reads and committed writebacks', () => {
    const projection = new TurnActivityProjection()
    const readMeta = memoryReadPresentation('documents', 'search')({ query: 'release' }, {
      results: [{ id: 'doc-1', title: 'Release plan', content: 'Ship after the final verification.' }],
      ignored: { credential: 'secret' },
    })
    const committedMeta = memoryWritePresentation('runtime', 'mutate')({
      action: 'add', target: 'memory', content: 'Use pnpm verify before release.',
    }, { success: true, added: 'Use pnpm verify before release.' })
    const pendingMeta = memoryWritePresentation('memory-spaces', 'remember')({ content: 'Not durable yet.' }, {
      memoryReceipt: { status: 'succeeded', completion: 'accepted' },
    })
    const events = [
      call(1, 3, 'read', 'mnemon_document_search'), result(2, 3, 'read', false, readMeta),
      call(3, 3, 'write', 'mnemon_runtime_memory'), result(4, 3, 'write', false, committedMeta),
      call(5, 3, 'pending', 'mnemon_remember'), result(6, 3, 'pending', false, pendingMeta),
    ]

    expect(projection.snapshot(events).activities[0]).toMatchObject({
      retrieved: [{ callId: 'read', toolName: 'mnemon_document_search', sourceTypeId: 'documents', operationId: 'search',
        items: [{ id: 'doc-1', title: 'Release plan', excerpt: 'Ship after the final verification.' }] }],
      writebacks: [{ callId: 'write', toolName: 'mnemon_runtime_memory', sourceTypeId: 'runtime', operationId: 'mutate',
        item: { id: 'mutate', title: 'Use pnpm verify before release.' } }],
    })
    expect(projection.snapshot(events).activities[0]!.writebacks).toHaveLength(1)
    expect(JSON.stringify(projection.snapshot(events))).not.toContain('credential')
  })

  it('names a removal by the text it removed and an item without text only by its id', () => {
    const removed = memoryWritePresentation('runtime', 'mutate')({ action: 'remove', target: 'memory', old_text: 'Checkout p75 LCP target is 2.5 s.' }, { success: true })
    const forgotten = memoryWritePresentation('memory-spaces', 'forget')({ id: 'memory-7', memoryBodyId: 'lumen' }, { success: true })
    expect(removed).toMatchObject({ item: { id: 'mutate', title: 'Checkout p75 LCP target is 2.5 s.' } })
    expect(forgotten).toMatchObject({ item: { id: 'memory-7', title: 'memory-7' } })
  })

  it('resets when the durable event log is replaced by a shorter session', () => {
    const projection = new TurnActivityProjection()
    projection.snapshot([call(1, 1, 'first', 'mnemon_recall'), result(2, 1, 'first')])

    expect(projection.snapshot([call(1, 2, 'second', 'mnemon_status')])).toEqual({ cursor: 1, activities: [] })
  })

  it('records a committed create-only Document as a visible document writeback', () => {
    const projection = new TurnActivityProjection()
    const meta = memoryWritePresentation('documents', 'create')({ title: 'Rollout follow-up', content: 'Separate new evidence.' }, {
      action: 'created', memoryReceipt: { status: 'succeeded', completion: 'committed', committedAt: '2026-09-08T00:00:00.000Z' },
    })
    const events = [call(1, 1, 'created', 'mnemon_document_create'), result(2, 1, 'created', false, meta)]
    expect(projection.snapshot(events).activities[0]).toMatchObject({
      writes: 1, names: ['mnemon_document_create'], writebacks: [{ toolName: 'mnemon_document_create', sourceTypeId: 'documents',
        operationId: 'create', item: { title: 'Rollout follow-up', excerpt: 'Separate new evidence.' } }],
    })
  })
})
