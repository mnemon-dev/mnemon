import { describe, expect, it, vi } from 'vitest'
import { createMemorySourcePageClient } from '../src/client/page-client.tsx'

describe('Source page revision tracking', () => {
  it('uses read-returned revisions and does not let an older concurrent read roll them back', async () => {
    let settleOld!: (value: { revision: string; value: null }) => void
    const old = new Promise<{ revision: string; value: null }>(resolve => { settleOld = resolve })
    const read = vi.fn().mockReturnValueOnce(old).mockResolvedValueOnce({ revision: 'r2', value: {} })
    const mutate = vi.fn().mockResolvedValue({ revision: 'r3', value: {} })
    const client = createMemorySourcePageClient({ sourceInstanceKey: 'source:notes', revision: 'r1', read, mutate })
    const pending = client.read('old')
    await client.read('fresh')
    settleOld({ revision: 'r1', value: null })
    await pending
    await client.mutate('save', {}, true)
    expect(mutate).toHaveBeenCalledWith('save', {}, { confirmed: true, expectedRevision: 'r2' })
  })

  it('does not retry a rejected mutation or drop the caller confirmation requirement', async () => {
    const mutate = vi.fn().mockRejectedValue(new Error('revision conflict'))
    const client = createMemorySourcePageClient({ sourceInstanceKey: 'source:notes', revision: 'r1', read: vi.fn(), mutate })
    await expect(client.mutate('save', {}, false as never)).rejects.toThrow('confirmation')
    expect(mutate).not.toHaveBeenCalled()
    await expect(client.mutate('save', {}, true)).rejects.toThrow('revision conflict')
    expect(mutate).toHaveBeenCalledOnce()
  })
})
