import { useSyncExternalStore } from 'react'
import { Button } from '@deepseek-ai/dsh-client-ui-primitives'
import type { PluginDetailProps } from '@deepseek-ai/dsh-client-ui-plugin-manager/client'
import type { MnemonActionSeat } from './action-seat.ts'
import type { MnemonTranslate } from './locales.ts'
import { MemoryIcon } from './memory-icon.tsx'

/** The npm package whose page under DSH Plugins carries the Mnemon configuration. */
export const MNEMON_PACKAGE_NAME = 'dsh-mnemon'

interface MnemonPluginActionsProps extends PluginDetailProps {
  /** Opens the memory workspace in its current placement; absent while no placement can show it. */
  workspace: MnemonActionSeat
  t: MnemonTranslate
}

/** Head controls on the dsh-mnemon page under Plugins: a way back to the memory workspace. */
export function MnemonPluginActions({ subject, workspace, t }: MnemonPluginActionsProps): JSX.Element | null {
  const open = useSyncExternalStore(workspace.subscribe, workspace.getSnapshot, workspace.getSnapshot)
  if (subject.kind !== 'bundle' || subject.pkg.name !== MNEMON_PACKAGE_NAME || !subject.pkg.enabled || open === undefined) return null
  return <Button variant="outline" size="sm" icon={<MemoryIcon size={13} />} onClick={open}>{t('plugin.openWorkspace')}</Button>
}
