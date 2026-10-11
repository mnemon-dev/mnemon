import type { Context } from '@deepseek-ai/cordis'
import { prepareStarterResolution } from './starter-resolution.ts'

// Public Cordis keys: `cordis.group` marks a Loader tree carrier whose config is
// a child entry list (the Loader leaves its expressions to the children), and
// `cordis.init` is Service.init.
const groupKey = Symbol.for('cordis.group')
const init = Symbol.for('cordis.init')

interface NativeGroup {
  update(config: unknown): Promise<void>
  stop(): void
}
type NativeGroupClass = new (ctx: Context, config: unknown) => NativeGroup

/**
 * The Starter's component group (`mnemon-bundle`). It makes the Starter's
 * dependency closure visible, then mounts the children with the Loader's own
 * `cordis:group`, so a component enabled without a restart resolves and no
 * separate readiness Entry can be turned off while the group waits for it.
 */
export default class MnemonBundle {
  static inject = ['loader']
  static [groupKey] = true

  constructor(private readonly ctx: Context, private readonly config: unknown) {}

  async *[init](): AsyncGenerator<() => void, void, unknown> {
    await prepareStarterResolution(this.ctx)
    // DSH registers the native group for `cordis:group` rows before it mounts a profile.
    const Group = (this.ctx as unknown as { loader?: { builtins?: { group?: NativeGroupClass } } }).loader?.builtins?.group
    if (typeof Group !== 'function') throw new Error('dsh-mnemon/bundle needs the Loader\'s cordis:group builtin')
    // Constructed in this fiber, the native group owns this Entry's children and config updates.
    const group = new Group(this.ctx, this.config)
    yield () => group.stop()
    await group.update(this.config)
  }
}
