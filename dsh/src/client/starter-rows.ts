/**
 * The component rows the Starter bundle's patch declares: each row's id and
 * the package it names. DSH lists them under the bundle's page; each opens a
 * page of its own, which shows that package's component page as the
 * configuration's board opens it.
 */
export const STARTER_COMPONENT_ROWS: ReadonlyArray<{ rowId: string; packageName: string }> = [
  { rowId: 'mnemon-source-runtime', packageName: 'dsh-mnemon-source-runtime' },
  { rowId: 'mnemon-source-documents', packageName: 'dsh-mnemon-source-documents' },
  { rowId: 'mnemon-source-memory-spaces', packageName: 'dsh-mnemon-source-memory-spaces' },
  { rowId: 'mnemon-strategy-default-three-tier', packageName: 'dsh-mnemon-strategy-default-three-tier' },
  { rowId: 'mnemon-strategy-general', packageName: 'dsh-mnemon-strategy-general' },
  { rowId: 'mnemon-strategy-auto-capture', packageName: 'dsh-mnemon-strategy-auto-capture' },
  { rowId: 'mnemon-strategy-light-context', packageName: 'dsh-mnemon-strategy-light-context' },
  { rowId: 'mnemon-strategy-scoped', packageName: 'dsh-mnemon-strategy-scoped' },
]
