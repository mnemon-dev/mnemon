import { createHash } from 'node:crypto'
import { mkdirSync } from 'node:fs'
import { basename, isAbsolute, join, resolve } from 'node:path'
import type { JsonValue } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { MemorySpaceAuthority } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { runProcess, type ProcessRunner } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { Insight, MemoryBody as MemorySpace, MemoryGraphSnapshot, MemoryListRequest, MemoryProviderConnection, RememberRequest, SearchRequest } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { NORMALIZED_RELEVANCE_SCORE, type MemoryProviderAdapter, type ProviderBodyStatus as ProviderSpaceStatus, type ProviderMemorySpace, type ProviderSearchResult } from 'dsh-mnemon-source-memory-spaces/provider-sdk'

interface ByteRoverProviderOptions {
  process?: ProcessRunner
  queryTimeoutMs?: number
  curateTimeoutMs?: number
}

export class ByteRoverProvider implements MemoryProviderAdapter {
  readonly id = 'byterover' as const
  readonly scoreSemantics = NORMALIZED_RELEVANCE_SCORE
  private readonly process: ProcessRunner
  private readonly queryTimeoutMs: number
  private readonly curateTimeoutMs: number
  private readonly statusCache = new Map<string, { checkedAt: number; value: ProviderSpaceStatus }>()
  private readonly statusInFlight = new Map<string, Promise<ProviderSpaceStatus>>()

  constructor(private readonly memorySpaces: MemorySpaceAuthority, options: ByteRoverProviderOptions = {}) {
    this.process = options.process ?? runProcess
    this.queryTimeoutMs = options.queryTimeoutMs ?? 10_000
    this.curateTimeoutMs = options.curateTimeoutMs ?? 120_000
  }

  async discover(connection: MemoryProviderConnection): Promise<ProviderMemorySpace[]> {
    const configured = String(connection.defaultDirectory ?? '').trim()
    const existingDirectory = this.memorySpaces.list()
      .find(body => (body.provider.typeId ?? body.provider.id) === this.id)?.provider.settings.workingDirectory
    const directory = configured === ''
      ? String(existingDirectory ?? '').trim() || join(this.memorySpaces.runner.effectiveDataDir(), 'state', 'byterover', 'default')
      : isAbsolute(configured)
        ? configured
        : resolve(this.memorySpaces.runner.effectiveDataDir(), configured)
    return [{
      externalId: directory,
      name: basename(directory) || 'ByteRover',
      description: `ByteRover knowledge directory at ${directory}`,
      connection: { workingDirectory: directory },
    }]
  }

  async status(body: MemorySpace, signal?: AbortSignal): Promise<ProviderSpaceStatus> {
    if (signal !== undefined) return this.checkStatus(body, signal)
    const cached = this.statusCache.get(body.id)
    if (cached !== undefined && Date.now() - cached.checkedAt < 60_000) return cached.value
    const running = this.statusInFlight.get(body.id)
    if (running !== undefined) return running
    const pending = this.checkStatus(body)
    this.statusInFlight.set(body.id, pending)
    try {
      const value = await pending
      this.statusCache.set(body.id, { checkedAt: Date.now(), value })
      return value
    } finally {
      if (this.statusInFlight.get(body.id) === pending) this.statusInFlight.delete(body.id)
    }
  }

  invalidateStatus(memoryBodyId?: string): void {
    if (memoryBodyId === undefined) this.statusCache.clear()
    else this.statusCache.delete(memoryBodyId)
  }

  private async checkStatus(body: MemorySpace, signal?: AbortSignal): Promise<ProviderSpaceStatus> {
    try {
      await this.run(body, ['status'], 15_000, signal)
      return { healthy: true }
    } catch (error) {
      return { healthy: false, error: error instanceof Error ? error.message : String(error) }
    }
  }

  async search(body: MemorySpace, request: SearchRequest, signal?: AbortSignal): Promise<ProviderSearchResult> {
    const output = await this.run(body, ['query', '--', request.query.slice(0, 5_000)], this.queryTimeoutMs, signal)
    if (output.length < 20) return { results: [], hint: 'ByteRover found no relevant memories.' }
    const content = output.length > 8_000 ? `${output.slice(0, 8_000)}\n\n[... truncated]` : output
    return {
      results: [{
        id: `byterover:${createHash('sha256').update(content).digest('hex').slice(0, 24)}`,
        content,
        category: 'context',
        source: 'external',
        score: 1,
      }],
    }
  }

  async graph(body: MemorySpace): Promise<MemoryGraphSnapshot> {
    this.connection(body)
    return { nodes: [], edges: [], generatedAt: new Date().toISOString() }
  }

  async list(body: MemorySpace, request: MemoryListRequest, signal?: AbortSignal): Promise<Insight[]> {
    if (request.query === undefined || request.query.trim() === '') {
      this.connection(body)
      return []
    }
    return (await this.search(body, {
      query: request.query,
      ...(request.limit === undefined ? {} : { limit: request.limit }),
    }, signal)).results
  }

  async remember(body: MemorySpace, request: RememberRequest, signal?: AbortSignal): Promise<JsonValue> {
    await this.run(body, ['curate', '--', request.content], this.curateTimeoutMs, signal)
    return { action: 'stored', provider: this.id, summary: 'ByteRover curated the memory into its knowledge tree.' }
  }

  private connection(body: MemorySpace): Record<string, string | number | boolean> {
    if ((body.provider.typeId ?? body.provider.id) !== this.id) throw new Error(`ByteRover cannot serve provider ${body.provider.id}`)
    return this.memorySpaces.providerConnection(body.id, body.provider.id)
  }

  private async run(body: MemorySpace, args: string[], timeoutMs: number, signal?: AbortSignal): Promise<string> {
    const connection = this.connection(body)
    const command = String(connection.cliPath ?? 'brv')
    const configuredDirectory = String(connection.workingDirectory ?? connection.defaultDirectory ?? '').trim()
    const defaultDirectory = join(this.memorySpaces.runner.effectiveDataDir(), 'state', 'byterover', 'default')
    const cwd = configuredDirectory === ''
      ? defaultDirectory
      : isAbsolute(configuredDirectory)
        ? configuredDirectory
        : resolve(this.memorySpaces.runner.effectiveDataDir(), configuredDirectory)
    mkdirSync(cwd, { recursive: true, mode: 0o700 })
    const apiKey = String(connection.apiKey ?? '').trim()
    const result = await this.process(command, args, {
      timeoutMs,
      maxOutputBytes: 256 * 1024,
      ...(signal === undefined ? {} : { signal }),
      cwd,
      label: 'ByteRover',
      env: { ...process.env, ...(apiKey === '' ? {} : { BRV_API_KEY: apiKey }) },
    })
    const stdout = result.stdout.trim()
    const stderr = result.stderr.trim()
    if (result.exitCode !== 0) throw new Error(stderr || stdout || `ByteRover exited with code ${String(result.exitCode)}`)
    return stdout
  }
}
