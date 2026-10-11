import type { JsonValue } from '../contracts.ts'
import type { MemorySpaceAuthority } from './adapter.ts'
import type {
  Insight,
  MemorySpace,
  MemoryGraphSnapshot,
  MemoryListRequest,
  MemoryProviderConnection,
  MemoryProviderId,
} from '../contracts.ts'

export interface HttpProviderOptions {
  label?: string
  fetch?: typeof fetch
  requestTimeoutMs?: number
}

export interface JsonRequestOptions {
  method?: string
  headers?: HeadersInit
  json?: JsonValue
  signal?: AbortSignal | undefined
  timeoutMs?: number
}

export function jsonObject(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined
}

export function jsonString(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined
}

export function jsonNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

export function jsonArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

export function firstArray(value: unknown, ...keys: string[]): unknown[] {
  if (Array.isArray(value)) return value
  const root = jsonObject(value)
  for (const key of keys) if (Array.isArray(root?.[key])) return root[key] as unknown[]
  const nested = jsonObject(root?.data)
  for (const key of keys) if (Array.isArray(nested?.[key])) return nested[key] as unknown[]
  return []
}

function errorDetail(payload: unknown): string | undefined {
  if (typeof payload === 'string') return payload.trim() || undefined
  const root = jsonObject(payload)
  const direct = jsonString(root?.message) ?? jsonString(root?.error) ?? jsonString(root?.detail)
  if (direct !== undefined) return direct
  const error = jsonObject(root?.error)
  return jsonString(error?.message) ?? jsonString(error?.detail)
}

/** Shared timeout, cancellation, error, and projection behavior for HTTP providers. */
export abstract class HttpMemoryProvider {
  abstract readonly id: MemoryProviderId
  protected readonly label: string | undefined
  protected readonly requestFetch: typeof fetch
  protected readonly requestTimeoutMs: number

  /** @deprecated Use memorySpaces. */
  protected readonly memoryBodies: MemorySpaceAuthority

  constructor(protected readonly memorySpaces: MemorySpaceAuthority, options: HttpProviderOptions = {}) {
    this.memoryBodies = memorySpaces
    this.label = options.label
    this.requestFetch = options.fetch ?? globalThis.fetch
    this.requestTimeoutMs = options.requestTimeoutMs ?? 15_000
  }

  abstract list(body: MemorySpace, request: MemoryListRequest, signal?: AbortSignal): Promise<Insight[]>

  async graph(body: MemorySpace, signal?: AbortSignal): Promise<MemoryGraphSnapshot> {
    const items = await this.list(body, { limit: 200 }, signal)
    return {
      nodes: items.map(item => ({ ...item, color: '#6574d9' })),
      edges: [],
      generatedAt: new Date().toISOString(),
    }
  }

  protected connection(body: MemorySpace): MemoryProviderConnection {
    if ((body.provider.typeId ?? body.provider.id) !== this.id) throw new Error(`${this.id} cannot serve provider ${body.provider.id}`)
    return this.memorySpaces.providerConnection(body.id, body.provider.id)
  }

  protected async request(body: MemorySpace, path: string, options: JsonRequestOptions = {}): Promise<unknown> {
    const connection = this.connection(body)
    return this.requestConnection(connection, path, options)
  }

  protected async requestConnection(connection: MemoryProviderConnection, path: string, options: JsonRequestOptions = {}): Promise<unknown> {
    const endpoint = String(connection.endpoint ?? '').replace(/\/+$/u, '')
    const label = this.label ?? this.id
    if (endpoint === '') throw new Error(`${label} endpoint is not configured`)
    if (!path.startsWith('/')) throw new Error(`${label} request path must be absolute`)
    options.signal?.throwIfAborted()
    const controller = new AbortController()
    const relay = () => controller.abort(options.signal?.reason)
    options.signal?.addEventListener('abort', relay, { once: true })
    const timeoutMs = options.timeoutMs ?? this.requestTimeoutMs
    const timer = setTimeout(() => controller.abort(new Error(`${label} request timed out`)), timeoutMs)
    const headers = new Headers(options.headers)
    if (options.json !== undefined && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
    try {
      const response = await this.requestFetch(`${endpoint}${path}`, {
        method: options.method ?? (options.json === undefined ? 'GET' : 'POST'),
        headers,
        ...(options.json === undefined ? {} : { body: JSON.stringify(options.json) }),
        signal: controller.signal,
      })
      const raw = await response.text()
      let payload: unknown = {}
      if (raw !== '') {
        try { payload = JSON.parse(raw) } catch { payload = raw }
      }
      if (!response.ok) {
        const detail = errorDetail(payload)
        throw new Error(`${label} HTTP ${response.status}${detail === undefined ? '' : `: ${detail}`}`)
      }
      return payload
    } catch (error) {
      if (controller.signal.aborted && options.signal?.aborted !== true) {
        throw new Error(`${label} request timed out after ${timeoutMs}ms`)
      }
      throw error
    } finally {
      clearTimeout(timer)
      options.signal?.removeEventListener('abort', relay)
    }
  }
}
