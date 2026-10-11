// @vitest-environment jsdom
import { mkdtempSync, mkdirSync, rmSync } from 'node:fs'
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
import { DocumentsSourcePage, installDocumentsMemoryUI } from '../src/client.ts'
import { DocumentsPage } from '../src/client/pages.tsx'
import type { DocumentsPageClient } from '../src/client/api.ts'
import type { DocumentSnapshot, DocumentView } from '../src/contracts.ts'

describe('independent Documents Source client', () => {
  it('orders active, archived and searched documents by creation before pagination, including after edits', async () => {
    const records: Array<DocumentView & { healthy: boolean; excerpt: string }> = [3, 1, 2, 4, 5, 6, 7, 8, 9, 10, 11, 12].map(day => ({
      id: `doc-${day}`, title: `Document ${day}`, description: '', content: `Searchable evidence ${day}`, healthy: true, excerpt: '',
      filename: `${day}.md`, relativePath: `documents/${day}.md`, sourcePaths: [], sessionIds: [], memoryBodyIds: [],
      status: day > 10 ? 'archived' : 'active', revision: day === 1 ? 2 : 1, contentHash: 'fixture', sizeBytes: 100,
      createdAt: `2026-08-${String(day).padStart(2, '0')}T08:00:00.000Z`, updatedAt: '2026-09-01T08:00:00.000Z', lastAccessedAt: '2026-09-01T08:00:00.000Z',
    }))
    Object.freeze(records)
    const snapshot: DocumentSnapshot = {
      documents: records, workspaceRoot: '/workspace', directory: '/documents', indexPath: '/documents/index.json',
      generatedAt: '2026-09-01T08:00:00Z', revision: 'fixture', limitBytes: 10000, activeBytes: 1000, activeCount: 10, archivedCount: 2, total: 12,
    }
    const client: DocumentsPageClient = {
      documents: async () => snapshot,
      document: async id => records.find(record => record.id === id)!,
      searchDocuments: async query => ({ query, includeArchived: true, total: records.length, generatedAt: snapshot.generatedAt, results: records.map(record => ({ ...record, score: 1 })) }),
      mutateDocument: vi.fn(), archiveDocument: vi.fn(),
    }
    const page = (writable: boolean) => <MemorySourcePageFrame locale="en"><DocumentsPage client={client} revision={0} writeEnabled={writable} onMutate={() => {}} /></MemorySourcePageFrame>
    const view = render(page(false))
    const listElement = await screen.findByLabelText(t('documents.list'))
    const list = within(listElement)
    const titles = () => Array.from(listElement.querySelectorAll('button strong'), item => item.textContent)
    await waitFor(() => expect(titles()).toEqual([10, 9, 8, 7, 6, 5, 4, 3].map(day => `Document ${day}`)))
    expect(await within(screen.getByLabelText(t('documents.reader'))).findByText('Document 10')).not.toBeNull()
    expect(screen.queryByText(t('documents.edit'))).toBeNull()
    fireEvent.click(list.getByText(t('common.showMore', { count: 2 })))
    expect(titles().slice(-2)).toEqual(['Document 2', 'Document 1'])
    expect(list.getByText('Document 1').closest('button')?.querySelector('time')?.dateTime).toBe('2026-08-01T08:00:00.000Z')
    fireEvent.change(screen.getByLabelText(t('documents.searchAria')), { target: { value: 'Searchable evidence' } })
    fireEvent.click(screen.getByText(t('documents.search')))
    await waitFor(() => expect(screen.queryByText(t('common.loading'))).toBeNull())
    expect(titles()).toEqual([10, 9, 8, 7, 6, 5, 4, 3].map(day => `Document ${day}`))
    fireEvent.click(within(screen.getByLabelText(t('documents.scope'))).getByText(t('documents.archivedCount')))
    await waitFor(() => expect(titles()).toEqual(['Document 12', 'Document 11']))
    fireEvent.change(screen.getByLabelText(t('documents.searchAria')), { target: { value: '' } })
    fireEvent.click(screen.getByText(t('documents.search')))
    await waitFor(() => expect(screen.queryByText(t('common.loading'))).toBeNull())
    expect(titles()).toEqual(['Document 12', 'Document 11'])
    view.rerender(page(true))
    fireEvent.click(within(screen.getByLabelText(t('documents.scope'))).getByText(t('documents.active')))
    expect(await screen.findByText(t('documents.edit'))).not.toBeNull()
    expect(client.mutateDocument).not.toHaveBeenCalled()
    expect(client.archiveDocument).not.toHaveBeenCalled()
  })

  it('creates and edits through the Source management contract without a legacy session', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'mnemon-documents-client-'))
    const workspace = join(directory, 'workspace')
    mkdirSync(workspace)
    const runner = new MemoryCompositionRunner()
    try {
      await runner.mount(strategy, { instanceId: 'strategy' })
      await runner.mount(plugin, { instanceId: 'notes', config: { dataDir: join(directory, 'data') } })
      const management = await runner.managementClient('source:notes', { storage: 'custom', workspaceId: workspace })
      render(<DocumentsSourcePage sourceTypeId="documents" sourceInstanceKey="source:notes" sourceInstances={[]} locale="en" writable management={management} />)
      fireEvent.click(await screen.findByRole('button', { name: t('documents.new') }))
      fireEvent.change(screen.getByLabelText(t('documents.name')), { target: { value: 'Owned document' } })
      fireEvent.change(screen.getByLabelText(t('documents.markdown')), { target: { value: 'Independent document content' } })
      fireEvent.click(screen.getByRole('button', { name: t('documents.create') }))
      expect(await screen.findByRole('heading', { name: 'Owned document' })).not.toBeNull()
      fireEvent.click(screen.getByRole('button', { name: t('documents.edit') }))
      fireEvent.change(screen.getByLabelText(t('documents.markdown')), { target: { value: 'Edited through Source management' } })
      fireEvent.click(screen.getByRole('button', { name: t('documents.save') }))
      expect(await screen.findByText('Edited through Source management')).not.toBeNull()
      expect((await management.read('snapshot')).value).toMatchObject({ total: 1 })
    } finally { cleanup(); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })

  it('opens the document a conversation turn names', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'mnemon-documents-focus-'))
    const workspace = join(directory, 'workspace')
    mkdirSync(workspace)
    const runner = new MemoryCompositionRunner()
    try {
      await runner.mount(strategy, { instanceId: 'strategy' })
      await runner.mount(plugin, { instanceId: 'notes', config: { dataDir: join(directory, 'data') } })
      const management = await runner.managementClient('source:notes', { storage: 'custom', workspaceId: workspace })
      const created: string[] = []
      for (const title of ['Queue decision', 'Release checklist']) {
        const result = await management.mutate('mutate', { action: 'create', title, description: '', content: `${title} body` }, { confirmed: true })
        created.push((result.value as { document: { id: string } }).document.id)
      }
      render(<DocumentsSourcePage sourceTypeId="documents" sourceInstanceKey="source:notes" sourceInstances={[]} locale="en" management={management} navigationInput={{ seed: created[0]!, nonce: 1 }} />)
      expect(await within(await screen.findByLabelText(t('documents.reader'))).findByRole('heading', { name: 'Queue decision' })).not.toBeNull()
      expect(screen.getByRole('button', { name: /Queue decision/, pressed: true })).not.toBeNull()
    } finally { cleanup(); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })

  it('owns its page registration and releases it', () => {
    const entries = new Set<string>()
    const release = installDocumentsMemoryUI({ slots: {
      inject: (_name: string, factory: () => () => void) => factory(),
      register: (options: { id: string }) => { entries.add(options.id); return () => entries.delete(options.id) },
    } } as never)
    expect([...entries]).toEqual(['documents/library'])
    release()
    expect(entries.size).toBe(0)
  })
})
