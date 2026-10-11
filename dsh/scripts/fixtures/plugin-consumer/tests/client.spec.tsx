// @vitest-environment jsdom
import { mkdtempSync, rmSync } from 'node:fs'
import { createRequire } from 'node:module'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { loadMemoryClientArtifact, MemoryCompositionRunner } from 'dsh-mnemon/testing'
import * as runtime from 'dsh-mnemon-source-runtime'
import * as focus from '../lib/external-strategy.js'

const require = createRequire(import.meta.url)
const dependencies = {
  react: await vi.importActual('react'),
  'react/jsx-runtime': await vi.importActual('react/jsx-runtime'),
  'react-dom': await vi.importActual('react-dom'),
  '@deepseek-ai/dsh-client-ui-primitives': await vi.importActual('@deepseek-ai/dsh-client-ui-primitives'),
}
const core = loadMemoryClientArtifact<typeof import('dsh-mnemon/client')>(require.resolve('dsh-mnemon/client'), dependencies)
const sourceDependencies = { ...dependencies, 'dsh-mnemon/client': core, 'markdown-to-jsx': require('markdown-to-jsx') }
const runtimeClient = loadMemoryClientArtifact<typeof import('dsh-mnemon-source-runtime/client')>(require.resolve('dsh-mnemon-source-runtime/client'), sourceDependencies)
const documentsClient = loadMemoryClientArtifact<typeof import('dsh-mnemon-source-documents/client')>(require.resolve('dsh-mnemon-source-documents/client'), sourceDependencies)
const spacesClient = loadMemoryClientArtifact<typeof import('dsh-mnemon-source-memory-spaces/client')>(require.resolve('dsh-mnemon-source-memory-spaces/client'), sourceDependencies)
afterEach(cleanup)

describe('compiled Source clients with compiled Core and no source aliases', () => {
  it('loads Source copy and styles even when the shared kit has stale business presentation', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'source-presentation-'))
    const runner = new MemoryCompositionRunner()
    const sharedCopy = () => 'stale shared copy'
    const oldKit = { ...core, useT: () => sharedCopy,
      memoryPageStyles: Object.fromEntries(Object.entries(core.memoryPageStyles).filter(([key]) => !key.startsWith('runtime'))),
      memorySidebarStyles: Object.fromEntries(Object.entries(core.memorySidebarStyles).filter(([key]) => !key.startsWith('runtime'))),
    }
    const owned = loadMemoryClientArtifact<typeof import('dsh-mnemon-source-runtime/client')>(require.resolve('dsh-mnemon-source-runtime/client'), {
      ...sourceDependencies, 'dsh-mnemon/client': oldKit,
    })
    try {
      await runner.mount(focus, { instanceId: 'focus', config: { sourceKeys: ['source:work'], mode: 'eager' } })
      await runner.mount(runtime, { instanceId: 'work', config: { dataDir: directory } })
      const management = await runner.managementClient('source:work')
      const view = render(<owned.RuntimeSourcePage sourceTypeId="runtime" sourceInstanceKey="source:work" sourceInstances={[]} locale="en" writable management={management} />)
      expect(await screen.findByRole('heading', { name: core.translateEn('runtime.title') })).not.toBeNull()
      expect(view.container.querySelector('[class*="runtimeSummaryCard"]')).not.toBeNull()
      const style = document.querySelector('style[data-plugin-css="dsh-mnemon-source-runtime/presentation/page.module.css"]')
      expect(style?.textContent).toContain('runtimeSummaryCard')
    } finally { cleanup(); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })

  it('registers all Source-owned pages and disposes them through the shared public UI contract', () => {
    const pages = new Map<string, unknown>()
    const slots = {
      inject: (_name: string, factory: () => () => void) => factory(),
      register: (options: { id: string }, component: unknown) => { pages.set(options.id, component); return () => pages.delete(options.id) },
    }
    const releases = [
      runtimeClient.installRuntimeMemoryUI({ slots } as never),
      documentsClient.installDocumentsMemoryUI({ slots } as never),
      spacesClient.installMemorySpacesUI({ slots } as never),
    ]
    expect(pages.get('runtime/entries')).toEqual(expect.any(Function))
    expect([...pages.keys()].some(key => key.startsWith('documents/'))).toBe(true)
    expect([...pages.keys()].some(key => key.startsWith('memory-spaces/'))).toBe(true)
    for (const release of releases.reverse()) release()
    expect(pages.size).toBe(0)
  })

  it('clicks a compiled Runtime page backed by a packed Source and keeps instances isolated', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'compiled-runtime-client-'))
    const runner = new MemoryCompositionRunner()
    try {
      await runner.mount(focus, { instanceId: 'focus', config: { sourceKeys: ['source:work', 'source:personal'], mode: 'eager' } })
      for (const id of ['work', 'personal']) await runner.mount(runtime, { instanceId: id, config: { dataDir: join(directory, id) } })
      const work = await runner.managementClient('source:work')
      const personal = await runner.managementClient('source:personal')
      const base = { sourceTypeId: 'runtime', sourceInstances: [], locale: 'en', writable: true }
      const Page = runtimeClient.RuntimeSourcePage
      const view = render(<Page {...base} sourceInstanceKey="source:work" management={work} />)
      fireEvent.click(await screen.findByRole('button', { name: core.translateEn('runtime.addButton') }))
      const textarea = await screen.findByRole('textbox', { name: core.translateEn('runtime.content') })
      fireEvent.change(textarea, { target: { value: 'compiled UI sentinel' } })
      fireEvent.click(screen.getByRole('button', { name: core.translateEn('runtime.addAction') }))
      expect(await screen.findByText('compiled UI sentinel')).not.toBeNull()
      expect((await work.read('snapshot')).value).toMatchObject({ entries: [expect.objectContaining({ content: 'compiled UI sentinel' })] })
      view.rerender(<Page {...base} sourceInstanceKey="source:personal" management={personal} />)
      await waitFor(() => expect(screen.queryByText('compiled UI sentinel')).toBeNull())
      expect((await personal.read('snapshot')).value).toMatchObject({ entries: [] })
    } finally { cleanup(); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })
})
