import type { HostContextShape } from './dsh.ts'
import type { ResolvedConfig } from './config.ts'
import type { ComposableMemoryView } from '../core/contracts/index.ts'

export const GUIDANCE_SECTION_NAME = 'mnemon:routing'
export const RUNTIME_MEMORY_CONTEXT_NAME = 'mnemon:runtime-memory'
export const STRATEGY_SECTION_NAME = 'mnemon:strategy'
export const ROUTING_GUIDANCE = 'Use memory only when needed. Follow the current Mnemon View: use mnemon_view_route or mnemon_view_action only for offered ids. Installed tools do not imply an available Source. Never infer missing historical facts. An action offer is not authorization; a write exists only after its receipt.'

/** A View is an own-plugin message, never part of the shared context snapshot. */
export function withoutMemoryViewContext<T extends { contexts: Array<{ name: string; text: string }> }>(assembly: T): T {
  return { ...assembly, contexts: assembly.contexts.filter(context => context.name !== RUNTIME_MEMORY_CONTEXT_NAME) }
}

/** Inject trusted Strategy instructions after interpolation; Source text stays quoted in the Wake. */
export function applyMemoryViewGuidance<T extends { sections: Array<{ name: string; text: string }>; contexts: Array<{ name: string; text: string }> }>(assembly: T, view: ComposableMemoryView | undefined, routingEnabled = true): T {
  if (view === undefined) return withoutMemoryViewContext(assembly)
  const sections = assembly.sections.filter(section => ![GUIDANCE_SECTION_NAME, STRATEGY_SECTION_NAME, 'mnemon:runtime-memory-protocol'].includes(section.name))
  if (view.guidance?.system) sections.push({ name: STRATEGY_SECTION_NAME, text: view.guidance.system })
  if (routingEnabled) sections.push({ name: GUIDANCE_SECTION_NAME, text: view.guidance?.routing ?? ROUTING_GUIDANCE })
  return withoutMemoryViewContext({ ...assembly, sections })
}

/** Register once the prompt service is active; Cordis re-runs this if the service is replaced. */
export function registerGuidance(ctx: HostContextShape, config?: Pick<ResolvedConfig, 'routingGuidance'>): void {
  ctx.inject(['systemPrompt'], prompted => {
    const prompt = prompted.get('systemPrompt') as { section(value: { name: string; order: number; text: () => string }): unknown }
    prompt.section({
      name: GUIDANCE_SECTION_NAME,
      order: 150,
      text: () => config?.routingGuidance === false ? '' : ROUTING_GUIDANCE,
    })
  })
}
