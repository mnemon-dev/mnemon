import { describe, expect, it } from 'vitest'
import { RUNTIME_MEMORY_PROTOCOL, ROUTING_GUIDANCE, THREE_TIER_REMINDERS } from '../src/guidance.ts'

describe('default three-tier instructions', () => {
  it('owns the unchanged runtime write protocol, separate from Source snapshots', () => {
    expect(Buffer.byteLength(RUNTIME_MEMORY_PROTOCOL, 'utf8')).toBe(3_990)
    expect(RUNTIME_MEMORY_PROTOCOL).toContain('Manage hot memory exclusively with mnemon_runtime_memory')
    expect(RUNTIME_MEMORY_PROTOCOL).toContain('Use action="add" only for a new independent fact')
    expect(RUNTIME_MEMORY_PROTOCOL).toContain('Use action="replace" with a short unique old_text')
    expect(RUNTIME_MEMORY_PROTOCOL).toContain('Use action="remove" with a short unique old_text')
    expect(RUNTIME_MEMORY_PROTOCOL).toContain("The user's explicit request in the current turn wins over both files")
    expect(RUNTIME_MEMORY_PROTOCOL).toContain('call mnemon_recall instead of inferring or filling the gap')
    expect(ROUTING_GUIDANCE).toContain('Search Mnemon Documents for substantial project records')
    expect(THREE_TIER_REMINDERS.read).toContain('Otherwise use neither.')
    expect(THREE_TIER_REMINDERS.write).toContain('never retrieved evidence')
    expect(THREE_TIER_REMINDERS.both).toContain('Otherwise use none.')
  })
})
