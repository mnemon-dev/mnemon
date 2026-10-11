import type { MnemonDisplayMode } from './protocol.ts'

/** Keep the historical misspelling at input boundaries, never in UI or runtime state. */
export function normalizeDisplayMode(value: unknown): MnemonDisplayMode {
  if (value === undefined || value === 'sidebar') return 'sidebar'
  if (value === 'builtin' || value === 'buildin') return 'builtin'
  throw new Error('dsh-mnemon: displayMode must be sidebar or builtin (legacy buildin is accepted)')
}
