import type { Context } from '@deepseek-ai/cordis'
import { Remote, TypertRemoteService } from '@deepseek-ai/dsh-typert-protocol'
import type { HostRpcHandler, RpcResult } from './dsh.ts'
import { MNEMON_REMOTE_NAMESPACE } from './protocol.ts'

export interface MnemonRemoteHandlers {
  readonly read: HostRpcHandler
  readonly activation: HostRpcHandler
  readonly write: HostRpcHandler
  readonly pack: HostRpcHandler
  readonly settings: HostRpcHandler
  readonly view: HostRpcHandler
  readonly viewWrite: HostRpcHandler
  readonly management: boolean
}

function denied(): RpcResult<unknown> {
  return {
    ok: false,
    error: {
      code: 'bad-request',
      message: 'remote Mnemon management requires remoteAccess: trusted-host',
      details: { issues: [] },
    },
  }
}

function unknownSettingsEndpoint(endpoint: string): RpcResult<unknown> {
  return {
    ok: false,
    error: {
      code: 'bad-request',
      message: `unknown remote Mnemon settings endpoint: ${endpoint}`,
      details: { issues: [] },
    },
  }
}

/**
 * Project Mnemon's existing handlers through DSH API Gateway. The Gateway owns
 * `/api`, Host/Origin validation, browser pairing, and response envelopes;
 * this Service retains Mnemon's narrower management grant.
 */
export class MnemonRemoteService extends TypertRemoteService {
  constructor(ctx: Context, private readonly handlers: MnemonRemoteHandlers) {
    super(ctx, 'mnemonRemote', { namespace: MNEMON_REMOTE_NAMESPACE })
  }

  @Remote('read')
  read(endpoint: string, payload: unknown, signal: AbortSignal): Promise<RpcResult<unknown>> {
    return this.handlers.read(endpoint, payload, signal)
  }

  @Remote('activation')
  activation(endpoint: string, payload: unknown, signal: AbortSignal): Promise<RpcResult<unknown>> {
    return this.handlers.activation(endpoint, payload, signal)
  }

  @Remote('write')
  write(endpoint: string, payload: unknown, signal: AbortSignal): Promise<RpcResult<unknown>> {
    if (!this.handlers.management) return Promise.resolve(denied())
    return this.handlers.write(endpoint, payload, signal)
  }

  @Remote('pack')
  pack(endpoint: string, payload: unknown, signal: AbortSignal): Promise<RpcResult<unknown>> {
    if (!this.handlers.management) return Promise.resolve(denied())
    return this.handlers.pack(endpoint, payload, signal)
  }

  @Remote('settings')
  async settings(endpoint: string, payload: unknown, signal: AbortSignal): Promise<RpcResult<unknown>> {
    if (endpoint === 'mutate' && !this.handlers.management) return denied()
    if (endpoint !== 'get' && endpoint !== 'mutate') return unknownSettingsEndpoint(endpoint)
    const response = await this.handlers.settings(endpoint, payload, signal)
    if (endpoint !== 'get' || this.handlers.management || !response.ok || typeof response.value !== 'object' || response.value === null || Array.isArray(response.value)) return response
    return { ...response, value: { ...(response.value as Record<string, unknown>), writable: false } }
  }

  @Remote('view')
  view(endpoint: string, payload: unknown, signal: AbortSignal): Promise<RpcResult<unknown>> {
    return this.handlers.view(endpoint, payload, signal)
  }

  @Remote('viewWrite')
  viewWrite(endpoint: string, payload: unknown, signal: AbortSignal): Promise<RpcResult<unknown>> {
    if (!this.handlers.management) return Promise.resolve(denied())
    return this.handlers.viewWrite(endpoint, payload, signal)
  }
}
