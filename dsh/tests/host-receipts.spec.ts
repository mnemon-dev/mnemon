import { describe, expect, it } from 'vitest'
import { mutationResultCommitted } from '../src/host/receipts.ts'

describe('Host durable completion evidence', () => {
  const committed = { status: 'succeeded', completion: 'committed', committedAt: '2026-08-31T00:00:00.000Z' }

  it('accepts explicit Core commits in both View and named tool results', () => {
    expect(mutationResultCommitted(committed)).toBe(true)
    expect(mutationResultCommitted({ action: 'stored', memoryReceipt: committed })).toBe(true)
  })

  it.each(['accepted', 'candidate', 'partial', 'failed', 'unknown'])('does not retire data on %s completion', completion => {
    expect(mutationResultCommitted({ action: 'stored', success: true, memoryReceipt: { ...committed, completion } })).toBe(false)
    expect(mutationResultCommitted({ ...committed, completion })).toBe(false)
  })

  it.each([
    { ...committed, status: 'failed' }, { ...committed, committedAt: undefined }, { ...committed, committedAt: 'invalid' },
    { ...committed, completion: 'unexpected' }, null, [], true,
  ])('rejects an incomplete or invalid commit envelope: %j', memoryReceipt => {
    expect(mutationResultCommitted({ action: 'stored', memoryReceipt })).toBe(false)
  })

  it.each([
    { success: true }, { ok: true }, { stored: Infinity }, { action: 'stored', status: 'partial' },
    { action: 'stored', state: 'candidate' }, { action: 'stored', errors: 1 }, { imported: 1, errors: ['failed row'] },
    { action: 'stored', status: 'queued' }, { action: 'stored', committed: false },
  ])('does not infer durability from ambiguous Source management output: %j', result => {
    expect(mutationResultCommitted(result)).toBe(false)
  })

  it.each([{ action: 'added' }, { status: 'committed' }, { imported: 1, errors: 0 }, { durable: true }])('retains explicit Source management completion: %j', result => {
    expect(mutationResultCommitted(result)).toBe(true)
  })
})
