import { beforeEach, describe, expect, it, vi } from 'vitest'

const preparation = vi.hoisted(() => ({ release: () => {} }))
vi.mock('../src/starter-resolution.ts', () => ({
  prepareStarterResolution: () => new Promise<void>(resolve => { preparation.release = resolve }),
}))
const { default: MnemonBundle } = await import('../src/bundle.ts')

const init = Symbol.for('cordis.init')
const events: string[] = []
class NativeGroup {
  constructor(readonly ctx: unknown, readonly config: unknown) { events.push('construct') }
  async update(config: unknown) { events.push(`update:${JSON.stringify(config)}`) }
  stop() { events.push('stop') }
}
type Bundle = { [init]: () => AsyncGenerator<() => void, void, unknown> }
const children = [{ id: 'mnemon', name: 'dsh-mnemon' }]

beforeEach(() => { events.length = 0 })

describe('the Starter component group', () => {
  it('is a Loader tree carrier that needs the Loader', () => {
    expect((MnemonBundle as unknown as Record<symbol, unknown>)[Symbol.for('cordis.group')]).toBe(true)
    expect(MnemonBundle.inject).toEqual(['loader'])
  })

  it('makes the dependency closure visible before the native group mounts its children', async () => {
    const ctx = { loader: { builtins: { group: NativeGroup } } }
    const lifecycle = (new MnemonBundle(ctx as never, children) as unknown as Bundle)[init]()
    const disposer = lifecycle.next()
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(events).toEqual([])
    preparation.release()
    const { value: stop } = await disposer
    expect(events).toEqual(['construct'])
    expect(await lifecycle.next()).toMatchObject({ done: true })
    expect(events).toEqual(['construct', `update:${JSON.stringify(children)}`])
    ;(stop as () => void)()
    expect(events.at(-1)).toBe('stop')
  })

  it('fails with a clear error when the Loader has no native group', async () => {
    const lifecycle = (new MnemonBundle({ loader: { builtins: {} } } as never, children) as unknown as Bundle)[init]()
    const first = lifecycle.next()
    preparation.release()
    await expect(first).rejects.toThrow('dsh-mnemon/bundle needs the Loader\'s cordis:group builtin')
    expect(events).toEqual([])
  })
})
