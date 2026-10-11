import { afterEach, describe, expect, it, vi } from 'vitest'
import type { HostAgent, HostContextShape, ToolDefinition } from '../src/host/dsh.ts'
import type { DocumentMutationResult, DocumentSnapshot, DocumentView } from 'dsh-mnemon-source-documents/contracts'
import type { Config } from '../src/host/config.ts'
import { registerTools } from '../src/host/tools.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { agentScope } from '../src/host/runtime.ts'
import { compositionFixture } from './fixtures/composition.ts'

const releases: Array<() => Promise<void>> = []
afterEach(async () => { for (const release of releases.splice(0).reverse()) await release() })

async function fixture(options: Config = {}) {
  const f = await compositionFixture(options)
  releases.push(f.dispose)
  const root = { id: 'root', session: { header: { cwd: f.workspace } } } as HostAgent
  const child = { id: 'review-child', session: { header: { cwd: f.workspace, origin: 'subagent', parentSession: root.id } } } as HostAgent
  const registered: ToolDefinition[] = []
  const coordinator = { document: vi.fn(), archiveDocument: vi.fn() } as unknown as MnemonSubagentCoordinator
  registerTools({ tools: { register: (tool: ToolDefinition) => { registered.push(tool) } } } as unknown as HostContextShape, f.live, coordinator)
  const documents = f.graph.source('documents')
  const turn = async () => {
    const pinned = await f.graph.composableTurns.beginTurn('root:documents', agentScope(root, f.config), 'test')
    f.graph.composableTurns.pinTurn('child:documents', agentScope(child, f.config), pinned.view.id)
    return pinned
  }
  return { ...f, root, child, registered, coordinator, documents, turn,
    create: registered.find(tool => tool.name === 'mnemon_document_create')!, signal: new AbortController().signal }
}

describe('Host document creation protection', () => {
  it('creates through the offered Source Action and preserves the user original', async () => {
    const f = await fixture()
    const original = await f.documents.mutate<DocumentMutationResult>('mutate', {
      action: 'create', title: 'Deployment code', content: '# Original\n\n```ts\nconst canary = true\n```', sessionIds: ['user-session'],
    })
    await f.turn()
    const args = { title: 'Deployment follow-up', content: 'A separate rollout note.', sourcePaths: ['README.md'] }
    const result = await f.create.execute(args as never, { agent: f.child, signal: f.signal })
    expect(result).toMatchObject({ action: 'created', document: { title: args.title, content: args.content, sessionIds: [f.child.id] },
      memoryReceipt: { completion: 'committed' } })
    expect(await f.documents.read<DocumentView>('document', { id: original.document.id })).toMatchObject({
      content: original.document.content, contentHash: original.document.contentHash, revision: original.document.revision,
    })
    expect(await f.documents.read<DocumentSnapshot>('snapshot')).toMatchObject({ activeCount: 2, archivedCount: 0 })
    expect(f.coordinator.document).not.toHaveBeenCalled()
    expect(f.coordinator.archiveDocument).not.toHaveBeenCalled()
  })

  it('rejects an update disguised as creation at the actual Source boundary', async () => {
    const f = await fixture()
    const original = await f.documents.mutate<DocumentMutationResult>('mutate', { action: 'create', title: 'Original code', content: 'Exact user code.' })
    await f.turn()
    await expect(f.create.execute({ title: 'Summary', content: 'Condensed.', action: 'update', id: original.document.id } as never,
      { agent: f.child, signal: f.signal })).rejects.toThrow()
    expect(await f.documents.read<DocumentView>('document', { id: original.document.id })).toMatchObject({ content: 'Exact user code.', revision: 1 })
    expect(await f.documents.read<DocumentSnapshot>('snapshot')).toMatchObject({ revision: original.snapshot.revision, activeCount: 1 })
  })

  it.each<[string, Config]>([
    ['read-only mode', { writeEnabled: false }],
    ['disabled Documents', { memoryTopology: { layers: { documents: { enabled: false } } } }],
    ['manual-only Documents', { memoryTopology: { layers: { documents: { participation: { write: 'manual' } } } } }],
  ])('rejects creation in %s before mutation', async (_name, options) => {
    const f = await fixture(options)
    expect(() => f.create.execute({ title: 'Blocked', content: 'Blocked candidate.' } as never, { agent: f.child, signal: f.signal })).toThrow()
    expect(f.coordinator.document).not.toHaveBeenCalled()
    expect(f.coordinator.archiveDocument).not.toHaveBeenCalled()
  })

  it('keeps explicit root document updates on the coordinated management path', async () => {
    const f = await fixture()
    const manage = f.registered.find(tool => tool.name === 'mnemon_document_manage')!
    await manage.execute({ action: 'update', id: 'document-id', content: 'User-requested revision.' } as never, { agent: f.root, signal: f.signal })
    expect(f.coordinator.document).toHaveBeenCalledWith(f.root, {
      action: 'update', id: 'document-id', content: 'User-requested revision.', sessionIds: [f.root.id],
    }, f.signal)
  })
})
