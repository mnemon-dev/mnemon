/** Memory Spaces-owned recall quality policy. */
import type { Insight, MemoryProviderId, ResolvedRecallQualityConfig } from '../contracts.ts'
import type { ProviderScoreSemantics } from '../providers/adapter.ts'

export type RecallQualityDecisionReason =
  | 'high-score'
  | 'medium-score'
  | 'low-score'
  | 'non-positive-score'
  | 'invalid-score'
  | 'unscaled-score'
  | 'unscored'

export interface RecallQualityCandidate {
  insight: Insight
  memoryBodyId: string
  providerId: MemoryProviderId
  providerRank: number
  bodyOrder: number
  scoreSemantics?: ProviderScoreSemantics
}

export interface RecallQualityDecision {
  action: 'keep' | 'drop'
  tier: 'high' | 'medium' | 'low' | 'unknown'
  reason: RecallQualityDecisionReason
  normalizedScore?: number
}

export interface EvaluatedRecallQualityCandidate {
  candidate: RecallQualityCandidate
  decision: RecallQualityDecision
}

export interface RecallQualityPolicyContext {
  requestedLimit: number
  config: ResolvedRecallQualityConfig
}

/** Pure, bounded policy applied before recall rows are serialized to an Agent. */
export interface RecallQualityPolicy {
  readonly id: string
  candidateLimit(context: RecallQualityPolicyContext): number
  evaluate(candidate: RecallQualityCandidate, context: RecallQualityPolicyContext): RecallQualityDecision
  select(candidates: readonly EvaluatedRecallQualityCandidate[], context: RecallQualityPolicyContext): EvaluatedRecallQualityCandidate[]
}
