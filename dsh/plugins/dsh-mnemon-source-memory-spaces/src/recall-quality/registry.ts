/** Memory Spaces-owned recall quality policy. */
import type { RecallQualityPolicy } from './contracts.ts'
import { BUILTIN_RECALL_QUALITY_POLICIES } from './policies.ts'

export class RecallQualityPolicyRegistry {
  private readonly policies = new Map<string, RecallQualityPolicy>()

  constructor(policies: readonly RecallQualityPolicy[] = BUILTIN_RECALL_QUALITY_POLICIES) {
    for (const policy of policies) this.register(policy)
  }

  register(policy: RecallQualityPolicy): () => void {
    const id = policy.id.trim()
    if (!/^[a-z][a-z0-9-]{0,63}$/u.test(id)) throw new Error('recall quality policy id must match [a-z][a-z0-9-]{0,63}')
    if (this.policies.has(id)) throw new Error(`recall quality policy is already registered: ${id}`)
    this.policies.set(id, policy)
    return () => {
      if (this.policies.get(id) === policy) this.policies.delete(id)
    }
  }

  resolve(id: string): RecallQualityPolicy {
    const policy = this.policies.get(id)
    if (policy === undefined) throw new Error(`unknown recall quality policy: ${id}`)
    return policy
  }

  ids(): string[] {
    return [...this.policies.keys()]
  }
}

export const recallQualityPolicies = new RecallQualityPolicyRegistry()
