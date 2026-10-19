import { useCallback, useEffect, useRef, useState } from 'react'
import { writeClipboard } from '@deepseek-ai/dsh-client-ui-primitives'
import type { VersionComponentStatus, VersionInstallMode, VersionPackageStatus, VersionStatus, VersionUpdateResult } from '../host/protocol.ts'
import { MnemonClient } from './api.ts'
import type { MnemonTranslate } from './locales.ts'
import { message, SidebarModal, useT } from './page-kit.tsx'
import { clearStarterUpdate, recordStarterUpdate, type StarterUpdateRecord } from './starter-update.ts'
import css from './MnemonView.module.css'

function versionModeLabel(t: MnemonTranslate, mode: VersionInstallMode, cli = false): string {
  if (mode === 'homebrew') return t('versions.modeHomebrew')
  if (mode === 'go') return t('versions.modeGo')
  if (mode === 'npm') return t(cli ? 'versions.modeNpmCli' : 'versions.modeNpm')
  if (mode === 'link') return t('versions.modeLink')
  if (mode === 'missing') return t('versions.modeMissing')
  return t('versions.modeManual')
}

function versionHint(t: MnemonTranslate, component: VersionComponentStatus): string {
  if (component.updateHint === 'npm') return t('versions.hintNpm')
  if (component.updateHint === 'npm-missing') return t('versions.hintNpmMissing')
  if (component.updateHint === 'npm-unmanaged') return t('versions.hintNpmUnmanaged')
  if (component.updateHint === 'cli-unreadable') return t('versions.hintUnreadable')
  if (component.updateHint === 'starter') return t('versions.hintStarter')
  if (component.updateHint === 'brew') return t('versions.hintHomebrew')
  if (component.updateHint === 'brew-missing') return t('versions.hintBrewMissing')
  if (component.updateHint === 'go') return t('versions.hintGo')
  if (component.updateHint === 'dsh') return t('versions.hintDsh')
  if (component.updateHint === 'pnpm') return t('versions.hintPnpm')
  if (component.updateHint === 'pnpm-missing') return t('versions.hintPnpmMissing')
  if (component.updateHint === 'link') return t('versions.hintLink')
  if (component.updateHint === 'install') return t('versions.hintInstall')
  return t('versions.hintManual')
}

function versionState(component: VersionComponentStatus): 'missing' | 'unknown' | 'restart' | 'available' | 'current' | 'local' {
  if (component.installMode === 'missing') return 'missing'
  if (component.restartRequired) return 'restart'
  if (component.current === undefined || component.latest === undefined || component.checkError !== undefined) return 'unknown'
  if (component.outdated) return 'available'
  // A local or prerelease build can be ahead of the published release.
  return component.current.replace(/^v/, '') === component.latest.replace(/^v/, '') ? 'current' : 'local'
}

function CommandSnippet({ command }: { command: string }): JSX.Element {
  const t = useT()
  const [copied, setCopied] = useState(false)
  const [failed, setFailed] = useState(false)
  const copy = async () => {
    const accepted = await writeClipboard(command)
    setCopied(accepted)
    setFailed(!accepted)
  }
  return <div className={css.versionCommand}>
    <code>{command}</code>
    <button type="button" className={css.ghostButton} aria-label={t('versions.copyCommand', { command })} onClick={() => void copy()}>{t(copied ? 'versions.copied' : 'versions.copy')}</button>
    {failed && <small role="status">{t('versions.copyFailed')}</small>}
  </div>
}

function NpmGuidance({ component }: { component: VersionComponentStatus }): JSX.Element {
  const t = useT()
  const missing = component.installMode === 'missing'
  const npm = component.installMode === 'npm'
  const managed = npm && component.updateHint === 'npm'
  return <section className={css.versionGuidance} aria-label={t('versions.npmRecommended')}>
    <strong>{t(managed ? 'versions.npmMaintenance' : npm ? 'versions.npmRepair' : missing ? 'versions.npmInstall' : 'versions.npmMigrate')}</strong>
    <p>{t(managed ? 'versions.npmUpdateDetail' : npm ? 'versions.npmRepairDetail' : missing ? 'versions.npmInstallDetail' : 'versions.npmMigrateDetail')}</p>
    <CommandSnippet command={managed ? 'mnemon update' : 'npm install --global @mnemon-dev/mnemon@latest'} />
    {!managed && <details className={css.versionGuidanceDetails}>
      <summary>{t('versions.npmNextSteps')}</summary>
      <p>{t('versions.npmVerify')}</p><CommandSnippet command="mnemon --version" /><p>{t('versions.npmPath')}</p>
      <a href="https://github.com/mnemon-dev/mnemon#install" target="_blank" rel="noreferrer">{t('versions.installGuide')}</a>
    </details>}
  </section>
}

function dshInstallLabel(t: MnemonTranslate, component: VersionComponentStatus): string {
  if (component.installMode === 'npm') return t('versions.profileLocation', { name: component.installProfile ?? '—' })
  if (component.installMode === 'link') return component.installProfile === undefined
    ? t('versions.sourceLocation')
    : t('versions.linkSourceLocation', { name: component.installProfile })
  return t('versions.packageLocation')
}

export function VersionDialog(props: {
  client: MnemonClient
  writeEnabled: boolean
  /** The Starter update this page started before DSH swapped in this client, which reopened the dialog. */
  resumedUpdate?: StarterUpdateRecord | undefined
  onClose: () => void
  onRefreshStatus: () => void
}): JSX.Element {
  const t = useT()
  const [snapshot, setSnapshot] = useState<VersionStatus | null>(null)
  const [checking, setChecking] = useState(true)
  const [updating, setUpdating] = useState<VersionComponentStatus['id'] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<VersionUpdateResult | null>(null)
  const [packagesOpen, setPackagesOpen] = useState(false)
  const updateInFlight = useRef(false)
  const checkRequestRef = useRef(0)
  const checkTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const check = useCallback(async () => {
    const requestVersion = ++checkRequestRef.current
    setChecking(true)
    setError(null)
    let timeout: ReturnType<typeof setTimeout> | undefined
    try {
      const deadline = new Promise<never>((_resolve, reject) => {
        timeout = setTimeout(() => reject(new Error(t('versions.timeout'))), 15_000)
        checkTimeoutRef.current = timeout
      })
      const next = await Promise.race([props.client.versions(), deadline])
      if (checkRequestRef.current === requestVersion) setSnapshot(next)
    }
    catch (reason) { if (checkRequestRef.current === requestVersion) setError(message(reason)) }
    finally {
      if (timeout !== undefined) clearTimeout(timeout)
      if (checkTimeoutRef.current === timeout) checkTimeoutRef.current = null
      if (checkRequestRef.current === requestVersion) setChecking(false)
    }
  }, [props.client, t])
  useEffect(() => {
    void check()
    return () => {
      checkRequestRef.current += 1
      if (checkTimeoutRef.current !== null) clearTimeout(checkTimeoutRef.current)
      checkTimeoutRef.current = null
    }
  }, [check])
  const update = async (component: VersionComponentStatus) => {
    if (updateInFlight.current || !props.writeEnabled) return
    updateInFlight.current = true
    setUpdating(component.id)
    setError(null)
    setResult(null)
    // DSH swaps in the new client once the Starter's files change; that client reopens this dialog from the record.
    if (component.id === 'dsh-mnemon') recordStarterUpdate({ from: component.current, to: component.latest })
    try {
      const next = await props.client.updateVersion(component.id)
      setResult(next)
      await check()
      props.onRefreshStatus()
    } catch (reason) {
      if (component.id === 'dsh-mnemon') clearStarterUpdate()
      setError(message(reason))
    } finally {
      setUpdating(null)
      updateInFlight.current = false
    }
  }
  const close = () => {
    clearStarterUpdate()
    props.onClose()
  }
  const updatingBusy = updating !== null
  const controlsBusy = checking || updatingBusy
  // The update's own reply went to the client DSH replaced. The Host keeps how it ended; a Host
  // from before it kept that leaves only its pending restart to go by.
  const resumed = props.resumedUpdate
  const starter = snapshot?.components.find(component => component.id === 'dsh-mnemon')
  const outcome = result === null && error === null && resumed !== undefined && snapshot?.lastUpdate?.component === 'dsh-mnemon'
    && Date.parse(snapshot.lastUpdate.at) >= resumed.at - 60_000 ? snapshot.lastUpdate : undefined
  const resumedResult: VersionUpdateResult | null = outcome !== undefined ? outcome.result ?? null
    : result === null && resumed !== undefined && snapshot?.lastUpdate === undefined && starter?.restartRequired === true && (resumed.to === undefined || starter.current === resumed.to)
      ? { component: 'dsh-mnemon', updated: true, restartRequired: true, ...(resumed.from === undefined ? {} : { previousVersion: resumed.from }), ...(starter.current === undefined ? {} : { currentVersion: starter.current }) }
      : null
  const shownResult = result ?? resumedResult
  const shownError = error ?? outcome?.error ?? null
  const updateButton = (component: VersionComponentStatus) => props.writeEnabled && component.outdated && component.updateSupported && component.checkError === undefined
    ? <button type="button" className={css.primaryButton} disabled={controlsBusy} onClick={() => void update(component)}>{updating === component.id ? t('versions.updating') : t('versions.update')}</button>
    : null
  const renderPackage = (component: VersionPackageStatus) => <li key={component.id} className={css.versionPackage}>
    <div className={css.versionPackageHeader}><strong>{component.name}</strong><em data-state={versionState(component)}>{t(`versions.${versionState(component)}`)}</em></div>
    <div className={css.versionPackageMeta}><span>{versionModeLabel(t, component.installMode)}</span><span>{t(component.managedBy === 'starter' ? 'versions.managedStarter' : 'versions.managedProfile')}</span></div>
    <div className={css.versionPackageNumbers}><span>{t('versions.installed')} <code>{component.current ?? '—'}</code></span><span>{t('versions.latest')} <code>{component.latest ?? '—'}</code></span>{component.expectedVersion !== undefined && <span>{t('versions.starterVersion')} <code>{component.expectedVersion}</code></span>}</div>
    <div className={css.versionPackageAction}><p>{versionHint(t, component)}{component.checkError !== undefined && <> {t('versions.latestUnavailable')}</>}</p>{updateButton(component)}</div>
  </li>
  return <SidebarModal title={t('versions.title')} description={t('versions.description')} busy={updatingBusy} contentReady={!checking} onClose={close} footer={<><span className={css.modalFooterMeta}>{snapshot === null ? '' : t('versions.checkedAt', { time: new Date(snapshot.checkedAt).toLocaleTimeString() })}</span><div className={css.modalFooterActions}><button type="button" data-autofocus className={css.secondaryButton} disabled={controlsBusy} onClick={() => void check()}>{checking ? t('versions.checkingShort') : t('versions.recheck')}</button><button type="button" data-dialog-close className={css.secondaryButton} disabled={updatingBusy} onClick={close}>{t('common.close')}</button></div></>}>
    <div className={css.versionDialogBody}>
      {checking && snapshot === null && <div className={css.versionChecking} role="status"><span />{t('versions.checking')}</div>}
      {shownError !== null && <div className={css.versionError} role="alert"><strong>{t('versions.failed')}</strong><p>{shownError}</p></div>}
      {!props.writeEnabled && <p className={css.versionNotice}>{t('versions.readOnly')}</p>}
      {shownResult !== null && <div className={css.versionResult} role="status"><strong>{shownResult.updated ? t('versions.updated', { name: shownResult.component === 'mnemon' ? 'Mnemon CLI' : shownResult.component }) : t('versions.alreadyCurrent')}</strong>{shownResult.restartRequired && <p>{t('versions.restartRequired')}</p>}</div>}
      {snapshot !== null && <div className={css.versionList}>{snapshot.components.map(component => {
        const state = versionState(component)
        const packages = component.packages ?? []
        const outdated = packages.filter(item => item.outdated).length
        return <article key={component.id} data-outdated={component.outdated || undefined}>
          <header><div><strong>{component.name}</strong><span>{versionModeLabel(t, component.installMode, component.id === 'mnemon')}</span></div><em data-state={state}>{t(`versions.${state}`)}</em></header>
          <div className={css.versionNumbers}><div><small>{t('versions.installed')}</small><code>{component.current ?? '—'}</code></div><span>→</span><div><small>{t('versions.latest')}</small><code>{component.latest ?? '—'}</code></div></div>
          {component.id === 'mnemon' && component.executablePath !== undefined && <small className={css.versionLocation} title={component.executablePath}><span>{t('versions.executable')}</span><code>{component.executablePath}</code></small>}
          {component.id === 'dsh-mnemon' && component.installPath !== undefined && <small className={css.versionLocation} title={component.installPath}><span>{dshInstallLabel(t, component)}</span><code>{component.installPath}</code></small>}
          {component.restartRequired && <p className={css.versionNotice} role="status">{t('versions.restartRequired')}</p>}
          {component.checkError !== undefined && <p className={css.versionNotice}>{t('versions.latestUnavailable')}</p>}
          <footer><p>{versionHint(t, component)}</p>{updateButton(component)}</footer>
          {component.id === 'mnemon' && <NpmGuidance component={component} />}
          {component.id === 'dsh-mnemon' && packages.length > 0 && <div className={css.versionPackages}>
            <button type="button" className={css.versionPackagesToggle} aria-expanded={packagesOpen} aria-controls="mnemon-version-packages" onClick={() => setPackagesOpen(open => !open)}>
              <span aria-hidden="true">{packagesOpen ? '▾' : '▸'}</span><strong>{t('versions.packages', { count: packages.length })}</strong><small>{outdated > 0 ? t('versions.packagesOutdated', { count: outdated }) : t(packagesOpen ? 'versions.packagesHide' : 'versions.packagesDetail')}</small>
            </button>
            {packagesOpen && <div id="mnemon-version-packages">
              <p className={css.versionNotice}>{t('versions.packagesHint')}</p>
              {(['source', 'strategy', 'provider'] as const).map(kind => {
                const members = packages.filter(item => item.kind === kind)
                return members.length === 0 ? null : <section key={kind} aria-label={t(`versions.kind.${kind}`)}><h4>{t(`versions.kind.${kind}`)}</h4><ul>{members.map(renderPackage)}</ul></section>
              })}
            </div>}
          </div>}
        </article>
      })}</div>}
    </div>
  </SidebarModal>
}
