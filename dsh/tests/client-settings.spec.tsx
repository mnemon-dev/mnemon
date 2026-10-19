// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MnemonSettingsCard } from '../src/client/MnemonSettingsCard.tsx'
import { MEMORY_SPACES_PACKAGE, MemorySpacesSettings, RUNTIME_PACKAGE, RuntimeSettings, THREE_TIER_PACKAGE, ThreeTierSettings } from '../src/client/component-settings.tsx'
import { translateEn, translateZh, type MnemonTranslate } from '../src/client/locales.ts'
import type { ClientConnectionHandle, ClientSettingsScope } from "../src/host/dsh.ts"
import type { Config } from "../src/host/config.ts"
import type { MemoryCompositionStatus, MemoryPluginEntryView, MemoryViewConfigurationRequest, MemoryViewDashboard } from "../src/host/protocol.ts"
import { TEST_PROVIDERS as MEMORY_PROVIDER_CATALOG } from './fixtures/providers.ts'
import { liveSettingsScope, settingsScope } from './helpers/settings-scope.ts'

afterEach(cleanup)

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(next => { resolve = next })
  return { promise, resolve }
}

const pattern = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&')

/** A DSH settings selector; its accessible name is the row title followed by the chosen value. */
function selector(title: string, root: HTMLElement = document.body): HTMLButtonElement {
  return within(root).getByRole('button', { name: new RegExp(`^${pattern(title)} `, 'u') }) as HTMLButtonElement
}

/** Open a selector and pick the option whose label starts with `option`. */
function choose(title: string, option: string, root: HTMLElement = document.body): void {
  fireEvent.click(selector(title, root))
  fireEvent.click(screen.getByRole('menuitem', { name: new RegExp(`^${pattern(option)}`, 'u') }))
}

const checked = (element: HTMLElement) => element.getAttribute('aria-checked') === 'true'

/** A component page's props, as the configuration hands them to the settings a component contributed. */
function page(packageName: string, options: { enabled?: boolean; writable?: boolean; language?: string; sessionId?: string; workspaceId?: string; workspaceLabel?: string } = {}) {
  return {
    component: { packageName, label: packageName, enabled: options.enabled ?? true }, writable: options.writable ?? true, language: options.language ?? 'zh',
    ...(options.sessionId === undefined ? {} : { sessionId: options.sessionId }),
    ...(options.workspaceId === undefined ? {} : { workspace: { id: options.workspaceId, ...(options.workspaceLabel === undefined ? {} : { label: options.workspaceLabel }) } }),
  }
}
type PageOptions = Parameters<typeof page>[1]
/** Memory Spaces' own settings: its Providers and the Native embedding runtime. */
const spaces = (scope: ClientSettingsScope<Config>, connection?: ClientConnectionHandle, options: PageOptions = {}, t: MnemonTranslate = translateZh) =>
  <MemorySpacesSettings scope={scope} {...(connection === undefined ? {} : { connection })} t={t} page={page(MEMORY_SPACES_PACKAGE, options)} />
/** The Layered strategy's own settings: the background tasks it drives. */
const threeTier = (scope: ClientSettingsScope<Config>, connection?: ClientConnectionHandle, options: PageOptions = {}, t: MnemonTranslate = translateZh) =>
  <ThreeTierSettings scope={scope} {...(connection === undefined ? {} : { connection })} t={t} page={page(THREE_TIER_PACKAGE, options)} />
/** Runtime Memory's own setting: where its user profile lives. */
const runtime = (scope: ClientSettingsScope<Config>, options: PageOptions = {}, t: MnemonTranslate = translateZh) =>
  <RuntimeSettings scope={scope} t={t} page={page(RUNTIME_PACKAGE, options)} />
/** A group's Apply, shown once something in it changed. */
const apply = (name = '应用') => screen.getByRole('button', { name }) as HTMLButtonElement
/** The data directory's default or custom choice. */
const directoryChoice = (option: string, group = '数据目录') => within(screen.getByRole('radiogroup', { name: group })).getByRole('radio', { name: option }) as HTMLInputElement
/** A selector found by its row id, where row titles share a prefix. */
const rowSelector = (id: string) => document.querySelector<HTMLButtonElement>(`[aria-labelledby^="${id}-title "]`)!
function chooseIn(id: string, option: string): void {
  fireEvent.click(rowSelector(id))
  fireEvent.click(screen.getAllByRole('menuitem').find(item => item.textContent?.startsWith(option) || item.textContent?.includes(option))!)
}

describe('MnemonSettingsCard', () => {
  it.each([false, true])('submits the OpenViking user-key scope to its owning instance and reports rejection=%s', async rejected => {
    const snapshot = { status: 'ready' as const, value: {}, base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const }
    const scope = settingsScope(snapshot)
    const descriptor = MEMORY_PROVIDER_CATALOG.find(candidate => candidate.id === 'openviking')!
    const providers = ['work-cloud', 'personal-cloud'].map(id => ({ ...descriptor, id, typeId: 'openviking', label: id }))
    const call = vi.fn(async (channel: string, endpoint: string, payload: unknown) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'provider-services') return { ok: true as const, value: {
        providers, items: providers.map(provider => ({ providerId: provider.id, enabled: true, configured: true, settings: { endpoint: 'https://memory.example/openviking', account: 'work' }, configuredSecrets: ['apiKey'] })), generatedAt: '2026-09-25T00:00:00.000Z',
      } }
      if (channel === '/dsh-mnemon-write' && endpoint === 'provider-service-update') {
        if (rejected) throw new Error('Synthetic user key cannot access this memory namespace')
        const request = payload as { providerId: string; settings: Record<string, string> }
        return { ok: true as const, value: { ...request, enabled: true, configured: true, configuredSecrets: ['apiKey'] } }
      }
      if (channel === '/dsh-mnemon-pack' && endpoint === 'target') return { ok: true as const, value: { root: '/fixture/.mnemon', scope: 'global' } }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })
    render(spaces(scope, { rpc: { call }, isLoopback: true } as ClientConnectionHandle))
    const card = await screen.findByRole('group', { name: 'work-cloud 服务配置' })
    await waitFor(() => expect((within(card).getByRole('button') as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(within(card).getByRole('button'))
    const user = within(card).getByRole('textbox', { name: 'User Key 所属用户（跳过 Admin）' })
    fireEvent.change(user, { target: { value: rejected ? 'bob' : 'alice' } })
    fireEvent.click(within(card).getByRole('button', { name: '保存服务配置' }))
    await waitFor(() => expect(call).toHaveBeenCalledWith('/dsh-mnemon-write', 'provider-service-update', {
      providerId: 'work-cloud', enabled: true, settings: { endpoint: 'https://memory.example/openviking', account: 'work', discoveryUser: rejected ? 'bob' : 'alice' },
    }))
    await within(card).findByText(rejected ? /Synthetic user key cannot access/ : '服务配置已保存，记忆空间目录已同步')
    expect(call.mock.calls.filter(([, endpoint]) => endpoint === 'provider-service-update')).toHaveLength(1)
    expect(within(screen.getByRole('group', { name: 'personal-cloud 服务配置' })).queryByRole('textbox')).toBeNull()
  })

  it('applies review choices at once and waits for Apply on the typed limits', async () => {
    const mutate = vi.fn(async () => {})
    const scope = liveSettingsScope<Config>({ status: 'ready' as const, value: {}, base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const }, mutate)
    render(threeTier(scope))
    // A choice applies when made, writing the idle review with only what changed.
    choose('Agent Teams 兼容模式', '受限子代理审查')
    await waitFor(() => expect(mutate).toHaveBeenCalledWith([{ op: 'set', path: ['idleReview'], value: { agentTeams: 'scoped' } }]))
    expect(selector('Agent Teams 兼容模式').textContent).toBe('受限子代理审查')
    // The limits are typed: Apply appears once one changes and refuses what the Host would.
    expect(screen.queryByRole('button', { name: '应用' })).toBeNull()
    expect((screen.getByRole('spinbutton', { name: '最小审查间隔（秒）' }) as HTMLInputElement).value).toBe('300')
    fireEvent.change(screen.getByRole('spinbutton', { name: '每会话最多尝试次数' }), { target: { value: '-1' } })
    expect(screen.getByRole('alert').textContent).toBe('每会话最多尝试次数需在 0–200 之间')
    expect(apply().disabled).toBe(true)
    fireEvent.change(screen.getByRole('spinbutton', { name: '每会话最多尝试次数' }), { target: { value: '3' } })
    fireEvent.change(screen.getByRole('spinbutton', { name: '最小审查间隔（秒）' }), { target: { value: '120' } })
    fireEvent.click(apply())
    await waitFor(() => expect(mutate).toHaveBeenLastCalledWith([{ op: 'set', path: ['idleReview'], value: { agentTeams: 'scoped', maxPerSession: 3, minIntervalMs: 120_000 } }]))
    await waitFor(() => expect(screen.queryByRole('button', { name: '应用' })).toBeNull())
    // Turning review off applies at once and hides what it controls.
    fireEvent.click(screen.getByRole('switch', { name: '启用空闲审查' }))
    await waitFor(() => expect(mutate).toHaveBeenLastCalledWith([{ op: 'set', path: ['idleReview'], value: { agentTeams: 'scoped', maxPerSession: 3, minIntervalMs: 120_000, enabled: false } }]))
    expect(screen.queryByRole('button', { name: /^Agent Teams 兼容模式 / })).toBeNull()
  })

  it('switches runtime memory writes for idle review at once (#319)', async () => {
    const mutate = vi.fn(async () => {})
    const scope = liveSettingsScope<Config>({ status: 'ready' as const, value: {}, base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const }, mutate)
    render(threeTier(scope))
    const runtime = screen.getByRole('switch', { name: '写入运行时记忆' })
    expect(checked(runtime)).toBe(true)
    expect(screen.getByText('只记你明确说过的偏好和规则；关闭后只建项目档案')).toBeTruthy()
    fireEvent.click(runtime)
    await waitFor(() => expect(mutate).toHaveBeenCalledWith([{ op: 'set', path: ['idleReview'], value: { runtimeMemory: false } }]))
    expect(checked(screen.getByRole('switch', { name: '写入运行时记忆' }))).toBe(false)
    // The switch belongs to review: turning review off hides it.
    fireEvent.click(screen.getByRole('switch', { name: '启用空闲审查' }))
    await waitFor(() => expect(screen.queryByRole('switch', { name: '写入运行时记忆' })).toBeNull())
  })

  it('keeps Team review compatibility selection read-only without the Host settings grant', () => {
    const snapshot = { status: 'ready' as const, value: { idleReview: { agentTeams: 'scoped' as const } }, revision: 0, writable: false, mode: 'host' as const }
    const scope = settingsScope(snapshot)
    render(threeTier(scope, undefined, { language: 'en' }, translateEn))
    const choice = selector('Agent Teams compatibility')
    expect(choice.textContent).toBe('Scoped child review')
    expect(choice.disabled).toBe(true)
    expect(scope.mutate).not.toHaveBeenCalled()
  })
  it('applies DSH management of the embedding at once, and the typed connection from its Apply', async () => {
    const mutate = vi.fn(async () => {})
    const scope = liveSettingsScope<Config>({ status: 'ready' as const, value: { storageScope: 'global' as const }, base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const }, mutate)

    render(spaces(scope))

    const managed = screen.getByRole('switch', { name: '由 DSH 管理嵌入配置' })
    expect(checked(managed)).toBe(false)
    // While Mnemon keeps its own embedding, the connection stays out of the way.
    expect(screen.queryByRole('textbox', { name: '嵌入 Endpoint' })).toBeNull()
    fireEvent.click(managed)
    await waitFor(() => expect(mutate).toHaveBeenCalledWith([{
      op: 'set', path: ['embedding'], value: { enabled: true, endpoint: 'http://localhost:11434', model: 'nomic-embed-text', apiKey: '', protocol: 'auto' },
    }]))
    const endpoint = await screen.findByRole('textbox', { name: '嵌入 Endpoint' }) as HTMLInputElement
    const model = screen.getByRole('textbox', { name: '嵌入模型' }) as HTMLInputElement
    const apiKey = screen.getByLabelText('API Key（可选，OpenAI 兼容服务）') as HTMLInputElement
    const protocol = screen.getByRole('button', { name: /^协议/ })
    expect(checked(screen.getByRole('switch', { name: '由 DSH 管理嵌入配置' }))).toBe(true)
    expect(endpoint.value).toBe('http://localhost:11434')
    expect(model.value).toBe('nomic-embed-text')
    expect(apiKey.value).toBe('')
    expect(apiKey.type).toBe('password')
    expect(protocol.textContent).toBe('自动（按 /v1 探测）')
    expect(screen.queryByRole('button', { name: '应用' })).toBeNull()

    fireEvent.change(endpoint, { target: { value: 'ftp://invalid.example' } })
    expect(screen.getByRole('alert').textContent).toContain('HTTP(S)')
    expect(apply().disabled).toBe(true)

    fireEvent.change(endpoint, { target: { value: 'http://127.0.0.1:11434?' } })
    expect(apply().disabled).toBe(true)

    fireEvent.change(endpoint, { target: { value: ' http://127.0.0.1:8080/api/// ' } })
    fireEvent.change(model, { target: { value: ' qwen3-embedding:0.6b ' } })
    fireEvent.change(apiKey, { target: { value: '  sk-secret  ' } })
    fireEvent.click(protocol)
    fireEvent.click(screen.getByRole('menuitem', { name: 'OpenAI 兼容' }))
    fireEvent.click(apply())

    await waitFor(() => expect(mutate).toHaveBeenLastCalledWith([{
      op: 'set',
      path: ['embedding'],
      value: { enabled: true, endpoint: 'http://127.0.0.1:8080/api', model: 'qwen3-embedding:0.6b', apiKey: 'sk-secret', protocol: 'openai' },
    }]))
  })

  it('turns DSH management off at once with the saved connection, dropping unapplied edits', async () => {
    const mutate = vi.fn(async () => {})
    const snapshot = {
      status: 'ready' as const,
      value: {
        storageScope: 'global' as const,
        embedding: { enabled: true, endpoint: 'http://127.0.0.1:11434', model: 'qwen3-embedding:0.6b' },
      },
      base: {}, user: {}, revision: 1, writable: true, mode: 'host' as const,
    }
    const scope = liveSettingsScope<Config>(snapshot, mutate)

    render(spaces(scope))
    fireEvent.change(screen.getByRole('textbox', { name: '嵌入 Endpoint' }), { target: { value: 'ftp://invalid.example' } })
    fireEvent.click(screen.getByRole('switch', { name: '由 DSH 管理嵌入配置' }))

    await waitFor(() => expect(mutate).toHaveBeenCalledWith([{
      op: 'set',
      path: ['embedding'],
      value: { enabled: false, endpoint: 'http://127.0.0.1:11434', model: 'qwen3-embedding:0.6b', apiKey: '', protocol: 'auto' },
    }]))
    expect(screen.queryByRole('textbox', { name: '嵌入 Endpoint' })).toBeNull()
    expect(screen.queryByRole('button', { name: '应用' })).toBeNull()
  })

  it('tests the saved embedding runtime through Mnemon and reports connection plus coverage', async () => {
    const snapshot = {
      status: 'ready' as const,
      value: {
        storageScope: 'global' as const,
        embedding: { enabled: true, endpoint: 'http://127.0.0.1:11434', model: 'qwen3-embedding:0.6b' },
      },
      base: {}, user: {}, revision: 1, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)
    const call = vi.fn(async (channel: string, endpoint: string) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'embedding-status') return {
        ok: true as const,
        value: { available: true, model: 'qwen3-embedding:0.6b', totalInsights: 8, embedded: 6, coverage: '75%' },
      }
      if (channel === '/dsh-mnemon-read' && endpoint === 'task-agent-models') return { ok: true as const, value: { groups: [], failures: [] } }
      if (channel === '/dsh-mnemon-read' && endpoint === 'provider-services') return { ok: true as const, value: { providers: [], items: [], generatedAt: '' } }
      if (channel === '/dsh-mnemon-pack' && endpoint === 'target') return { ok: true as const, value: { root: '/root/.mnemon', scope: 'global' as const } }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })

    render(spaces(scope, { rpc: { call }, isLoopback: true } as ClientConnectionHandle))
    fireEvent.click(screen.getByRole('button', { name: '测试状态' }))

    expect(await screen.findByText('嵌入服务可用 · qwen3-embedding:0.6b · 已嵌入 6/8（75%）')).toBeTruthy()
    expect(call).toHaveBeenCalledWith('/dsh-mnemon-read', 'embedding-status', {})
  })

  it('renders one switch per live Catalog layer and persists only its enabled state', async () => {
    const mutate = vi.fn(async () => {})
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot, mutate)
    const participation = { recall: 'automatic', write: 'automatic', projection: 'automatic', maintenance: 'automatic' } as const
    const descriptor: MemoryCompositionStatus = {
      serving: true,
      evaluation: { state: 'ready', contributionRevision: 4, sourceInstanceKeys: [], diagnostics: [] },
      sources: [],
      configuration: {
        id: 'default-three-tier', strategyId: 'default-three-tier',
        layers: Object.fromEntries(['runtime', 'documents', 'memory-spaces'].map(id => [id, { enabled: true, participation: { ...participation }, adapterIds: [] }])),
      },
    }
    const call = vi.fn(async (channel: string, endpoint: string) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'memory-system') return { ok: true as const, value: descriptor }
      if (channel === '/dsh-mnemon-read' && endpoint === 'task-agent-models') return { ok: true as const, value: { groups: [], failures: [] } }
      if (channel === '/dsh-mnemon-read' && endpoint === 'provider-services') return { ok: true as const, value: { providers: [], items: [], generatedAt: '' } }
      if (channel === '/dsh-mnemon-pack' && endpoint === 'target') return { ok: true as const, value: { root: '/root/.mnemon', scope: 'global' as const } }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })

    const view = render(<MnemonSettingsCard scope={scope} connection={{ rpc: { call }, isLoopback: true } as ClientConnectionHandle} />)

    // The components cannot be read here, but the saved layers still switch,
    // named by their ids since no component can name them.
    await screen.findByRole('switch', { name: 'documents' })
    expect(screen.getByText('暂时无法读取记忆组件；这里只能开关记忆层。')).toBeTruthy()
    expect(screen.queryByText('Narrative records')).toBeNull()

    view.rerender(<MnemonSettingsCard scope={scope} connection={{ rpc: { call }, isLoopback: true } as ClientConnectionHandle} t={translateEn} />)
    const enabled = await screen.findByRole('switch', { name: 'documents' })
    expect(screen.getByText('Memory components cannot be read right now; only the memory layers can be switched here.')).toBeTruthy()
    expect(screen.queryByText('Narrative records')).toBeNull()
    expect(screen.queryByRole('combobox', { name: /documents/ })).toBeNull()
    // A layer switch applies at once, without the Save the staged groups use.
    fireEvent.click(enabled)
    await waitFor(() => expect(mutate).toHaveBeenCalledWith([{
      op: 'set',
      path: ['memoryTopology', 'layers', 'documents', 'enabled'],
      value: false,
    }]))
  })

  it('shows a layer whose component is off as off, and keeps the Providers on Memory Spaces\' page until it runs', async () => {
    const scope = settingsScope({ status: 'ready' as const, value: { storageScope: 'global' as const }, base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const })
    const participation = { recall: 'automatic', write: 'automatic', projection: 'automatic', maintenance: 'automatic' } as const
    const descriptor: MemoryCompositionStatus = {
      serving: true,
      evaluation: { state: 'ready', contributionRevision: 4, sourceInstanceKeys: [], diagnostics: [] },
      sources: [],
      configuration: {
        id: 'default-three-tier', strategyId: 'default-three-tier',
        layers: Object.fromEntries(['runtime', 'documents', 'memory-spaces'].map(id => [id, { enabled: true, participation: { ...participation }, adapterIds: [] }])),
      },
    }
    // Each component names itself, as the shipped packages do.
    const names: Record<string, string> = { 'mnemon-source-runtime': '运行时记忆', 'mnemon-source-documents': '项目档案', 'mnemon-source-memory-spaces': '记忆空间', 'mnemon-strategy-default-three-tier': '分层策略' }
    const component = (entryId: string, packageName: string, roles: MemoryPluginEntryView['roles'], values: Partial<MemoryPluginEntryView>): MemoryPluginEntryView => ({
      entryId, packageName, roles, label: { en: entryId, 'zh-CN': names[entryId] ?? entryId }, description: { en: '', 'zh-CN': '' }, fields: [], provides: [], requires: [], requiredBy: [],
      enabled: true, active: true, writable: true, config: {}, ...values,
    })
    let dashboard: MemoryViewDashboard = {
      revision: 'view-1', writable: true, strategyTypeId: 'default-three-tier', currentUnavailable: 'no-session', sources: [], diagnostics: [],
      pluginInstallation: { supported: false, reason: 'loader-unavailable', suggestions: [] },
      entries: [
        component('mnemon-strategy-default-three-tier', 'dsh-mnemon-strategy-default-three-tier', ['strategy'], { typeId: 'default-three-tier', provides: [{ id: 'strategy', exclusive: false }] }),
        component('mnemon-source-runtime', 'dsh-mnemon-source-runtime', ['source'], { typeId: 'runtime' }),
        // A Source that is off has registered nothing, so it has no type id.
        component('mnemon-source-documents', 'dsh-mnemon-source-documents', ['source'], { enabled: false, active: false }),
        component('mnemon-source-memory-spaces', 'dsh-mnemon-source-memory-spaces', ['source'], { enabled: false, active: false }),
      ],
    }
    const applied: MemoryViewConfigurationRequest[] = []
    const call = vi.fn(async (channel: string, endpoint: string, payload?: unknown) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'memory-system') return { ok: true as const, value: descriptor }
      if (channel === '/dsh-mnemon-view' && endpoint === 'dashboard') return { ok: true as const, value: structuredClone(dashboard) }
      if (channel === '/dsh-mnemon-view-settings' && endpoint === 'apply') {
        const request = (payload as { configuration: MemoryViewConfigurationRequest }).configuration
        applied.push(request)
        dashboard = { ...dashboard, revision: 'view-2', entries: dashboard.entries.map(entry => request.entries[entry.entryId] === undefined ? entry : { ...entry, enabled: true, active: true, typeId: 'memory-spaces' }) }
        return { ok: true as const, value: { saved: true as const } }
      }
      if (channel === '/dsh-mnemon-read' && endpoint === 'task-agent-models') return { ok: true as const, value: { groups: [], failures: [] } }
      if (channel === '/dsh-mnemon-read' && endpoint === 'provider-services') return { ok: true as const, value: { providers: [], items: [], generatedAt: '2026-09-27T00:00:00.000Z' } }
      if (channel === '/dsh-mnemon-pack' && endpoint === 'target') return { ok: true as const, value: { root: '/root/.mnemon', scope: 'global' as const } }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })

    const connection = { rpc: { call }, isLoopback: true } as ClientConnectionHandle
    // The configuration renders what components contributed to their pages; here Memory Spaces' own settings.
    const componentSettings = {
      has: (packageName: string) => packageName === MEMORY_SPACES_PACKAGE,
      render: (entry: MemoryPluginEntryView, current: { enabled: boolean; label: string; writable: boolean; language: string }) =>
        <MemorySpacesSettings scope={scope} connection={connection} t={translateZh} page={{ component: { packageName: entry.packageName, label: current.label, enabled: current.enabled }, writable: current.writable, language: current.language }} />,
    }
    render(<MnemonSettingsCard scope={scope} connection={connection} componentSettings={componentSettings} />)
    // A layer whose Source component is off reads as off: the component is the layer's switch.
    const documents = (await screen.findByRole('switch', { name: '项目档案' })).closest('[data-mnemon-target]') as HTMLElement
    expect(within(documents).getByText('已关闭')).toBeTruthy()
    // The page's top level carries no Provider group: they are Memory Spaces' own settings.
    expect(screen.queryByRole('region', { name: '记忆 Provider' })).toBeNull()
    const spacesRow = screen.getByRole('switch', { name: '记忆空间' }).closest('[data-mnemon-target]') as HTMLElement
    fireEvent.click(within(spacesRow).getByRole('button', { name: '“记忆空间”的选项' }))
    const spacesPage = screen.getByRole('dialog', { name: '记忆空间' })
    expect(within(spacesPage).getByText('开启后可配置 Provider 与嵌入')).toBeTruthy()
    // The Providers are not read while their Source is off.
    expect(call.mock.calls.some(([, endpoint]) => endpoint === 'provider-services')).toBe(false)

    // The page's switch is the row's; once it runs, its settings read the Providers.
    fireEvent.click(within(spacesPage).getByRole('switch', { name: '记忆空间' }))
    await waitFor(() => expect(applied).toEqual([{ expectedRevision: 'view-1', strategyTypeId: 'default-three-tier', entries: { 'mnemon-source-memory-spaces': { enabled: true, config: {} } } }]))
    await waitFor(() => expect(call.mock.calls.some(([, endpoint]) => endpoint === 'provider-services')).toBe(true))
    expect(within(screen.getByRole('dialog', { name: '记忆空间' })).getByRole('heading', { name: '记忆 Provider' })).toBeTruthy()
    expect(within(documents).getByText('已关闭')).toBeTruthy()
  })

  it('ignores a Provider catalog response from the previously selected workspace', async () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'workspace' as const },
      base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)
    const provider = MEMORY_PROVIDER_CATALOG.find(candidate => candidate.id === 'openviking')!
    const catalog = (endpoint: string) => ({
      ok: true as const,
      value: {
        providers: [provider],
        items: [{ providerId: 'openviking' as const, enabled: true, configured: true, settings: { endpoint }, configuredSecrets: [] }],
        generatedAt: '2026-08-17T00:00:00.000Z',
      },
    })
    const firstWorkspace = deferred<ReturnType<typeof catalog>>()
    const call = vi.fn(async (channel: string, endpoint: string, payload?: { workspaceId?: string }) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'provider-services') {
        return payload?.workspaceId === 'workspace-1' ? firstWorkspace.promise : catalog('https://workspace-2.example')
      }
      if (channel === '/dsh-mnemon-pack' && endpoint === 'target') return { ok: true as const, value: { root: '/workspace/.mnemon', scope: 'workspace' as const } }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })
    const connection = { rpc: { call }, isLoopback: true } as ClientConnectionHandle
    const view = render(spaces(scope, connection, { workspaceId: 'workspace-1', workspaceLabel: 'One' }))

    view.rerender(spaces(scope, connection, { workspaceId: 'workspace-2', workspaceLabel: 'Two' }))
    await waitFor(() => expect(call).toHaveBeenCalledWith('/dsh-mnemon-read', 'provider-services', { workspaceId: 'workspace-2' }))
    const providerGroup = await screen.findByRole('group', { name: 'OpenViking 服务配置' }, { timeout: 5_000 })
    fireEvent.click(within(providerGroup).getByRole('button'))
    await waitFor(() => expect((screen.getByLabelText('服务地址') as HTMLInputElement).value).toBe('https://workspace-2.example'), { timeout: 5_000 })

    await act(async () => { firstWorkspace.resolve(catalog('https://workspace-1.example')); await firstWorkspace.promise })
    expect((screen.getByLabelText('服务地址') as HTMLInputElement).value).toBe('https://workspace-2.example')
  }, 10_000)

  it('uses the Host settings grant instead of transport locality on a trusted remote connection', async () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)
    const call = vi.fn(async (channel: string, remoteEndpoint: string, rawArgs: unknown) => {
      const endpoint = (rawArgs as { args: { endpoint: string } }).args.endpoint
      if (channel === '/api' && remoteEndpoint === 'dshMnemon/read' && endpoint === 'task-agent-models') return {
        ok: true as const,
        value: { ok: true as const, value: { effective: { provider: 'deepseek', model: 'deepseek-chat', source: 'dsh-default' as const }, groups: [], failures: [] } },
      }
      if (channel === '/api' && remoteEndpoint === 'dshMnemon/read' && endpoint === 'provider-services') return {
        ok: true as const, value: { ok: true as const, value: { providers: [], items: [], generatedAt: '' } },
      }
      if (channel === '/api' && remoteEndpoint === 'dshMnemon/pack' && endpoint === 'target') return {
        ok: true as const, value: { ok: true as const, value: { root: '/root/.mnemon', scope: 'global' as const } },
      }
      throw new Error(`unexpected ${channel} ${remoteEndpoint}/${endpoint}`)
    })
    const connection = { rpc: { call }, isLoopback: false } as ClientConnectionHandle

    render(<><MnemonSettingsCard scope={scope} connection={connection} />{spaces(scope, connection)}{threeTier(scope, connection)}</>)

    await waitFor(() => expect(call).toHaveBeenCalledWith('/api', 'dshMnemon/read', {
      args: { endpoint: 'task-agent-models', payload: { includeCatalog: false } },
    }))
    expect(selector('存储范围').disabled).toBe(false)
    expect(selector('任务 Agent 模型').disabled).toBe(false)
    expect(screen.queryByText('当前部署的插件设置为只读。')).toBeNull()
    await waitFor(() => expect(call.mock.calls.some(([, remoteEndpoint, args]) => remoteEndpoint === 'dshMnemon/read' && (args as { args: { endpoint: string } }).args.endpoint === 'provider-services')).toBe(true))
    expect(call.mock.calls.some(([, remoteEndpoint, args]) => remoteEndpoint === 'dshMnemon/pack' && (args as { args: { endpoint: string } }).args.endpoint === 'target')).toBe(true)
  })

  it('shows an actionable error instead of a blank settings page when both scopes are unavailable', () => {
    const snapshot = { status: 'unavailable' as const, writable: false, mode: 'host' as const }
    const scope = settingsScope<Config>(snapshot)

    render(<MnemonSettingsCard scope={scope} />)

    expect(screen.getByRole('alert').textContent).toContain('无法加载记忆系统设置')
  })

  it('inherits the new-session route by default and applies a chosen Provider and model at once', async () => {
    const mutate = vi.fn(async () => {})
    const scope = liveSettingsScope<Config>({ status: 'ready' as const, value: { storageScope: 'global' as const }, base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const }, mutate)
    const call = vi.fn(async (channel: string, endpoint: string, _payload: unknown) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'task-agent-models') return {
        ok: true as const,
        value: {
          effective: { provider: 'deepseek', model: 'deepseek-chat', source: 'dsh-default' as const },
          defaultSelection: { provider: 'deepseek', model: 'deepseek-chat' },
          groups: [
            { id: 'deepseek', name: 'DeepSeek', models: [{ id: 'deepseek-chat', name: 'DeepSeek Chat' }] },
            {
              id: 'deepseek-official',
              name: 'DeepSeek',
              models: [{
                id: 'deepseek-v4-flash-vision-exp',
                name: 'DeepSeek-V4-Flash-Vision-Exp',
                inputModalities: ['text', 'image'],
              }],
            },
            { id: 'openai', name: 'OpenAI', models: [{ id: 'gpt-5', name: 'GPT-5' }] },
          ],
          failures: [],
        },
      }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })

    render(threeTier(scope, { rpc: { call }, isLoopback: true } as ClientConnectionHandle))

    expect(selector('任务 Agent 模型').textContent).toBe('跟随主链路')
    expect(await screen.findByText('deepseek / deepseek-chat')).toBeTruthy()
    expect(call).toHaveBeenCalledWith('/dsh-mnemon-read', 'task-agent-models', { includeCatalog: false })
    // Choosing a fixed model applies at once, starting from DSH's default route.
    choose('任务 Agent 模型', '指定模型')
    await waitFor(() => expect(call).toHaveBeenCalledWith('/dsh-mnemon-read', 'task-agent-models', { includeCatalog: true }))
    await waitFor(() => expect(mutate).toHaveBeenCalledWith([{ op: 'set', path: ['taskAgentModel'], value: { mode: 'fixed', provider: 'deepseek', model: 'deepseek-chat' } }]))
    // Providers sharing a name are told apart by their ids; a Provider starts from its first model.
    chooseIn('mnemon-task-agent-provider', 'deepseek-official')
    await waitFor(() => expect(mutate).toHaveBeenLastCalledWith([{ op: 'set', path: ['taskAgentModel'], value: { mode: 'fixed', provider: 'deepseek-official', model: 'deepseek-v4-flash-vision-exp' } }]))
    fireEvent.click(rowSelector('mnemon-task-agent-model'))
    expect(screen.getByRole('menuitem', { name: /DeepSeek-V4-Flash-Vision-Exp.*图片输入/u })).toBeTruthy()
    fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
    chooseIn('mnemon-task-agent-provider', 'OpenAI')
    await waitFor(() => expect(mutate).toHaveBeenLastCalledWith([{ op: 'set', path: ['taskAgentModel'], value: { mode: 'fixed', provider: 'openai', model: 'gpt-5' } }]))
    expect(rowSelector('mnemon-task-agent-model').textContent).toBe('GPT-5')
    expect(screen.getByText('openai / gpt-5')).toBeTruthy()
  })

  it('keeps rapid route-mode switches stable while model requests finish out of order', async () => {
    const mutate = vi.fn(async () => {})
    const scope = liveSettingsScope<Config>({ status: 'ready' as const, value: { storageScope: 'global' as const }, base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const }, mutate)
    const catalog = {
      effective: { provider: 'deepseek', model: 'deepseek-chat', source: 'dsh-default' as const },
      defaultSelection: { provider: 'deepseek', model: 'deepseek-chat' },
      groups: [{ id: 'deepseek', name: 'DeepSeek', models: [{ id: 'deepseek-chat', name: 'DeepSeek Chat' }] }],
      failures: [],
    }
    let resolveRoute!: (value: { ok: true; value: typeof catalog }) => void
    let resolveCatalog!: (value: { ok: true; value: typeof catalog }) => void
    const route = new Promise<{ ok: true; value: typeof catalog }>(resolve => { resolveRoute = resolve })
    const full = new Promise<{ ok: true; value: typeof catalog }>(resolve => { resolveCatalog = resolve })
    const call = vi.fn(async (channel: string, endpoint: string, payload: unknown) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'task-agent-models') {
        return (payload as { includeCatalog: boolean }).includeCatalog ? full : route
      }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })

    render(threeTier(scope, { rpc: { call }, isLoopback: true } as ClientConnectionHandle))
    // A fixed model chosen before the catalog arrives waits for it; choosing to inherit again cancels it.
    choose('任务 Agent 模型', '指定模型')
    choose('任务 Agent 模型', '跟随主链路')

    await act(async () => {
      resolveCatalog({ ok: true, value: catalog })
      await full
      resolveRoute({ ok: true, value: catalog })
      await route
    })

    expect(selector('任务 Agent 模型').textContent).toBe('跟随主链路')
    const writes = mutate.mock.calls as unknown as Array<[Array<{ value: { mode: string } }>]>
    expect(writes.every(([operations]) => operations[0]!.value.mode === 'inherit')).toBe(true)
    choose('任务 Agent 模型', '指定模型')
    await waitFor(() => expect(rowSelector('mnemon-task-agent-provider').disabled).toBe(false))
    expect(rowSelector('mnemon-task-agent-provider').textContent).toBe('DeepSeek')
    // The catalog read once in full is not read again.
    expect(call.mock.calls.filter(([, endpoint]) => endpoint === 'task-agent-models')).toHaveLength(2)
  })

  it('hydrates the full model catalog when a fixed route was already saved', async () => {
    const snapshot = {
      status: 'ready' as const,
      value: {
        storageScope: 'global' as const,
        taskAgentModel: { mode: 'fixed' as const, provider: 'openai', model: 'gpt-5' },
      },
      base: {}, user: {}, revision: 1, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)
    const call = vi.fn(async (channel: string, endpoint: string, payload: unknown) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'task-agent-models') return {
        ok: true as const,
        value: {
          effective: { provider: 'openai', model: 'gpt-5', source: 'fixed' as const },
          defaultSelection: { provider: 'deepseek', model: 'deepseek-chat' },
          groups: [
            { id: 'deepseek', name: 'DeepSeek', models: [{ id: 'deepseek-chat', name: 'DeepSeek Chat' }] },
            { id: 'openai', name: 'OpenAI', models: [{ id: 'gpt-5', name: 'GPT-5' }] },
          ],
          failures: [],
        },
      }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })

    render(threeTier(scope, { rpc: { call }, isLoopback: true } as ClientConnectionHandle))

    expect(selector('任务 Agent 模型').textContent).toBe('指定模型')
    await waitFor(() => expect(rowSelector('mnemon-task-agent-provider').disabled).toBe(false))
    expect(rowSelector('mnemon-task-agent-provider').textContent).toBe('OpenAI')
    expect(rowSelector('mnemon-task-agent-model').textContent).toBe('GPT-5')
    expect(call).toHaveBeenCalledWith('/dsh-mnemon-read', 'task-agent-models', { includeCatalog: true })
  })

  it.each([undefined, 'builtin', 'buildin', 'sidebar'] as const)('applies displayMode=%s at once, while storage waits for its Apply', async displayMode => {
    const mutate = vi.fn(async () => {})
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const, ...(displayMode === undefined ? {} : { displayMode }) },
      base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = liveSettingsScope<Config>(snapshot, mutate)

    render(<MnemonSettingsCard scope={scope} />)

    const isBuiltin = displayMode === 'builtin' || displayMode === 'buildin'
    expect(selector('记忆系统入口').textContent).toBe(isBuiltin ? '会话标签页' : '侧边栏')
    expect(mutate).not.toHaveBeenCalled()
    choose('记忆系统入口', isBuiltin ? '侧边栏' : '会话标签页')
    await waitFor(() => expect(mutate).toHaveBeenCalledWith([{ op: 'set', path: ['displayMode'], value: isBuiltin ? 'sidebar' : 'builtin' }]))
    expect(selector('记忆系统入口').textContent).toBe(isBuiltin ? '侧边栏' : '会话标签页')
    // Moving the storage says what it does and waits for its own Apply.
    choose('存储范围', '工作区')
    expect(mutate).toHaveBeenCalledTimes(1)
    expect(screen.getByText('应用后读写新位置，已有数据不会迁移')).toBeTruthy()
    fireEvent.click(apply())
    await waitFor(() => expect(mutate).toHaveBeenLastCalledWith([{ op: 'set', path: ['storageScope'], value: 'workspace' }]))
  })

  it('applies a storage change from its own line and says it was saved', async () => {
    const mutate = vi.fn(async () => {})
    const snapshot = {
      status: 'ready' as const,
      value: { timeoutMs: 10000, defaultRecallLimit: 10, routingGuidance: true, lifecycleEnabled: true, recallMode: 'guided' as const, writebackMode: 'guided' as const, tabEnabled: true, writeEnabled: true },
      base: { timeoutMs: 10000, defaultRecallLimit: 10, routingGuidance: true, lifecycleEnabled: true, recallMode: 'guided' as const, writebackMode: 'guided' as const, tabEnabled: true, writeEnabled: true },
      user: {},
      revision: 0,
      writable: true,
      mode: 'host' as const,
    }
    const scope = settingsScope(snapshot, mutate)

    render(<MnemonSettingsCard scope={scope} />)
    choose('存储范围', '工作区')
    expect(screen.queryByRole('textbox', { name: '数据目录' })).toBeNull()
    fireEvent.click(apply())

    await waitFor(() => expect(mutate).toHaveBeenCalledWith([{ op: 'set', path: ['storageScope'], value: 'workspace' }]))
    expect(screen.getByText('已保存并实时生效')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '恢复默认' })).toBeNull()
    expect(screen.queryByText(/\.dsh\/settings.yaml/)).toBeNull()
    expect(mutate).toHaveBeenCalledOnce()
  })

  it('keeps where USER.md lives on Runtime Memory\'s page, applied when chosen', async () => {
    const mutate = vi.fn(async () => {})
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'workspace' as const, runtimeUserScope: 'storage' as const },
      base: {},
      user: {},
      revision: 0,
      writable: true,
      mode: 'host' as const,
    }
    const scope = liveSettingsScope<Config>(snapshot, mutate)

    render(<><MnemonSettingsCard scope={scope} />{runtime(scope)}</>)

    // The configuration page keeps only what belongs to no component.
    expect(screen.getAllByRole('button', { name: /^用户画像范围 /u })).toHaveLength(1)
    expect(selector('用户画像范围').textContent).toBe('跟随存储范围')
    choose('用户画像范围', '全局共享')
    await waitFor(() => expect(mutate).toHaveBeenCalledWith([{ op: 'set', path: ['runtimeUserScope'], value: 'global' }]))
    expect(selector('用户画像范围').textContent).toBe('全局共享')
  })

  it('uses the DSH-bound locale in the plugin configuration slot', () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {},
      user: {},
      revision: 0,
      writable: true,
      mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)

    render(<MnemonSettingsCard scope={scope} t={translateEn} />)

    expect(selector('Storage scope').textContent).toBe('Global')
    choose('Storage scope', 'Workspace')
    expect(screen.getByText('Memory moves to the new location; existing data stays where it is')).toBeTruthy()
    expect(apply('Apply')).toBeTruthy()
  })

  it('accepts and persists a manually entered custom directory', async () => {
    const mutate = vi.fn(async () => {})
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {},
      user: {},
      revision: 0,
      writable: true,
      mode: 'host' as const,
    }
    const scope = settingsScope(snapshot, mutate)
    const view = render(<MnemonSettingsCard scope={scope} />)

    // The default location has no field; choosing Custom opens one, ready to type in.
    expect(directoryChoice('默认').checked).toBe(true)
    expect(view.queryByRole('textbox', { name: '数据目录' })).toBeNull()
    fireEvent.click(directoryChoice('自定义'))
    const directory = view.getByRole('textbox', { name: '数据目录' }) as HTMLInputElement
    expect(document.activeElement).toBe(directory)
    expect(directory.placeholder).toBe('例如 ~/Documents/mnemon')
    expect(apply().disabled).toBe(true)
    fireEvent.change(directory, { target: { value: '  /tmp/mnemon-custom  ' } })
    expect(selector('存储范围').textContent).toBe('全局')
    fireEvent.click(apply())

    await waitFor(() => expect(mutate).toHaveBeenCalledWith([
      { op: 'set', path: ['storageScope'], value: 'custom' },
      { op: 'set', path: ['dataDir'], value: '/tmp/mnemon-custom' },
    ]))
  })

  it('migrates a legacy named Pack to the single selected directory', async () => {
    const mutate = vi.fn(async () => {})
    const snapshot = {
      status: 'ready' as const,
      value: {
        storageScope: 'custom' as const,
        customPackId: 'project',
        customPacks: [
          { id: 'project', name: 'Project', dataDir: '/packs/project' },
          { id: 'research', name: 'Research', dataDir: '/packs/research' },
        ],
      },
      base: {},
      user: {
        storageScope: 'custom' as const,
        customPackId: 'project',
        customPacks: [
          { id: 'project', name: 'Project', dataDir: '/packs/project' },
          { id: 'research', name: 'Research', dataDir: '/packs/research' },
        ],
      },
      revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot, mutate)

    render(<MnemonSettingsCard scope={scope} />)
    const directory = screen.getByRole('textbox', { name: '数据目录' }) as HTMLInputElement
    expect(directory.value).toBe('/packs/project')
    expect(selector('存储范围').textContent).toBe('全局')
    fireEvent.change(directory, { target: { value: '/packs/research' } })
    fireEvent.click(apply())

    await waitFor(() => expect(mutate).toHaveBeenCalledWith([
      { op: 'set', path: ['dataDir'], value: '/packs/research' },
      { op: 'unset', path: ['customPackId'] },
      { op: 'unset', path: ['customPacks'] },
    ]))
  })

  it('returns to the default directory by choosing it, not by emptying the field', async () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {},
      user: {},
      revision: 0,
      writable: true,
      mode: 'host' as const,
    }
    const mutate = vi.fn(async () => {})
    const scope = settingsScope({ ...snapshot, value: { storageScope: 'custom' as const, dataDir: '/data/mnemon' } }, mutate)

    render(<MnemonSettingsCard scope={scope} />)
    const directory = screen.getByRole('textbox', { name: '数据目录' }) as HTMLInputElement
    expect(directory.value).toBe('/data/mnemon')
    expect(directoryChoice('自定义').checked).toBe(true)
    // An emptied field waits for a directory rather than meaning the default, which is a choice of its own.
    fireEvent.change(directory, { target: { value: '' } })
    expect(apply().disabled).toBe(true)
    expect(screen.queryByRole('alert')).toBeNull()
    expect(directory.getAttribute('aria-invalid')).toBe('false')
    fireEvent.click(directoryChoice('默认'))
    expect(screen.queryByRole('textbox', { name: '数据目录' })).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    fireEvent.click(apply())

    await waitFor(() => expect(mutate).toHaveBeenCalledWith([
      { op: 'set', path: ['storageScope'], value: 'global' },
      { op: 'unset', path: ['dataDir'] },
    ]))
  })

  it('accepts Windows drive and UNC paths in the browser form', () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)

    render(<MnemonSettingsCard scope={scope} />)
    fireEvent.click(directoryChoice('自定义'))
    const directory = screen.getByRole('textbox', { name: '数据目录' })
    fireEvent.change(directory, { target: { value: 'relative/mnemon' } })
    expect(screen.getByRole('alert').textContent).toContain('绝对路径')
    fireEvent.change(directory, { target: { value: 'C:\\memory\\mnemon' } })
    expect(screen.queryByRole('alert')).toBeNull()
    fireEvent.change(directory, { target: { value: '\\\\server\\share\\mnemon' } })
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('uses native disabled semantics for a read-only settings scope', () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {},
      user: {},
      revision: 0,
      writable: false,
      mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)

    render(<><MnemonSettingsCard scope={scope} />{threeTier(scope)}</>)

    expect(selector('存储范围').disabled).toBe(true)
    expect(directoryChoice('默认').disabled).toBe(true)
    expect(directoryChoice('自定义').disabled).toBe(true)
    expect((screen.getByRole('switch', { name: '启用空闲审查' }) as HTMLButtonElement).disabled).toBe(true)
    expect(selector('审查方式').disabled).toBe(true)
    expect(selector('记忆系统入口').disabled).toBe(true)
    expect((screen.getByRole('switch', { name: '回合记忆栏' }) as HTMLButtonElement).disabled).toBe(true)
    // The notice leads the page, above every group, as on DSH's own settings forms.
    const notice = screen.getByText('当前部署的插件设置为只读。')
    const configuration = screen.getByRole('region', { name: '记忆系统配置' })
    expect(configuration.firstElementChild).toBe(notice)
  })

  it('says a remote page needs the management grant instead of calling the deployment read-only', () => {
    const snapshot = { status: 'ready' as const, value: { storageScope: 'global' as const }, base: {}, user: {}, revision: 0, writable: false, mode: 'host' as const }
    const call = vi.fn(async () => ({ ok: false as const, error: { code: 'unavailable', message: 'offline', details: { issues: [] } } }))
    vi.stubGlobal('location', { protocol: 'https:', hostname: 'memory.example' })
    try {
      render(<MnemonSettingsCard scope={settingsScope(snapshot)} connection={{ isLoopback: false, rpc: { call } } as never} />)
      expect(screen.getByText(/^远程访问时插件设置为只读：/)).toBeTruthy()
      expect(screen.queryByText('当前部署的插件设置为只读。')).toBeNull()
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('does not present temporary defaults as read-only while settings load', () => {
    const snapshot = {
      status: 'loading' as const,
      writable: false,
      mode: 'host' as const,
    }
    const scope = settingsScope<Config>(snapshot)

    render(<MnemonSettingsCard scope={scope} />)

    expect(screen.getByRole('status').textContent).toBe('载入中…')
    expect(screen.queryByText('当前部署的插件设置为只读。')).toBeNull()
    expect(screen.queryByRole('button', { name: /^存储范围 / })).toBeNull()
  })

  it('applies an interaction toggle at once through the mnemon-ui scope', async () => {
    const interactionMutate = vi.fn(async () => {})
    const coreSnapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {},
      user: {},
      revision: 0,
      writable: true,
      mode: 'host' as const,
    }
    const scope = settingsScope(coreSnapshot)
    const interactionSnapshot = {
      status: 'ready' as const,
      value: { turnBar: true, saveAction: true },
      base: {},
      user: {},
      revision: 0,
      writable: true,
      mode: 'host' as const,
    }
    const interactionScope = liveSettingsScope(interactionSnapshot, interactionMutate)

    const view = render(<MnemonSettingsCard scope={scope} interactionScope={interactionScope} />)

    const turnBar = view.getByRole('switch', { name: '回合记忆栏' })
    expect(checked(turnBar)).toBe(true)
    expect(view.queryByLabelText('记忆工具卡')).toBeNull()

    fireEvent.click(turnBar)
    await waitFor(() => expect(interactionMutate).toHaveBeenCalledWith([
      { op: 'set', path: ['turnBar'], value: false },
    ]))
    expect(checked(view.getByRole('switch', { name: '回合记忆栏' }))).toBe(false)
    expect(view.queryByRole('button', { name: '应用' })).toBeNull()
  })

  it('shows the saved value again beside the reason when a toggle is refused', async () => {
    const scope = settingsScope({ status: 'ready' as const, value: { storageScope: 'global' as const }, base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const })
    const interactionScope = liveSettingsScope({ status: 'ready' as const, value: { turnBar: true, saveAction: true }, base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const },
      vi.fn(async () => { throw new Error('settings are locked') }))

    render(<MnemonSettingsCard scope={scope} interactionScope={interactionScope} />)
    fireEvent.click(screen.getByRole('switch', { name: '存入记忆按钮' }))
    expect((await screen.findByRole('alert')).textContent).toBe('保存失败：settings are locked')
    expect(checked(screen.getByRole('switch', { name: '存入记忆按钮' }))).toBe(true)
  })

  it('presents the two remaining interaction toggles checked by default', () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {},
      user: {},
      revision: 0,
      writable: true,
      mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)

    const view = render(<MnemonSettingsCard scope={scope} />)

    expect(view.queryByLabelText('记忆工具卡')).toBeNull()
    expect(checked(view.getByRole('switch', { name: '回合记忆栏' }))).toBe(true)
    expect(checked(view.getByRole('switch', { name: '存入记忆按钮' }))).toBe(true)
  })

  it('does not invent Provider registrations while the Host catalog is loading', () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'workspace' as const },
      base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)
    const providerSettings = new Promise<never>(() => {})
    const call = vi.fn((channel: string, endpoint: string) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'provider-services') return providerSettings
      if (channel === '/dsh-mnemon-pack' && endpoint === 'target') return Promise.resolve({ ok: true as const, value: { root: '/active/.mnemon', scope: 'global' } })
      return Promise.reject(new Error(`unexpected ${channel} ${endpoint}`))
    })

    render(spaces(scope, { rpc: { call }, isLoopback: true } as ClientConnectionHandle))

    expect(screen.queryAllByRole('group', { name: /服务配置/ })).toHaveLength(0)
    expect(screen.queryAllByRole('switch', { name: /^启用 / })).toHaveLength(0)
    expect(screen.getByText('正在读取 Provider 配置…').getAttribute('role')).toBe('status')
  })

  it('uses the native default/custom location pattern for a scope-aware local provider', async () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)
    const provider = {
      id: 'holographic' as const,
      label: 'Holographic', kind: 'local' as const, origin: 'third-party' as const, summary: 'Local facts',
      workspaceBinding: 'optional-override' as const,
      capabilities: { search: true, browse: true, graph: true, entities: true, related: true, remember: true, link: false, forget: true, writeMode: 'exact' as const, deletionMode: 'hard' as const },
      fields: [{ key: 'dataPath', label: 'Fact store path', i18nKey: 'overview.providerField.dataPath' as const, scope: 'service' as const, role: 'global-location' as const, input: 'path' as const, required: false }],
    }
    const call = vi.fn(async (channel: string, endpoint: string, payload: unknown) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'provider-services') return { ok: true as const, value: { providers: [provider], items: [{ providerId: 'holographic', enabled: true, configured: true, settings: { dataPath: '/srv/dsh/holographic.json' }, configuredSecrets: [] }], generatedAt: '2026-08-17T00:00:00.000Z' } }
      if (channel === '/dsh-mnemon-write' && endpoint === 'provider-service-update') {
        const request = payload as { settings: Record<string, string> }
        return { ok: true as const, value: { providerId: 'holographic', enabled: true, configured: true, settings: request.settings, configuredSecrets: [] } }
      }
      if (channel === '/dsh-mnemon-pack' && endpoint === 'target') return { ok: true as const, value: { root: '/active/.mnemon', scope: 'global' } }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })

    render(spaces(scope, { rpc: { call }, isLoopback: true } as ClientConnectionHandle))

    const card = await screen.findByRole('group', { name: 'Holographic 服务配置' })
    fireEvent.click(within(card).getByText('Holographic'))
    const location = within(card).getByRole('radiogroup', { name: 'Holographic 全局数据位置' })
    const defaultLocation = within(location).getByRole('radio', { name: '默认（跟随范围）' }) as HTMLInputElement
    const customLocation = within(location).getByRole('radio', { name: '自定义' }) as HTMLInputElement
    const scrollViewport = card.parentElement as HTMLElement
    scrollViewport.style.overflowY = 'auto'
    Object.defineProperties(scrollViewport, { clientHeight: { configurable: true, value: 100 }, scrollHeight: { configurable: true, value: 1000 } })
    const hiddenViewport = scrollViewport.parentElement as HTMLElement
    hiddenViewport.style.overflowY = 'hidden'

    expect(customLocation.checked).toBe(true)
    hiddenViewport.scrollTop = 240
    fireEvent.click(customLocation)
    expect(hiddenViewport.scrollTop).toBe(0)
    for (let cycle = 0; cycle < 3; cycle += 1) {
      hiddenViewport.scrollTop = 240
      fireEvent.click(defaultLocation)
      expect(within(card).queryByRole('textbox', { name: '事实存储路径' })).toBeNull()
      expect(hiddenViewport.scrollTop).toBe(0)
      fireEvent.click(customLocation)
      expect((within(card).getByRole('textbox', { name: '事实存储路径' }) as HTMLInputElement).value).toBe('/srv/dsh/holographic.json')
    }
    const path = within(card).getByRole('textbox', { name: '事实存储路径' })
    const save = within(card).getByRole('button', { name: '保存服务配置' }) as HTMLButtonElement
    fireEvent.change(path, { target: { value: '/srv/dsh/holographic-v2.json' } })
    expect(save.disabled).toBe(false)
    fireEvent.click(save)

    await waitFor(() => expect(call).toHaveBeenCalledWith('/dsh-mnemon-write', 'provider-service-update', expect.objectContaining({
      providerId: 'holographic', enabled: true, settings: { dataPath: '/srv/dsh/holographic-v2.json' },
    })))
  })

  it('renders and saves an unknown Provider instance entirely from its descriptor', async () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)
    const provider = {
      id: 'work-account', typeId: 'vector-store', label: 'Vector Store', icon: { kind: 'glyph' as const, value: 'VS' },
      kind: 'remote' as const, origin: 'third-party' as const, workspaceBinding: 'provider-global' as const,
      summary: 'Independent vector memory.',
      capabilities: { search: true, browse: true, graph: false, entities: false, related: false, remember: true, link: false, forget: false, writeMode: 'exact' as const, deletionMode: 'unsupported' as const },
      fields: [
        { key: 'serverUrl', label: 'Vector endpoint', scope: 'service' as const, input: 'url' as const, required: true },
        { key: 'apiToken', label: 'Vector API token', scope: 'service' as const, input: 'secret' as const, required: true },
        { key: 'collection', label: 'Collection', scope: 'memory' as const, input: 'text' as const, required: true },
      ],
    }
    const call = vi.fn(async (channel: string, endpoint: string, payload: unknown) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'provider-services') return {
        ok: true as const,
        value: {
          providers: [provider],
          items: [{ providerId: 'work-account', enabled: true, configured: true, settings: { serverUrl: 'https://vectors.example' }, configuredSecrets: ['apiToken'] }],
          generatedAt: '2026-08-30T00:00:00.000Z',
        },
      }
      if (channel === '/dsh-mnemon-write' && endpoint === 'provider-service-update') {
        const request = payload as { settings: Record<string, string> }
        return { ok: true as const, value: { providerId: 'work-account', enabled: true, configured: true, settings: request.settings, configuredSecrets: ['apiToken'] } }
      }
      if (channel === '/dsh-mnemon-pack' && endpoint === 'target') return { ok: true as const, value: { root: '/active/.mnemon', scope: 'global' } }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })

    render(spaces(scope, { rpc: { call }, isLoopback: true } as ClientConnectionHandle))

    const card = await screen.findByRole('group', { name: 'Vector Store 服务配置' })
    expect(card.querySelector('[data-provider-icon="work-account"]')?.textContent).toBe('VS')
    expect(within(card).getByText('Independent vector memory.')).toBeTruthy()
    fireEvent.click(within(card).getByRole('button'))
    const endpoint = within(card).getByRole('textbox', { name: 'Vector endpoint' })
    expect((within(card).getByLabelText('Vector API token') as HTMLInputElement).type).toBe('password')
    fireEvent.change(endpoint, { target: { value: 'https://vectors-v2.example' } })
    fireEvent.click(within(card).getByRole('button', { name: '保存服务配置' }))

    await waitFor(() => expect(call).toHaveBeenCalledWith('/dsh-mnemon-write', 'provider-service-update', expect.objectContaining({
      providerId: 'work-account', enabled: true, settings: { serverUrl: 'https://vectors-v2.example' },
    })))
  })

  it('edits a reusable provider service and reports its Memory Space synchronization', async () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'workspace' as const },
      base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)
    const provider = {
      id: 'openviking' as const,
      label: 'OpenViking', kind: 'remote' as const, origin: 'third-party' as const, summary: 'Shared memory',
      workspaceBinding: 'provider-global' as const,
      capabilities: { search: true, browse: true, graph: false, entities: false, related: false, remember: true, link: false, forget: true, writeMode: 'async-extracting' as const, deletionMode: 'hard' as const },
      fields: [
        { key: 'endpoint', label: 'Endpoint', i18nKey: 'overview.providerEndpoint' as const, scope: 'service' as const, input: 'url' as const, required: true, defaultValue: 'http://127.0.0.1:1933' },
        { key: 'apiKey', label: 'API key', i18nKey: 'overview.providerApiKey' as const, scope: 'service' as const, input: 'secret' as const, required: false },
        { key: 'targetUri', label: 'Memory URI', scope: 'memory' as const, input: 'text' as const, required: true, defaultValue: 'viking://user/memories' },
      ],
    }
    const call = vi.fn(async (channel: string, endpoint: string) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'provider-services') return { ok: true as const, value: { providers: [provider], items: [{ providerId: 'openviking', enabled: true, configured: true, settings: { endpoint: 'http://127.0.0.1:1933' }, configuredSecrets: ['apiKey'], secretValues: { apiKey: 'service-secret' } }], generatedAt: '2026-08-17T00:00:00.000Z' } }
      if (channel === '/dsh-mnemon-write' && endpoint === 'provider-service-update') return { ok: true as const, value: { providerId: 'openviking', enabled: true, configured: true, settings: { endpoint: 'http://127.0.0.1:1933' }, configuredSecrets: ['apiKey'], secretValues: { apiKey: 'service-secret' } } }
      if (channel === '/dsh-mnemon-pack' && endpoint === 'target') return { ok: true as const, value: { root: '/workspace/.mnemon', scope: 'workspace' } }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })
    const connection = { rpc: { call }, isLoopback: true } as ClientConnectionHandle

    render(spaces(scope, connection, { sessionId: 'session-1', workspaceId: 'workspace-1', workspaceLabel: 'dsh-mnemon' }))

    await waitFor(() => expect(screen.getByText('OpenViking')).toBeTruthy())
    expect(screen.getByRole('group', { name: 'OpenViking 服务配置' })).toBeTruthy()
    const disclosure = screen.getByText('OpenViking').closest('button') as HTMLButtonElement
    expect(disclosure.getAttribute('aria-expanded')).toBe('false')
    expect(checked(screen.getByRole('switch', { name: '启用 OpenViking' }))).toBe(true)
    expect(screen.getByText('当前工作区：dsh-mnemon；标记“工作区”的 Provider 配置与记忆空间使用此范围。')).toBeTruthy()
    fireEvent.click(screen.getByText('OpenViking'))
    expect(disclosure.getAttribute('aria-expanded')).toBe('true')
    expect((screen.getByLabelText('服务地址') as HTMLInputElement).value).toBe('http://127.0.0.1:1933')
    expect(screen.queryByLabelText('记忆范围 URI')).toBeNull()
    expect(screen.queryByLabelText('记忆空间名称')).toBeNull()
    expect(screen.queryByRole('checkbox', { name: '清除已保存的凭据' })).toBeNull()
    const apiKey = screen.getByLabelText('API Key') as HTMLInputElement
    expect(apiKey.value).toBe('service-secret')
    expect(apiKey.type).toBe('password')
    expect(screen.queryByText(/已安全保存/)).toBeNull()
    expect(screen.queryByRole('button', { name: /移除已保存/ })).toBeNull()
    const showCredential = screen.getByRole('button', { name: '显示凭证' }) as HTMLButtonElement
    expect(showCredential.disabled).toBe(false)
    fireEvent.click(showCredential)
    expect(apiKey.type).toBe('text')
    expect(apiKey.value).toBe('service-secret')
    fireEvent.change(apiKey, { target: { value: 'replacement-secret' } })
    expect(apiKey.value).toBe('replacement-secret')
    fireEvent.click(screen.getByRole('button', { name: '隐藏凭证' }))
    expect(apiKey.type).toBe('password')
    fireEvent.click(screen.getByRole('button', { name: '保存服务配置' }))

    await waitFor(() => expect(call).toHaveBeenCalledWith('/dsh-mnemon-write', 'provider-service-update', {
      providerId: 'openviking',
      settings: { endpoint: 'http://127.0.0.1:1933', apiKey: 'replacement-secret' },
      enabled: true,
      sessionId: 'session-1',
      workspaceId: 'workspace-1',
    }))
    expect(await screen.findByText('服务配置已保存，记忆空间目录已同步')).toBeTruthy()
    expect(disclosure.getAttribute('aria-expanded')).toBe('true')
    expect(call.mock.calls.filter(([, endpoint]) => endpoint === 'provider-services')).toHaveLength(1)
    expect(call.mock.calls.some(([, endpoint]) => endpoint === 'body-create')).toBe(false)
  })

  it('keeps a new provider off until its required service configuration is saved', async () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)
    const provider = {
      id: 'supermemory' as const,
      label: 'Supermemory', kind: 'remote' as const, origin: 'third-party' as const, summary: 'Semantic memory',
      workspaceBinding: 'provider-global' as const,
      capabilities: { search: true, browse: true, graph: false, entities: false, related: false, remember: true, link: false, forget: true, writeMode: 'async-extracting' as const, deletionMode: 'soft' as const },
      fields: [
        { key: 'endpoint', label: 'Endpoint', i18nKey: 'overview.providerEndpoint' as const, scope: 'service' as const, input: 'url' as const, required: true, defaultValue: 'https://api.supermemory.ai' },
        { key: 'apiKey', label: 'API key', i18nKey: 'overview.providerApiKey' as const, scope: 'service' as const, input: 'secret' as const, required: true },
      ],
    }
    let service = { providerId: 'supermemory' as const, enabled: false, configured: false, settings: {}, configuredSecrets: [] as string[] }
    const call = vi.fn(async (channel: string, endpoint: string, payload: unknown) => {
      if (channel === '/dsh-mnemon-read' && endpoint === 'provider-services') return { ok: true as const, value: { providers: [provider], items: [service], generatedAt: '2026-08-17T00:00:00.000Z' } }
      if (channel === '/dsh-mnemon-write' && endpoint === 'provider-service-update') {
        const request = payload as { enabled: boolean; settings: Record<string, unknown> }
        service = {
          providerId: 'supermemory', enabled: request.enabled, configured: service.configured || Object.keys(request.settings).length > 0,
          settings: Object.hasOwn(request.settings, 'endpoint') ? { endpoint: request.settings.endpoint as string } : service.settings,
          configuredSecrets: Object.hasOwn(request.settings, 'apiKey') ? ['apiKey'] : service.configuredSecrets,
        }
        return { ok: true as const, value: service }
      }
      if (channel === '/dsh-mnemon-pack' && endpoint === 'target') return { ok: true as const, value: { root: '/active/.mnemon', scope: 'global' } }
      throw new Error(`unexpected ${channel} ${endpoint}`)
    })

    render(spaces(scope, { rpc: { call }, isLoopback: true } as ClientConnectionHandle))

    const providerToggle = await screen.findByRole('switch', { name: '启用 Supermemory' })
    const providerCard = screen.getByRole('group', { name: 'Supermemory 服务配置' }) as HTMLDivElement
    const scrollViewport = providerCard.parentElement as HTMLDivElement
    scrollViewport.style.overflowY = 'auto'
    Object.defineProperties(scrollViewport, { clientHeight: { configurable: true, value: 100 }, scrollHeight: { configurable: true, value: 1000 } })
    const hiddenViewport = scrollViewport.parentElement as HTMLDivElement
    hiddenViewport.style.overflowY = 'hidden'
    hiddenViewport.scrollTop = 240
    expect(checked(providerToggle)).toBe(false)
    expect(screen.queryByLabelText('服务地址')).toBeNull()

    fireEvent.click(providerToggle)
    expect(checked(providerToggle)).toBe(true)
    expect(screen.getByLabelText('服务地址')).toBeTruthy()
    const enable = screen.getByRole('button', { name: '保存并启用' }) as HTMLButtonElement
    expect(enable.disabled).toBe(true)
    fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'service-secret' } })
    expect(enable.disabled).toBe(false)
    fireEvent.click(enable)

    await waitFor(() => expect(call).toHaveBeenCalledWith('/dsh-mnemon-write', 'provider-service-update', expect.objectContaining({
      providerId: 'supermemory', enabled: true, settings: { endpoint: 'https://api.supermemory.ai', apiKey: 'service-secret' },
    })))
    expect(await screen.findByText('服务配置已保存，记忆空间目录已同步')).toBeTruthy()

    fireEvent.click(providerToggle)
    await waitFor(() => expect(call).toHaveBeenCalledWith('/dsh-mnemon-write', 'provider-service-update', expect.objectContaining({
      providerId: 'supermemory', enabled: false, settings: {},
    })))
    expect(hiddenViewport.scrollTop).toBe(0)
    expect(screen.queryByLabelText('服务地址')).toBeNull()

    fireEvent.click(providerToggle)
    await waitFor(() => expect(call).toHaveBeenLastCalledWith('/dsh-mnemon-write', 'provider-service-update', expect.objectContaining({
      providerId: 'supermemory', enabled: true, settings: {},
    })))
    expect(checked(providerToggle)).toBe(true)
    fireEvent.click(screen.getByText('Supermemory'))
    expect(screen.getByLabelText('服务地址')).toBeTruthy()
    expect(call.mock.calls.filter(([, endpoint]) => endpoint === 'provider-services')).toHaveLength(1)
  })

  it('previews and safely imports one complete directory ZIP', async () => {
    const snapshot = {
      status: 'ready' as const,
      value: { storageScope: 'global' as const },
      base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const,
    }
    const scope = settingsScope(snapshot)
    const call = vi.fn(async (_channel: string, endpoint: string, payload: unknown) => {
      if (endpoint === 'target') return { ok: true as const, value: { root: '/active/.mnemon', scope: 'global' } }
      if (endpoint === 'inspect') return {
        ok: true as const,
        value: {
          fileName: 'backup.zip', archiveBytes: 4, expandedBytes: 2048,
          targetRoot: '/active/.mnemon', targetScope: 'global',
          occupied: { runtime: true, documents: true, 'memory-spaces': true },
          manifest: {
            format: 'mnemonpack', version: 1, scope: 'full', exportedAt: '2026-08-14T12:00:00.000Z',
            source: { plugin: 'dsh-mnemon', pluginVersion: '0.1.0' },
            components: ['runtime', 'documents', 'memory-spaces'],
            summary: [
              { component: 'runtime', files: 3, bytes: 600, items: 2 },
              { component: 'documents', files: 2, bytes: 700, items: 1 },
              { component: 'memory-spaces', files: 2, bytes: 748, items: 1 },
            ],
          },
        },
      }
      if (endpoint === 'import') return {
        ok: true as const,
        value: { imported: true, mode: 'merge', targetRoot: '/active/.mnemon', components: ['runtime', 'documents', 'memory-spaces'], summary: [] },
      }
      throw new Error(`unexpected endpoint ${endpoint}: ${JSON.stringify(payload)}`)
    })
    const connection = { rpc: { call }, isLoopback: true } as ClientConnectionHandle

    render(<MnemonSettingsCard scope={scope} connection={connection} />)
    await waitFor(() => expect(screen.getByTitle('/active/.mnemon')).toBeTruthy())

    const file = new File(['pack'], 'backup.zip', { type: 'application/zip' })
    fireEvent.change(screen.getByLabelText('选择 Mnemon 备份 ZIP'), { target: { files: [file] } })
    await waitFor(() => expect(screen.getByText('backup.zip')).toBeTruthy())
    expect(screen.getByText(/校验通过 · 3 个组件 · 4 项/)).toBeTruthy()
    expect(screen.queryByRole('checkbox', { name: /Documents/ })).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: '安全导入' }))
    await waitFor(() => expect(call).toHaveBeenCalledWith('/dsh-mnemon-pack', 'import', {
      base64: 'cGFjaw==',
    }))
    expect(screen.getByText('已将 ZIP 安全合并到 /active/.mnemon。')).toBeTruthy()
  })
})

describe('centralized workspace storage settings', () => {
  function settings(value: Config, writable = true) {
    const snapshot = { status: 'ready' as const, value, base: {}, user: {}, revision: 0, writable, mode: 'host' as const }
    const mutate = vi.fn(async () => {})
    return { scope: settingsScope(snapshot, mutate), mutate }
  }
  it('applies the scope and central root together from the storage section', async () => {
    const { scope, mutate } = settings({ storageScope: 'global' })
    render(<MnemonSettingsCard scope={scope} />)
    choose('存储范围', '集中存储 · 按工作区隔离')
    const section = screen.getByRole('region', { name: '存储' })
    fireEvent.click(directoryChoice('自定义'))
    fireEvent.change(within(section).getByRole('textbox', { name: '数据目录' }), { target: { value: '  /tmp/central-memory  ' } })
    fireEvent.click(within(section).getByRole('button', { name: '应用' }))
    await waitFor(() => expect(mutate).toHaveBeenCalledWith([
      { op: 'set', path: ['storageScope'], value: 'workspaces' },
      { op: 'set', path: ['dataDir'], value: '/tmp/central-memory' },
    ]))
  })

  it('rejects a relative central root and returns to the default root without changing scope', async () => {
    const { scope, mutate } = settings({ storageScope: 'workspaces', dataDir: '/old-root' })
    render(<MnemonSettingsCard scope={scope} t={translateEn} />)
    const input = screen.getByRole('textbox', { name: 'Data directory' })
    fireEvent.change(input, { target: { value: 'relative' } })
    expect(apply('Apply').disabled).toBe(true)
    expect(screen.getByRole('alert').textContent).toContain('absolute')
    fireEvent.click(directoryChoice('Default', 'Data directory'))
    fireEvent.click(apply('Apply'))
    await waitFor(() => expect(mutate).toHaveBeenCalledWith([{ op: 'set', path: ['dataDir'], value: '' }]))
  })
  it('disables the new scope and its root when settings are read-only', () => {
    const { scope, mutate } = settings({ storageScope: 'workspaces' }, false)
    render(<MnemonSettingsCard scope={scope} />)
    expect(selector('存储范围').disabled).toBe(true)
    expect(selector('存储范围').textContent).toBe('集中存储 · 按工作区隔离')
    expect(directoryChoice('默认').disabled).toBe(true)
    expect(directoryChoice('自定义').disabled).toBe(true)
    expect(mutate).not.toHaveBeenCalled()
  })

  it('shows the one directory memory uses: the default under its title, a typed one in its field', async () => {
    const connection = (root: string, scope: string) => {
      const call = vi.fn(async (channel: string, endpoint: string) => {
        if (channel === '/dsh-mnemon-pack' && endpoint === 'target') return { ok: true as const, value: { root, scope, defaultRoot: '/home/me/.mnemon' } }
        throw new Error(`unexpected ${channel} ${endpoint}`)
      })
      return { call, connection: { rpc: { call }, isLoopback: true } as unknown as ClientConnectionHandle }
    }

    // The default: its path under the title, with no empty field beside it.
    const global = connection('/home/me/.mnemon', 'global')
    const first = render(<MnemonSettingsCard scope={settings({ storageScope: 'global' }).scope} connection={global.connection} />)
    expect(await screen.findByTitle('/home/me/.mnemon')).toBeTruthy()
    expect(directoryChoice('默认').checked).toBe(true)
    expect(screen.queryByRole('textbox', { name: '数据目录' })).toBeNull()
    first.unmount()

    // A central root: this workspace's own directory under it.
    const central = connection('/home/me/.mnemon/workspaces/project-1a2b', 'workspaces')
    const second = render(<MnemonSettingsCard scope={settings({ storageScope: 'workspaces' }).scope} connection={central.connection} />)
    expect(await screen.findByTitle('/home/me/.mnemon/workspaces/project-1a2b')).toBeTruthy()
    expect(screen.getByText('本工作区')).toBeTruthy()
    second.unmount()

    // A typed directory shows in its field only; choosing Default shows where memory would go.
    const custom = connection('/data/mnemon', 'custom')
    render(<MnemonSettingsCard scope={settings({ storageScope: 'custom', dataDir: '/data/mnemon' }).scope} connection={custom.connection} />)
    await waitFor(() => expect(custom.call).toHaveBeenCalledWith('/dsh-mnemon-pack', 'target', expect.anything()))
    await act(async () => {})
    expect((screen.getByRole('textbox', { name: '数据目录' }) as HTMLInputElement).value).toBe('/data/mnemon')
    expect(screen.queryByTitle('/data/mnemon')).toBeNull()
    fireEvent.click(directoryChoice('默认'))
    expect(screen.getByTitle('/home/me/.mnemon')).toBeTruthy()
    expect(screen.getByText('应用后读写新位置，已有数据不会迁移')).toBeTruthy()
  })
})
