import type { MemoryPluginEntryView } from '../host/view-protocol.ts'
import type { MnemonTranslate } from './locales.ts'

/** Packages the dsh-mnemon Starter installs; any other component came from an installed extension. */
const SHIPPED_PACKAGES: ReadonlySet<string> = new Set([
  'dsh-mnemon-source-runtime', 'dsh-mnemon-source-documents', 'dsh-mnemon-source-memory-spaces',
  'dsh-mnemon-strategy-default-three-tier', 'dsh-mnemon-strategy-general',
  'dsh-mnemon-strategy-auto-capture', 'dsh-mnemon-strategy-light-context', 'dsh-mnemon-strategy-scoped',
])

/** Whether a component ships with dsh-mnemon rather than an installed extension. */
export function isShipped(entry: MemoryPluginEntryView): boolean {
  return SHIPPED_PACKAGES.has(entry.packageName)
}

/**
 * A component's name and description in the page's language, from its own
 * declaration: shipped and installed components are named the same way.
 */
export function componentCopy(entry: MemoryPluginEntryView, language: string): { label: string; hint: string } {
  const zh = language.toLowerCase().startsWith('zh')
  return { label: zh ? entry.label['zh-CN'] : entry.label.en, hint: zh ? entry.description['zh-CN'] : entry.description.en }
}

/** Quoted names joined in the page's language. */
export function nameList(names: readonly string[], t: MnemonTranslate): string {
  return names.map(name => t('config.quoted', { name })).join(t('config.listSeparator'))
}
