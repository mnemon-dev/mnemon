/**
 * Compile-time boundary against the public DSH browser contracts.
 *
 * Keep version-sensitive declaration merging in one place so a future DSH
 * upgrade fails here instead of being papered over by local copies of slots.
 */
import type { ConnectionHandle } from '@deepseek-ai/dsh-client-connection/client'
import type { LocaleRuntime } from '@deepseek-ai/dsh-client-locale/client'
import type { ObservableSnapshot } from '@deepseek-ai/dsh-client-store'
import type { Context } from '@deepseek-ai/cordis'
import type {} from '@deepseek-ai/dsh-client-ui-chat/client'
import type {} from '@deepseek-ai/dsh-client-ui-plugin-manager/client'
import type {} from '@deepseek-ai/dsh-client-ui-conversation/client'
import type {} from '@deepseek-ai/dsh-client-ui-renderer/client'
import type {} from '@deepseek-ai/dsh-client-ui-layout/client'
import type {} from '@deepseek-ai/dsh-client-ui-sidebar/client'
import type {} from '@deepseek-ai/dsh-client-ui-settings/client'
import type {} from '@deepseek-ai/dsh-client-ui-session/client'
import type { MnemonKey } from './locales.ts'
import type { MemorySourcePageProps } from './source-contracts.ts'
import type { MemoryComponentSettingsProps, MemoryComponentStatusProps } from './component-ui.tsx'
export type { MnemonSourceManagementClient } from './source-contracts.ts'
export type MnemonSourcePageOwnerProps = MemorySourcePageProps

declare module '@deepseek-ai/dsh-client-ui-slots' {
  interface LocaleNamespaceMap {
    mnemon: MnemonKey
  }

  interface SlotMap {
    /** Optional Source-specific pages owned by the canonical Mnemon workspace. */
    'mnemon.source.page': {
      kind: 'list'
      scope: 'root'
      owner: MnemonSourcePageOwnerProps
    }
    /** A component's own settings on its page, keyed by the component's package name. */
    'mnemon.component.settings': {
      kind: 'keyed'
      scope: 'root'
      owner: MemoryComponentSettingsProps
    }
    /** A component's card on the Memory System's Status page, keyed by the component's package name. */
    'mnemon.component.status': {
      kind: 'keyed'
      scope: 'root'
      owner: MemoryComponentStatusProps
    }
  }
}

export interface MnemonSessionSummary {
  cwd?: string
  origin?: string
  projectionValues?: Readonly<Record<string, unknown>>
  [key: string]: unknown
}

export interface MnemonSessionListState {
  byId: Record<string, MnemonSessionSummary>
  [key: string]: unknown
}

interface MnemonWorkspaceSummary {
  workspaceId: unknown
  title: string
  path: string
}

interface MnemonWorkspaceListState {
  items: MnemonWorkspaceSummary[]
  [key: string]: unknown
}

/**
 * DSH client Context as Mnemon's apply receives it. The session and workspace
 * controller packages are not dependencies, so their list states are narrowed
 * locally to the fields Mnemon reads.
 */
export type MnemonClientContext = Context & {
  connection: ConnectionHandle
  locale: LocaleRuntime
  sessions: { list: ObservableSnapshot<MnemonSessionListState> }
  workspaces: { list: ObservableSnapshot<MnemonWorkspaceListState> }
}
