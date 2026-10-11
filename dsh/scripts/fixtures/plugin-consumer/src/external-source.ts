import { randomUUID } from 'node:crypto'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, isAbsolute } from 'node:path'
import type { Context } from '@deepseek-ai/cordis'
import { COMPOSABLE_MEMORY_API_VERSION, type MemoryJsonValue } from 'dsh-mnemon/contracts'
import {
  defineMemoryPlugin, defineMemorySource, installMemory, memoryConfigurationDigest, createMemoryMutationReceipt,
  memoryInputRecord, memoryInputText, truncateMemoryText,
} from 'dsh-mnemon/extension-sdk'

export const name = 'dsh-mnemon-source-external-notes'
export const inject = ['mnemonMemory']
export const memoryPlugin = defineMemoryPlugin({
  packageName: name,
  label: { en: 'External notes', 'zh-CN': '外部笔记' },
  description: { en: 'A Source-owned file and management protocol.', 'zh-CN': '由 Source 自主管理的文件与协议。' },
  roles: ['source'],
  provides: [{ id: 'source' }, { id: 'source.notes' }],
})
export interface Config { path: string }

/** A deliberately small external Source: the file, protocol and snapshots are its own. */
export function apply(ctx: Context, config: Config): void {
  if (!isAbsolute(config.path)) throw new Error('notes path must be absolute')
  const path = config.path
  const read = () => existsSync(path) ? readFileSync(path, 'utf8') : ''
  const revision = (text: string) => memoryConfigurationDigest({ text })
  const save = (input: MemoryJsonValue) => {
    const content = memoryInputText(memoryInputRecord(input, 'notes input').content, 'content', 8_000)!
    mkdirSync(dirname(path), { recursive: true })
    writeFileSync(path, content, { mode: 0o600 })
    return { revision: revision(content), value: { content } }
  }
  const definition = defineMemorySource({
    manifest: {
      apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'external-notes', packageName: name,
      role: 'notes', capabilities: ['project', 'read', 'write'], consistency: 'exact-snapshot',
      routes: [{ id: 'read', description: 'Read the exact notes captured by this View.', capability: 'read',
        inputSchema: { type: 'object', properties: {}, additionalProperties: false }, maxCalls: 3, maxResults: 1, maxCharacters: 8_000 }],
      actions: [{ id: 'replace', description: 'Replace this instance of notes.', capability: 'write',
        inputSchema: { type: 'object', required: ['content'], properties: { content: { type: 'string' } }, additionalProperties: false } }],
      management: { label: 'External notes', description: 'A Source-owned file and management protocol.' },
    },
    create(context) {
      const snapshots = new WeakMap<object, string>()
      return {
        facts(request) {
          const captured = read()
          snapshots.set(request.scope, captured)
          return { sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'external-notes', role: 'notes', availability: 'ready',
            revision: revision(captured), capabilities: ['project', 'read', 'write'], routeIds: ['read'], actionIds: ['replace'] }
        },
        project(request) {
          const captured = snapshots.get(request.scope)
          snapshots.delete(request.scope)
          if (captured === undefined) throw new Error('missing request snapshot')
          const rev = revision(captured)
          if (rev !== request.expectedRevision) throw new Error('snapshot revision mismatch')
          return {
            fragments: request.includeProjection ? [{ id: 'notes', sourceInstanceKey: context.sourceInstanceKey, revision: rev,
              mode: request.mode, text: truncateMemoryText(captured, request.maxCharacters) }] : [],
            readGrant: { id: context.sourceInstanceKey + '/notes', sourceInstanceKey: context.sourceInstanceKey,
              schema: 'external-notes/v1', consistency: 'exact-snapshot', revision: rev, value: { text: captured } },
          }
        },
        query(request) {
          const text = memoryInputRecord(request.grant.value, 'notes grant').text as string
          return { id: randomUUID(), viewId: request.view.id, routeId: request.route.id, sourceInstanceKey: context.sourceInstanceKey,
            observedAt: new Date().toISOString(), truncated: false,
            items: [{ id: 'notes', text, revision: request.grant.revision, provenance: { source: context.sourceInstanceKey } }] }
        },
        mutate(request) {
          const saved = save(request.input)
          return createMemoryMutationReceipt(request.view.id, request.offer.id, context.sourceInstanceKey, saved.revision, saved.value, 'committed')
        },
        manage(request) {
          if (request.mode === 'read' && request.operation === 'read') {
            const content = read()
            return { revision: revision(content), value: { content } }
          }
          if (request.mode !== 'mutate' || request.operation !== 'replace') throw new Error('unsupported notes operation')
          if (!request.confirmed || request.expectedRevision !== revision(read())) throw new Error('confirmation or notes revision conflict')
          return save(request.input)
        },
      }
    },
  })
  installMemory(ctx, { plugin: memoryPlugin, sources: [definition] }, { effectiveDigest: memoryConfigurationDigest(config) })
}
