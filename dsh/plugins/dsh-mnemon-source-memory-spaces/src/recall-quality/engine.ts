/** Memory Spaces-owned recall quality policy. */
import type {
  EvaluatedRecallQualityCandidate,
  RecallQualityCandidate,
  RecallQualityDecision,
  RecallQualityPolicy,
  RecallQualityPolicyContext,
} from './contracts.ts'
import { STRICT_RECALL_QUALITY_POLICY } from './policies.ts'

const ACTIONS = new Set(['keep', 'drop'])
const TIERS = new Set(['high', 'medium', 'low', 'unknown'])
const REASONS = new Set([
  'high-score', 'medium-score', 'low-score', 'non-positive-score', 'invalid-score', 'unscaled-score', 'unscored',
])

function assertDecision(decision: RecallQualityDecision): void {
  if (typeof decision !== 'object' || decision === null || !ACTIONS.has(decision.action) || !TIERS.has(decision.tier) || !REASONS.has(decision.reason)) {
    throw new Error('recall quality policy returned an invalid decision')
  }
  if (decision.normalizedScore !== undefined && (!Number.isFinite(decision.normalizedScore) || decision.normalizedScore < 0 || decision.normalizedScore > 1)) {
    throw new Error('recall quality policy returned an invalid normalized score')
  }
}

function runPolicy(
  policy: RecallQualityPolicy,
  candidates: readonly RecallQualityCandidate[],
  context: RecallQualityPolicyContext,
): { evaluated: EvaluatedRecallQualityCandidate[]; selected: EvaluatedRecallQualityCandidate[] } {
  const evaluated = candidates.map(candidate => {
    const decision = policy.evaluate(candidate, context)
    assertDecision(decision)
    return { candidate, decision }
  })
  const eligible = new Set(evaluated.filter(candidate => candidate.decision.action === 'keep'))
  const selected = policy.select(evaluated, context)
  if (!Array.isArray(selected) || selected.length > context.requestedLimit) throw new Error('recall quality policy returned too many results')
  const seen = new Set<EvaluatedRecallQualityCandidate>()
  for (const candidate of selected) {
    if (!eligible.has(candidate) || seen.has(candidate)) throw new Error('recall quality policy selected an ineligible or duplicate result')
    seen.add(candidate)
  }
  return { evaluated, selected }
}

export interface PreparedRecallQualityPolicy {
  policy: RecallQualityPolicy
  candidateLimit: number
  fallbackFrom?: string
}

export function prepareRecallQualityPolicy(
  policy: RecallQualityPolicy,
  context: RecallQualityPolicyContext,
  fallback: RecallQualityPolicy = STRICT_RECALL_QUALITY_POLICY,
): PreparedRecallQualityPolicy {
  try {
    const candidateLimit = policy.candidateLimit(context)
    if (!Number.isInteger(candidateLimit) || candidateLimit < context.requestedLimit || candidateLimit > 50) throw new Error('invalid candidate limit')
    return { policy, candidateLimit }
  } catch {
    if (policy === fallback) throw new Error(`recall quality policy ${policy.id} returned an invalid candidate limit`)
    const candidateLimit = fallback.candidateLimit(context)
    return { policy: fallback, candidateLimit, fallbackFrom: policy.id }
  }
}

export function applyRecallQualityPolicy(
  prepared: PreparedRecallQualityPolicy,
  candidates: readonly RecallQualityCandidate[],
  context: RecallQualityPolicyContext,
  fallback: RecallQualityPolicy = STRICT_RECALL_QUALITY_POLICY,
): { policyId: string; fallbackFrom?: string; evaluated: EvaluatedRecallQualityCandidate[]; selected: EvaluatedRecallQualityCandidate[] } {
  try {
    return { policyId: prepared.policy.id, ...runPolicy(prepared.policy, candidates, context), ...(prepared.fallbackFrom === undefined ? {} : { fallbackFrom: prepared.fallbackFrom }) }
  } catch {
    if (prepared.policy === fallback) throw new Error(`recall quality policy ${prepared.policy.id} failed`)
    return { policyId: fallback.id, fallbackFrom: prepared.policy.id, ...runPolicy(fallback, candidates, context) }
  }
}
