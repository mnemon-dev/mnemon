import { describe, expect, it, vi } from 'vitest'
import { canonicalMemoryJson, jsonClone } from '../src/core/definitions.ts'

describe('Core JSON boundary validation without serialization', () => {
  it('retains frozen detached values, shared references, sparse arrays and negative zero', () => {
    const shared = { values: ['Unicode 中文 😀', -0, null, true] }
    const sparse = new Array(3) as string[]
    sparse[1] = 'middle'
    const value = { shared, same: shared, sparse, nullPrototype: Object.assign(Object.create(null) as { note: string }, { note: 'kept' }) }
    const cloned = jsonClone(value, 'boundary')
    expect(cloned).toEqual(value)
    expect(cloned).not.toBe(value)
    expect(cloned.shared).not.toBe(shared)
    expect(cloned.same).toBe(cloned.shared)
    expect(Object.is(cloned.shared.values[1], -0)).toBe(true)
    expect(Object.hasOwn(cloned.sparse, 0)).toBe(false)
    expect(Object.hasOwn(cloned.sparse, 1)).toBe(true)
    expect(Object.isFrozen(cloned)).toBe(true)
    expect(Object.isFrozen(cloned.shared.values)).toBe(true)
    expect(Object.isFrozen(cloned.nullPrototype)).toBe(true)
    shared.values[0] = 'caller changed'
    expect(cloned.shared.values[0]).toBe('Unicode 中文 😀')
  })

  it.each([
    undefined, () => undefined, Symbol('unsupported'), 1n, NaN, Infinity, -Infinity,
    new Date(), new Map(), new Set(), new Uint8Array([1]), Object.create({ inherited: true }) as unknown,
  ])('preserves canonical rejection for unsupported value %#', value => {
    for (const input of [value, { nested: value }, [value]]) {
      let expected: string | undefined
      try { canonicalMemoryJson(input, 'boundary') } catch (error) { expected = (error as Error).message }
      expect(expected).toBeDefined()
      expect(() => jsonClone(input, 'boundary')).toThrow(expected!)
    }
  })

  it('enforces the same depth and ancestor-cycle checks without rejecting shared subtrees', () => {
    let depth48: unknown = null
    for (let index = 0; index < 48; index++) depth48 = { nested: depth48 }
    expect(() => jsonClone(depth48, 'boundary')).not.toThrow()
    expect(() => jsonClone({ nested: depth48 }, 'boundary')).toThrow('boundary is nested too deeply')
    const cycle: { next?: unknown } = {}
    cycle.next = { back: cycle }
    expect(() => jsonClone(cycle, 'boundary')).toThrow('boundary contains a cycle')
    const shared = { value: 'allowed' }
    expect(jsonClone({ first: shared, second: shared }, 'boundary')).toEqual({ first: shared, second: shared })
  })

  it.each(['forEach', 'map'])('never allows a caller-owned %s override to bypass element checks', method => {
    for (const value of [NaN, new Date(0), () => undefined, undefined]) {
      const input = [value]
      Object.defineProperty(input, method, { value() {} })
      expect(() => jsonClone(input, 'boundary')).toThrow(/non-finite|non-JSON/u)
    }
    const cycle: unknown[] = []
    cycle.push(cycle)
    Object.defineProperty(cycle, method, { value() {} })
    expect(() => jsonClone(cycle, 'boundary')).toThrow('boundary contains a cycle')

    const valid = new Array(3) as string[]
    valid[1] = 'middle'
    Object.defineProperty(valid, method, { value: null })
    const result = jsonClone(valid, 'boundary')
    expect(result[1]).toBe('middle')
    expect(Object.hasOwn(result, 0)).toBe(false)
    expect(Object.hasOwn(result, method)).toBe(false)
    expect(Object.isFrozen(result)).toBe(true)
  })

  it('preserves canonical object visitation order when nested accessors affect later values', () => {
    const input = () => {
      const victim = { value: 0 }
      const trigger = {}
      Object.defineProperty(trigger, 'touch', { enumerable: true, get() { victim.value = NaN; return true } })
      return { z: victim, a: trigger }
    }
    expect(() => canonicalMemoryJson(input(), 'boundary')).toThrow('boundary contains a non-finite number')
    expect(() => jsonClone(input(), 'boundary')).toThrow('boundary contains a non-finite number')
  })

  it('does not stringify management payloads while canonical digests keep their exact representation', () => {
    const value = { z: [3, { z: 'last', a: 'first' }], a: 'start' }
    const stringify = vi.spyOn(JSON, 'stringify')
    try {
      expect(jsonClone(value, 'boundary')).toEqual(value)
      expect(stringify).not.toHaveBeenCalled()
      expect(canonicalMemoryJson(value)).toBe('{"a":"start","z":[3,{"a":"first","z":"last"}]}')
      expect(stringify).toHaveBeenCalled()
    } finally { stringify.mockRestore() }
  })
})
