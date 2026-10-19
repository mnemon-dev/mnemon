import { resolveRecallQuality } from 'dsh-mnemon-source-memory-spaces'

export const DEFAULT_IDLE_REVIEW_MS = 30_000
export const DEFAULT_RUNTIME_MEMORY_LIMIT_BYTES = 10 * 1024
export const DEFAULT_RUNTIME_USER_LIMIT_BYTES = 4 * 1024
export const MAX_RUNTIME_MEMORY_LIMIT_BYTES = 1024 * 1024
export const DEFAULT_RUNTIME_MAINTENANCE_MAX_TOKENS = 8_192
export const MAX_RUNTIME_MAINTENANCE_MAX_TOKENS = 1_000_000
export { DEFAULT_EMBEDDING_ENDPOINT, DEFAULT_EMBEDDING_MODEL, DEFAULT_EMBEDDING_PROTOCOL, MNEMON_EMBEDDING_PROTOCOLS } from "./protocol.ts"

export const DEFAULT_TIMEOUT_MS = 10_000
export const DEFAULT_RECALL_LIMIT = 10
const recallDefaults = resolveRecallQuality(undefined)
export const DEFAULT_RECALL_QUALITY_POLICY = recallDefaults.policy
export const DEFAULT_RECALL_LOW_SCORE_THRESHOLD = recallDefaults.lowScoreThreshold
export const DEFAULT_RECALL_HIGH_SCORE_THRESHOLD = recallDefaults.highScoreThreshold
export const DEFAULT_RECALL_CANDIDATE_MULTIPLIER = recallDefaults.candidateMultiplier
export const DEFAULT_RECALL_MAX_MEDIUM_RESULTS = recallDefaults.maxMediumResults
export const DEFAULT_RECALL_MAX_UNKNOWN_RESULTS = recallDefaults.maxUnknownResults
