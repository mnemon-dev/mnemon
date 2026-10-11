import type { JSX } from 'react'
import { createContext, useContext } from 'react'
import { IconSearchOutlineRegular } from '@deepseek-ai/dsh-client-ui-primitives'
import { translateZh, type MnemonTranslate } from './locales.ts'
import { MnemonDialog, type MnemonDialogProps } from './MnemonDialog.tsx'
import { appearanceClass } from './view-styles.ts'
import sidebarCss from './MnemonSidebarView.module.css'
import css from './MnemonView.module.css'
import runtimeCss from 'dsh-mnemon-source-runtime/presentation/page.module.css'
import runtimeSidebarCss from 'dsh-mnemon-source-runtime/presentation/sidebar.module.css'
import documentsCss from 'dsh-mnemon-source-documents/presentation/page.module.css'
import documentsSidebarCss from 'dsh-mnemon-source-documents/presentation/sidebar.module.css'
import spacesCss from 'dsh-mnemon-source-memory-spaces/presentation/page.module.css'
import spacesSidebarCss from 'dsh-mnemon-source-memory-spaces/presentation/sidebar.module.css'

export const I18nContext = createContext<MnemonTranslate>(translateZh)

export const LocaleContext = createContext<string>('zh')

export function useT(): MnemonTranslate { return useContext(I18nContext) }

export function useLocale(): string {
  const locale = useContext(LocaleContext)
  return locale === 'en' ? 'en-US' : locale === 'zh' ? 'zh-CN' : locale
}

export function humanBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

export function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function parseBranchesInput(raw: string): string[] | undefined {
  const parsed = raw.split(',').map(value => value.trim()).filter(value => value !== '')
  return parsed.length === 0 ? undefined : parsed
}

export function short(value: string, max: number): string {
  return value.length <= max ? value : `${value.slice(0, max - 1)}…`
}

export function PageHeader(props: { title: string; description: string; meta?: string; loadingLabel?: string; action?: JSX.Element }): JSX.Element {
  return (
    <div className={appearanceClass(css.pageHeader, sidebarCss.pageHeader)}>
      <div><h2>{props.title}</h2><p>{props.description}</p></div>
      <div className={css.pageHeaderMeta}>{props.loadingLabel !== undefined && <PageSpinner label={props.loadingLabel} />}{props.meta !== undefined && <code>{props.meta}</code>}{props.action}</div>
    </div>
  )
}

export function PageSpinner({ label }: { label: string }): JSX.Element {
  return <span className={css.pageSpinner} role="status" aria-label={label} title={label}><i aria-hidden="true" /></span>
}

export function SectionSpinner({ label }: { label: string }): JSX.Element {
  return <span className={css.sectionSpinner} role="status" aria-label={label} title={label}><i aria-hidden="true" /></span>
}

export function ProgressiveFooter(props: { visible: number; total: number; pageSize: number; compact?: boolean; onMore: () => void }): JSX.Element | null {
  const t = useT()
  if (props.total === 0) return null
  const remaining = Math.max(0, props.total - props.visible)
  return <div className={props.compact === true ? css.compactListProgress : css.listProgress}><span>{t('common.showing', { visible: props.visible, total: props.total })}</span>{remaining > 0 && <button type="button" className={css.secondaryButton} onClick={props.onMore}>{t('common.showMore', { count: Math.min(props.pageSize, remaining) })}</button>}</div>
}

/** DSH-style action dialog shared by Sidebar add/write flows. */
export function SidebarModal(props: Omit<MnemonDialogProps, 'closeLabel'>): JSX.Element {
  const t = useT()
  return <MnemonDialog {...props} closeLabel={t('common.close')} />
}

/** The search glyph is drawn with DSH's search icon, as every search field shows it. */
const SEARCH_GLYPH = '⌕'

export function EmptyState(props: { glyph: string; title: string; children: string }): JSX.Element {
  return (
    <div className={css.emptyState}>
      <div className={css.emptyGlyph} aria-hidden="true">{props.glyph === SEARCH_GLYPH ? <IconSearchOutlineRegular size={18} /> : <span>{props.glyph}</span>}</div>
      <div><h3>{props.title}</h3><p>{props.children}</p></div>
    </div>
  )
}

// The default product combines Source assets through public package paths.
// Existing page-kit consumers keep the same class map; Core does not load UI.
export const memoryPageStyles: Readonly<Record<string, string>> = { ...css, ...runtimeCss, ...documentsCss, ...spacesCss }
export const memorySidebarStyles: Readonly<Record<string, string>> = { ...sidebarCss, ...runtimeSidebarCss, ...documentsSidebarCss, ...spacesSidebarCss }
