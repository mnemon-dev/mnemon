// @vitest-environment jsdom
//
// Regression test for a real DSH Host crash: the real `ClientSettingsScope`
// implementation (e.g. @deepseek-ai/dsh-client-ui-settings) exposes
// `getSnapshot`/`subscribe` as `this`-dependent class methods. React's
// `useSyncExternalStore` invokes its callbacks as bare functions (`this` is
// `undefined`), so passing `settingsScope.getSnapshot` unbound throws
// `Cannot read properties of undefined` and the Memory System view goes blank.
//
// The scope mock below deliberately mirrors that `this`-dependent shape
// (methods, not closure arrow properties) so this test is red against the
// unbound call sites and green once they wrap the methods.
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ClientConnectionHandle, ClientSettingsScope, ClientSettingsSnapshot, Config } from '../src/host/protocol.ts'
import { MnemonSaveAction } from '../src/client/MnemonSaveAction.tsx'
import { MnemonWorkbench } from '../src/client/MnemonWorkbench.tsx'

class ThisBoundSettingsScope implements ClientSettingsScope<Config> {
  private state: ClientSettingsSnapshot<Config>
  private listeners = new Set<() => void>()
  constructor() {
    this.state = { status: 'ready', value: {}, revision: 1, writable: true, mode: 'host' }
  }
  getSnapshot(): ClientSettingsSnapshot<Config> {
    return this.state
  }
  subscribe(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }
  async mutate(): Promise<void> {}
}

/** Rejecting RPC is fine: both views catch async load failures; only the synchronous render path matters here. */
const connection = { rpc: { call: vi.fn(() => Promise.reject(new Error('no host'))) } } as unknown as ClientConnectionHandle
const locale = { getSnapshot: () => 'en-US', subscribe: () => () => {} }

describe('settings scope this-binding', () => {
  afterEach(cleanup)

  it('renders MnemonWorkbench with a this-bound settings scope (real Host shape)', () => {
    expect(() => render(<MnemonWorkbench connection={connection} settingsScope={new ThisBoundSettingsScope()} sessionId="session-1" />)).not.toThrow()
  })

  it('renders MnemonSaveAction with a this-bound settings scope (real Host shape)', () => {
    render(<MnemonSaveAction messageId="message-1" connection={connection} settingsScope={new ThisBoundSettingsScope()} localeRuntime={locale as never} t={key => key} />)
    expect(screen.getByLabelText('saveAction.button')).toBeTruthy()
  })
})
