/** Validate external mutation receipts before retiring or compacting local data. */

const COMMITTED_MUTATION_STATES = new Set([
  'added',
  'committed',
  'completed',
  'created',
  'deleted',
  'forgotten',
  'imported',
  'invalidated',
  'linked',
  'merged',
  'removed',
  'replaced',
  'stored',
  'succeeded',
  'success',
  'updated',
])
const UNCOMMITTED_MUTATION_STATES = new Set([
  'accepted',
  'candidate',
  'canceled',
  'cancelled',
  'error',
  'failed',
  'pending',
  'partial',
  'processing',
  'queued',
  'running',
  'skipped',
])
const COMMITTED_MUTATION_COUNTS = ['created', 'deleted', 'edges_inserted', 'imported', 'removed', 'stored', 'updated'] as const

/** A provider mutation is authoritative only after it reports an explicit durable terminal state. */
export function mutationResultCommitted(result: unknown): boolean {
  if (typeof result !== 'object' || result === null || Array.isArray(result)) return false
  const value = result as Record<string, unknown>
  // A Core receipt wins over Provider-shaped details or a successful tool transport.
  if ('memoryReceipt' in value || 'completion' in value) {
    const receipt = 'memoryReceipt' in value ? value.memoryReceipt : value
    if (typeof receipt !== 'object' || receipt === null || Array.isArray(receipt)) return false
    const declared = receipt as Record<string, unknown>
    return declared.completion === 'committed' && declared.status === 'succeeded'
      && typeof declared.committedAt === 'string' && Number.isFinite(Date.parse(declared.committedAt))
  }
  // Host-owned management workflows also consume existing Source product results.
  const states = [value.action, value.status, value.state]
    .filter((entry): entry is string => typeof entry === 'string')
    .map(entry => entry.trim().toLocaleLowerCase())
  if (value.success === false || value.ok === false || value.committed === false || value.durable === false) return false
  if (typeof value.errors === 'number' && value.errors > 0 || Array.isArray(value.errors) && value.errors.length > 0) return false
  if (states.some(state => UNCOMMITTED_MUTATION_STATES.has(state))) return false
  if (value.committed === true || value.durable === true) return true
  if (states.some(state => COMMITTED_MUTATION_STATES.has(state))) return true
  return COMMITTED_MUTATION_COUNTS.some(key => typeof value[key] === 'number' && Number.isFinite(value[key]) && value[key] > 0)
}
