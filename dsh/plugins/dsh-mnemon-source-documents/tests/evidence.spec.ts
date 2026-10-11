import { describe, expect, it } from 'vitest'
import { documentEvidence } from '../src/evidence.ts'

describe('Source-owned query-local excerpts', () => {
  it('keeps a matching window and explicitly omits surrounding text', () => {
    const content = 'before '.repeat(1_000) + 'sentinel here ' + 'after '.repeat(1_000)
    const excerpt = documentEvidence(content, 'sentinel', 1_000)
    expect(excerpt).toContain('sentinel here')
    expect(excerpt).toContain('[earlier content omitted]')
    expect(excerpt).toContain('[later content omitted]')
    expect(excerpt.length).toBeLessThanOrEqual(1_000)
  })

  it('never overflows tiny budgets with omission labels or broken Unicode', () => {
    for (const content of ['a'.repeat(900) + 'sentinel', '😀'.repeat(500), '中文'.repeat(500) + '😀 sentinel']) {
      for (let limit = 0; limit < 70; limit++) {
        const excerpt = documentEvidence(content, 'sentinel', limit)
        expect(excerpt.length).toBeLessThanOrEqual(limit)
        expect(/\p{Surrogate}/u.test(excerpt)).toBe(false)
      }
    }
  })
})
