// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { MnemonClient } from '../src/client/api.ts'
import { I18nContext } from '../src/client/page-kit.tsx'
import { translateEn } from '../src/client/locales.ts'
import { VersionDialog } from '../src/client/VersionDialog.tsx'
import { MnemonActionSeat } from '../src/client/action-seat.ts'
import { clearStarterUpdate, pendingStarterUpdate, recordStarterUpdate, reopenAfterStarterUpdate, type StarterUpdateRecord } from '../src/client/starter-update.ts'
import type { VersionComponentStatus, VersionPackageStatus, VersionStatus, VersionUpdateResult } from '../src/host/protocol.ts'

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); clearStarterUpdate() })

const cli = (overrides: Partial<VersionComponentStatus> = {}): VersionComponentStatus => ({
  id: 'mnemon', name: 'Mnemon CLI', current: '0.2.8', latest: '0.2.9', installMode: 'npm', outdated: true, updateSupported: true, updateHint: 'npm', ...overrides,
})
const child = (overrides: Partial<VersionPackageStatus> = {}): VersionPackageStatus => ({
  id: 'dsh-mnemon-source-runtime', name: 'dsh-mnemon-source-runtime', kind: 'source', managedBy: 'profile',
  current: '0.5.2', expectedVersion: '0.5.2', latest: '0.5.3', outdated: true, installMode: 'npm', updateSupported: true, updateHint: 'pnpm', ...overrides,
})
const starter = (packages: VersionPackageStatus[]): VersionComponentStatus => ({
  id: 'dsh-mnemon', name: 'dsh-mnemon', current: '0.5.2', latest: '0.5.2', outdated: false, installMode: 'npm', updateSupported: true, updateHint: 'pnpm', packages,
})

function fixture(components: VersionComponentStatus[], options: { writable?: boolean; english?: boolean; resumedUpdate?: StarterUpdateRecord; lastUpdate?: VersionStatus['lastUpdate'] } = {}) {
  const snapshot: VersionStatus = { checkedAt: '2026-09-06T08:00:00Z', components, ...(options.lastUpdate === undefined ? {} : { lastUpdate: options.lastUpdate }) }
  const versions = vi.fn(async () => snapshot)
  const updateVersion = vi.fn(async (component: VersionComponentStatus['id']): Promise<VersionUpdateResult> => ({ component, updated: true, restartRequired: component !== 'mnemon' }))
  const onRefreshStatus = vi.fn()
  const onClose = vi.fn()
  const view = <VersionDialog client={{ versions, updateVersion } as unknown as MnemonClient} writeEnabled={options.writable ?? true} resumedUpdate={options.resumedUpdate} onClose={onClose} onRefreshStatus={onRefreshStatus} />
  render(options.english ? <I18nContext.Provider value={translateEn}>{view}</I18nContext.Provider> : view)
  return { snapshot, versions, updateVersion, onRefreshStatus, onClose }
}

describe('version maintenance', () => {
  it('keeps npm installation guidance usable when the CLI is missing and the registry is offline', async () => {
    const value = cli({ installMode: 'missing', updateSupported: false, outdated: false, updateHint: 'install', checkError: 'latest-unavailable' })
    delete value.current
    delete value.latest
    const f = fixture([value])
    expect(await screen.findByText('未安装（可选）')).toBeTruthy()
    expect(screen.queryByText('已是最新')).toBeNull()
    expect(screen.getByText('通过 npm 安装（推荐）')).toBeTruthy()
    expect(screen.getByText('npm install --global @mnemon-dev/mnemon@latest')).toBeTruthy()
    fireEvent.click(screen.getByText('安装后：验证版本与生效路径'))
    expect(screen.getByText(/MNEMON_CLI_PATH 或 mnemon.cliPath/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: '更新' })).toBeNull()
    expect(f.updateVersion).not.toHaveBeenCalled()
  })

  it('does not label an unreadable CLI or a newer local build as up to date', async () => {
    const broken = cli({ updateSupported: false, updateHint: 'cli-unreadable' })
    delete broken.current
    fixture([broken, { ...starter([]), current: '0.6.0-beta.1' }])
    expect(await screen.findByText('无法确认')).toBeTruthy()
    expect(screen.getByText('本地版本')).toBeTruthy()
    expect(screen.getByText('检查或修复 npm 安装')).toBeTruthy()
    expect(screen.queryByText('已是最新')).toBeNull()
  })

  it('offers npm migration guidance alongside the existing Homebrew update', async () => {
    fixture([cli({ installMode: 'homebrew', updateHint: 'brew' })])
    expect(await screen.findByText('改用 npm 维护（推荐）')).toBeTruthy()
    expect(screen.getByRole('button', { name: '更新' })).toBeTruthy()
    expect(screen.getByText(/由 Homebrew 管理/)).toBeTruthy()
  })

  it('copies the recommended command and handles unavailable clipboard access', async () => {
    const writeText = vi.fn(async () => {})
    vi.stubGlobal('navigator', Object.create(navigator, { clipboard: { value: { writeText } } }))
    fixture([cli()])
    fireEvent.click(await screen.findByRole('button', { name: '复制命令：mnemon update' }))
    expect(await screen.findByText('已复制')).toBeTruthy()
    expect(writeText).toHaveBeenCalledWith('mnemon update')
    writeText.mockRejectedValue(new Error('clipboard unavailable'))
    fireEvent.click(screen.getByRole('button', { name: '复制命令：mnemon update' }))
    expect(await screen.findByText('无法访问剪贴板，请选中命令手动复制。')).toBeTruthy()
  })

  it('copies through the DSH execCommand fallback when the Clipboard API is absent', async () => {
    const execCommand = vi.fn(() => true)
    Object.defineProperty(document, 'execCommand', { value: execCommand, configurable: true })
    try {
      fixture([cli()])
      fireEvent.click(await screen.findByRole('button', { name: '复制命令：mnemon update' }))
      expect(await screen.findByText('已复制')).toBeTruthy()
      expect(execCommand).toHaveBeenCalledWith('copy')
    } finally {
      Reflect.deleteProperty(document, 'execCommand')
    }
  })

  it('starts collapsed, groups packages, updates the selected package and keeps its restart state after recheck', async () => {
    const provider = child({ id: 'dsh-mnemon-provider-mnemon-native', name: 'dsh-mnemon-provider-mnemon-native', kind: 'provider', managedBy: 'starter', updateSupported: false, updateHint: 'starter' })
    const direct = child()
    const f = fixture([starter([direct, provider])])
    const toggle = await screen.findByRole('button', { name: /子包版本（2）/ })
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(screen.queryByText(direct.name)).toBeNull()
    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(screen.getByRole('region', { name: 'Providers · 后端' })).toBeTruthy()
    expect(screen.getByText('随主包维护')).toBeTruthy()
    const sources = screen.getByRole('region', { name: 'Sources · 记忆源' })
    f.updateVersion.mockImplementation(async component => {
      direct.current = '0.5.3'; direct.outdated = false; direct.restartRequired = true
      f.snapshot.components[0]!.restartRequired = true
      return { component, currentVersion: '0.5.3', updated: true, restartRequired: true }
    })
    fireEvent.click(within(sources).getByRole('button', { name: '更新' }))
    expect(await screen.findByText('dsh-mnemon-source-runtime 已更新')).toBeTruthy()
    expect(f.updateVersion).toHaveBeenCalledWith(direct.id)
    await waitFor(() => expect(f.onRefreshStatus).toHaveBeenCalledOnce())
    expect(screen.getAllByText('待重启').length).toBeGreaterThan(0)
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    fireEvent.click(screen.getByRole('button', { name: '重新检查' }))
    await waitFor(() => expect(f.versions).toHaveBeenCalledTimes(3))
    expect(within(sources).queryByRole('button', { name: '更新' })).toBeNull()
  })

  it('leaves checks and commands available to read-only users without exposing package updates', async () => {
    const f = fixture([cli(), starter([child()])], { writable: false, english: true })
    fireEvent.click(await screen.findByRole('button', { name: /Subpackage versions/ }))
    expect(screen.getByText(/This connection is read-only/)).toBeTruthy()
    expect(screen.getByText('npm')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Copy command: mnemon update' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Update' })).toBeNull()
    expect(f.updateVersion).not.toHaveBeenCalled()
  })

  it('updates the Starter through DSH where no pnpm is on PATH and asks for a DSH restart', async () => {
    const f = fixture([{ ...starter([]), latest: '0.5.3', outdated: true, updateHint: 'dsh' }])
    expect(await screen.findByText('由当前 DSH Profile 管理；用 DSH 自己的插件安装方式更新，完成后重启 DSH。')).toBeTruthy()
    expect(screen.queryByText(/找不到 pnpm/u)).toBeNull()
    fireEvent.click(await screen.findByRole('button', { name: '更新' }))
    await waitFor(() => expect(f.updateVersion).toHaveBeenCalledWith('dsh-mnemon'))
    expect((await screen.findAllByText('请重启 DSH 以加载新的 dsh-mnemon 插件代码；桌面版请完全退出后重新打开。')).length).toBeGreaterThan(0)
  })

  it('shows update failures, restores controls, and prevents duplicate clicks while updating', async () => {
    const f = fixture([cli()])
    let reject!: (reason: Error) => void
    f.updateVersion.mockImplementation(async () => await new Promise((_resolve, rejectUpdate) => { reject = rejectUpdate }))
    const update = await screen.findByRole('button', { name: '更新' })
    fireEvent.click(update)
    fireEvent.click(update)
    expect(f.updateVersion).toHaveBeenCalledOnce()
    expect(screen.getByRole('button', { name: '重新检查' }).hasAttribute('disabled')).toBe(true)
    await act(async () => { reject(new Error('npm permission denied')) })
    expect(screen.getByRole('alert').textContent).toContain('npm permission denied')
    expect(screen.getByRole('button', { name: '更新' }).hasAttribute('disabled')).toBe(false)
  })
})

describe('a Starter update across DSH\'s client swap', () => {
  const restartNotice = '请重启 DSH 以加载新的 dsh-mnemon 插件代码；桌面版请完全退出后重新打开。'
  const outdatedStarter = (): VersionComponentStatus => ({ ...starter([]), latest: '0.5.3', outdated: true, updateHint: 'dsh' })

  it('records the update it starts, keeps the record for the swapped-in client, and forgets it once closed', async () => {
    const f = fixture([outdatedStarter()])
    let finish!: (result: VersionUpdateResult) => void
    f.updateVersion.mockImplementation(async () => await new Promise(resolve => { finish = resolve }))
    fireEvent.click(await screen.findByRole('button', { name: '更新' }))
    expect(pendingStarterUpdate()).toMatchObject({ from: '0.5.2', to: '0.5.3' })
    await act(async () => { finish({ component: 'dsh-mnemon', previousVersion: '0.5.2', currentVersion: '0.5.3', updated: true, restartRequired: true }) })
    expect(await screen.findByText('dsh-mnemon 已更新')).toBeTruthy()
    // DSH may swap the client in after this reply; the record lets that client reopen the dialog.
    expect(pendingStarterUpdate()).toMatchObject({ to: '0.5.3' })
    fireEvent.click(screen.getAllByRole('button', { name: '关闭' }).at(-1)!)
    expect(f.onClose).toHaveBeenCalledOnce()
    expect(pendingStarterUpdate()).toBeUndefined()
  })

  it('forgets the record when the update fails, and never records a CLI update', async () => {
    const f = fixture([cli(), outdatedStarter()])
    f.updateVersion.mockRejectedValueOnce(new Error('DSH could not install dsh-mnemon@0.5.3: operation-error'))
    fireEvent.click((await screen.findAllByRole('button', { name: '更新' }))[1]!)
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(pendingStarterUpdate()).toBeUndefined()
    fireEvent.click(screen.getAllByRole('button', { name: '更新' })[0]!)
    await waitFor(() => expect(f.updateVersion).toHaveBeenCalledWith('mnemon'))
    expect(pendingStarterUpdate()).toBeUndefined()
  })

  it('reopened by the swapped-in client, shows the outcome the Host reports without updating again', async () => {
    recordStarterUpdate({ from: '0.5.2', to: '0.5.3' })
    const f = fixture([{ ...starter([]), current: '0.5.3', latest: '0.5.3', restartRequired: true, updateHint: 'dsh' }], { resumedUpdate: pendingStarterUpdate()! })
    expect(await screen.findByText('dsh-mnemon 已更新')).toBeTruthy()
    expect(screen.getAllByText(restartNotice).length).toBeGreaterThan(0)
    expect(screen.queryByRole('button', { name: '更新' })).toBeNull()
    expect(f.updateVersion).not.toHaveBeenCalled()
  })

  it('shows the update the Host reports it finished', async () => {
    recordStarterUpdate({ from: '0.5.2', to: '0.5.3' })
    const lastUpdate = { at: new Date().toISOString(), component: 'dsh-mnemon' as const, result: { component: 'dsh-mnemon' as const, updated: true, previousVersion: '0.5.2', currentVersion: '0.5.3', restartRequired: true } }
    const f = fixture([{ ...starter([]), current: '0.5.3', latest: '0.5.3', restartRequired: true, updateHint: 'dsh' }], { resumedUpdate: pendingStarterUpdate()!, lastUpdate })
    expect(await screen.findByText('dsh-mnemon 已更新')).toBeTruthy()
    expect(screen.getAllByText(restartNotice).length).toBeGreaterThan(0)
    expect(f.updateVersion).not.toHaveBeenCalled()
  })

  it('shows the failure the Host reports, even when files the install left suggest otherwise', async () => {
    recordStarterUpdate({ from: '0.5.2', to: '0.5.3' })
    const reason = 'DSH could not install dsh-mnemon@0.5.3: incompatible-version (requires @deepseek-ai/dsh ^0.3.0; this DSH is 0.2.0-rc.2)'
    fixture([outdatedStarter()], { resumedUpdate: pendingStarterUpdate()!, lastUpdate: { at: new Date().toISOString(), component: 'dsh-mnemon', error: reason } })
    expect(await screen.findByText(reason)).toBeTruthy()
    expect(screen.getByText('版本操作失败')).toBeTruthy()
    expect(screen.queryByText('dsh-mnemon 已更新')).toBeNull()
  })

  it('ignores an outcome from before the update this page started', async () => {
    recordStarterUpdate({ from: '0.5.2', to: '0.5.3' })
    const earlier = new Date(Date.now() - 10 * 60_000).toISOString()
    fixture([outdatedStarter()], { resumedUpdate: pendingStarterUpdate()!, lastUpdate: { at: earlier, component: 'dsh-mnemon', error: 'an older failure' } })
    expect(await screen.findByRole('button', { name: '更新' })).toBeTruthy()
    expect(screen.queryByText('an older failure')).toBeNull()
  })

  it('claims no update the Host does not report', async () => {
    recordStarterUpdate({ from: '0.5.2', to: '0.5.3' })
    // The update failed after DSH swapped the client in: nothing waits for a restart.
    fixture([outdatedStarter()], { resumedUpdate: pendingStarterUpdate()! })
    expect(await screen.findByRole('button', { name: '更新' })).toBeTruthy()
    expect(screen.queryByText('dsh-mnemon 已更新')).toBeNull()
  })

  it('reopens the Memory System once it can open, only for a fresh record', () => {
    const seat = new MnemonActionSeat()
    const open = vi.fn()
    reopenAfterStarterUpdate(seat)()
    seat.provide(open)()
    expect(open).not.toHaveBeenCalled()

    recordStarterUpdate({ to: '0.5.3' })
    const stop = reopenAfterStarterUpdate(seat)
    expect(open).not.toHaveBeenCalled()
    const withdraw = seat.provide(open)
    expect(open).toHaveBeenCalledOnce()
    withdraw()
    seat.provide(open)
    expect(open).toHaveBeenCalledOnce()
    stop()

    recordStarterUpdate({ to: '0.5.3' }, Date.now() - 11 * 60_000)
    reopenAfterStarterUpdate(new MnemonActionSeat())
    expect(pendingStarterUpdate()).toBeUndefined()
  })
})
