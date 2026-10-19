import { realpathSync } from 'node:fs'
import { createRequire } from 'node:module'
import { performance } from 'node:perf_hooks'
import { LlmAdapter, type GenerateOptions, type StreamChunk } from '@deepseek-ai/dsh-llm'
import SessionQueryEngine from '@deepseek-ai/dsh-session-query'

export const MODEL = 'deepseek-v4-flash'
export const PROVIDER = 'flash-quality'
const ENDPOINT = 'https://api.deepseek.com/chat/completions'
export interface FlashAudit {
  scenario: string; modelCalls: Array<{ session: string; purpose: string; inputTokens: number; outputTokens: number; elapsedMs: number }>
  toolErrors: string[]; maxConcurrentModels: number; modelsReturned: string[]
}

export function sanitized(error: unknown): string {
  const text = error instanceof Error ? error.message : typeof error === 'string' ? error : JSON.stringify(error)
  const key = process.env.DEEPSEEK_API_KEY
  return (key ? text.replaceAll(key, '[credential]') : text).replace(/\/(?:Users|private|tmp|var)\/[^\s"']+/gu, '[temporary-path]').slice(0, 600)
}

export class SessionReads extends SessionQueryEngine {
  override async searchSessions(): Promise<never> { throw new Error('Stress test does not use a session index') }
  override async searchEvents(): Promise<never> { throw new Error('Stress test does not use a session index') }
}

/** The published DSH DeepSeek adapter handles the wire; this wrapper only observes it. */
class FlashAdapter extends LlmAdapter {
  private active = 0
  private readonly sessionLabels = new Map<string, string>()
  constructor(private readonly inner: LlmAdapter, private readonly report: FlashAudit, private readonly maxCalls: number) { super() }
  override providerInfo(provider: string) { return this.inner.providerInfo(provider) }
  override listModels(provider: string) { return this.inner.listModels(provider) }
  override resolveModel(provider: string, model: string, signal?: AbortSignal) {
    if (model !== MODEL) throw new Error('Stress test permits DeepSeek V4 Flash only')
    return this.inner.resolveModel(provider, model, signal)
  }
  override async *stream(options: GenerateOptions): AsyncIterable<StreamChunk> {
    if (options.model !== MODEL) throw new Error('Stress test permits DeepSeek V4 Flash only')
    if (this.report.modelCalls.length >= this.maxCalls) throw new Error('Stress test exceeded its bounded model-call budget')
    const sessionId = String(options.sessionId ?? 'unknown')
    if (!this.sessionLabels.has(sessionId)) this.sessionLabels.set(sessionId, `session-${this.sessionLabels.size + 1}`)
    const call: FlashAudit['modelCalls'][number] = { session: this.sessionLabels.get(sessionId)!, purpose: String(options.purpose ?? 'conversation'), inputTokens: 0, outputTokens: 0, elapsedMs: 0 }
    this.report.modelCalls.push(call)
    this.report.maxConcurrentModels = Math.max(this.report.maxConcurrentModels, ++this.active)
    const start = performance.now()
    try {
      for await (const chunk of this.inner.stream(options)) {
        if (chunk.type === 'usage') { call.inputTokens = chunk.usage.inputTokens; call.outputTokens = chunk.usage.outputTokens }
        yield chunk
      }
    } catch (error) {
      this.report.toolErrors.push(sanitized(error))
      console.log(JSON.stringify({ scenario: this.report.scenario, modelError: sanitized(error) }))
      throw error
    } finally { call.elapsedMs = Math.round(performance.now() - start); this.active-- }
  }
}


export async function liveFlash(report: FlashAudit, maxCalls = 600) {
  const key = process.env.DEEPSEEK_API_KEY
  if (!key) throw new Error('Explicit DEEPSEEK_API_KEY is required')
  // Load dependencies before installing the request observer, so setup failure cannot leave it installed.
  const requireDsh = createRequire(realpathSync(new URL('../../../node_modules/@deepseek-ai/dsh/package.json', import.meta.url)))
  const deepseek = await import(requireDsh.resolve('@deepseek-ai/dsh-llm-deepseek'))
  const fork = await import(requireDsh.resolve('@deepseek-ai/dsh-subagent-fork-in-process'))
  const audits: Promise<void>[] = []
  const wireErrors: string[] = []
  const originalFetch = globalThis.fetch
  globalThis.fetch = async (input, init) => {
    if (String(input) !== ENDPOINT || typeof init?.body !== 'string') throw new Error('Unexpected network request in isolated Flash stress test')
    const body = JSON.parse(init.body) as { model: string }
    if (body.model !== MODEL) throw new Error('Blocked a non-Flash request before network dispatch')
    const response = await originalFetch(input, init)
    audits.push((async () => {
      const reader = response.clone().body!.getReader()
      const decoder = new TextDecoder()
      let pending = ''
      let sawModel = false
      try {
        while (true) {
          const { value: bytes, done } = await reader.read()
          if (done) break
          pending += decoder.decode(bytes, { stream: true })
          const lines = pending.split('\n')
          pending = lines.pop()!
          for (const line of lines) {
            if (!line.startsWith('data: ') || line.trim() === 'data: [DONE]') continue
            const value = JSON.parse(line.slice(6)) as { model?: string }
            if (value.model === undefined) continue
            sawModel = true
            if (!report.modelsReturned.includes(value.model)) report.modelsReturned.push(value.model)
            if (value.model !== MODEL) throw new Error('Provider returned a non-Flash model')
          }
        }
      } catch (error) {
        // DSH closes a completed tool-call stream without waiting for socket EOF.
        // Validate the already-read model; actual request failures are observed by FlashAdapter.
        if (!sawModel || sanitized(error) !== 'DeepSeek stream consumer stopped') wireErrors.push(sanitized(error))
      } finally { reader.releaseLock() }
    })())
    return response
  }

  const options = deepseek.resolveAdapterOptions({ baseURL: 'https://api.deepseek.com', thinking: 'disabled', reasoningEffort: 'off', maxTokens: 8192, streamIdleTimeoutMs: 60000 })
  const adapter = new FlashAdapter(new deepseek.DeepSeekAdapter({ options: () => options,
    resolveApiKey: async () => key, resolveUserId: () => 'mnemon-synthetic-quality-acceptance',
    prepareExtensions: async () => ({ fields: {}, accept: async () => {} }) }), report, maxCalls)
  return { adapter, fork, async drain() { await Promise.all(audits); return wireErrors }, async dispose() {
    try { await Promise.all(audits); report.toolErrors.push(...wireErrors) } finally { globalThis.fetch = originalFetch }
  } }
}
