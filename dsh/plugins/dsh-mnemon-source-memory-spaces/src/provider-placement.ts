import type {
  AutomaticMemoryPlacementRequest,
  LlmMemoryPlacementSelection,
  MemoryPlacementCandidate,
  MemoryPlacementCapability,
  MemoryPlacementDecision,
  MemoryPlacementPreference,
  MemoryProviderCapabilities,
  MemoryProviderId,
  PreparedMemoryPlacement,
} from './contracts.ts'

const CAPABILITY_LABELS: Record<MemoryPlacementCapability, string> = {
  graph: 'typed graph',
  entities: 'entity index',
  related: 'related-memory traversal',
  'exact-write': 'exact writes',
  link: 'explicit links',
  forget: 'safe forget',
}
const CAPABILITIES = new Set<MemoryPlacementCapability>(Object.keys(CAPABILITY_LABELS) as MemoryPlacementCapability[])
const PREFERENCES = new Set<MemoryPlacementPreference>(['balanced', 'local-first', 'shared-first'])

function supports(candidate: MemoryPlacementCandidate, capability: MemoryPlacementCapability): boolean {
  if (capability === 'exact-write') return candidate.capabilities.writeMode === 'exact'
  return candidate.capabilities[capability]
}

function boundedPrompt(value: string | undefined): string {
  const normalized = value?.trim() ?? ''
  if (normalized.length > 4000) throw new Error('provider placement prompt is too long (max 4000 characters)')
  return normalized
}

function uniqueProviderIds(ids: MemoryProviderId[] | undefined): MemoryProviderId[] | undefined {
  if (ids === undefined) return undefined
  return [...new Set(ids)]
}

export function prepareMemoryPlacement(
  request: AutomaticMemoryPlacementRequest,
  candidates: readonly MemoryPlacementCandidate[],
): PreparedMemoryPlacement {
  if (request.mode !== 'automatic') throw new Error(`unsupported provider placement mode: ${String(request.mode)}`)
  const prompt = boundedPrompt(request.prompt)
  const rules = request.rules ?? {}
  const allowed = uniqueProviderIds(rules.allowedProviderIds)
  const required = [...new Set(rules.requiredCapabilities ?? [])]
  const candidateIds = new Set(candidates.map(candidate => candidate.id))
  for (const providerId of allowed ?? []) if (!candidateIds.has(providerId)) throw new Error(`unsupported memory provider in placement rules: ${String(providerId)}`)
  if (rules.dataBoundary !== undefined && rules.dataBoundary !== 'allow-remote' && rules.dataBoundary !== 'local-only') throw new Error(`unsupported data boundary: ${String(rules.dataBoundary)}`)
  for (const capability of required) if (!CAPABILITIES.has(capability)) throw new Error(`unsupported required memory capability: ${String(capability)}`)
  if (rules.preference !== undefined && !PREFERENCES.has(rules.preference)) throw new Error(`unsupported provider placement preference: ${String(rules.preference)}`)
  const appliedRules: string[] = []
  let eligible = candidates.filter(candidate => candidate.configured)

  if (allowed !== undefined) {
    if (allowed.length === 0) throw new Error('automatic provider placement requires at least one allowed provider')
    eligible = eligible.filter(candidate => allowed.includes(candidate.id))
    appliedRules.push(`allowed:${allowed.join(',')}`)
  }
  if (rules.dataBoundary === 'local-only') {
    eligible = eligible.filter(candidate => candidate.kind === 'local')
    appliedRules.push('data-boundary:local-only')
  }
  for (const capability of required) {
    eligible = eligible.filter(candidate => supports(candidate, capability))
    appliedRules.push(`requires:${capability}`)
  }
  const preference = rules.preference ?? 'balanced'
  appliedRules.push(`preference:${preference}`)

  if (eligible.length === 0) {
    const requirements = required.map(value => CAPABILITY_LABELS[value]).join(', ')
    throw new Error(`no configured memory provider satisfies the placement rules${requirements === '' ? '' : ` (${requirements})`}`)
  }

  const selectorBrief = [
    `Soft preference: ${preference}.`,
    'Eligible providers after host-enforced rules:',
    ...eligible.map(candidate => {
      const capabilities = [
        ...Object.entries(candidate.capabilities)
          .filter(([key, value]) => typeof value === 'boolean' && value)
          .map(([key]) => key),
        `writeMode=${candidate.capabilities.writeMode}`,
        `deletionMode=${candidate.capabilities.deletionMode}`,
      ]
      return `- ${candidate.id} (${candidate.label}, ${candidate.kind}): ${candidate.summary} Capabilities: ${capabilities.join(', ')}.`
    }),
  ].join('\n')

  return { prompt, candidates: eligible, appliedRules, selectorBrief }
}

export function rulesOnlyPlacement(
  prepared: PreparedMemoryPlacement,
  now: () => Date = () => new Date(),
): MemoryPlacementDecision | undefined {
  const candidate = prepared.candidates.length === 1 ? prepared.candidates[0] : undefined
  if (candidate === undefined) return undefined
  return {
    mode: 'automatic',
    providerId: candidate.id,
    decidedBy: 'rules',
    reason: `Only ${candidate.label} satisfies the configured placement rules.`,
    confidence: 'high',
    candidateProviderIds: [candidate.id],
    appliedRules: prepared.appliedRules,
    decidedAt: now().toISOString(),
  }
}

export function finalizeLlmPlacement(
  prepared: PreparedMemoryPlacement,
  selection: LlmMemoryPlacementSelection,
  delegation: { runId: string; provider: string },
  now: () => Date = () => new Date(),
): MemoryPlacementDecision {
  const providerId = selection.providerId.trim() as MemoryProviderId
  if (!prepared.candidates.some(candidate => candidate.id === providerId)) {
    throw new Error(`memory placement model selected an ineligible provider: ${selection.providerId}`)
  }
  const reason = selection.reason.trim()
  if (reason === '') throw new Error('memory placement model returned no reason')
  if (reason.length > 1000) throw new Error('memory placement reason is too long (max 1000 characters)')
  const confidence = selection.confidence.trim()
  if (confidence !== 'high' && confidence !== 'medium' && confidence !== 'low') {
    throw new Error(`memory placement model returned invalid confidence: ${selection.confidence}`)
  }
  return {
    mode: 'automatic',
    providerId,
    decidedBy: 'llm',
    reason,
    confidence,
    candidateProviderIds: prepared.candidates.map(candidate => candidate.id),
    appliedRules: prepared.appliedRules,
    decidedAt: now().toISOString(),
    runId: delegation.runId,
    subagentProvider: delegation.provider,
  }
}
