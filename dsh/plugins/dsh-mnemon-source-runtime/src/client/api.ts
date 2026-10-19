import type { RuntimeMemoryImportance, RuntimeMemoryMutationResult, RuntimeMemorySnapshot, RuntimeMemoryTarget } from '../contracts.ts'

/** Source-owned structural page API; the default bundle may supply agent-assisted callbacks. */
export interface RuntimePageClient {
  runtimeMemory(): Promise<RuntimeMemorySnapshot>
  mutateRuntimeMemory(request: {
    action: 'add' | 'replace' | 'remove'
    target: RuntimeMemoryTarget
    content?: string
    old_text?: string
    importance?: RuntimeMemoryImportance
    branches?: string[]
  }): Promise<RuntimeMemoryMutationResult>
}
