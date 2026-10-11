/** Memory Spaces-owned recall quality policy. */
import type {
  EvaluatedRecallQualityCandidate,
  RecallQualityCandidate,
  RecallQualityDecision,
  RecallQualityPolicy,
  RecallQualityPolicyContext,
} from './contracts.ts'

function expandedLimit(context: RecallQualityPolicyContext): number {
  return Math.min(50, Math.max(context.requestedLimit, Math.ceil(context.requestedLimit * context.config.candidateMultiplier)))
}

function scoreDecision(
  candidate: RecallQualityCandidate,
  context: RecallQualityPolicyContext,
  lowScoreAction: RecallQualityDecision['action'],
  nonPositiveAction: RecallQualityDecision['action'],
): RecallQualityDecision {
  const score = candidate.insight.score
  if (score === undefined) return { action: 'keep', tier: 'unknown', reason: 'unscored' }
  if (!Number.isFinite(score)) return { action: 'drop', tier: 'unknown', reason: 'invalid-score' }
  if (candidate.scoreSemantics?.kind !== 'normalized-relevance') {
    return { action: 'keep', tier: 'unknown', reason: 'unscaled-score' }
  }
  if (score <= 0) return { action: nonPositiveAction, tier: 'low', reason: 'non-positive-score', normalizedScore: 0 }
  // A Provider violating its declared 0..1 range is safer as an unknown-scale
  // ranked result than as a fabricated high-confidence result.
  if (score > 1) return { action: 'keep', tier: 'unknown', reason: 'unscaled-score' }
  if (score < context.config.lowScoreThreshold) return { action: lowScoreAction, tier: 'low', reason: 'low-score', normalizedScore: score }
  if (score < context.config.highScoreThreshold) return { action: 'keep', tier: 'medium', reason: 'medium-score', normalizedScore: score }
  return { action: 'keep', tier: 'high', reason: 'high-score', normalizedScore: score }
}

function primarySelection(candidates: readonly EvaluatedRecallQualityCandidate[], context: RecallQualityPolicyContext): EvaluatedRecallQualityCandidate[] {
  return candidates.filter(candidate => candidate.decision.action === 'keep').slice(0, context.requestedLimit)
}

export const STRICT_RECALL_QUALITY_POLICY: RecallQualityPolicy = {
  id: 'strict-v1',
  candidateLimit: expandedLimit,
  evaluate: (candidate, context) => scoreDecision(candidate, context, 'drop', 'drop'),
  select(candidates, context) {
    const kept = candidates.filter(candidate => candidate.decision.action === 'keep')
    return [
      ...kept.filter(candidate => candidate.decision.tier === 'high'),
      ...kept.filter(candidate => candidate.decision.tier === 'medium').slice(0, context.config.maxMediumResults),
      ...kept.filter(candidate => candidate.decision.tier === 'unknown').slice(0, context.config.maxUnknownResults),
    ].slice(0, context.requestedLimit)
  },
}

export const BALANCED_RECALL_QUALITY_POLICY: RecallQualityPolicy = {
  id: 'balanced-v1',
  candidateLimit: expandedLimit,
  evaluate: (candidate, context) => scoreDecision(candidate, context, 'keep', 'drop'),
  select(candidates, context) {
    const kept = candidates.filter(candidate => candidate.decision.action === 'keep')
    return [
      ...kept.filter(candidate => candidate.decision.tier !== 'low'),
      ...kept.filter(candidate => candidate.decision.tier === 'low'),
    ].slice(0, context.requestedLimit)
  },
}

export const EXHAUSTIVE_RECALL_QUALITY_POLICY: RecallQualityPolicy = {
  id: 'exhaustive-v1',
  candidateLimit: expandedLimit,
  evaluate: (candidate, context) => scoreDecision(candidate, context, 'keep', 'keep'),
  select: primarySelection,
}

export const BUILTIN_RECALL_QUALITY_POLICIES = [
  STRICT_RECALL_QUALITY_POLICY,
  BALANCED_RECALL_QUALITY_POLICY,
  EXHAUSTIVE_RECALL_QUALITY_POLICY,
] as const
