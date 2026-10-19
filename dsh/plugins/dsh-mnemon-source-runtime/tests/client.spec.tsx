// @vitest-environment jsdom
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'
import { MemorySourcePageFrame, translateEn as t } from 'dsh-mnemon/client'
import { strategy } from './fixture.ts'

// Load the installed Core's actual DSH browser artifact; no repository source alias.
vi.mock('dsh-mnemon/client', async () => {
  const { createRequire } = await import('node:module')
  const { loadMemoryClientArtifact } = await import('dsh-mnemon/testing')
  return loadMemoryClientArtifact(createRequire(import.meta.url).resolve('dsh-mnemon/client'), {
    react: await vi.importActual('react'),
    'react/jsx-runtime': await vi.importActual('react/jsx-runtime'),
    'react-dom': await vi.importActual('react-dom'),
    '@deepseek-ai/dsh-client-ui-primitives': await vi.importActual('@deepseek-ai/dsh-client-ui-primitives'),
  })
})
afterEach(cleanup)

import * as plugin from '../src/index.ts'
import { RuntimeSourcePage, installRuntimeMemoryUI } from '../src/client.ts'
import { RuntimePage } from '../src/client/pages.tsx'
import type { RuntimeMemorySnapshot } from '../src/contracts.ts'

describe('independent Runtime Source client', () => {
  it('browses newest creations before pagination and keeps edits in their original position', async () => {
    const snapshot: RuntimeMemorySnapshot = {
      directory: '/runtime', sourcePath: '/runtime/memories.json', revision: 'fixture', generatedAt: '2026-09-01T08:00:00Z',
      targets: {
        user: { target: 'user', entryCount: 6, used: 100, limit: 4096, markdownPath: '/runtime/USER.md' },
        memory: { target: 'memory', entryCount: 6, used: 100, limit: 10240, markdownPath: '/runtime/MEMORY.md' },
      },
      entries: [3, 1, 2, 4, 5, 6, 7, 8, 9, 10, 11, 12].map(day => ({
        target: day % 2 === 0 ? 'memory' : 'user', importance: 'normal', content: day === 2 ? 'Entry 2 edited' : `Entry ${day}`,
        created_at: `2026-08-${String(day).padStart(2, '0')}T08:00:00.000Z`, updated_at: '2026-09-01T08:00:00.000Z',
      })),
    }
    Object.freeze(snapshot.entries)
    const client = { runtimeMemory: async () => snapshot, mutateRuntimeMemory: vi.fn() }
    const page = (writable: boolean) => <MemorySourcePageFrame locale="en"><RuntimePage client={client} revision={0} writeEnabled={writable} onMutate={() => {}} /></MemorySourcePageFrame>
    const view = render(page(false))
    const listElement = await screen.findByLabelText(t('runtime.entriesAria'))
    const list = within(listElement)
    const contents = () => Array.from(listElement.querySelectorAll('article > p'), item => item.textContent)
    await waitFor(() => expect(contents()).toEqual([12, 11, 10, 9, 8, 7, 6, 5, 4, 3].map(day => `Entry ${day}`)))
    expect(list.queryByText(t('runtime.editAction'))).toBeNull()
    fireEvent.click(list.getByText(t('common.showMore', { count: 2 })))
    expect(contents().slice(-2)).toEqual(['Entry 2 edited', 'Entry 1'])
    expect(Array.from(listElement.querySelectorAll('article time')).at(-2)?.getAttribute('datetime')).toBe('2026-08-02T08:00:00.000Z')
    fireEvent.click(list.getByText(t('runtime.target.memory')))
    expect(contents()).toEqual(['Entry 12', 'Entry 10', 'Entry 8', 'Entry 6', 'Entry 4', 'Entry 2 edited'])
    fireEvent.change(list.getByLabelText(t('runtime.filterAria')), { target: { value: 'Entry 1' } })
    expect(contents()).toEqual(['Entry 12', 'Entry 10'])
    view.rerender(page(true))
    expect(list.getAllByText(t('runtime.editAction'))).toHaveLength(2)
    expect(client.mutateRuntimeMemory).not.toHaveBeenCalled()
  })

  it('marks and reveals the entry a conversation turn wrote, beyond the first page too', async () => {
    const entries = Array.from({ length: 12 }, (_, index) => ({
      target: 'memory' as const, importance: 'normal' as const, content: index === 0 ? 'Checkout p75   LCP is 2.4 s after deferring the payment SDK.' : `Entry ${index}`,
      created_at: `2026-08-${String(index + 1).padStart(2, '0')}T08:00:00.000Z`, updated_at: '2026-09-01T08:00:00.000Z',
    }))
    const snapshot = {
      directory: '/runtime', sourcePath: '/runtime/memories.json', revision: 'fixture', generatedAt: '2026-09-01T08:00:00Z',
      targets: {
        user: { target: 'user', entryCount: 0, used: 0, limit: 4096, markdownPath: '/runtime/USER.md' },
        memory: { target: 'memory', entryCount: 12, used: 100, limit: 10240, markdownPath: '/runtime/MEMORY.md' },
      },
      entries,
    } as RuntimeMemorySnapshot
    const reveal = vi.fn()
    render(<MemorySourcePageFrame locale="en"><RuntimePage client={{ runtimeMemory: async () => snapshot, mutateRuntimeMemory: vi.fn() }} revision={0} writeEnabled={false} focusText="Checkout p75 LCP is 2.4 s after…" onRevealElement={reveal} onMutate={() => {}} /></MemorySourcePageFrame>)
    await waitFor(() => expect(reveal).toHaveBeenCalledTimes(1))
    const focused = document.querySelectorAll('article[data-focused]')
    expect(focused).toHaveLength(1)
    expect(focused[0]!.querySelector('p')?.textContent).toContain('deferring the payment SDK')
    expect(reveal).toHaveBeenCalledWith(focused[0])
  })

  it('clicks through an actual Source write and keeps a second instance isolated', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'mnemon-runtime-client-'))
    const runner = new MemoryCompositionRunner()
    try {
      await runner.mount(strategy, { instanceId: 'strategy' })
      for (const id of ['work', 'personal']) await runner.mount(plugin, { instanceId: id, config: { dataDir: join(directory, id) } })
      const work = await runner.managementClient('source:work')
      const personal = await runner.managementClient('source:personal')
      const base = { sourceTypeId: 'runtime', sourceInstances: [], locale: 'en', writable: true }
      const view = render(<RuntimeSourcePage {...base} sourceInstanceKey="source:work" management={work} />)
      fireEvent.click(await screen.findByRole('button', { name: t('runtime.addButton') }))
      const textarea = await screen.findByRole('textbox', { name: t('runtime.content') })
      await waitFor(() => expect(document.querySelector('button[type="submit"]')?.hasAttribute('disabled')).toBe(true))
      fireEvent.change(textarea, { target: { value: 'Source-owned runtime entry' } })
      fireEvent.click(screen.getByRole('button', { name: t('runtime.addAction') }))
      expect(await screen.findByText('Source-owned runtime entry')).not.toBeNull()
      expect((await work.read('snapshot')).value).toMatchObject({ entries: [expect.objectContaining({ content: 'Source-owned runtime entry' })] })
      view.rerender(<RuntimeSourcePage {...base} sourceInstanceKey="source:personal" management={personal} />)
      await waitFor(() => expect(screen.queryByText('Source-owned runtime entry')).toBeNull())
      expect((await personal.read('snapshot')).value).toMatchObject({ entries: [] })
      view.rerender(<RuntimeSourcePage {...base} writable={false} sourceInstanceKey="source:personal" management={personal} />)
      expect(screen.queryByRole('textbox', { name: t('runtime.content') })).toBeNull()
    } finally { cleanup(); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })

  it('clears branch restrictions through the Sidebar editor and real Source', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'mnemon-runtime-editor-'))
    const runner = new MemoryCompositionRunner()
    try {
      await runner.mount(strategy, { instanceId: 'strategy' })
      await runner.mount(plugin, { instanceId: 'work', config: { dataDir: directory } })
      const management = await runner.managementClient('source:work')
      await management.mutate('mutate', { action: 'add', target: 'memory', content: 'branch-scoped note', branches: ['main'] }, { confirmed: true })
      render(<RuntimeSourcePage sourceTypeId="runtime" sourceInstanceKey="source:work" sourceInstances={[]} locale="en" writable management={management} />)
      await screen.findByText('branch-scoped note')
      fireEvent.click(screen.getByRole('button', { name: t('runtime.editAction') }))
      fireEvent.change(screen.getByRole('textbox', { name: t('runtime.branches') }), { target: { value: '' } })
      fireEvent.click(screen.getByRole('button', { name: t('runtime.saveEdit') }))
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      const { value } = await management.read('snapshot')
      expect((value as { entries: object[] }).entries[0]).not.toHaveProperty('branches')
    } finally { cleanup(); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })

  it('edits and removes exact entries through the real Source without changing containing entries or another instance', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'mnemon-runtime-exact-client-'))
    const runner = new MemoryCompositionRunner()
    try {
      await runner.mount(strategy, { instanceId: 'strategy' })
      for (const id of ['work', 'personal']) await runner.mount(plugin, { instanceId: id, config: { dataDir: join(directory, id) } })
      const work = await runner.managementClient('source:work')
      const personal = await runner.managementClient('source:personal')
      for (const client of [work, personal]) {
        await client.mutate('mutate', { action: 'add', target: 'memory', content: 'EGO_LINUX_CHROME' }, { confirmed: true })
        await client.mutate('mutate', { action: 'add', target: 'memory', content: 'X', branches: ['main'] }, { confirmed: true })
      }
      const otherBefore = await personal.read('snapshot')
      const props = { sourceTypeId: 'runtime', sourceInstanceKey: 'source:work', sourceInstances: [], locale: 'en', management: work }
      const view = render(<RuntimeSourcePage {...props} writable={false} />)
      await screen.findByText('X')
      expect(screen.queryByRole('button', { name: t('runtime.editAction') })).toBeNull()
      expect(screen.queryByRole('button', { name: t('runtime.removeAction') })).toBeNull()

      view.rerender(<RuntimeSourcePage {...props} writable />)
      fireEvent.click(within(screen.getByText('X').closest('article')!).getByRole('button', { name: t('runtime.editAction') }))
      fireEvent.change(screen.getByRole('textbox', { name: t('runtime.editContent') }), { target: { value: 'LINUX' } })
      fireEvent.click(screen.getByRole('button', { name: t('runtime.saveEdit') }))
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      expect((await work.read('snapshot')).value).toMatchObject({ entries: [
        { content: 'EGO_LINUX_CHROME' }, { content: 'LINUX', branches: ['main'] },
      ] })

      fireEvent.click(within(screen.getByText('LINUX').closest('article')!).getByRole('button', { name: t('runtime.removeAction') }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: t('runtime.removeAction') }))
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      expect(screen.queryByText('LINUX')).toBeNull()
      expect(screen.getByText('EGO_LINUX_CHROME')).not.toBeNull()
      expect((await work.read('snapshot')).value).toMatchObject({ entries: [{ content: 'EGO_LINUX_CHROME' }] })
      const otherAfter = await personal.read('snapshot')
      expect(otherAfter.revision).toBe(otherBefore.revision)
      expect(otherAfter.value).toMatchObject({ entries: [
        { content: 'EGO_LINUX_CHROME' }, { content: 'X', branches: ['main'] },
      ] })
    } finally { cleanup(); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })

  it('owns one complete, disposable page contribution', () => {
    const entries = new Map<string, unknown>()
    const release = installRuntimeMemoryUI({ slots: {
      inject: (_name: string, factory: () => () => void) => factory(),
      register: (options: { id: string }, component: unknown) => { entries.set(options.id, component); return () => entries.delete(options.id) },
    } } as never)
    expect([...entries.keys()]).toEqual(['runtime/entries'])
    expect(entries.get('runtime/entries')).toEqual(expect.any(Function))
    release()
    expect(entries.size).toBe(0)
  })
})
