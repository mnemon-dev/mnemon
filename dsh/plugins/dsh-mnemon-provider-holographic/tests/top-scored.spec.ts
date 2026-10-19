import { describe, expect, it } from 'vitest'
import { topScored } from '../src/top-scored.ts'

describe('bounded score selection', () => {
  it('matches stable full sorting across sizes, ties, directions and limits', () => {
    for (const n of [0, 1, 10, 64, 65, 100, 1000]) {
      for (const scores of [
        Array.from({ length: n }, (_, i) => i),
        Array.from({ length: n }, (_, i) => n - i),
        Array.from({ length: n }, (_, i) => (i * 37) % 11),
        Array.from({ length: n }, () => 0.5),
      ]) {
        const values = scores.map((score, id) => ({ score, id }))
        for (const limit of [0, 1, 2, 10, 20, 50, 99, 1001, 3.5, NaN, Infinity]) {
          expect(topScored([...values], limit)).toEqual([...values].sort((a, b) => b.score - a.score).slice(0, limit))
        }
      }
    }
  })

  it('retains the legacy sort result for non-finite scores', () => {
    for (const score of [NaN, Infinity, -Infinity]) for (const position of [0, 37, 99]) {
      const values = Array.from({ length: 100 }, (_, i) => ({ id: i, score: i === position ? score : i % 7 }))
      expect(topScored([...values], 10)).toEqual([...values].sort((a, b) => b.score - a.score).slice(0, 10))
    }
  })

  it('bounds ranking work when the requested result count is small', () => {
    let reads = 0
    const n = 10_000
    const values = Array.from({ length: n }, (_, i) => ({ id: i, get score() { reads += 1; return i } }))
    expect(topScored(values, 10).map(row => row.id)).toEqual(Array.from({ length: 10 }, (_, i) => n - i - 1))
    expect(reads).toBeLessThan(n * 20)
  })
})
