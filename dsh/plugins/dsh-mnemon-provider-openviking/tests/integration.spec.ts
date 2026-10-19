import { randomUUID } from 'node:crypto'
import { writeFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { createMemorySpaceProviderFixture } from 'dsh-mnemon-source-memory-spaces/testing'
import { OpenVikingProvider, descriptor } from '../src/index.ts'

const endpoint = process.env.MNEMON_OPENVIKING_TEST_ENDPOINT
const apiKey = process.env.MNEMON_OPENVIKING_TEST_API_KEY ?? ''

describe.skipIf(endpoint === undefined)('published OpenViking loopback integration', () => {
  it('creates, reads, searches, browses, and deletes an exact synthetic memory', async () => {
    if (!['127.0.0.1', '[::1]', 'localhost'].includes(new URL(endpoint!).hostname)) throw new Error('Use a disposable loopback OpenViking backend')
    const canary = `OpenViking integration ${randomUUID()}: release approval requires a completed canary.\n保留完整中文和换行。`
    const requests: Array<{ method: string; path: string }> = []
    const requestFetch: typeof fetch = async (url, init) => {
      requests.push({ method: init?.method ?? 'GET', path: new URL(String(url)).pathname })
      return fetch(url, init)
    }
    const { authority, body } = createMemorySpaceProviderFixture(descriptor, {
      endpoint: endpoint!, apiKey, targetUri: 'viking://user/default/memories', user: 'default', account: 'default', actorPeerId: 'dsh',
    }, { dataDir: '/unused', instanceId: 'isolated-openviking' })
    const provider = new OpenVikingProvider(authority, { fetch: requestFetch, requestTimeoutMs: 30_000 })
    let receipt: { id: string; action: string; vectorStatus: string } | undefined
    let removed: unknown
    try {
      receipt = await provider.remember(body, { content: canary, category: 'decision', importance: 4 }) as typeof receipt
      expect(receipt).toMatchObject({ action: 'stored', vectorStatus: 'complete' })
      const readback = await fetch(`${endpoint}/api/v1/content/read?${new URLSearchParams({ uri: receipt!.id })}`, {
        headers: apiKey ? { Authorization: `Bearer ${apiKey}` } : {},
      }).then(response => response.json()) as { result: string }
      expect(readback.result).toBe(canary)
      const recalled = await provider.search(body, { query: canary, limit: 20 })
      expect(recalled.results).toContainEqual(expect.objectContaining({ id: receipt!.id, content: canary }))
      const listed = await provider.list(body, { limit: 200 })
      expect(listed).toContainEqual(expect.objectContaining({ id: receipt!.id, content: canary }))
      expect(requests.some(request => request.path.includes('/sessions'))).toBe(false)
    } finally {
      if (receipt !== undefined) removed = await provider.forget(body, receipt.id)
    }
    expect(removed).toMatchObject({ action: 'deleted', uri: receipt!.id })
    expect((await provider.list(body, { limit: 200 })).some(item => item.id === receipt!.id)).toBe(false)
    const reportPath = process.env.MNEMON_OPENVIKING_TEST_REPORT
    if (reportPath) writeFileSync(reportPath, JSON.stringify({ endpoint, canary, receipt, removed, exactReadback: true, recalled: true, browsed: true, deleted: true, requests }, null, 2))
  }, 60_000)
})
