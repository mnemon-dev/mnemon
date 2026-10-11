// Browser-only public helpers behind dsh-mnemon/client. No Source registration,
// default workbench, Host Context, or raw transport is part of this module.
export {
  installMemorySourceUI, memorySourcePageEntryId, MNEMON_SOURCE_PAGE_SLOT,
  MNEMON_SOURCE_CONFIGURATION_MUTATE, MNEMON_SOURCE_CONFIGURATION_READ,
  type MemorySourcePageComponent, type MemorySourcePageDefinition,
  type MemorySourcePageProps, type MemorySourceUIContribution, type MemorySourceUIContext, type MemorySourcePageNavigation,
} from './source-pages.tsx'
export type { MnemonSourceManagementClient, MemorySourcePageInstance } from './source-contracts.ts'
export {
  installMemoryComponentUI, MNEMON_COMPONENT_SETTINGS_SLOT, MNEMON_COMPONENT_STATUS_SLOT,
  type MemoryComponentSettingsComponent, type MemoryComponentSettingsProps, type MemoryComponentStatusComponent, type MemoryComponentStatusProps,
  type MemoryComponentUIContext, type MemoryComponentUIContribution,
} from './component-ui.tsx'
export * from './page-kit.tsx'
export * from './page-controls.tsx'
export * from './page-client.tsx'
export { MnemonDialog, type MnemonDialogProps } from './MnemonDialog.tsx'
export { MnemonLogo } from './MnemonLogo.tsx'
export { useRequestVersion } from './use-request-version.ts'
export { appearanceClass } from './view-styles.ts'
export { translateEn, translateZh, type MnemonKey, type MnemonTranslate } from './locales.ts'
export { IconChevronLeftOutline14 } from './ui-icons.ts'
