// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PluginPackageRef, PluginsSubject } from '@deepseek-ai/dsh-client-ui-plugin-manager/client'
import type { StandardSourceBinding } from '@deepseek-ai/dsh-client-ui-slots'

vi.mock('../src/client/MnemonSettingsCard.tsx', () => ({
  MnemonSettingsCard: ({ language }: { language?: string }) => <section aria-label="configuration" data-language={language} />,
}))

import { MnemonActionSeat } from '../src/client/action-seat.ts'
import { MnemonPluginActions } from '../src/client/MnemonPluginActions.tsx'
import { MnemonSettingsHost } from '../src/client/MnemonSettingsHost.tsx'
import { translateEn } from '../src/client/locales.ts'
import type { Config } from '../src/host/protocol.ts'
import { settingsScope } from './helpers/settings-scope.ts'

afterEach(cleanup)

function store<T>(value: T) {
  return { getSnapshot: () => value, subscribe: () => () => {} }
}

const pkg: PluginPackageRef = { name: 'dsh-mnemon', installed: true, enabled: true, rows: [{ rowId: 'mnemon', moduleName: 'dsh-mnemon', enabled: true }] }
const bundle = (overrides: Partial<PluginPackageRef> = {}): PluginsSubject => ({ kind: 'bundle', pkg: { ...pkg, ...overrides } })

describe('dsh-mnemon page under DSH Plugins', () => {
  it('renders the configuration only for the page view the Plugins page asks for', () => {
    const props = {
      scope: settingsScope<Config>({ status: 'ready', value: {}, writable: true, mode: 'host' }),
      currentSession: store<StandardSourceBinding>({ key: undefined, hooks: {}, keyedHooks: {}, props: {} }),
      sessions: { list: store({ byId: {} }) } as never,
      workspaces: { list: store({ items: [] }) } as never,
      localeRuntime: { ...store({ active: 'en', locales: [], revision: 0 }) } as never,
      t: translateEn,
    }
    const { rerender } = render(<MnemonSettingsHost {...props} view="summary" />)
    expect(screen.queryByRole('region', { name: 'configuration' })).toBeNull()
    rerender(<MnemonSettingsHost {...props} view="page" />)
    expect(screen.getByRole('region', { name: 'configuration' }).dataset.language).toBe('en')
  })

  it('offers the memory workspace from the dsh-mnemon page only while a placement can show it', () => {
    const workspace = new MnemonActionSeat()
    const { rerender } = render(<MnemonPluginActions subject={bundle()} workspace={workspace} t={translateEn} />)
    expect(screen.queryByRole('button')).toBeNull()

    const open = vi.fn()
    let withdraw = (): void => {}
    act(() => { withdraw = workspace.provide(open) })
    fireEvent.click(screen.getByRole('button', { name: 'Open Memory System' }))
    expect(open).toHaveBeenCalledOnce()

    for (const subject of [
      bundle({ enabled: false }),
      bundle({ name: 'dsh-mnemon-strategy-general' }),
      { kind: 'row', pkg, row: pkg.rows[0]! },
      { kind: 'item', id: 'web-search' },
    ] satisfies PluginsSubject[]) {
      rerender(<MnemonPluginActions subject={subject} workspace={workspace} t={translateEn} />)
      expect(screen.queryByRole('button')).toBeNull()
    }

    rerender(<MnemonPluginActions subject={bundle()} workspace={workspace} t={translateEn} />)
    act(() => withdraw())
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('keeps the newest offer when an older owner withdraws late', () => {
    const seat = new MnemonActionSeat()
    const listener = vi.fn()
    seat.subscribe(listener)
    const first = vi.fn()
    const second = vi.fn()
    const withdrawFirst = seat.provide(first)
    const withdrawSecond = seat.provide(second)
    withdrawFirst()
    expect(seat.getSnapshot()).toBe(second)
    withdrawSecond()
    expect(seat.getSnapshot()).toBeUndefined()
    expect(listener).toHaveBeenCalledTimes(3)
  })
})
