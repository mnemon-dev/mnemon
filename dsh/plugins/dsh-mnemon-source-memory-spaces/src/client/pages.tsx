import { css, sidebarCss, useT } from './presentation.ts'
import type { JSX } from 'react'
import { useCallback, useEffect, useId, useMemo, useRef, useState, type FormEvent, type PointerEvent as ReactPointerEvent } from 'react'
import { writeClipboard } from '@deepseek-ai/dsh-client-ui-primitives'
import { CATEGORIES, normalizeEntityKey, type Category, type EntityMemoriesView, type EntityRelatedView, type EntityView, type Insight, type MemorySpaceCatalog, type MemorySpaceMetadataUpdate, type MemorySpaceProvider, type MemorySpaceView, type MemoryGraphNode, type MemoryGraphSnapshot, type MemoryPlacementCapability, type MemoryPlacementPreference, type MemoryListView, type MemoryProviderConfigField, type MemoryProviderConnection, type MemoryProviderDescriptor, type MemoryProviderId, type MemoryReadSource } from '../contracts.ts'
import type { MemorySpacesPageClient } from './api.ts'
import { ProviderIcon } from './ProviderIcon.tsx'
import { providerFieldLabel, providerOptionLabel, providerSummary } from './provider-presentation.ts'
import { type MnemonKey, type MnemonTranslate, useRequestVersion, appearanceClass, useLocale, humanBytes, message, short, PageHeader, SectionSpinner, ProgressiveFooter, SidebarModal, EmptyState, SearchField, SelectField, TaskAgentTag, WriteReceipt } from 'dsh-mnemon/client'

import type { MemoryPersistenceStrategy } from '../contracts.ts'

export interface MemoryPlacementSettings { setPath(path: string[], value: unknown): Promise<void> }
export interface MemoryPlacementPageConfig { persistenceStrategy?: MemoryPersistenceStrategy }
export interface MemorySpacesPageStatus { defaultRecallLimit?: number }

type MemoryPlacementMode = 'manual' | 'automatic'

type ProviderDrafts = Partial<Record<MemoryProviderId, MemoryProviderConnection>>

function memoryProviderFields(provider: MemoryProviderDescriptor): MemoryProviderConfigField[] {
  return provider.fields.filter(field => field.scope !== 'service')
}

function providerDefaults(provider: MemoryProviderDescriptor): MemoryProviderConnection {
  return Object.fromEntries(memoryProviderFields(provider).flatMap(field => field.defaultValue === undefined ? [] : [[field.key, field.defaultValue]]))
}

function mergeProviderDefaults(providers: readonly MemoryProviderDescriptor[], current: ProviderDrafts): ProviderDrafts {
  return Object.fromEntries(providers.map(provider => [provider.id, { ...providerDefaults(provider), ...(current[provider.id] ?? {}) }]))
}

function providerDraftComplete(provider: MemoryProviderDescriptor | undefined, connection: MemoryProviderConnection | undefined): boolean {
  if (provider === undefined || provider.serviceConfigured === false) return false
  return provider.origin === 'native' || memoryProviderFields(provider).every(field => !field.required || String(connection?.[field.key] ?? '').trim() !== '')
}

/** Keep a ready provider; otherwise Mnemon Native while its CLI is available, then the first other ready one. */
function readyProviderId(providers: readonly MemoryProviderDescriptor[], current?: MemoryProviderId): MemoryProviderId | undefined {
  const ready = providers.filter(provider => provider.serviceConfigured !== false)
  return ready.find(provider => provider.id === current)?.id ?? (ready.find(provider => provider.origin === 'native') ?? ready[0])?.id
}

/** Ready Providers first, in catalog order, so the choices that work lead the list. */
function readyFirst(providers: readonly MemoryProviderDescriptor[]): MemoryProviderDescriptor[] {
  return [...providers].sort((left, right) => Number(left.serviceConfigured === false) - Number(right.serviceConfigured === false))
}

/** Why a provider cannot be chosen: Mnemon Native needs its CLI, the others a service configured on the dsh-mnemon page under Plugins. */
function providerUnavailableKey(provider: MemoryProviderDescriptor): 'overview.nativeCliRequired' | 'overview.providerServiceRequired' {
  return provider.origin === 'native' ? 'overview.nativeCliRequired' : 'overview.providerServiceRequired'
}

export function nativeSpaceProvider(provider: MemorySpaceProvider): boolean {
  return provider.origin === 'native'
}

/** Shared memory-level Provider form used by manual creation, editing, and distillation policy. */
function ProviderMemoryFields(props: {
  provider: MemoryProviderDescriptor
  connection: MemoryProviderConnection
  onChange: (key: string, value: string | number | boolean) => void
  body?: MemorySpaceView
  clearSecrets?: string[]
  onClearSecretsChange?: (keys: string[]) => void
}): JSX.Element {
  const t = useT()
  return <div className={css.providerFields} data-provider={props.provider.id}>
    <div className={css.providerFieldHeading}><div className={css.providerFieldIdentity}><ProviderIcon providerId={props.provider.id} icon={props.provider.icon} className={css.providerFieldIcon} /><div><strong>{props.provider.label}</strong><small>{providerSummary(t, props.provider)}</small></div></div><span>{props.provider.kind === 'local' ? t('overview.providerKindLocal') : t('overview.providerKindRemote')} · {t(`overview.workspaceBinding.${props.provider.workspaceBinding}`)}</span></div>
    <div className={css.providerAdvancedGrid}>{memoryProviderFields(props.provider).map(field => {
      const label = providerFieldLabel(t, field)
      const value = props.connection[field.key] ?? ''
      const savedSecret = props.body?.provider.configuredSecrets.includes(field.key) === true
      const clearingSecret = props.clearSecrets?.includes(field.key) === true
      const required = field.required && (!savedSecret || clearingSecret)
      const input = field.input === 'boolean'
        ? <input aria-label={label} type="checkbox" checked={Boolean(value)} onChange={event => props.onChange(field.key, event.target.checked)} />
        : field.input === 'select'
          ? <SelectField hideLabel label={label} value={String(value)} options={(field.options ?? []).map(option => ({ value: option.value, label: providerOptionLabel(t, option) }))} onChange={next => props.onChange(field.key, next)} />
          : <input aria-label={label} type={field.input === 'secret' ? 'password' : field.input === 'number' ? 'number' : field.input === 'url' ? 'url' : 'text'} value={String(value)} required={required} autoComplete={field.input === 'secret' ? 'new-password' : undefined} placeholder={savedSecret ? t('overview.providerApiKeyKeep') : field.placeholder ?? (field.input === 'secret' ? t('overview.providerApiKeyOptional') : undefined)} maxLength={field.maxLength ?? (field.input === 'secret' ? 8000 : 2000)} min={field.min} max={field.max} pattern={field.pattern} step={field.input === 'number' ? 'any' : undefined} onChange={event => props.onChange(field.key, event.target.value)} />
      return <div key={field.key} className={css.providerFieldControl}><label>{label}{input}</label>{props.body !== undefined && field.input === 'secret' && savedSecret && props.onClearSecretsChange !== undefined && <label className={css.providerSecretClear}><input type="checkbox" checked={clearingSecret} onChange={event => props.onClearSecretsChange!(event.target.checked ? [...new Set([...(props.clearSecrets ?? []), field.key])] : (props.clearSecrets ?? []).filter(key => key !== field.key))} />{t('overview.providerSecretClear')}</label>}</div>
    })}</div>
    <small className={css.providerWriteHint}>{props.provider.capabilities.writeMode === 'exact' ? t('overview.providerWriteExact') : t('overview.providerWriteAsync')} · {props.provider.capabilities.graph ? t('overview.providerGraphReady') : t('overview.providerSearchReady')}</small>
  </div>
}

const CATEGORY_KEYS: Record<string, MnemonKey> = {
  decision: 'category.decision',
  preference: 'category.preference',
  fact: 'category.fact',
  insight: 'category.insight',
  context: 'category.context',
  general: 'category.general',
}

function categoryLabel(t: MnemonTranslate, category: string): string { return CATEGORY_KEYS[category] === undefined ? category : t(CATEGORY_KEYS[category]!) }

function insightKey(insight: Insight): string {
  return `${insight.memoryBodyId ?? 'memory'}:${insight.id}`
}

function MemoryProviderBadge(props: { providerId: MemoryProviderId; label: string }): JSX.Element {
  return <span className={css.providerBadge} data-provider={props.providerId} title={props.label}>{props.label}</span>
}

function ReadSourcePanel(props: {
  title: string
  hint?: string
  sources: readonly MemoryReadSource[]
  selectedBodyId?: string | undefined
  onSelect?: (memoryBodyId: string | undefined) => void
}): JSX.Element | null {
  const t = useT()
  if (props.sources.length === 0) return null
  const content = (source: MemoryReadSource) => <>
    <span className={css.readSourceSignal} aria-hidden="true" />
    <span className={css.readSourceIdentity}><strong>{source.memoryBodyName}</strong><span className={css.readSourceMeta}><MemoryProviderBadge providerId={source.providerId} label={source.providerLabel} /><small>{t(`readSources.model.${source.providerId}` as MnemonKey)}</small></span></span>
    <span className={css.readSourceState}><em>{t(`readSources.mode.${source.mode}` as MnemonKey)}</em><small>{source.mode === 'entities' && source.status === 'ready'
      ? t(source.complete === false ? 'entities.sourcePartial' : 'entities.sourceReady', { count: source.itemCount })
      : t(`readSources.status.${source.status}` as MnemonKey, { count: source.itemCount })}{source.edgeCount === undefined || source.edgeCount === 0 ? '' : ` · ${t('readSources.edges', { count: source.edgeCount })}`}</small></span>
  </>
  return <section className={css.readSources} aria-label={props.title}>
    <header><div><strong>{props.title}</strong>{props.hint !== undefined && <p>{props.hint}</p>}</div>{props.onSelect !== undefined && <button type="button" aria-pressed={props.selectedBodyId === undefined} data-selected={props.selectedBodyId === undefined ? '' : undefined} onClick={() => props.onSelect?.(undefined)}>{t('readSources.all')}</button>}</header>
    <div>{props.sources.map(source => props.onSelect === undefined
      ? <article key={source.memoryBodyId} className={css.readSourceCard} data-provider={source.providerId} data-mode={source.mode} data-status={source.status} title={source.hint}>{content(source)}</article>
      : <button key={source.memoryBodyId} type="button" className={css.readSourceCard} data-provider={source.providerId} data-mode={source.mode} data-status={source.status} aria-pressed={props.selectedBodyId === source.memoryBodyId} data-selected={props.selectedBodyId === source.memoryBodyId || undefined} title={source.hint} onClick={() => props.onSelect?.(props.selectedBodyId === source.memoryBodyId ? undefined : source.memoryBodyId)}>{content(source)}</button>,
    )}</div>
  </section>
}

/** Full-text popup for a selected graph node whose inspector preview is clamped. */
function ContentPreview(props: { node: MemoryGraphNode; kind: string; onClose: () => void }): JSX.Element {
  const t = useT()
  const meta = [props.kind, props.node.id, props.node.memoryBodyName].filter((entry): entry is string => entry !== undefined).join(' · ')
  return (
    <SidebarModal title={t('overview.previewTitle')} description={meta} onClose={props.onClose}>
      <p className={css.previewContent}>{props.node.content}</p>
    </SidebarModal>
  )
}

function InsightCard(props: {
  insight: Insight
  writeEnabled: boolean
  onForget: (insight: Insight) => Promise<void>
  onRelated?: (insight: Insight) => void
  onClone?: (insight: Insight) => void
}): JSX.Element {
  const t = useT()
  const [confirming, setConfirming] = useState(false)
  const [forgetting, setForgetting] = useState(false)
  const { insight } = props
  const neutralActionClass = appearanceClass(css.ghostButton, appearanceClass(sidebarCss.itemActionButton, sidebarCss.itemEditAction))
  const forgetActionClass = appearanceClass(css.dangerButton, appearanceClass(sidebarCss.itemActionButton, sidebarCss.itemDangerAction))
  const providerLabel = insight.memoryProviderLabel ?? insight.memoryProviderId
  const supportsRelated = insight.memoryCapabilities?.related === true
  const supportsForget = insight.memoryCapabilities?.forget === true
  const meta = [
    insight.memoryBodyName,
    providerLabel,
    insight.category !== undefined ? categoryLabel(t, insight.category) : undefined,
    insight.importance !== undefined ? t('common.importance', { value: insight.importance }) : undefined,
    insight.score !== undefined ? t('common.score', { value: insight.score.toFixed(2) }) : undefined,
    insight.depth !== undefined ? t('common.hops', { count: insight.depth }) : undefined,
  ].filter((entry): entry is string => entry !== undefined)

  const forget = async () => {
    setForgetting(true)
    try {
      await props.onForget(insight)
    } finally {
      setForgetting(false)
      setConfirming(false)
    }
  }

  return (
    <>
    <article className={css.insightCard}>
      <div className={css.cardTop}>
        <div className={css.badges}>{meta.map(entry => <span key={entry} className={css.badge}>{entry}</span>)}</div>
      </div>
      <p className={css.content}>{insight.content}</p>
      {(insight.tags?.length ?? 0) > 0 && <div className={css.tags}>{insight.tags!.map(tag => <span key={tag}>#{tag}</span>)}</div>}
      {(insight.entities?.length ?? 0) > 0 && <div className={css.entities}>{insight.entities!.map(entity => <span key={entity}>{entity}</span>)}</div>}
      <div className={css.cardActions}>
        {(
          <>
            {props.onRelated !== undefined && supportsRelated && <button type="button" className={neutralActionClass} onClick={() => props.onRelated?.(insight)}>{t('card.related')}</button>}
            {props.onClone !== undefined && <button type="button" className={neutralActionClass} onClick={() => props.onClone?.(insight)}>{t('card.clone')}</button>}
            <button type="button" className={neutralActionClass} onClick={() => void writeClipboard(insight.id)}>{t('common.copyId')}</button>
            {props.writeEnabled && supportsForget && <button type="button" className={forgetActionClass} onClick={() => setConfirming(true)}>{t('card.forget')}</button>}
          </>
        )}
      </div>
    </article>
    {confirming && <SidebarModal title={t('card.confirmText')} description={`${insight.memoryBodyName ?? insight.memoryBodyId ?? ''}${insight.memoryBodyName === undefined && insight.memoryBodyId === undefined ? '' : ' · '}${insight.id}`} busy={forgetting} onClose={() => setConfirming(false)} footer={<div className={css.modalFooterActions}><button type="button" data-dialog-close data-autofocus className={css.secondaryButton} disabled={forgetting} onClick={() => setConfirming(false)}>{t('common.cancel')}</button><button type="button" className={css.dangerSolidButton} disabled={forgetting} onClick={() => void forget()}>{forgetting ? t('card.processing') : t('card.confirmForget')}</button></div>}><div className={css.bodyDeleteConfirm}><div className={css.bodyDeleteSummary}><p className={css.bodyDeleteContent}>{insight.content}</p><span>{meta.join(' · ')}</span></div></div></SidebarModal>}
    </>
  )
}

const GRAPH_WIDTH = 930

const GRAPH_HEIGHT = 520

const GRAPH_MARGIN_X = 58

const GRAPH_MARGIN_Y = 58

const CATEGORY_ORDER = ['space', 'entity', 'preference', 'decision', 'fact', 'insight', 'context', 'general']

interface GraphPosition { x: number; y: number }

type GraphPositions = Map<string, GraphPosition>

type GraphLayoutMode = 'natural' | 'uniform' | 'custom'

function hash(value: string): number {
  let result = 2166136261
  for (const char of value) result = Math.imul(result ^ char.charCodeAt(0), 16777619)
  return result >>> 0
}

function graphNodeKey(node: MemoryGraphNode): string {
  return node.graphId ?? node.id
}

function graphNodeKind(node: MemoryGraphNode): NonNullable<MemoryGraphNode['kind']> {
  return node.kind ?? 'memory'
}

function spaceGraphId(id: string): string {
  return `space:${id}`
}

function entityGraphId(entity: string): string {
  return `entity:${encodeURIComponent(normalizeEntity(entity))}`
}

/** The graph and the Entities page count an entity by the same key. */
const normalizeEntity = normalizeEntityKey

/** Add routing scopes and entity indexes without issuing another recall. */
function enrichMultiSpaceGraph(graph: MemoryGraphSnapshot, bodies: MemorySpaceView[]): MemoryGraphSnapshot {
  if (graph.nodes.length === 0) return graph
  const memories = graph.nodes.map(node => ({ ...node, kind: 'memory' as const }))
  const memoriesBySpace = new Map<string, MemoryGraphNode[]>()
  for (const node of memories) {
    if (node.memoryBodyId === undefined) continue
    memoriesBySpace.set(node.memoryBodyId, [...(memoriesBySpace.get(node.memoryBodyId) ?? []), node])
  }

  const activeSpaces = bodies.filter(body => body.active && ((memoriesBySpace.get(body.id)?.length ?? 0) > 0 || (body.stats?.topEntities.length ?? 0) > 0))
  const spaceNodes: MemoryGraphNode[] = activeSpaces.map(body => ({
    id: body.id,
    graphId: spaceGraphId(body.id),
    kind: 'space',
    category: 'space',
    content: body.name,
    color: '#22a879',
    memoryBodyId: body.id,
    memoryBodyName: body.name,
    memoryProviderId: body.provider.id,
    occurrenceCount: body.stats?.totalInsights ?? memoriesBySpace.get(body.id)?.length ?? 0,
  }))

  // Native Mnemon entity edges connect two memories. This overview renders
  // entities as first-class nodes, so retaining those edges would falsely make
  // a memory-to-memory edge look like an entity-to-memory association.
  const edges: MemoryGraphSnapshot['edges'] = graph.edges.filter(edge => edge.type !== 'entity')
  for (const body of activeSpaces) {
    for (const memory of memoriesBySpace.get(body.id) ?? []) {
      edges.push({ sourceId: spaceGraphId(body.id), targetId: graphNodeKey(memory), label: 'scope', color: '#708199', type: 'scope' })
    }
  }

  const spacesById = new Map(activeSpaces.map(body => [body.id, body]))
  const indexedEntities = new Map<string, { entity: string; memories: MemoryGraphNode[]; bodies: MemorySpaceView[] }>()
  for (const memory of memories) {
    const body = memory.memoryBodyId === undefined ? undefined : spacesById.get(memory.memoryBodyId)
    if (body === undefined) continue
    const seen = new Set<string>()
    for (const rawEntity of memory.entities ?? []) {
      const entity = rawEntity.trim()
      const key = normalizeEntity(entity)
      if (key === '' || seen.has(key)) continue
      seen.add(key)
      const current = indexedEntities.get(key)
      if (current === undefined) indexedEntities.set(key, { entity, memories: [memory], bodies: [body] })
      else {
        current.memories.push(memory)
        if (!current.bodies.some(candidate => candidate.id === body.id)) current.bodies.push(body)
      }
    }
  }
  const entities = [...indexedEntities.values()].sort((left, right) => right.memories.length - left.memories.length || left.entity.localeCompare(right.entity)).slice(0, 24)
  const entityNodes: MemoryGraphNode[] = entities.map(item => ({
    id: item.entity,
    graphId: entityGraphId(item.entity),
    kind: 'entity',
    category: 'entity',
    content: item.entity,
    color: '#2b9db9',
    occurrenceCount: item.memories.length,
    memoryBodyIds: item.bodies.map(body => body.id),
    memoryBodyNames: item.bodies.map(body => body.name),
  }))
  for (const item of entities) {
    const key = entityGraphId(item.entity)
    for (const memory of item.memories) edges.push({ sourceId: key, targetId: graphNodeKey(memory), label: item.entity, color: '#22a879', type: 'entity' })
  }

  return { ...graph, nodes: [...spaceNodes, ...entityNodes, ...memories], edges }
}

function graphKindLabel(t: MnemonTranslate, node: MemoryGraphNode): string {
  const kind = graphNodeKind(node)
  return kind === 'space' ? t('graph.kindSpace') : kind === 'entity' ? t('graph.kindEntity') : categoryLabel(t, node.category ?? 'general')
}

function activeCategoryAnchors(grouped: Map<string, MemoryGraphNode[]>): Map<string, GraphPosition> {
  const categories = [...grouped.keys()].sort((left, right) => {
    const leftIndex = CATEGORY_ORDER.indexOf(left)
    const rightIndex = CATEGORY_ORDER.indexOf(right)
    return (leftIndex < 0 ? CATEGORY_ORDER.length : leftIndex) - (rightIndex < 0 ? CATEGORY_ORDER.length : rightIndex)
  })
  const anchors = new Map<string, GraphPosition>()
  if (categories.length === 1) {
    anchors.set(categories[0]!, { x: GRAPH_WIDTH / 2, y: GRAPH_HEIGHT / 2 })
    return anchors
  }
  categories.forEach((category, index) => {
    const angle = -Math.PI / 2 + (index / categories.length) * Math.PI * 2
    anchors.set(category, {
      x: GRAPH_WIDTH / 2 + Math.cos(angle) * Math.min(250, 115 + categories.length * 23),
      y: GRAPH_HEIGHT / 2 + Math.sin(angle) * Math.min(165, 78 + categories.length * 15),
    })
  })
  return anchors
}

function clampGraphPosition(position: GraphPosition): GraphPosition {
  return {
    x: Math.min(GRAPH_WIDTH - GRAPH_MARGIN_X, Math.max(GRAPH_MARGIN_X, position.x)),
    y: Math.min(GRAPH_HEIGHT - GRAPH_MARGIN_Y, Math.max(GRAPH_MARGIN_Y, position.y)),
  }
}

function naturalGraphPositions(nodes: MemoryGraphNode[], edges: MemoryGraphSnapshot['edges']): GraphPositions {
  const positions: GraphPositions = new Map()
  const grouped = new Map<string, MemoryGraphNode[]>()
  for (const node of nodes) {
    const category = node.category ?? 'general'
    grouped.set(category, [...(grouped.get(category) ?? []), node])
  }
  const anchors = activeCategoryAnchors(grouped)
  for (const [category, items] of grouped) {
    const anchor = anchors.get(category) ?? { x: GRAPH_WIDTH / 2, y: GRAPH_HEIGHT / 2 }
    items.forEach((node, index) => {
      const seed = hash(graphNodeKey(node))
      const angle = index * 2.399963 + ((seed % 37) / 37) * .4
      const radius = items.length === 1 ? 0 : 24 + Math.sqrt(index + 1) * 35
      positions.set(graphNodeKey(node), clampGraphPosition({ x: anchor.x + Math.cos(angle) * radius, y: anchor.y + Math.sin(angle) * radius }))
    })
  }

  const velocities = new Map(nodes.map(node => [graphNodeKey(node), { x: 0, y: 0 }]))
  const visibleIds = new Set(nodes.map(graphNodeKey))
  const visibleEdges = edges.filter(edge => visibleIds.has(edge.sourceId) && visibleIds.has(edge.targetId))
  for (let iteration = 0; iteration < 150; iteration += 1) {
    const cooling = 1 - iteration / 180
    for (let leftIndex = 0; leftIndex < nodes.length; leftIndex += 1) {
      const left = nodes[leftIndex]!
      const leftPosition = positions.get(graphNodeKey(left))!
      const leftVelocity = velocities.get(graphNodeKey(left))!
      for (let rightIndex = leftIndex + 1; rightIndex < nodes.length; rightIndex += 1) {
        const right = nodes[rightIndex]!
        const rightPosition = positions.get(graphNodeKey(right))!
        const rightVelocity = velocities.get(graphNodeKey(right))!
        let dx = leftPosition.x - rightPosition.x
        let dy = leftPosition.y - rightPosition.y
        if (dx === 0 && dy === 0) { dx = ((hash(graphNodeKey(left)) % 13) - 6) || 1; dy = ((hash(graphNodeKey(right)) % 11) - 5) || -1 }
        const distanceSquared = Math.max(100, dx * dx + dy * dy)
        const distance = Math.sqrt(distanceSquared)
        const repulsion = Math.min(9, 18_000 / distanceSquared) * cooling
        const collision = distance < 66 ? (66 - distance) * .08 : 0
        const force = repulsion + collision
        const forceX = (dx / distance) * force
        const forceY = (dy / distance) * force
        leftVelocity.x += forceX; leftVelocity.y += forceY
        rightVelocity.x -= forceX; rightVelocity.y -= forceY
      }
    }
    for (const edge of visibleEdges) {
      const source = positions.get(edge.sourceId)!
      const target = positions.get(edge.targetId)!
      const sourceVelocity = velocities.get(edge.sourceId)!
      const targetVelocity = velocities.get(edge.targetId)!
      const dx = target.x - source.x
      const dy = target.y - source.y
      const distance = Math.max(1, Math.hypot(dx, dy))
      const sparseScale = nodes.length <= 3 ? 2 : nodes.length <= 8 ? 1.45 : 1
      const desired = (edge.type === 'scope' ? 138 : edge.type === 'entity' ? 94 : edge.type === 'semantic' ? 118 : 106) * sparseScale
      const spring = (distance - desired) * .018 * cooling
      const forceX = (dx / distance) * spring
      const forceY = (dy / distance) * spring
      sourceVelocity.x += forceX; sourceVelocity.y += forceY
      targetVelocity.x -= forceX; targetVelocity.y -= forceY
    }
    for (const node of nodes) {
      const key = graphNodeKey(node)
      const position = positions.get(key)!
      const velocity = velocities.get(key)!
      const anchor = anchors.get(node.category ?? 'general') ?? { x: GRAPH_WIDTH / 2, y: GRAPH_HEIGHT / 2 }
      velocity.x += (anchor.x - position.x) * .0035 * cooling + (GRAPH_WIDTH / 2 - position.x) * .0008
      velocity.y += (anchor.y - position.y) * .0035 * cooling + (GRAPH_HEIGHT / 2 - position.y) * .0008
      velocity.x = Math.max(-12, Math.min(12, velocity.x * .76))
      velocity.y = Math.max(-12, Math.min(12, velocity.y * .76))
      positions.set(key, clampGraphPosition({ x: position.x + velocity.x, y: position.y + velocity.y }))
    }
  }
  return positions
}

function uniformGraphPositions(nodes: MemoryGraphNode[]): GraphPositions {
  const positions: GraphPositions = new Map()
  const ordered = [...nodes].sort((left, right) => {
    const categoryDifference = CATEGORY_ORDER.indexOf(left.category ?? 'general') - CATEGORY_ORDER.indexOf(right.category ?? 'general')
    return categoryDifference === 0 ? left.id.localeCompare(right.id) : categoryDifference
  })
  const columns = Math.max(1, Math.ceil(Math.sqrt(ordered.length * 1.65)))
  const rows = Math.max(1, Math.ceil(ordered.length / columns))
  const cellWidth = (GRAPH_WIDTH - GRAPH_MARGIN_X * 2) / columns
  const cellHeight = (GRAPH_HEIGHT - GRAPH_MARGIN_Y * 2) / rows
  ordered.forEach((node, index) => {
    const row = Math.floor(index / columns)
    const column = index % columns
    const rowLength = Math.min(columns, ordered.length - row * columns)
    const rowOffset = (columns - rowLength) * cellWidth / 2
    positions.set(graphNodeKey(node), {
      x: GRAPH_MARGIN_X + rowOffset + cellWidth * (column + .5),
      y: GRAPH_MARGIN_Y + cellHeight * (row + .5),
    })
  })
  return positions
}

function graphPoint(svg: SVGSVGElement, clientX: number, clientY: number): GraphPosition {
  const matrix = svg.getScreenCTM?.()
  if (matrix !== null && matrix !== undefined && typeof svg.createSVGPoint === 'function') {
    const point = svg.createSVGPoint()
    point.x = clientX; point.y = clientY
    return clampGraphPosition(point.matrixTransform(matrix.inverse()))
  }
  const bounds = svg.getBoundingClientRect()
  const width = bounds.width || GRAPH_WIDTH
  const height = bounds.height || GRAPH_HEIGHT
  return clampGraphPosition({ x: (clientX - bounds.left) * GRAPH_WIDTH / width, y: (clientY - bounds.top) * GRAPH_HEIGHT / height })
}

function MemoryGraph(props: { graph: MemoryGraphSnapshot; selectedId?: string | undefined; onSelect: (node: MemoryGraphNode) => void }): JSX.Element {
  const t = useT()
  const visibleNodes = useMemo(() => {
    const spaces = props.graph.nodes.filter(node => graphNodeKind(node) === 'space')
    const entities = props.graph.nodes.filter(node => graphNodeKind(node) === 'entity').slice(0, 20)
    const memories = props.graph.nodes.filter(node => graphNodeKind(node) === 'memory').slice(0, Math.max(0, 60 - spaces.length - entities.length))
    return [...spaces, ...entities, ...memories].slice(0, 60)
  }, [props.graph.nodes])
  const visibleIds = useMemo(() => new Set(visibleNodes.map(graphNodeKey)), [visibleNodes])
  const visibleKinds = useMemo(() => new Map(visibleNodes.map(node => [graphNodeKey(node), graphNodeKind(node)])), [visibleNodes])
  const edges = useMemo(() => {
    const priority = new Map<string, number>([['entity', 0], ['scope', 1], ['causal', 2], ['semantic', 3], ['temporal', 4]])
    return props.graph.edges
      .filter(edge => visibleIds.has(edge.sourceId) && visibleIds.has(edge.targetId))
      .map((edge, index) => ({ edge, index }))
      .sort((left, right) => (priority.get(left.edge.type ?? 'temporal') ?? 5) - (priority.get(right.edge.type ?? 'temporal') ?? 5) || left.index - right.index)
      .slice(0, 180)
      .map(({ edge }) => edge)
  }, [props.graph.edges, visibleIds])
  const curvedEdges = useMemo(() => {
    const groups = new Map<string, number[]>()
    edges.forEach((edge, index) => {
      const key = [edge.sourceId, edge.targetId].sort().join('::')
      groups.set(key, [...(groups.get(key) ?? []), index])
    })
    return edges.map((edge, index) => {
      const key = [edge.sourceId, edge.targetId].sort().join('::')
      const group = groups.get(key) ?? [index]
      const groupIndex = group.indexOf(index)
      return { edge, offset: (groupIndex - (group.length - 1) / 2) * 12 }
    })
  }, [edges])
  const layoutKey = `${visibleNodes.map(node => `${graphNodeKey(node)}:${graphNodeKind(node)}:${node.category ?? 'general'}`).join('|')}::${edges.map(edge => `${edge.sourceId}>${edge.targetId}:${edge.type ?? 'temporal'}`).join('|')}`
  const naturalLayout = useMemo(() => naturalGraphPositions(visibleNodes, edges), [layoutKey])
  const [positions, setPositions] = useState<GraphPositions>(() => naturalLayout)
  const [layoutMode, setLayoutMode] = useState<GraphLayoutMode>('natural')
  const positionsRef = useRef(positions)
  const animationRef = useRef<number | null>(null)
  const dragRef = useRef<{ nodeId: string; pointerId: number; startX: number; startY: number; moved: boolean } | null>(null)

  const commitPositions = useCallback((next: GraphPositions) => {
    positionsRef.current = next
    setPositions(next)
  }, [])

  const cancelAnimation = useCallback(() => {
    if (animationRef.current !== null && typeof window.cancelAnimationFrame === 'function') window.cancelAnimationFrame(animationRef.current)
    animationRef.current = null
  }, [])

  const animateTo = useCallback((target: GraphPositions, mode: Exclude<GraphLayoutMode, 'custom'>) => {
    cancelAnimation()
    setLayoutMode(mode)
    if (typeof window.requestAnimationFrame !== 'function') { commitPositions(target); return }
    const start = new Map(positionsRef.current)
    const startedAt = performance.now()
    const tick = (time: number) => {
      const progress = Math.min(1, (time - startedAt) / 620)
      const eased = 1 - Math.pow(1 - progress, 3)
      const next: GraphPositions = new Map()
      for (const [id, destination] of target) {
        const origin = start.get(id) ?? { x: GRAPH_WIDTH / 2, y: GRAPH_HEIGHT / 2 }
        next.set(id, { x: origin.x + (destination.x - origin.x) * eased, y: origin.y + (destination.y - origin.y) * eased })
      }
      commitPositions(next)
      if (progress < 1) animationRef.current = window.requestAnimationFrame(tick)
      else animationRef.current = null
    }
    animationRef.current = window.requestAnimationFrame(tick)
  }, [cancelAnimation, commitPositions])

  useEffect(() => { animateTo(naturalLayout, 'natural') }, [layoutKey])
  useEffect(() => () => cancelAnimation(), [cancelAnimation])

  const beginDrag = (event: ReactPointerEvent<SVGGElement>, nodeId: string) => {
    cancelAnimation()
    dragRef.current = { nodeId, pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, moved: false }
    event.currentTarget.setPointerCapture?.(event.pointerId)
  }
  const moveDrag = (event: ReactPointerEvent<SVGGElement>) => {
    const drag = dragRef.current
    const svg = event.currentTarget.ownerSVGElement
    if (drag === null || svg === null || drag.pointerId !== event.pointerId) return
    if (!drag.moved && Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) < 4) return
    drag.moved = true
    const point = graphPoint(svg, event.clientX, event.clientY)
    const next = new Map(positionsRef.current)
    next.set(drag.nodeId, point)
    commitPositions(next)
    setLayoutMode('custom')
  }
  const endDrag = (event: ReactPointerEvent<SVGGElement>) => {
    const drag = dragRef.current
    if (drag === null || drag.pointerId !== event.pointerId) return
    const svg = event.currentTarget.ownerSVGElement
    if (drag.moved && svg !== null) {
      const next = new Map(positionsRef.current)
      next.set(drag.nodeId, graphPoint(svg, event.clientX, event.clientY))
      commitPositions(next)
    }
    dragRef.current = null
    event.currentTarget.releasePointerCapture?.(event.pointerId)
    if (!drag.moved) {
      const node = visibleNodes.find(candidate => graphNodeKey(candidate) === drag.nodeId)
      if (node !== undefined) props.onSelect(node)
    }
  }
  const cancelDrag = (event: ReactPointerEvent<SVGGElement>) => {
    if (dragRef.current?.pointerId === event.pointerId) dragRef.current = null
  }
  const nudge = (nodeId: string, dx: number, dy: number) => {
    cancelAnimation()
    const current = positionsRef.current.get(nodeId)
    if (current === undefined) return
    const next = new Map(positionsRef.current)
    next.set(nodeId, clampGraphPosition({ x: current.x + dx, y: current.y + dy }))
    commitPositions(next)
    setLayoutMode('custom')
  }
  const layoutLabel = t(layoutMode === 'natural' ? 'graph.layoutNatural' : layoutMode === 'uniform' ? 'graph.layoutUniform' : 'graph.layoutCustom')
  return (
    <>
      <div className={css.graphCanvasControls} role="toolbar" aria-label={t('graph.layoutAria')}>
        <span role="status" aria-label={t('graph.layoutStatus', { layout: layoutLabel })}><i />{t('graph.draggable', { layout: layoutLabel })}</span>
        <button type="button" data-active={layoutMode === 'natural' || undefined} onClick={() => animateTo(naturalGraphPositions(visibleNodes, edges), 'natural')}>{t('graph.naturalAction')}</button>
        <button type="button" data-active={layoutMode === 'uniform' || undefined} onClick={() => animateTo(uniformGraphPositions(visibleNodes), 'uniform')}>{t('graph.uniformAction')}</button>
      </div>
      <svg className={css.graphSvg} viewBox={`0 0 ${GRAPH_WIDTH} ${GRAPH_HEIGHT}`} role="img" data-layout={layoutMode} data-density={visibleNodes.length <= 12 ? 'sparse' : 'dense'} aria-label={t('graph.aria', { nodes: props.graph.nodes.length, edges: props.graph.edges.length })}>
      <defs>
        <pattern id="mnemon-grid" width="26" height="26" patternUnits="userSpaceOnUse"><path d="M 26 0 L 0 0 0 26" className={css.graphGridLine} fill="none" /></pattern>
        <filter id="mnemon-glow" x="-100%" y="-100%" width="300%" height="300%"><feGaussianBlur stdDeviation="4" result="blur" /><feMerge><feMergeNode in="blur" /><feMergeNode in="SourceGraphic" /></feMerge></filter>
      </defs>
      <rect width={GRAPH_WIDTH} height={GRAPH_HEIGHT} className={css.graphBackdrop} />
      <rect width={GRAPH_WIDTH} height={GRAPH_HEIGHT} fill="url(#mnemon-grid)" />
      {curvedEdges.map(({ edge, offset }, index) => {
        const source = positions.get(edge.sourceId) ?? naturalLayout.get(edge.sourceId) ?? { x: GRAPH_WIDTH / 2, y: GRAPH_HEIGHT / 2 }
        const target = positions.get(edge.targetId) ?? naturalLayout.get(edge.targetId) ?? { x: GRAPH_WIDTH / 2, y: GRAPH_HEIGHT / 2 }
        const dx = target.x - source.x
        const dy = target.y - source.y
        const distance = Math.max(1, Math.hypot(dx, dy))
        const direction = edge.sourceId.localeCompare(edge.targetId) <= 0 ? 1 : -1
        const controlX = (source.x + target.x) / 2 - (dy / distance) * offset * direction
        const controlY = (source.y + target.y) / 2 + (dx / distance) * offset * direction
        return <path key={`${edge.sourceId}-${edge.targetId}-${index}`} d={`M ${source.x} ${source.y} Q ${controlX} ${controlY} ${target.x} ${target.y}`} className={css.graphEdge} data-edge={edge.type ?? 'temporal'} data-source-id={edge.sourceId} data-target-id={edge.targetId} data-source-kind={visibleKinds.get(edge.sourceId)} data-target-kind={visibleKinds.get(edge.targetId)} />
      })}
      {visibleNodes.map((node, index) => {
        const nodeKey = graphNodeKey(node)
        const position = positions.get(nodeKey) ?? naturalLayout.get(nodeKey) ?? { x: GRAPH_WIDTH / 2, y: GRAPH_HEIGHT / 2 }
        const selected = props.selectedId === nodeKey
        const showLabel = selected || visibleNodes.length < 22 || index % 3 === 0
        return (
          <g key={nodeKey} className={css.graphNode} data-node-id={nodeKey} data-provider={node.memoryProviderId} data-category={node.category ?? 'general'} data-kind={graphNodeKind(node)} data-selected={selected || undefined}
            transform={`translate(${position.x} ${position.y})`} role="button" tabIndex={0} aria-label={`${graphKindLabel(t, node)}: ${short(node.content, 80)}`}
            data-dragging={dragRef.current?.nodeId === nodeKey || undefined}
            onPointerDown={event => beginDrag(event, nodeKey)} onPointerMove={moveDrag} onPointerUp={endDrag} onPointerCancel={cancelDrag} onLostPointerCapture={cancelDrag}
            onClick={() => props.onSelect(node)}
            onKeyDown={event => {
              if (event.key === 'Enter' || event.key === ' ') props.onSelect(node)
              else if (event.key === 'ArrowLeft') { event.preventDefault(); nudge(nodeKey, -12, 0) }
              else if (event.key === 'ArrowRight') { event.preventDefault(); nudge(nodeKey, 12, 0) }
              else if (event.key === 'ArrowUp') { event.preventDefault(); nudge(nodeKey, 0, -12) }
              else if (event.key === 'ArrowDown') { event.preventDefault(); nudge(nodeKey, 0, 12) }
            }}>
            {graphNodeKind(node) === 'space'
              ? <><rect x={selected ? -20 : -17} y={selected ? -15 : -13} width={selected ? 40 : 34} height={selected ? 30 : 26} rx="9" className={css.nodeHalo} filter={selected ? 'url(#mnemon-glow)' : undefined} /><circle r={selected ? 6 : 5} className={css.nodeCore} /></>
              : graphNodeKind(node) === 'entity'
                ? <><path d={selected ? 'M 0 -18 L 18 0 L 0 18 L -18 0 Z' : 'M 0 -14 L 14 0 L 0 14 L -14 0 Z'} className={css.nodeHalo} filter={selected ? 'url(#mnemon-glow)' : undefined} /><circle r={selected ? 5 : 4} className={css.nodeCore} /></>
                : <><circle r={selected ? 17 : visibleNodes.length <= 12 ? 14 : 11} className={css.nodeHalo} filter={selected ? 'url(#mnemon-glow)' : undefined} /><circle r={selected ? 7 : visibleNodes.length <= 12 ? 6 : 4.5} className={css.nodeCore} /></>}
            {(selected || visibleNodes.length <= 12) && graphNodeKind(node) === 'memory' && node.memoryBodyName !== undefined && <text x="0" y="-18" textAnchor="middle" className={css.nodeBodyLabel}>{short(node.memoryBodyName, 12)}</text>}
            {showLabel && <text x={visibleNodes.length <= 12 ? 19 : 15} y="4" className={css.nodeLabel}>{short(node.content.replace(/\s+/gu, ' '), selected ? 34 : visibleNodes.length <= 12 ? 26 : 19)}</text>}
          </g>
        )
      })}
      </svg>
    </>
  )
}

export function OverviewPage(props: { client: MemorySpacesPageClient; metadataClient: MemorySpacesPageClient; revision: number; reloadKey?: number; activationEnabled: boolean; writeEnabled: boolean; agentAvailable: boolean; fallbackBodies: MemorySpaceView[]; fallbackDirectory: string | undefined; catalogKnown: boolean; onMutate: () => void; onAgentRefresh: () => void; onBodyReconnect: (body: MemorySpaceView) => void; onBodyMetadata: (updates: readonly MemorySpaceMetadataUpdate[]) => void; onExplore: (query: string) => void }): JSX.Element {
  const t = useT()
  const locale = useLocale()
  const spaceCreateFormId = useId()
  const spaceEditFormId = useId()
  const [graph, setGraph] = useState<MemoryGraphSnapshot | null>(null)
  const [catalog, setCatalog] = useState<MemorySpaceCatalog | null>(null)
  const [selected, setSelected] = useState<MemoryGraphNode | null>(null)
  const [catalogLoading, setCatalogLoading] = useState(true)
  const [healthLoading, setHealthLoading] = useState(true)
  const [graphLoading, setGraphLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [changing, setChanging] = useState<string | null>(null)
  const [reconnectingSpace, setReconnectingSpace] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [creatingSpaceOpen, setCreatingSpaceOpen] = useState(false)
  const [spaceName, setSpaceName] = useState('')
  const [spaceDescription, setSpaceDescription] = useState('')
  const [spaceProviderId, setSpaceProviderId] = useState<MemoryProviderId>('mnemon-native')
  const [providerDrafts, setProviderDrafts] = useState<ProviderDrafts>({})
  const [catalogUnavailable, setCatalogUnavailable] = useState(false)
  const [editingSpace, setEditingSpace] = useState<string | null>(null)
  const [editName, setEditName] = useState('')
  const [editDescription, setEditDescription] = useState('')
  const [editConnection, setEditConnection] = useState<MemoryProviderConnection>({})
  const [editClearSecrets, setEditClearSecrets] = useState<string[]>([])
  const [savingSpace, setSavingSpace] = useState<string | null>(null)
  const [confirmingDeleteSpace, setConfirmingDeleteSpace] = useState<string | null>(null)
  const [deletingSpace, setDeletingSpace] = useState<string | null>(null)
  const [preview, setPreview] = useState<MemoryGraphNode | null>(null)
  const [metadataOpen, setMetadataOpen] = useState(false)
  const [metadataSelection, setMetadataSelection] = useState<string[]>([])
  const [metadataTasks, setMetadataTasks] = useState<Record<string, { status: 'running' | 'success' | 'error'; error?: string }>>({})
  const [lastFullSyncAt, setLastFullSyncAt] = useState<number | null>(null)
  const [syncClock, setSyncClock] = useState(() => Date.now())
  const loadRequest = useRef(0)
  const initialSyncStarted = useRef(false)
  const directoryRetryStarted = useRef(false)
  const fullSyncObserved = useRef(true)
  const load = useCallback(async (quiet = false) => {
    const request = ++loadRequest.current
    setCatalogLoading(true)
    setHealthLoading(true)
    setGraphLoading(true)
    setError(null)
    let directoryUnavailable = false
    try {
      // Render the control-plane directory first. Health and graph I/O then
      // resolve independently without holding the cards or each other back.
      const nextCatalog = await props.client.bodyDirectory().then(next => { setCatalogUnavailable(false); return next }).catch(() => {
        directoryUnavailable = true
        setCatalogUnavailable(!props.catalogKnown)
        return {
          items: props.fallbackBodies,
          providers: [],
          total: props.fallbackBodies.length,
          activeCount: props.fallbackBodies.filter(body => body.active).length,
          directory: props.fallbackDirectory ?? '',
          generatedAt: new Date().toISOString(),
        }
      })
      if (request !== loadRequest.current) return
      setProviderDrafts(current => mergeProviderDefaults(nextCatalog.providers, current))
      setCatalog(nextCatalog)
      setCatalogLoading(false)
      void props.client.bodies().then(next => {
        if (request !== loadRequest.current) return
        setCatalog(next)
      }).catch(reason => {
        if (request === loadRequest.current && !quiet && !directoryUnavailable) setError(message(reason))
      }).finally(() => { if (request === loadRequest.current) setHealthLoading(false) })
      void props.client.graph().then(next => {
        if (request !== loadRequest.current) return
        const enriched = enrichMultiSpaceGraph(next, nextCatalog.items)
        setGraph(enriched)
        setSelected(current => current === null ? null : enriched.nodes.find(node => graphNodeKey(node) === graphNodeKey(current)) ?? null)
      }).catch(reason => {
        if (request === loadRequest.current && !directoryUnavailable) setError(message(reason))
      }).finally(() => { if (request === loadRequest.current) setGraphLoading(false) })
    } catch (reason) {
      if (request === loadRequest.current) {
        setError(message(reason))
        setCatalogLoading(false)
        setHealthLoading(false)
        setGraphLoading(false)
      }
    }
  }, [props.catalogKnown, props.client, props.fallbackBodies, props.fallbackDirectory])

  useEffect(() => {
    if (initialSyncStarted.current) return
    initialSyncStarted.current = true
    void load()
  }, [load])
  // The workspace's refresh reloads the whole directory quietly.
  const seenReload = useRef(props.reloadKey)
  useEffect(() => {
    if (seenReload.current === props.reloadKey) return
    seenReload.current = props.reloadKey
    void load(true)
  }, [load, props.reloadKey])
  useEffect(() => {
    if (!catalogUnavailable || !props.catalogKnown || directoryRetryStarted.current) return
    directoryRetryStarted.current = true
    void load(true)
  }, [catalogUnavailable, load, props.catalogKnown])
  useEffect(() => {
    const timer = window.setInterval(() => setSyncClock(Date.now()), 1_000)
    return () => window.clearInterval(timer)
  }, [])
  // A new space starts with a provider that can take it now.
  useEffect(() => { setSpaceProviderId(current => readyProviderId(catalog?.providers ?? [], current) ?? current) }, [catalog])

  const toggle = async (body: MemorySpaceView) => {
    setChanging(body.id); setError(null)
    try {
      await props.client.updateBody(body.id, { active: !body.active })
      await load(true)
      props.onMutate()
    } catch (reason) { setError(message(reason)) } finally { setChanging(null) }
  }

  const reconnect = async (body: MemorySpaceView) => {
    if (reconnectingSpace !== null || editingSpace !== null || deletingSpace !== null) return
    setReconnectingSpace(body.id); setError(null)
    setCatalog(current => current === null ? current : {
      ...current,
      items: current.items.map(item => item.id === body.id ? { ...item, statusLoading: true } : item),
    })
    try {
      const next = await props.client.reconnectBody(body.id)
      setCatalog(current => current === null ? current : {
        ...current,
        items: current.items.map(item => item.id === next.id ? next : item),
      })
      props.onBodyReconnect(next)
    } catch (reason) {
      const failure = message(reason)
      setCatalog(current => current === null ? current : {
        ...current,
        items: current.items.map(item => item.id === body.id ? { ...item, healthy: false, statusLoading: false, error: failure } : item),
      })
      setError(failure)
    } finally {
      setReconnectingSpace(null)
    }
  }

  const beginEdit = (body: MemorySpaceView) => {
    setEditingSpace(body.id); setEditName(body.name); setEditDescription(body.description ?? ''); setError(null)
    setEditConnection(nativeSpaceProvider(body.provider) ? {} : { ...body.provider.settings })
    setEditClearSecrets([])
  }

  const saveEdit = async (event: FormEvent, body: MemorySpaceView) => {
    event.preventDefault()
    if (editName.trim() === '') return
    setSavingSpace(body.id); setError(null)
    try {
      const descriptor = catalog?.providers.find(provider => provider.id === body.provider.id)
      const connection = descriptor === undefined ? {} : Object.fromEntries(Object.entries(editConnection).filter(([key, value]) => {
        const field = descriptor.fields.find(candidate => candidate.key === key)
        return field?.input !== 'secret' || String(value) !== ''
      }))
      await props.client.updateBody(body.id, {
        name: editName,
        description: editDescription,
        ...(nativeSpaceProvider(body.provider) ? {} : { connection, ...(editClearSecrets.length === 0 ? {} : { clearSecrets: editClearSecrets }) }),
      })
      setEditingSpace(null)
      await load(true)
      props.onMutate()
    } catch (reason) { setError(message(reason)) } finally { setSavingSpace(null) }
  }

  const create = async (event: FormEvent) => {
    event.preventDefault()
    const providers = catalog?.providers ?? []
    const manualProvider = providers.find(provider => provider.id === spaceProviderId)
    if (spaceName.trim() === '' || spaceDescription.trim() === '' || !providerDraftComplete(manualProvider, providerDrafts[spaceProviderId])) return
    setCreating(true); setError(null)
    try {
      await props.client.createBody({
        name: spaceName,
        description: spaceDescription,
        providerId: spaceProviderId,
        ...(manualProvider?.origin === 'native' ? {} : { connection: providerDrafts[spaceProviderId] ?? {} }),
      })
      setSpaceName(''); setSpaceDescription(''); setSpaceProviderId(current => readyProviderId(providers) ?? current)
      setProviderDrafts(current => Object.fromEntries(providers.map(provider => [provider.id, Object.fromEntries(Object.entries(current[provider.id] ?? {}).map(([key, value]) => [key, provider.fields.some(field => field.key === key && field.input === 'secret') ? '' : value]))])))
      setCreatingSpaceOpen(false)
      await load(true)
      props.onMutate()
    } catch (reason) { setError(message(reason)) } finally { setCreating(false) }
  }

  const deleteBody = async (body: MemorySpaceView) => {
    setDeletingSpace(body.id); setError(null)
    try {
      await props.client.deleteBody(body.id)
      setConfirmingDeleteSpace(null)
      await load(true)
      props.onMutate()
    } catch (reason) { setError(message(reason)) } finally { setDeletingSpace(null) }
  }

  const maintainMetadata = () => {
    if (metadataSelection.length === 0) return
    const selectedIds = metadataSelection.filter(id => metadataTasks[id]?.status !== 'running')
    if (selectedIds.length === 0) return
    setError(null)
    setMetadataSelection([])
    setMetadataTasks(current => ({
      ...current,
      ...Object.fromEntries(selectedIds.map(id => [id, { status: 'running' as const }])),
    }))
    for (const id of selectedIds) {
      void props.metadataClient.maintainBodyMetadata([id]).then(result => {
        const update = result.updates.find(candidate => candidate.memoryBodyId === id)
        if (update === undefined) throw new Error(`metadata task Agent omitted Memory Space ${id}`)
        setCatalog(current => current === null ? current : {
          ...current,
          items: current.items.map(body => body.id === id ? { ...body, name: update.title, description: update.description } : body),
        })
        props.onBodyMetadata([update])
        setMetadataTasks(current => ({ ...current, [id]: { status: 'success' } }))
      }).catch(reason => {
        setMetadataTasks(current => ({ ...current, [id]: { status: 'error', error: message(reason) } }))
      })
    }
  }

  const generated = graph === null ? t('overview.waitingSnapshot') : t('overview.updatedAt', { time: new Date(graph.generatedAt).toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', second: '2-digit' }) })
  const graphSpaces = graph?.nodes.filter(node => graphNodeKind(node) === 'space').length ?? 0
  const graphEntities = graph?.nodes.filter(node => graphNodeKind(node) === 'entity').length ?? 0
  const graphMemories = graph?.nodes.filter(node => graphNodeKind(node) === 'memory').length ?? 0
  const graphSources = graph?.sources ?? []
  const onlyQueryOrUnsupported = graphSources.length > 0 && graphSources.every(source => source.mode === 'query-only' || source.mode === 'unsupported' || source.status === 'unavailable')
  const selectedKind = selected === null ? null : graphNodeKind(selected)
  const editingSpaceView = editingSpace === null ? undefined : catalog?.items.find(body => body.id === editingSpace)
  const deletingSpaceView = confirmingDeleteSpace === null ? undefined : catalog?.items.find(body => body.id === confirmingDeleteSpace)
  const providers = catalog?.providers ?? []
  // The status summary already carries the non-blocking control-plane
  // directory. Keep metadata maintenance usable while the richer Memory page
  // health/graph requests are still resolving.
  const metadataCandidates = (catalog?.items ?? props.fallbackBodies).filter(body => body.active && body.providerEnabled !== false)
  const metadataRunningCount = Object.values(metadataTasks).filter(task => task.status === 'running').length
  const metadataBusy = metadataRunningCount > 0
  const metadataSelectable = metadataCandidates.filter(body => metadataTasks[body.id]?.status !== 'running')
  const metadataAllSelected = metadataSelectable.length > 0 && metadataSelectable.every(body => metadataSelection.includes(body.id))
  const loading = catalogLoading || healthLoading || graphLoading
  useEffect(() => {
    if (loading) {
      fullSyncObserved.current = true
      return
    }
    if (!fullSyncObserved.current) return
    fullSyncObserved.current = false
    if (error !== null) return
    const completedAt = Date.now()
    setLastFullSyncAt(completedAt)
    setSyncClock(completedAt)
  }, [error, loading])
  const fullSyncAge = lastFullSyncAt === null
    ? t('overview.fullSyncPending')
    : (() => {
        const seconds = Math.max(0, Math.floor((syncClock - lastFullSyncAt) / 1_000))
        if (seconds < 5) return t('overview.fullSyncJustNow')
        if (seconds < 60) return t('overview.fullSyncSeconds', { count: seconds })
        const minutes = Math.floor(seconds / 60)
        if (minutes < 60) return t('overview.fullSyncMinutes', { count: minutes })
        const hours = Math.floor(minutes / 60)
        if (hours < 24) return t('overview.fullSyncHours', { count: hours })
        return t('overview.fullSyncDays', { count: Math.floor(hours / 24) })
      })()
  const selectedProvider = providers.find(provider => provider.id === spaceProviderId)
  const nativeSpaceCount = catalog?.items.filter(body => nativeSpaceProvider(body.provider)).length ?? 0
  const canDeleteSpace = (body: MemorySpaceView): boolean => !nativeSpaceProvider(body.provider) || nativeSpaceCount > 1
  const updateProviderDraft = (providerId: MemoryProviderId, key: string, value: string | number | boolean) => setProviderDrafts(current => ({ ...current, [providerId]: { ...(current[providerId] ?? {}), [key]: value } }))
  const placementReceipt = (body: MemorySpaceView) => body.placement === undefined ? null : <div className={css.placementReceipt} title={body.placement.reason}><span aria-hidden="true">✦</span><div><strong>{t(body.placement.decidedBy === 'llm' ? 'overview.placementByLlm' : 'overview.placementByRules')}</strong><small>{t('overview.placementConfidence', { confidence: t(`overview.confidence.${body.placement.confidence}`) })}</small><p>{body.placement.reason}</p></div></div>
  const spaceEditForm = (body: MemorySpaceView) => <form id={spaceEditFormId} className={css.bodyEdit} onSubmit={event => void saveEdit(event, body)}>
    <label>{t('overview.editName')}<input aria-label={t('overview.editName')} value={editName} onChange={event => setEditName(event.target.value)} maxLength={100} required /></label>
    <label>{t('overview.editDescription')}<textarea aria-label={t('overview.editDescription')} value={editDescription} onChange={event => setEditDescription(event.target.value)} rows={4} maxLength={1000} /></label>
    {!nativeSpaceProvider(body.provider) && (() => { const descriptor = providers.find(provider => provider.id === body.provider.id); return descriptor === undefined ? null : <ProviderMemoryFields provider={descriptor} connection={editConnection} onChange={(key, value) => setEditConnection(current => ({ ...current, [key]: value }))} body={body} clearSecrets={editClearSecrets} onClearSecretsChange={setEditClearSecrets} /> })()}
  </form>
  const spaceCreateForm = <form id={spaceCreateFormId} className={appearanceClass(css.bodyEdit, css.bodyCreateForm)} onSubmit={event => void create(event)}>
    <section className={css.createSection}>
      <div className={css.createSectionHeading}><span>01</span><div><strong>{t('overview.createIdentityTitle')}</strong><small>{t('overview.createIdentityHint')}</small></div></div>
      <div className={css.createIdentityGrid}>
        <label>{t('overview.createName')}<input data-autofocus aria-label={t('overview.createName')} value={spaceName} onChange={event => setSpaceName(event.target.value)} placeholder={t('overview.createNamePlaceholder')} maxLength={100} required /></label>
        <label>{t('overview.createDescription')}<textarea aria-label={t('overview.createDescription')} value={spaceDescription} onChange={event => setSpaceDescription(event.target.value)} placeholder={t('overview.createDescriptionPlaceholder')} rows={3} maxLength={1000} required /></label>
      </div>
    </section>
    <section className={css.createSection}>
      <div className={css.createSectionHeading}><span>02</span><div><strong>{t('overview.createPlacementTitle')}</strong><small>{t('overview.createPlacementHint')}</small></div></div>
      <fieldset className={css.providerChoice}><legend>{t('overview.providerLabel')}</legend>{readyFirst(providers).map(provider => {
        const serviceMissing = provider.serviceConfigured === false
        return <label key={provider.id} data-selected={spaceProviderId === provider.id || undefined} data-native={provider.origin === 'native' || undefined} data-disabled={serviceMissing || undefined}>
          <input type="radio" name="memory-provider" value={provider.id} checked={spaceProviderId === provider.id} disabled={serviceMissing} onChange={() => setSpaceProviderId(provider.id)} />
          <ProviderIcon providerId={provider.id} icon={provider.icon} className={css.providerChoiceIcon} />
          <span><strong>{provider.label}{provider.origin === 'native' && <em>{t('overview.nativeOfficial')}</em>}</strong><small>{serviceMissing ? t(providerUnavailableKey(provider)) : `${t(`overview.workspaceBinding.${provider.workspaceBinding}`)} · ${providerSummary(t, provider)}`}</small></span>
          <i className={css.choiceControl} data-kind="radio" aria-hidden="true" />
        </label>
      })}</fieldset>
      {providers.some(provider => provider.serviceConfigured === false) && <small className={css.providerEnableHint}>{t('overview.providerEnableHint')}</small>}
      {selectedProvider !== undefined && selectedProvider.origin !== 'native' && <ProviderMemoryFields provider={selectedProvider} connection={providerDrafts[selectedProvider.id] ?? {}} onChange={(key, value) => updateProviderDraft(selectedProvider.id, key, value)} />}
    </section>
  </form>
  const spaceToggle = (body: MemorySpaceView) => <button type="button" className={css.bodySwitch} role="switch" aria-checked={body.active} aria-label={t('overview.toggleAria', { name: body.name })} disabled={!props.activationEnabled || changing === body.id || deletingSpace === body.id} onClick={() => void toggle(body)}><span className={css.bodySwitchTrack} aria-hidden="true"><i /></span><span>{changing === body.id ? t('overview.toggling') : body.active ? t('common.active') : t('common.inactive')}</span></button>
  const spaceEditActionClass = appearanceClass(css.ghostButton, appearanceClass(sidebarCss.itemActionButton, sidebarCss.itemEditAction))
  const spaceDeleteActionClass = appearanceClass(css.dangerButton, appearanceClass(sidebarCss.itemActionButton, sidebarCss.itemDangerAction))
  return (
    <div className={css.page}>
      <PageHeader title={t('nav.overview')} description={t(('overview.pageDescription'))} meta={fullSyncAge} {...(loading ? { loadingLabel: catalogLoading ? t('overview.directoryLoading') : graphLoading ? t('overview.snapshotLoading') : t('overview.healthLoading') } : {})}
 />
      {error !== null && <div className={css.inlineError} role="alert">{error}</div>}
      <section className={css.bodyDirectory} aria-label={t('overview.directory')}>
        <div className={css.bodyDirectoryHeader}>
          <div><h3>{t('overview.directory')}</h3><p>{t('overview.directory.description')}</p>{catalogUnavailable && <code className={css.bodyDirectoryPath}>{t('overview.directory.unsynced')}</code>}</div>
          <div className={appearanceClass(css.bodyDirectoryControls, sidebarCss.bodyDirectoryActions)}>
            <strong>{catalogUnavailable ? t('overview.directory.unsyncedBadge') : `${catalog?.activeCount ?? '—'} / ${catalog?.total ?? '—'} ${t('common.active')}`}</strong>
            {props.writeEnabled && !catalogUnavailable && <button type="button" className={spaceEditActionClass} title={!props.agentAvailable ? t('overview.metadataUnavailable') : undefined} onClick={() => { setMetadataSelection([]); setMetadataTasks({}); setMetadataOpen(true); if (!props.agentAvailable) props.onAgentRefresh() }}>{t('overview.metadataAction')}</button>}
            {props.writeEnabled && !catalogUnavailable && <button type="button" className={spaceEditActionClass} onClick={() => setCreatingSpaceOpen(true)}>{t('overview.createTitle')}</button>}
          </div>
        </div>
        <div className={css.bodyGrid}>
          {catalog?.items.map(body => (
            <article key={body.id} className={css.bodyCard} data-provider={body.provider.id} data-active={body.active || undefined} data-healthy={!body.statusLoading && body.healthy || undefined} data-status-loading={body.statusLoading || undefined} data-reconnectable="" data-reconnecting={reconnectingSpace === body.id || undefined} data-mnemon-default={body.mnemonDefault || undefined} data-editing={undefined} tabIndex={0} aria-label={t('overview.reconnectAria', { name: body.name })} title={reconnectingSpace === body.id ? t('overview.reconnecting') : body.error ?? t('overview.reconnectHint')} onClick={event => {
              if (event.target instanceof Element && event.target.closest('button, input, textarea, select, label, a, [role="switch"]') !== null) return
              void reconnect(body)
            }} onKeyDown={event => {
              if (event.target !== event.currentTarget || (event.key !== 'Enter' && event.key !== ' ')) return
              event.preventDefault(); void reconnect(body)
            }}>
              {<><div className={sidebarCss.bodyCardHeader}><div className={sidebarCss.bodyCardIdentity}><span className={css.bodySignal} /><div><strong title={body.id}>{body.name}</strong><div className={sidebarCss.bodyCardMeta}><MemoryProviderBadge providerId={body.provider.id} label={body.provider.label} /><small className={css.bodyHealth}>{reconnectingSpace === body.id ? t('overview.reconnecting') : body.statusLoading ? t('overview.storageChecking') : body.healthy ? t('overview.storageHealthy') : t('overview.storageUnhealthy')}</small>{body.mnemonDefault && <small className={css.mnemonDefaultBadge}>{t('overview.mnemonDefault')}</small>}</div></div></div>{spaceToggle(body)}</div><p title={body.description || t('overview.noDescription')}>{body.description || t('overview.noDescription')}</p>{placementReceipt(body)}<footer className={sidebarCss.bodyCardFooter}><div className={sidebarCss.bodyCardStats}>{!nativeSpaceProvider(body.provider) ? <><span className={css.bodyFooterBlock} title={t(body.provider.kind === 'remote' ? 'overview.providerRemote' : 'overview.providerLocal')}>{t(body.provider.kind === 'remote' ? 'overview.providerRemote' : 'overview.providerLocal')}</span><span className={`${css.bodyFooterBlock} ${css.bodyFooterGrow}`} title={body.provider.location || body.provider.label}>{body.provider.location || body.provider.label}</span></> : <><span className={css.bodyFooterBlock} title={t('common.memories', { count: body.stats?.totalInsights ?? 0 })}>{t('common.memories', { count: body.stats?.totalInsights ?? 0 })}</span><span className={css.bodyFooterBlock} title={t('common.edges', { count: body.stats?.edgeCount ?? 0 })}>{t('common.edges', { count: body.stats?.edgeCount ?? 0 })}</span><span className={css.bodyFooterBlock} title={humanBytes(body.stats?.dbSizeBytes ?? 0)}>{humanBytes(body.stats?.dbSizeBytes ?? 0)}</span></>}</div><div className={css.bodyCardActions}><button type="button" className={spaceEditActionClass} aria-label={t('overview.editSpaceAria', { name: body.name })} disabled={!props.writeEnabled || deletingSpace === body.id} onClick={() => beginEdit(body)}>{t('overview.editSpace')}</button><button type="button" className={spaceDeleteActionClass} aria-label={t(!nativeSpaceProvider(body.provider) ? 'overview.disconnectSpaceAria' : 'overview.deleteSpaceAria', { name: body.name })} title={canDeleteSpace(body) ? undefined : t('overview.lastStoreDeleteHint')} disabled={!props.writeEnabled || deletingSpace === body.id || !canDeleteSpace(body)} onClick={() => setConfirmingDeleteSpace(body.id)}>{!nativeSpaceProvider(body.provider) ? t('overview.disconnectSpace') : t('overview.deleteSpace')}</button></div></footer></>}
            </article>
          ))}
          {catalog?.total === 0 && <div className={css.bodyDirectoryEmpty}><span>◇</span><div><strong>{catalogUnavailable ? t('overview.unsyncedTitle') : t('overview.emptyTitle')}</strong><p>{catalogUnavailable ? t('overview.unsyncedShort') : t('overview.emptyShort')}</p></div></div>}
        </div>

      </section>
      <div className={css.asyncRegion}><ReadSourcePanel title={t('overview.snapshotSources')} hint={t('overview.snapshotSourcesHint')} sources={graphSources} /></div>
      {creatingSpaceOpen && <SidebarModal title={t('overview.createTitle')} description={t('overview.createDialogHint')} busy={creating} wide onClose={() => setCreatingSpaceOpen(false)} footer={<div className={css.modalFooterActions}><button type="button" data-dialog-close className={css.secondaryButton} disabled={creating} onClick={() => setCreatingSpaceOpen(false)}>{t('common.cancel')}</button><button type="submit" form={spaceCreateFormId} className={css.primaryButton} disabled={creating || spaceName.trim() === '' || spaceDescription.trim() === '' || !providerDraftComplete(selectedProvider, providerDrafts[spaceProviderId])}>{creating ? t('overview.creating') : t('overview.createAction')}</button></div>}>{spaceCreateForm}</SidebarModal>}
      {metadataOpen && <SidebarModal title={t('overview.metadataTitle')} description={t('overview.metadataDescription')} busy={metadataBusy} wide onClose={() => setMetadataOpen(false)} footer={<><p className={css.modalFooterNote}>{t('overview.metadataSafety')}</p><div className={css.modalFooterActions}><button type="button" data-dialog-close className={css.secondaryButton} disabled={metadataBusy} onClick={() => setMetadataOpen(false)}>{t('common.cancel')}</button><button type="button" className={css.primaryButton} disabled={!props.agentAvailable || metadataSelection.length === 0} title={!props.agentAvailable ? t('overview.metadataUnavailable') : undefined} onClick={maintainMetadata}>{t('overview.metadataGenerate', { count: metadataSelection.length })}</button></div></>}><div className={css.metadataDialog}>
        {!props.agentAvailable && <div className={css.inlineError} role="status">{t('overview.metadataUnavailable')}</div>}
        <div className={css.metadataToolbar}><span>{t('overview.metadataSelected', { count: metadataSelection.length })}{metadataRunningCount > 0 && <em>{t('overview.metadataRunningCount', { count: metadataRunningCount })}</em>}</span><button type="button" className={css.ghostButton} disabled={metadataSelectable.length === 0} onClick={() => setMetadataSelection(metadataAllSelected ? [] : metadataSelectable.map(body => body.id))}>{metadataAllSelected ? t('overview.metadataClear') : t('overview.metadataSelectAll')}</button></div>
        <div className={css.metadataList} aria-live="polite">{metadataCandidates.length === 0 && <div className={css.metadataEmpty}>{catalogLoading ? t('overview.metadataLoading') : t('overview.metadataEmpty')}</div>}{metadataCandidates.map(body => {
          const selected = metadataSelection.includes(body.id)
          const task = metadataTasks[body.id]
          return <label key={body.id} data-provider={body.provider.id} data-selected={selected || undefined} data-refreshing={task?.status === 'running' || undefined} data-refreshed={task?.status === 'success' || undefined} data-failed={task?.status === 'error' || undefined}><input type="checkbox" checked={selected} disabled={task?.status === 'running'} onChange={event => setMetadataSelection(current => event.target.checked ? [...new Set([...current, body.id])] : current.filter(id => id !== body.id))} /><i className={css.choiceControl} data-kind="check" aria-hidden="true" /><span><strong>{body.name}</strong><small>{body.description || t('overview.noDescription')}</small><span><MemoryProviderBadge providerId={body.provider.id} label={body.provider.label} />{task === undefined ? <code>{body.id}</code> : <small className={css.metadataTaskStatus} data-status={task.status} title={task.error}>{task.status === 'running' ? t('overview.metadataTaskRunning') : task.status === 'success' ? t('overview.metadataTaskSuccess') : t('overview.metadataTaskError', { error: task.error ?? t('overview.metadataTaskUnknown') })}</small>}</span></span></label>
        })}</div>
      </div></SidebarModal>}
      {editingSpaceView !== undefined && <SidebarModal title={t('overview.editSpaceAria', { name: editingSpaceView.name })} description={editingSpaceView.id} busy={savingSpace === editingSpaceView.id} onClose={() => setEditingSpace(null)} footer={<div className={css.modalFooterActions}><button type="button" data-dialog-close className={css.secondaryButton} disabled={savingSpace === editingSpaceView.id} onClick={() => setEditingSpace(null)}>{t('common.cancel')}</button><button type="submit" form={spaceEditFormId} className={css.primaryButton} disabled={savingSpace === editingSpaceView.id || editName.trim() === ''}>{savingSpace === editingSpaceView.id ? t('overview.savingSpace') : t('overview.saveSpace')}</button></div>}>{spaceEditForm(editingSpaceView)}</SidebarModal>}
      {deletingSpaceView !== undefined && <SidebarModal title={t(!nativeSpaceProvider(deletingSpaceView.provider) ? 'overview.disconnectTitle' : 'overview.deleteTitle', { name: deletingSpaceView.name })} description={deletingSpaceView.id} busy={deletingSpace === deletingSpaceView.id} onClose={() => setConfirmingDeleteSpace(null)} footer={<div className={css.modalFooterActions}><button type="button" data-dialog-close data-autofocus className={css.secondaryButton} disabled={deletingSpace === deletingSpaceView.id} onClick={() => setConfirmingDeleteSpace(null)}>{t('common.cancel')}</button><button type="button" className={css.dangerSolidButton} title={canDeleteSpace(deletingSpaceView) ? undefined : t('overview.lastStoreDeleteHint')} disabled={deletingSpace === deletingSpaceView.id || !canDeleteSpace(deletingSpaceView)} onClick={() => void deleteBody(deletingSpaceView)}>{deletingSpace === deletingSpaceView.id ? t('overview.deletingSpace') : t(!nativeSpaceProvider(deletingSpaceView.provider) ? 'overview.disconnectAction' : 'overview.deleteAction')}</button></div>}><div className={css.bodyDeleteConfirm}><p>{t(!nativeSpaceProvider(deletingSpaceView.provider) ? 'overview.disconnectWarning' : 'overview.deleteWarning', { provider: deletingSpaceView.provider.label })}</p><div className={css.bodyDeleteSummary}><strong>{deletingSpaceView.name}</strong><span>{deletingSpaceView.provider.label} · {deletingSpaceView.provider.location || t('common.memories', { count: deletingSpaceView.stats?.totalInsights ?? 0 })}</span></div></div></SidebarModal>}
      {!catalogUnavailable && graph !== null && graph.nodes.length > 0 ? (
        <div className={css.graphLayout}>
          <section className={css.graphPanel}>
            <div className={css.graphToolbar}>
              <div><span className={css.liveDot} />{t('overview.snapshot')} <small>{generated}</small></div>
              <div className={css.graphLegend}><span data-edge="scope">{t('overview.edgeScope')}</span><span data-edge="temporal">{t('overview.edgeTemporal')}</span><span data-edge="semantic">{t('overview.edgeSemantic')}</span><span data-edge="causal">{t('overview.edgeCausal')}</span><span data-edge="entity">{t('overview.edgeEntity')}</span></div>
            </div>
            <div className={css.graphViewport}><MemoryGraph graph={graph} selectedId={selected === null ? undefined : graphNodeKey(selected)} onSelect={setSelected} /></div>
            <div className={css.graphFooter}><span>{t('overview.graphComposition', { spaces: graphSpaces, memories: graphMemories, entities: graphEntities })}</span><span>{t('overview.graphCount', { visible: Math.min(graph.nodes.length, 60), total: graph.nodes.length })} · {t('overview.graphEdges', { count: graph.edges.length })}</span></div>
          </section>
          <aside className={css.graphInspector} data-empty={selected === null || undefined}>
            {selected === null ? (
              <div className={css.inspectorEmpty}><span className={appearanceClass(css.inspectorLogo, sidebarCss.inspectorGlyph)} aria-hidden="true">◇</span><h3>{t('overview.selectNode')}</h3><p>{t('overview.selectNodeText')}</p></div>
            ) : (
              <>
                <div className={css.inspectorHeading}><span>{t(selectedKind === 'space' ? 'overview.inspectorSpace' : selectedKind === 'entity' ? 'overview.inspectorEntity' : 'overview.inspector')}</span><button type="button" onClick={() => setSelected(null)} aria-label={t('overview.closeInspector')}>×</button></div>
                <div className={css.inspectorChips}><span className={css.categoryChip}>{graphKindLabel(t, selected)}</span>{selected.memoryProviderId !== undefined && <MemoryProviderBadge providerId={selected.memoryProviderId} label={selected.memoryProviderLabel ?? selected.memoryProviderId} />}</div>
                <div className={css.inspectorTitleRow}><h3 className={css.inspectorTitle}>{selected.content}</h3>{selectedKind === 'memory' && selected.content.length > 140 && <button type="button" className={css.inspectorEye} onClick={() => setPreview(selected)} aria-label={t('overview.previewAria')} title={t('overview.previewAria')}><svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true"><path d="M1 8s2.6-4.4 7-4.4S15 8 15 8s-2.6 4.4-7 4.4S1 8 1 8z" fill="none" stroke="currentColor" strokeWidth="1.5" /><circle cx="8" cy="8" r="2.1" fill="currentColor" /></svg></button>}</div>
                {selectedKind === 'space'
                  ? <dl className={css.inspectorMeta}><div><dt>{t('overview.spaceId')}</dt><dd><code>{selected.memoryBodyId ?? selected.id}</code></dd></div><div><dt>{t('overview.containedMemories')}</dt><dd>{selected.occurrenceCount ?? 0}</dd></div></dl>
                  : selectedKind === 'entity'
                    ? <dl className={css.inspectorMeta}><div><dt>{t('overview.entityMentions')}</dt><dd>{selected.occurrenceCount ?? 0}</dd></div><div><dt>{t('term.spaces')}</dt><dd>{selected.memoryBodyNames?.join(' · ') || '—'}</dd></div></dl>
                    : <dl className={css.inspectorMeta}><div><dt>{t('term.space')}</dt><dd>{selected.memoryBodyName ?? '—'} <code>{selected.memoryBodyId ?? ''}</code></dd></div><div><dt>{t('overview.memoryId')}</dt><dd><code>{selected.id}</code></dd></div><div><dt>{t('common.category')}</dt><dd>{categoryLabel(t, selected.category ?? 'general')}</dd></div></dl>}
                <div className={css.inspectorActions}>{selectedKind !== 'space' && <button type="button" className={css.primaryButton} onClick={() => props.onExplore(selected.content)}>{t('overview.exploreNode')}</button>}<button type="button" className={css.secondaryButton} onClick={() => void writeClipboard(selected.id)}>{t('common.copyId')}</button></div>
              </>
            )}
          </aside>
        </div>
      ) : !graphLoading && error === null ? (
        catalogUnavailable
          ? <EmptyState glyph="◇" title={t('overview.unsyncedTitle')}>{t('overview.unsyncedLong')}</EmptyState>
          : catalog?.total === 0
          ? <EmptyState glyph="◇" title={t('overview.emptyTitle')}>{t('overview.emptyLong')}</EmptyState>
          : catalog?.activeCount === 0
            ? <EmptyState glyph="◇" title={t('overview.noActiveTitle')}>{t('overview.noActiveText')}</EmptyState>
            : <EmptyState glyph="◇" title={t(onlyQueryOrUnsupported ? 'overview.noVisualTitle' : 'overview.noContentTitle')}>{t(onlyQueryOrUnsupported ? 'overview.noVisualText' : 'overview.noContentText')}</EmptyState>
      ) : (
        <div className={css.asyncPlaceholder}><span>{t('overview.loading')}</span></div>
      )}
      {preview !== null && <ContentPreview node={preview} kind={graphKindLabel(t, preview)} onClose={() => setPreview(null)} />}
    </div>
  )
}

export function ExplorePage(props: { client: MemorySpacesPageClient; agentClient: MemorySpacesPageClient; agentAvailable: boolean; status: MemorySpacesPageStatus | null; seed: string; writeEnabled: boolean; onForget: (insight: Insight) => Promise<void>; onRevealElement?(element: HTMLElement): void }): JSX.Element {
  const t = useT()
  const pageSize = 6
  const [query, setQuery] = useState(props.seed)
  const [mode, setMode] = useState<'smart' | 'keyword' | 'basic'>('smart')
  const [category, setCategory] = useState<Category | ''>('')
  const [results, setResults] = useState<Insight[]>([])
  const [sources, setSources] = useState<MemoryReadSource[]>([])
  const [searchKind, setSearchKind] = useState<'direct' | 'agent' | null>(null)
  const [agentAnswer, setAgentAnswer] = useState<{ answer: string; citations: string[]; runId: string } | null>(null)
  /** A citation names the recalled memory it points at, not its identifiers. */
  const citationLabel = (citation: string): string => {
    const cited = results.find(result => `${result.memoryBodyId}/${result.id}` === citation || result.id === citation)
    return cited === undefined ? short(citation.split('/').at(-1) ?? citation, 12) : short(cited.content, 40)
  }
  const [searched, setSearched] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [relatedTo, setRelatedTo] = useState<{ insight: Insight; request: number } | null>(null)
  const relatedPaneRef = useRef<HTMLElement | null>(null)
  const [related, setRelated] = useState<Insight[]>([])
  const [relatedLoading, setRelatedLoading] = useState(false)
  const [visibleResultLimit, setVisibleResultLimit] = useState(pageSize)
  const [visibleRelatedLimit, setVisibleRelatedLimit] = useState(pageSize)
  const relatedRequests = useRequestVersion()

  useEffect(() => { if (props.seed !== '') setQuery(props.seed) }, [props.seed])
  // Opening Recall with a query runs it once, with the configured recall limit,
  // whether a conversation turn or another page sent it.
  const ranSeed = useRef('')
  useEffect(() => {
    if (props.seed === '' || props.status === null || ranSeed.current === props.seed) return
    ranSeed.current = props.seed
    void runSearch(false, props.seed)
  }, [props.seed, props.status])
  useEffect(() => {
    if (relatedTo === null || props.onRevealElement === undefined) return
    const reveal = () => {
      if (relatedPaneRef.current !== null && relatedRequests.isCurrent(relatedTo.request)) props.onRevealElement?.(relatedPaneRef.current)
    }
    if (typeof window.requestAnimationFrame !== 'function') { reveal(); return }
    const frame = window.requestAnimationFrame(reveal)
    return () => { if (typeof window.cancelAnimationFrame === 'function') window.cancelAnimationFrame(frame) }
  }, [relatedTo, relatedRequests, props.onRevealElement])

  const runSearch = async (withAgent: boolean, text = query) => {
    if (text.trim() === '') return
    relatedRequests.begin()
    setSearchKind(withAgent ? 'agent' : 'direct'); setSearched(true); setError(null); setRelatedTo(null); setAgentAnswer(null); setVisibleResultLimit(pageSize); setVisibleRelatedLimit(pageSize)
    try {
      const request = { query: text, mode, ...(category === '' ? {} : { category }), limit: props.status?.defaultRecallLimit ?? 10 }
      if (withAgent) {
        const response = await props.agentClient.agentSearch(request)
        setResults(response.results)
        setSources(response.sources ?? [])
        setAgentAnswer({ answer: response.answer, citations: response.citations, runId: response.delegation.runId })
      } else {
        const response = await props.client.search(request)
        setResults(response.results)
        setSources(response.sources ?? [])
      }
    } catch (reason) {
      setError(message(reason)); setResults([]); setSources([]); setAgentAnswer(null)
    } finally {
      setSearchKind(null)
    }
  }

  const search = (event: FormEvent) => { event.preventDefault(); void runSearch(false) }
  const searching = searchKind !== null

  const showRelated = async (insight: Insight) => {
    const request = relatedRequests.begin()
    setRelatedTo({ insight, request }); setRelated([]); setRelatedLoading(true); setError(null); setVisibleRelatedLimit(pageSize)
    try {
      const response = await props.client.related(insight.id, insight.memoryBodyId)
      if (relatedRequests.isCurrent(request)) setRelated(response)
    } catch (reason) {
      if (relatedRequests.isCurrent(request)) setError(message(reason))
    } finally {
      if (relatedRequests.isCurrent(request)) setRelatedLoading(false)
    }
  }

  const forget = async (insight: Insight) => {
    await props.onForget(insight)
    setResults(items => items.filter(item => insightKey(item) !== insightKey(insight)))
    setRelated(items => items.filter(item => insightKey(item) !== insightKey(insight)))
    if (relatedTo !== null && insightKey(relatedTo.insight) === insightKey(insight)) setRelatedTo(null)
  }
  const visibleResults = results.slice(0, visibleResultLimit)
  const visibleRelated = related.slice(0, visibleRelatedLimit)

  return (
    <div className={css.page}>
      <PageHeader title={t('search.title')} description={t('search.description')} meta={t('search.maxResults', { count: props.status?.defaultRecallLimit ?? '—' })} />
      <form className={css.searchBar} onSubmit={event => void search(event)}>
        <SearchField className={css.queryField} label={t('search.queryAria')} value={query} onChange={event => setQuery(event.target.value)} placeholder={t('search.placeholder')} />
        <div className={css.searchControls}>
          <SelectField className={css.searchChoice} label={t('common.category')} ariaLabel={t('search.categoryAria')} value={category} options={[{ value: '' as Category | '', label: t('common.allCategories') }, ...CATEGORIES.map(value => ({ value, label: categoryLabel(t, value) }))]} onChange={setCategory} />
          <SelectField className={css.searchChoice} label={t('search.strategy')} ariaLabel={t('search.modeAria')} value={mode} options={[{ value: 'smart', label: t('search.modeSmart') }, { value: 'keyword', label: t('search.modeKeyword') }, { value: 'basic', label: t('search.modeBasic') }]} onChange={setMode} />
          <div className={css.searchActions}><button type="submit" className={css.secondaryButton} disabled={searching || query.trim() === ''}>{searchKind === 'direct' ? t('search.searching') : t('search.action')}</button><button type="button" className={css.primaryButton} disabled={searching || query.trim() === '' || !props.agentAvailable} onClick={() => void runSearch(true)}>{searchKind === 'agent' ? t('search.agentSearching') : t('search.agentAction')}</button></div>
        </div>
      </form>
      <ReadSourcePanel title={t('search.sourcesTitle')} sources={sources} />
      <div className={css.asyncResults}>
      {searching && <SectionSpinner label={searchKind === 'agent' ? t('search.agentSearching') : t('search.searching')} />}
      {agentAnswer !== null && <section className={css.agentAnswer} aria-label={t('search.agentAnswer')}><div className={css.agentAnswerHeading}><div><span>{t('search.agentAnswerHint')}</span><h3>{t('search.agentAnswer')}</h3></div></div><p>{agentAnswer.answer}</p>{agentAnswer.citations.length > 0 && <div className={css.agentCitations}><small>{t('search.citations')}</small>{agentAnswer.citations.map((citation, index) => <span key={citation} title={citation}>{index + 1}. {citationLabel(citation)}</span>)}</div>}</section>}
      {error !== null && <div className={css.inlineError} role="alert">{error}</div>}
      {!searched && <EmptyState glyph="⌕" title={t('search.startTitle')}>{t('search.startText')}</EmptyState>}
      {searched && !searching && results.length === 0 && error === null && <EmptyState glyph="0" title={t('search.emptyTitle')}>{t('search.emptyText')}</EmptyState>}
      {results.length > 0 && (
        <div className={relatedTo === null ? css.singleColumn : css.resultLayout}>
          <section className={css.results}><div className={css.sectionHeading}><div><h3>{t('search.results')}</h3></div><strong>{results.length}</strong></div>{visibleResults.map(insight => <InsightCard key={insightKey(insight)} insight={insight} writeEnabled={props.writeEnabled} onForget={forget} onRelated={item => void showRelated(item)} />)}{<ProgressiveFooter visible={visibleResults.length} total={results.length} pageSize={pageSize} onMore={() => setVisibleResultLimit(value => value + pageSize)} />}</section>
          {relatedTo !== null && <aside ref={relatedPaneRef} className={css.relatedPane}><div className={css.sectionHeading}><div><h3>{t('search.related')}</h3></div><button type="button" onClick={() => { relatedRequests.begin(); setRelatedTo(null); setRelatedLoading(false) }} aria-label={t('search.closeRelated')}>×</button></div><p className={css.relatedSource}>{relatedTo.insight.content}</p>{relatedLoading && <div className={css.loading}>{t('search.traversing')}</div>}{!relatedLoading && related.length === 0 && <div className={css.muted}>{t('search.noRelated')}</div>}{visibleRelated.map(insight => <InsightCard key={insightKey(insight)} insight={insight} writeEnabled={props.writeEnabled} onForget={forget} onRelated={item => void showRelated(item)} />)}{!relatedLoading && <ProgressiveFooter visible={visibleRelated.length} total={related.length} pageSize={pageSize} onMore={() => setVisibleRelatedLimit(value => value + pageSize)} />}</aside>}
        </div>
      )}
      </div>
    </div>
  )
}

/** Show a loading placeholder only when a read outlasts this, so a quick read never flashes one. */
const ENTITY_PLACEHOLDER_DELAY_MS = 160
const ENTITY_RAIL_PAGE = 10
const ENTITY_CARD_PAGE = 6
/** Memories one entity-memories read returns; more pages load as the list is revealed. */
const ENTITY_MEMORY_BATCH = 48
const ENTITY_RELATED_LIMIT = 20
/** The choice to find related memories lasts for this browser session; each new session starts collapsed. */
const RELATED_PREFERENCE_KEY = 'dsh-mnemon.memory-spaces.entities.related'

function readRelatedPreference(): boolean {
  try { return globalThis.sessionStorage?.getItem(RELATED_PREFERENCE_KEY) === 'open' } catch { return false }
}

function writeRelatedPreference(open: boolean): void {
  try {
    if (open) globalThis.sessionStorage?.setItem(RELATED_PREFERENCE_KEY, 'open')
    else globalThis.sessionStorage?.removeItem(RELATED_PREFERENCE_KEY)
  } catch { /* Without storage the choice lasts until the page closes. */ }
}

function useDelayedFlag(active: boolean, delayMs = ENTITY_PLACEHOLDER_DELAY_MS): boolean {
  const [shown, setShown] = useState(false)
  useEffect(() => {
    if (!active) { setShown(false); return }
    const timer = setTimeout(() => setShown(true), delayMs)
    return () => clearTimeout(timer)
  }, [active, delayMs])
  return active && shown
}

function InsightSkeletons(props: { count: number }): JSX.Element {
  return <div aria-hidden="true" data-placeholder="">{Array.from({ length: props.count }, (_, index) => <div key={index} className={css.insightSkeleton}><span /><span /><span /></div>)}</div>
}

/** ProgressiveFooter's layout, with a busy button while the next page loads. */
function EntityFooter(props: { visible: number; total: number; pageSize: number; busy: boolean; onMore: () => void }): JSX.Element | null {
  const t = useT()
  if (props.total === 0) return null
  const remaining = Math.max(0, props.total - props.visible)
  return <div className={css.listProgress}><span>{t('common.showing', { visible: props.visible, total: props.total })}</span>{remaining > 0 && <button type="button" className={css.secondaryButton} disabled={props.busy} aria-busy={props.busy || undefined} onClick={props.onMore}>{t('common.showMore', { count: Math.min(props.pageSize, remaining) })}</button>}</div>
}

/** The active spaces from the directory, shown while their entity indexes are read. */
function pendingEntitySources(catalog: MemorySpaceCatalog): MemoryReadSource[] {
  return catalog.items.filter(body => body.active && body.providerEnabled !== false).map(body => ({
    memoryBodyId: body.id,
    memoryBodyName: body.name,
    providerId: body.provider.id,
    providerLabel: body.provider.label,
    mode: body.provider.capabilities.entities ? 'entities' : 'unsupported',
    status: body.provider.capabilities.entities ? 'loading' : 'unsupported',
    itemCount: 0,
  }))
}

export function EntitiesPage(props: { client: MemorySpacesPageClient; revision: number; writeEnabled: boolean; onForget: (insight: Insight) => Promise<void>; onExplore: (query: string) => void }): JSX.Element {
  const t = useT()
  const { client } = props
  const [rail, setRail] = useState<EntityView | null>(null)
  const [railLoading, setRailLoading] = useState(true)
  const [railError, setRailError] = useState<string | null>(null)
  const [pendingSources, setPendingSources] = useState<MemoryReadSource[]>([])
  const [entity, setEntity] = useState('')
  const [entityFilter, setEntityFilter] = useState('')
  const [visibleEntityLimit, setVisibleEntityLimit] = useState(ENTITY_RAIL_PAGE)
  const [selected, setSelected] = useState<string | undefined>(undefined)
  const [memories, setMemories] = useState<EntityMemoriesView | null>(null)
  const [memoriesLoading, setMemoriesLoading] = useState(false)
  const [memoriesError, setMemoriesError] = useState<string | null>(null)
  const [moreLoading, setMoreLoading] = useState(false)
  const [visibleMemoryLimit, setVisibleMemoryLimit] = useState(ENTITY_CARD_PAGE)
  const [related, setRelated] = useState<EntityRelatedView | null>(null)
  const [relatedLoading, setRelatedLoading] = useState(false)
  const [relatedError, setRelatedError] = useState<string | null>(null)
  const [visibleRelatedLimit, setVisibleRelatedLimit] = useState(ENTITY_CARD_PAGE)
  // Related memories cost a recall per space, so they load only when asked for; the choice is remembered.
  const [relatedOpen, setRelatedOpen] = useState(readRelatedPreference)
  const relatedOpenRef = useRef(relatedOpen)
  // Whether related memories show by themselves, because nothing carries the selection.
  const relatedAutoRef = useRef(false)
  const relatedId = useId()
  const railRequests = useRequestVersion()
  const directoryRequests = useRequestVersion()
  const memoryRequests = useRequestVersion()
  const moreRequests = useRequestVersion()
  const relatedRequests = useRequestVersion()
  const selectedRef = useRef<string | undefined>(undefined)
  const revisionRef = useRef(props.revision)
  // One id per page view, so its next selection cancels the related read it is still waiting for; other tabs keep theirs.
  const [view] = useState(() => globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`)
  const split = client.entityMemories !== undefined && client.entityRelated !== undefined

  const loadRail = useCallback(async () => {
    const request = railRequests.begin()
    setRailLoading(true); setRailError(null)
    try {
      const view = await client.entities()
      if (railRequests.isCurrent(request)) setRail(view)
    } catch (reason) {
      if (railRequests.isCurrent(request)) setRailError(message(reason))
    } finally {
      if (railRequests.isCurrent(request)) setRailLoading(false)
    }
  }, [client, railRequests])

  const loadRelated = useCallback((name: string) => {
    if (client.entityRelated === undefined) return
    const request = relatedRequests.begin()
    setRelatedLoading(true); setRelatedError(null)
    void client.entityRelated(name, ENTITY_RELATED_LIMIT, view).then(next => {
      if (relatedRequests.isCurrent(request)) setRelated(next)
    }, (reason: unknown) => {
      if (relatedRequests.isCurrent(request)) setRelatedError(message(reason))
    }).finally(() => {
      if (relatedRequests.isCurrent(request)) setRelatedLoading(false)
    })
  }, [client, relatedRequests, view])

  /**
   * The carrying memories load at once; related memories only when they were asked for, or when
   * nothing carries the name. A refresh keeps the current cards until they are replaced.
   */
  const loadSelection = useCallback((name: string, refresh: boolean) => {
    const memoryRequest = memoryRequests.begin()
    relatedRequests.begin()
    moreRequests.begin()
    setMoreLoading(false)
    setRelatedLoading(false); setRelatedError(null)
    if (!refresh) {
      setMemories(null)
      setVisibleMemoryLimit(ENTITY_CARD_PAGE); setVisibleRelatedLimit(ENTITY_CARD_PAGE)
      relatedAutoRef.current = false
    }
    // Related memories from before a refresh stay while shown, until replaced; collapsed, they are read again when opened.
    if (!refresh || !(relatedOpenRef.current || relatedAutoRef.current)) setRelated(null)
    setMemoriesLoading(true); setMemoriesError(null)
    const memoryRead: Promise<EntityMemoriesView> = split
      ? client.entityMemories!(name, 0, ENTITY_MEMORY_BATCH)
      : client.entities(name, 20).then(view => ({ entity: view.selected ?? name, total: view.insights.length, offset: 0, items: view.insights, complete: view.complete ?? true, sources: view.sources ?? [] }))
    void memoryRead.then(next => {
      if (!memoryRequests.isCurrent(memoryRequest)) return
      setMemories(next)
      // Nothing carries a name the user asked for: related memories are all the page can offer.
      const auto = split && next.total === 0
      if (auto && !relatedOpenRef.current) loadRelated(name)
      else if (relatedAutoRef.current && !relatedOpenRef.current) setRelated(null)
      relatedAutoRef.current = auto
    }, (reason: unknown) => {
      if (memoryRequests.isCurrent(memoryRequest)) setMemoriesError(message(reason))
    }).finally(() => {
      if (memoryRequests.isCurrent(memoryRequest)) setMemoriesLoading(false)
    })
    if (split && relatedOpenRef.current) loadRelated(name)
  }, [client, loadRelated, memoryRequests, moreRequests, relatedRequests, split])

  const select = (name: string) => {
    const trimmed = name.trim()
    if (trimmed === '') return
    selectedRef.current = trimmed
    setSelected(trimmed)
    setEntity(trimmed)
    loadSelection(trimmed, false)
  }

  // The directory needs no Provider I/O, so the spaces show before their indexes are read.
  useEffect(() => {
    const request = directoryRequests.begin()
    void client.bodyDirectory().then(catalog => {
      if (directoryRequests.isCurrent(request)) setPendingSources(pendingEntitySources(catalog))
    }, () => undefined)
  }, [client, directoryRequests])
  useEffect(() => { void loadRail() }, [loadRail, props.revision])
  useEffect(() => {
    if (revisionRef.current === props.revision) return
    revisionRef.current = props.revision
    if (selectedRef.current !== undefined) loadSelection(selectedRef.current, true)
  }, [loadSelection, props.revision])

  const showMoreMemories = () => {
    const current = memories
    if (current === null || moreLoading) return
    const nextVisible = visibleMemoryLimit + ENTITY_CARD_PAGE
    if (nextVisible <= current.items.length || current.items.length >= current.total || client.entityMemories === undefined) {
      setVisibleMemoryLimit(nextVisible)
      return
    }
    const request = moreRequests.begin()
    setMoreLoading(true)
    void client.entityMemories(current.entity, current.items.length, ENTITY_MEMORY_BATCH).then(page => {
      if (!moreRequests.isCurrent(request)) return
      setMemories(previous => {
        if (previous === null) return previous
        const seen = new Set(previous.items.map(insightKey))
        return { ...page, offset: 0, items: [...previous.items, ...page.items.filter(item => !seen.has(insightKey(item)))] }
      })
      setVisibleMemoryLimit(nextVisible)
    }, (reason: unknown) => {
      if (moreRequests.isCurrent(request)) setMemoriesError(message(reason))
    }).finally(() => {
      if (moreRequests.isCurrent(request)) setMoreLoading(false)
    })
  }

  const findRelated = () => {
    relatedOpenRef.current = true
    setRelatedOpen(true)
    writeRelatedPreference(true)
    if (selected !== undefined && related === null && !relatedLoading) loadRelated(selected)
  }
  const hideRelated = () => {
    relatedOpenRef.current = false
    setRelatedOpen(false)
    writeRelatedPreference(false)
  }

  /** A forgotten memory leaves both lists at once; the refresh that follows replaces them. */
  const forget = async (insight: Insight) => {
    await props.onForget(insight)
    const key = insightKey(insight)
    setMemories(current => {
      const items = current?.items.filter(item => insightKey(item) !== key)
      return current === null || items === undefined || items.length === current.items.length ? current : { ...current, total: Math.max(0, current.total - 1), items }
    })
    setRelated(current => current === null ? current : { ...current, items: current.items.filter(item => insightKey(item) !== key) })
  }

  const submit = (event: FormEvent) => { event.preventDefault(); select(entity) }
  const items = rail?.items ?? []
  const filterKey = entityFilter.trim().toLocaleLowerCase()
  const filteredItems = useMemo(() => filterKey === '' ? items : items.filter(item => item.entity.toLocaleLowerCase().includes(filterKey)), [items, filterKey])
  const visibleEntities = filteredItems.slice(0, visibleEntityLimit)
  const selectedKey = selected === undefined ? undefined : normalizeEntityKey(selected)
  const railCount = selectedKey === undefined ? undefined : items.find(item => normalizeEntityKey(item.entity) === selectedKey)?.count
  const sources = rail?.sources ?? pendingSources
  const hasEntityProvider = sources.length === 0 || sources.some(source => source.mode === 'entities' && source.status !== 'unavailable')
  const railPlaceholder = useDelayedFlag(rail === null && railLoading)
  const memoryPlaceholder = useDelayedFlag(memories === null && memoriesLoading)
  // When nothing carries the entity, related memories open by themselves and need no toggle.
  const relatedAuto = memories !== null && memories.total === 0
  const relatedShown = split && (relatedOpen || relatedAuto)
  const relatedPlaceholder = useDelayedFlag(relatedShown && related === null && relatedLoading)
  const railBusy = useDelayedFlag(railLoading)
  const detailBusy = useDelayedFlag(memoriesLoading || (relatedShown && relatedLoading))
  const memoryTotal = memories?.total ?? railCount
  const visibleMemories = memories?.items.slice(0, visibleMemoryLimit) ?? []
  const visibleRelated = related?.items.slice(0, visibleRelatedLimit) ?? []
  const nothingFound = memories !== null && memories.total === 0 && (!split || (related !== null && related.items.length === 0))
  const card = (insight: Insight) => <InsightCard key={insightKey(insight)} insight={insight} writeEnabled={props.writeEnabled} onForget={forget} onRelated={() => props.onExplore(insight.content)} />

  return (
    <div className={css.page}>
      <PageHeader title={t('entities.title')} description={t('entities.description')} {...(rail === null ? {} : { meta: t('entities.count', { count: rail.total ?? rail.items.length }) })} {...(railBusy ? { loadingLabel: t('entities.railLoading') } : {})} />
      <ReadSourcePanel title={t('entities.sourcesTitle')} sources={sources} />
      {rail !== null && !hasEntityProvider
        ? <EmptyState glyph="◎" title={t('entities.unsupportedTitle')}>{t('entities.unsupportedText')}</EmptyState>
        : <div className={css.entityLayout}>
        <aside className={css.entityRail} aria-busy={rail === null && railLoading ? true : undefined}>
          <form className={css.entitySearch} onSubmit={submit}><SearchField label={t('entities.nameAria')} value={entity} onChange={event => { setEntity(event.target.value); setEntityFilter(event.target.value); setVisibleEntityLimit(ENTITY_RAIL_PAGE) }} onKeyDown={event => { if (event.key === 'Escape' && entityFilter !== '') { event.preventDefault(); setEntityFilter('') } }} placeholder={t('entities.placeholder')} /><button type="submit" className={css.secondaryButton} disabled={entity.trim() === ''}>{t('entities.action')}</button></form>
          <div className={css.entityHeading}><span>{t('entities.top')}</span><small>{entityFilter === '' ? t('entities.frequency') : t('entities.filterHint')}</small></div>
          {railPlaceholder && <div className={css.entityList} aria-hidden="true" data-placeholder="">{Array.from({ length: 8 }, (_, index) => <span key={index} className={css.entityRailSkeleton} />)}</div>}
          <div className={css.entityList}>{visibleEntities.map(item => <button key={item.entity} type="button" aria-pressed={selectedKey === normalizeEntityKey(item.entity)} onClick={() => select(item.entity)}><span>{item.entity}</span><strong>{item.count}</strong></button>)}</div>
          {visibleEntities.length > 0 && filteredItems.length > visibleEntityLimit && <ProgressiveFooter compact visible={visibleEntities.length} total={filteredItems.length} pageSize={ENTITY_RAIL_PAGE} onMore={() => setVisibleEntityLimit(value => value + ENTITY_RAIL_PAGE)} />}
          {rail === null && railError !== null && <div className={appearanceClass(css.inlineError, css.entityRetry)} role="alert"><span>{railError}</span><button type="button" className={css.secondaryButton} onClick={() => void loadRail()}>{t('entities.retry')}</button></div>}
          {rail !== null && items.length === 0 && <p className={css.muted}>{t('entities.emptyRail')}</p>}
          {rail !== null && items.length > 0 && visibleEntities.length === 0 && entityFilter !== '' && <p className={css.muted}>{t('entities.filterEmpty')}</p>}
        </aside>
        <section className={appearanceClass(css.entityResults, css.asyncResults)} aria-busy={memoriesLoading || (relatedShown && relatedLoading) ? true : undefined}>
          {detailBusy && selected !== undefined && <SectionSpinner label={t('entities.loading')} />}
          {selected === undefined && <EmptyState glyph="◎" title={t('entities.selectTitle')}>{t('entities.selectText')}</EmptyState>}
          {selected !== undefined && nothingFound && <EmptyState glyph="0" title={t('entities.emptyTitle')}>{t('entities.emptyText')}</EmptyState>}
          {selected !== undefined && !nothingFound && <>
            <div className={css.entitySection}>
              <div className={css.sectionHeading}><div><h3>{memories?.entity ?? selected}</h3><span>{t('entities.memoriesHint')}</span></div>{memoryTotal !== undefined && <strong>{memoryTotal}</strong>}</div>
              <div className={css.entitySectionBody}>
                {memoriesError !== null && <div className={appearanceClass(css.inlineError, css.entityRetry)} role="alert"><span>{memoriesError}</span><button type="button" className={css.secondaryButton} onClick={() => loadSelection(selected, memories !== null)}>{t('entities.retry')}</button></div>}
                {memoryPlaceholder && <InsightSkeletons count={Math.min(3, Math.max(1, railCount ?? 3))} />}
                {memories !== null && memories.total === 0 && <p className={css.entityQuiet}>{t('entities.noCarrying')}</p>}
                {visibleMemories.map(card)}
                {memories !== null && <EntityFooter visible={visibleMemories.length} total={memories.total} pageSize={ENTITY_CARD_PAGE} busy={moreLoading} onMore={showMoreMemories} />}
              </div>
            </div>
            {split && <div className={css.entitySection}>
              <div className={css.entityRelatedHead}>
                <div className={css.sectionHeading}><div><h3 id={`${relatedId}-title`}>{t('entities.relatedTitle')}</h3><span>{t('entities.relatedHint')}</span></div>{relatedShown && related !== null && <strong>{related.items.length}</strong>}</div>
                {!relatedAuto && <button type="button" className={css.secondaryButton} aria-expanded={relatedShown} aria-controls={`${relatedId}-list`} onClick={relatedShown ? hideRelated : findRelated}>{t(relatedShown ? 'entities.hideRelated' : 'entities.findRelated')}</button>}
              </div>
              <div id={`${relatedId}-list`} role="region" aria-labelledby={`${relatedId}-title`} className={css.entitySectionBody} hidden={!relatedShown}>
                {relatedShown && <>
                  {relatedError !== null && <div className={appearanceClass(css.inlineError, css.entityRetry)} role="alert"><span>{relatedError}</span><button type="button" className={css.secondaryButton} onClick={() => loadRelated(selected)}>{t('entities.retry')}</button></div>}
                  {relatedPlaceholder && <InsightSkeletons count={2} />}
                  {related !== null && related.items.length === 0 && <p className={css.entityQuiet}>{t('entities.noRelated')}</p>}
                  {visibleRelated.map(card)}
                  {related !== null && <ProgressiveFooter visible={visibleRelated.length} total={related.items.length} pageSize={ENTITY_CARD_PAGE} onMore={() => setVisibleRelatedLimit(value => value + ENTITY_CARD_PAGE)} />}
                </>}
              </div>
            </div>}
          </>}
        </section>
      </div>}
    </div>
  )
}

export function PersistenceStrategyDialog(props: {
  client: MemorySpacesPageClient
  settingsScope: MemoryPlacementSettings
  config: MemoryPlacementPageConfig | undefined
  writable: boolean
  agentAvailable: boolean
  onClose: () => void
}): JSX.Element {
  const t = useT()
  const strategyFormId = useId()
  const configured = props.config?.persistenceStrategy
  const [mode, setMode] = useState<MemoryPlacementMode>(configured?.mode ?? 'manual')
  const [providerId, setProviderId] = useState<MemoryProviderId>(configured?.providerId ?? 'mnemon-native')
  const [prompt, setPrompt] = useState(configured?.prompt ?? '')
  const [dataBoundary, setDataBoundary] = useState<'allow-remote' | 'local-only'>(configured?.rules?.dataBoundary ?? 'allow-remote')
  const [preference, setPreference] = useState<MemoryPlacementPreference>(configured?.rules?.preference ?? 'balanced')
  const [requiredCapabilities, setRequiredCapabilities] = useState<MemoryPlacementCapability[]>(configured?.rules?.requiredCapabilities ?? [])
  const [automaticProviderIds, setAutomaticProviderIds] = useState<MemoryProviderId[]>(configured?.rules?.allowedProviderIds ?? ['mnemon-native'])
  const [providerDrafts, setProviderDrafts] = useState<ProviderDrafts>(configured?.providerConnections ?? {})
  const [providers, setProviders] = useState<MemoryProviderDescriptor[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let current = true
    void props.client.bodyDirectory().then(catalog => {
      if (!current) return
      const next = catalog.providers
      setProviders(next)
      setProviderDrafts(previous => mergeProviderDefaults(next, previous))
      setProviderId(currentProviderId => readyProviderId(next, currentProviderId) ?? currentProviderId)
      // Without a saved candidate list, start from the provider that is ready now.
      if (configured?.rules?.allowedProviderIds === undefined) {
        setAutomaticProviderIds(current => {
          const ready = current.filter(id => next.some(provider => provider.id === id && provider.serviceConfigured !== false))
          const first = readyProviderId(next)
          return ready.length > 0 || first === undefined ? ready : [first]
        })
      }
    }).catch(reason => { if (current) setError(message(reason)) }).finally(() => { if (current) setLoading(false) })
    return () => { current = false }
  }, [props.client])

  const selectedProvider = providers.find(provider => provider.id === providerId)
  const selectedAutomaticProviders = automaticProviderIds.map(id => providers.find(provider => provider.id === id)).filter((provider): provider is MemoryProviderDescriptor => provider !== undefined)
  const selectedProvidersValid = mode === 'manual'
    ? providerDraftComplete(selectedProvider, providerDrafts[providerId])
    : automaticProviderIds.length > 0 && selectedAutomaticProviders.length === automaticProviderIds.length && selectedAutomaticProviders.every(provider => providerDraftComplete(provider, providerDrafts[provider.id]))
  const updateDraft = (id: MemoryProviderId, key: string, value: string | number | boolean) => setProviderDrafts(current => ({ ...current, [id]: { ...(current[id] ?? {}), [key]: value } }))
  const toggleCapability = (capability: MemoryPlacementCapability) => setRequiredCapabilities(current => current.includes(capability) ? current.filter(value => value !== capability) : [...current, capability])
  const toggleProvider = (id: MemoryProviderId, selected: boolean) => setAutomaticProviderIds(current => selected ? [...new Set([...current, id])] : current.filter(value => value !== id))

  const save = async (event: FormEvent) => {
    event.preventDefault()
    if (loading || saving || !props.writable || !selectedProvidersValid) return
    const selectedIds = mode === 'manual' ? [providerId] : automaticProviderIds
    const connections = Object.fromEntries(selectedIds.filter(id => providers.find(provider => provider.id === id)?.origin !== 'native').map(id => [id, providerDrafts[id] ?? {}])) as ProviderDrafts
    setSaving(true); setError(null)
    try {
      await props.settingsScope.setPath(['persistenceStrategy'], {
        mode,
        providerId,
        prompt,
        rules: {
          allowedProviderIds: automaticProviderIds,
          dataBoundary,
          requiredCapabilities,
          preference,
        },
        providerConnections: connections,
      })
      props.onClose()
    } catch (reason) { setError(message(reason)) } finally { setSaving(false) }
  }

  return <SidebarModal title={t('strategy.title')} description={t('strategy.description')} busy={saving} contentReady={!loading} wide onClose={props.onClose} footer={<div className={css.modalFooterActions}><button type="button" data-dialog-close className={css.secondaryButton} disabled={saving} onClick={props.onClose}>{t('common.cancel')}</button><button type="submit" form={strategyFormId} className={css.primaryButton} disabled={loading || saving || !props.writable || !selectedProvidersValid}>{saving ? t('strategy.saving') : t('strategy.save')}</button></div>}>
    <form id={strategyFormId} className={appearanceClass(css.bodyEdit, css.strategyForm)} onSubmit={event => void save(event)}>
      {loading && <div className={css.strategyLoading}><SectionSpinner label={t('strategy.loading')} /><span>{t('strategy.loading')}</span></div>}
      {error !== null && <div className={css.inlineError} role="alert">{error}</div>}
      {!loading && <>
        <section className={css.createSection}>
          <div className={css.createSectionHeading}><span>01</span><div><strong>{t('strategy.modeTitle')}</strong><small>{t('strategy.modeHint')}</small></div></div>
          <fieldset className={css.placementMode}><legend>{t('overview.placementMode')}</legend>
            <label data-selected={mode === 'manual' || undefined}><input type="radio" name="persistence-mode" value="manual" checked={mode === 'manual'} data-autofocus={mode === 'manual' || undefined} onChange={() => setMode('manual')} /><i className={css.choiceControl} data-kind="radio" aria-hidden="true" /><span><strong>{t('overview.placementManual')}</strong><small>{t('strategy.manualHint')}</small></span></label>
            <label data-selected={mode === 'automatic' || undefined}><input type="radio" name="persistence-mode" value="automatic" checked={mode === 'automatic'} data-autofocus={mode === 'automatic' || undefined} onChange={() => setMode('automatic')} /><i className={css.choiceControl} data-kind="radio" aria-hidden="true" /><span><strong>{t('overview.placementAutomatic')} <em>{t('overview.recommended')}</em></strong><small>{t('strategy.automaticHint')}</small></span></label>
          </fieldset>
        </section>
        <section className={css.createSection}>
          <div className={css.createSectionHeading}><span>02</span><div><strong>{t(mode === 'manual' ? 'strategy.manualTitle' : 'strategy.automaticTitle')}</strong><small>{t(mode === 'manual' ? 'strategy.manualDescription' : 'strategy.automaticDescription')}</small></div></div>
          {mode === 'manual' ? <>
            <fieldset className={css.providerChoice}><legend>{t('overview.providerLabel')}</legend>{readyFirst(providers).map(provider => {
              const disabled = provider.serviceConfigured === false
              return <label key={provider.id} data-selected={providerId === provider.id || undefined} data-native={provider.origin === 'native' || undefined} data-disabled={disabled || undefined}>
                <input type="radio" name="strategy-provider" value={provider.id} checked={providerId === provider.id} disabled={disabled} onChange={() => setProviderId(provider.id)} />
                <ProviderIcon providerId={provider.id} icon={provider.icon} className={css.providerChoiceIcon} />
                <span><strong>{provider.label}{provider.origin === 'native' && <em>{t('overview.nativeOfficial')}</em>}</strong><small>{disabled ? t(providerUnavailableKey(provider)) : `${t(`overview.workspaceBinding.${provider.workspaceBinding}`)} · ${providerSummary(t, provider)}`}</small></span>
                <i className={css.choiceControl} data-kind="radio" aria-hidden="true" />
              </label>
            })}</fieldset>
            {selectedProvider !== undefined && selectedProvider.origin !== 'native' && <ProviderMemoryFields provider={selectedProvider} connection={providerDrafts[selectedProvider.id] ?? {}} onChange={(key, value) => updateDraft(selectedProvider.id, key, value)} />}
          </> : <section className={css.placementPolicy} aria-label={t('overview.placementPolicy')}>
            <div className={css.placementPolicyHeading}><div><strong>{t('overview.placementPolicy')}</strong><small>{t('overview.placementPolicyHint')}</small></div><TaskAgentTag available={props.agentAvailable} /></div>
            <label>{t('overview.placementPrompt')}<textarea aria-label={t('overview.placementPrompt')} value={prompt} onChange={event => setPrompt(event.target.value)} placeholder={t('overview.placementPromptPlaceholder')} rows={3} maxLength={4000} /></label>
            <div className={css.placementRuleGrid}><SelectField label={t('overview.dataBoundary')} value={dataBoundary} options={[{ value: 'allow-remote', label: t('overview.dataBoundaryRemote') }, { value: 'local-only', label: t('overview.dataBoundaryLocal') }]} onChange={value => { setDataBoundary(value); if (value === 'local-only') setAutomaticProviderIds(current => current.filter(id => providers.find(provider => provider.id === id)?.kind === 'local')) }} /><SelectField label={t('overview.preference')} value={preference} options={[{ value: 'balanced', label: t('overview.preferenceBalanced') }, { value: 'local-first', label: t('overview.preferenceLocal') }, { value: 'shared-first', label: t('overview.preferenceShared') }]} onChange={setPreference} /></div>
            <fieldset className={css.capabilityRules}><legend>{t('overview.requiredCapabilities')}</legend>{(['graph', 'exact-write', 'forget'] as const).map(capability => {
              const selected = requiredCapabilities.includes(capability)
              return <label key={capability} data-selected={selected || undefined}><input type="checkbox" checked={selected} onChange={() => toggleCapability(capability)} /><i className={css.choiceControl} data-kind="check" aria-hidden="true" /><span>{t(`overview.capability.${capability}`)}</span></label>
            })}</fieldset>
            <div className={css.placementCandidates}>{readyFirst(providers).map(provider => {
              const selected = automaticProviderIds.includes(provider.id)
              // A saved candidate that is no longer ready can still be cleared.
              const disabled = (provider.serviceConfigured === false && !selected) || (dataBoundary === 'local-only' && provider.kind === 'remote')
              return <label key={provider.id} data-selected={selected || undefined} data-disabled={disabled || undefined}><input type="checkbox" checked={selected} disabled={disabled} onChange={event => toggleProvider(provider.id, event.target.checked)} /><ProviderIcon providerId={provider.id} icon={provider.icon} className={css.candidateIcon} /><span><strong>{provider.label}</strong><small>{provider.serviceConfigured === false ? t(providerUnavailableKey(provider)) : provider.origin === 'native' ? t('overview.candidateNativeReady') : provider.kind === 'local' ? t('overview.candidateLocal') : t('overview.candidateRemote')}</small></span><i className={css.choiceControl} data-kind="check" aria-hidden="true" /></label>
            })}</div>
            {automaticProviderIds.map(id => { const provider = providers.find(candidate => candidate.id === id); return provider === undefined || provider.origin === 'native' ? null : <ProviderMemoryFields key={id} provider={provider} connection={providerDrafts[id] ?? {}} onChange={(key, value) => updateDraft(id, key, value)} /> })}
          </section>}
        </section>
      </>}
    </form>
  </SidebarModal>
}

export function RememberPage(props: { client: MemorySpacesPageClient; agentAvailable: boolean; memoryBodies: MemorySpaceView[]; writeEnabled: boolean; seed: string; onMutate: () => void; onClose: () => void; onView?: () => void }): JSX.Element {
  const t = useT()
  const rememberFormId = useId()
  const candidateId = useId()
  const [content, setContent] = useState(props.seed)
  const [category, setCategory] = useState<Category>('general')
  const [importance, setImportance] = useState(3)
  const [tags, setTags] = useState('')
  const [entities, setEntities] = useState('')
  const [memoryBodyId, setMemoryBodyId] = useState('')
  const [supervising, setSupervising] = useState(false)
  const [saving, setSaving] = useState(false)
  const [outcome, setOutcome] = useState<{ content: string; action?: string; summary?: string; error?: string } | null>(null)
  useEffect(() => { if (props.seed !== '') setContent(props.seed) }, [props.seed])
  useEffect(() => {
    if (memoryBodyId === '' && props.memoryBodies.length > 0) setMemoryBodyId((props.memoryBodies.find(body => body.active) ?? props.memoryBodies[0])!.id)
  }, [memoryBodyId, props.memoryBodies])
  const selectedMemoryBody = props.memoryBodies.find(body => body.id === memoryBodyId)
  const candidate = content.trim()
  // A receipt answers one text: sending the same text again waits for an edit.
  const answered = outcome !== null && outcome.content === candidate
  const busy = supervising || saving

  const supervise = async (event: FormEvent) => {
    event.preventDefault()
    if (candidate === '' || !props.agentAvailable || answered || busy) return
    setSupervising(true); setOutcome(null)
    try {
      const response = await props.client.supervise(candidate)
      setOutcome({ content: candidate, action: response.action, summary: response.summary })
      props.onMutate()
    } catch (reason) { setOutcome({ content: candidate, error: message(reason) }) } finally { setSupervising(false) }
  }

  const manualSave = async (event: FormEvent) => {
    event.preventDefault()
    if (candidate === '' || busy) return
    setSaving(true); setOutcome(null)
    try {
      const response = await props.client.remember({ content: candidate, category, importance, tags: tags.split(',').map(value => value.trim()).filter(Boolean), entities: entities.split(',').map(value => value.trim()).filter(Boolean), source: 'user', ...(memoryBodyId === '' ? {} : { memoryBodyId }) })
      setOutcome({ content: candidate, action: typeof response.action === 'string' ? response.action : 'stored', summary: typeof response.summary === 'string' ? response.summary : '' })
      props.onMutate()
    } catch (reason) { setOutcome({ content: candidate, error: message(reason) }) } finally { setSaving(false) }
  }

  const composer = <section className={css.supervisedComposer}>
    <form id={rememberFormId} className={css.supervisedForm} onSubmit={event => void supervise(event)}>
      <div className={css.candidateHeading}><label htmlFor={candidateId}>{t('remember.candidate')}</label><TaskAgentTag available={props.agentAvailable} /></div>
      <textarea id={candidateId} value={content} onChange={event => setContent(event.target.value)} maxLength={8000} rows={8} placeholder={t('remember.placeholder')} />
      {!props.agentAvailable && <p className={css.sessionHint}>{t('remember.taskAgentHint')}</p>}
      {outcome !== null && <WriteReceipt action={outcome.action} summary={outcome.summary} error={outcome.error} onView={props.onView} />}
    </form>
    <details className={css.advancedWrite}>
      <summary><span><strong>{t('remember.advanced')}</strong><small>{t('remember.advancedHint')}</small></span><span>{t('remember.expand')}</span></summary>
      <form className={css.manualForm} onSubmit={event => void manualSave(event)}>
        <div className={css.formGrid}>
          <div className={css.fieldWide}>
            <SelectField label={t('remember.target')} value={memoryBodyId} options={props.memoryBodies.map(body => ({ value: body.id, label: `${body.name} · ${body.provider.label}${body.active ? ` · ${t('common.active')}` : ''}` }))} onChange={setMemoryBodyId} />
            {selectedMemoryBody?.provider.capabilities.writeMode === 'async-extracting' && <small className={css.providerWriteHint}>{t('remember.asyncProviderHint')}</small>}
          </div>
          <SelectField label={t('common.category')} value={category} options={CATEGORIES.map(value => ({ value, label: categoryLabel(t, value) }))} onChange={setCategory} />
          <SelectField label={t('common.importanceLabel')} value={String(importance)} options={[1, 2, 3, 4, 5].map(value => ({ value: String(value), label: `${value} / 5` }))} onChange={value => setImportance(Number(value))} />
          <label className={css.fieldWide}>{t('remember.entities')}<input value={entities} onChange={event => setEntities(event.target.value)} placeholder="SQLite, DSH" /></label>
          <label className={css.fieldWide}>{t('remember.tags')}<input value={tags} onChange={event => setTags(event.target.value)} placeholder="architecture, local-first" /></label>
        </div>
        <div className={css.manualActions}><p>{t('remember.advancedText')}</p><button type="submit" className={css.secondaryButton} disabled={busy || candidate === '' || memoryBodyId === '' || answered}>{saving ? t('remember.saving') : t('remember.advancedAction')}</button></div>
      </form>
    </details>
  </section>

  return <SidebarModal title={t('remember.title')} description={t('remember.description')} busy={busy} onClose={props.onClose} footer={props.writeEnabled ? <div className={css.modalFooterActions}><button type="button" data-dialog-close className={css.secondaryButton} disabled={busy} onClick={props.onClose}>{t(outcome === null ? 'common.cancel' : 'common.close')}</button><button type="submit" form={rememberFormId} className={css.primaryButton} disabled={busy || candidate === '' || !props.agentAvailable || answered}>{supervising ? t('remember.processing') : t('remember.action')}</button></div> : undefined}>{props.writeEnabled ? composer : <EmptyState glyph="⊘" title={t('remember.readOnlyTitle')}>{t('remember.readOnlyText')}</EmptyState>}</SidebarModal>
}

export function ListPage(props: { client: MemorySpacesPageClient; revision: number; writeEnabled: boolean; onForget: (insight: Insight) => Promise<void>; onClone: (insight: Insight) => void; onExplore: (query: string) => void }): JSX.Element {
  const t = useT()
  const pageSize = 12
  const [query, setQuery] = useState('')
  const [category, setCategory] = useState<Category | ''>('')
  const [view, setView] = useState<MemoryListView | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [visibleLimit, setVisibleLimit] = useState(pageSize)
  const [selectedBodyId, setSelectedBodyId] = useState<string | undefined>()

  const load = useCallback(async () => {
    setLoading(true); setError(null)
    try { setView(await props.client.list({ ...(query.trim() === '' ? {} : { query }), ...(category === '' ? {} : { category }), limit: 1000 })) } catch (reason) { setError(message(reason)) } finally { setLoading(false) }
  }, [category, props.client, query])
  useEffect(() => { setVisibleLimit(pageSize); void load() }, [pageSize, props.revision])
  const submit = (event: FormEvent) => { event.preventDefault(); setVisibleLimit(pageSize); void load() }
  const forget = async (insight: Insight) => { await props.onForget(insight); setView(current => current === null ? current : { ...current, total: Math.max(0, current.total - 1), items: current.items.filter(item => insightKey(item) !== insightKey(insight)) }) }
  const filteredItems = view?.items.filter(item => selectedBodyId === undefined || item.memoryBodyId === selectedBodyId) ?? []
  const visibleItems = filteredItems.slice(0, visibleLimit)
  const sources = view?.sources ?? []
  const waitingForQuery = query.trim() === '' && sources.some(source => source.status === 'query-required' && (selectedBodyId === undefined || source.memoryBodyId === selectedBodyId))
  const selectBody = (memoryBodyId: string | undefined) => { setSelectedBodyId(memoryBodyId); setVisibleLimit(pageSize) }

  return (
    <div className={css.page}>
      <PageHeader title={t('content.title')} description={t('content.description')} meta={t('content.count', { count: view === null ? '—' : selectedBodyId === undefined ? view.total : filteredItems.length })} />
      <form className={css.listToolbar} onSubmit={submit}><SearchField label={t('content.filterAria')} value={query} onChange={event => setQuery(event.target.value)} placeholder={t('content.filterPlaceholder')} /><SelectField hideLabel label={t('content.categoryAria')} value={category} options={[{ value: '' as Category | '', label: t('common.allCategories') }, ...CATEGORIES.map(value => ({ value, label: categoryLabel(t, value) }))]} onChange={setCategory} /><button type="submit" className={css.secondaryButton} disabled={loading}>{loading ? t('common.loading') : t('content.apply')}</button></form>
      <div className={css.listNotice}>{t('content.notice')}</div>
      <ReadSourcePanel title={t('content.sourcesTitle')} sources={sources} selectedBodyId={selectedBodyId} onSelect={selectBody} />
      {error !== null && <div className={css.inlineError} role="alert">{error}</div>}
      <div className={css.asyncResults}>
        {loading && <SectionSpinner label={t('common.loading')} />}
        {!loading && filteredItems.length === 0 && <EmptyState glyph="≡" title={t(waitingForQuery ? 'content.queryRequiredTitle' : 'content.emptyTitle')}>{t(waitingForQuery ? 'content.queryRequiredText' : 'content.emptyText')}</EmptyState>}
        <div className={css.memoryList}>{visibleItems.map(insight => <InsightCard key={insightKey(insight)} insight={insight} writeEnabled={props.writeEnabled} onForget={forget} onClone={props.onClone} onRelated={() => props.onExplore(insight.content)} />)}</div>
        {view !== null && !loading && <ProgressiveFooter visible={visibleItems.length} total={filteredItems.length} pageSize={pageSize} onMore={() => setVisibleLimit(value => value + pageSize)} />}
      </div>
    </div>
  )
}

/** @deprecated Use nativeSpaceProvider. */
export const nativeBodyProvider = nativeSpaceProvider
