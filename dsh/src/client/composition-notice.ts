import type { MemoryCompositionStatus } from '../host/protocol.ts'
import type { MnemonTranslate } from './locales.ts'

/** What the workspace says about how memory is composed, when that needs the user's attention. */
export interface CompositionNotice {
  /** `error`: no composition serves, so conversations run without memory. */
  tone: 'error' | 'warn'
  title: string
  detail: string
}

/** The Host's first diagnostic, in the user's words where it is one the Host is known to give. */
function reason(system: MemoryCompositionStatus, t: MnemonTranslate): string {
  for (const diagnostic of system.evaluation.diagnostics) {
    if (diagnostic.code === 'missing-strategy') return t('status.reasonNoStrategy')
    if (diagnostic.code === 'missing-source') return t('status.reasonNoSource')
    if (diagnostic.code === 'not-composed') return t('status.reasonNotComposed')
    if (diagnostic.code !== 'composition-rejected') continue
    if (/selected memory Strategy (?:type|instance) is unavailable/u.test(diagnostic.message)) return t('status.reasonSelectedStrategyOff')
    const dependency = /dependency unavailable: (\S+) requires (\S+)/u.exec(diagnostic.message)
    if (dependency !== null) return t('status.reasonDependency', { component: dependency[1], requirement: dependency[2] })
    if (/(?:capability|slot) conflict/u.test(diagnostic.message)) return t('status.reasonConflict')
    return t('status.reasonRejected', { detail: diagnostic.message })
  }
  return ''
}

export function compositionNotice(system: MemoryCompositionStatus | undefined, t: MnemonTranslate): CompositionNotice | undefined {
  if (system === undefined) return undefined
  const cause = reason(system, t)
  const detail = (...sentences: string[]): string => sentences.filter(sentence => sentence !== '').join(t('config.sentenceGap'))
  if (!system.serving) return { tone: 'error', title: t('status.memoryOff'), detail: detail(cause, t('status.memoryOffDetail')) }
  // A rejected change leaves the previous composition serving.
  if (system.evaluation.state !== 'ready') return { tone: 'warn', title: t('status.compositionStale'), detail: detail(cause, t('status.compositionStaleDetail')) }
  if (system.evaluation.diagnostics.some(diagnostic => diagnostic.code === 'strategy-fallback')) {
    return { tone: 'warn', title: t('status.strategyFallback'), detail: t('status.strategyFallbackDetail') }
  }
  return undefined
}
