#!/usr/bin/env node
import { createHash } from 'node:crypto'
import { createReadStream, realpathSync } from 'node:fs'
import { link, mkdtemp, open, rm } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { isDeepStrictEqual } from 'node:util'
import * as zlib from 'node:zlib'

const maximumBytes = 128 * 1024 * 1024
const summaries = {
  recall: ['Memory View snapshot', 'Runtime memory snapshot'],
  instructions: ['Optional memory recall and remember reminder'],
}
const sha256 = value => createHash('sha256').update(value).digest('hex')
const record = value => value !== null && typeof value === 'object' && !Array.isArray(value)
const nonempty = value => typeof value === 'string' && value.length > 0
const integer = value => Number.isSafeInteger(value) && !Object.is(value, -0)
const count = value => integer(value) && value >= 0
const exactKeys = (value, required, optional = []) => record(value) && required.every(key => Object.hasOwn(value, key)) &&
  Object.keys(value).every(key => required.includes(key) || optional.includes(key))

// Official dsh-subagent 0.1.1-rc.2 v2 -> 0.1.2-alpha.2 v3 adds only the
// optional agentReasoningEffort composition input. With that field absent,
// these exact old records reconstruct the same declared child options. Apply
// the stricter frozen-v3 constraints too; accepting v2 alone is insufficient.
function supportedDescriptorV2(value) {
  if (!record(value) || value.version !== 2 || !nonempty(value.provider)) return false
  const base = ['version', 'mode', 'provider', 'label']
  if (value.mode === 'one-shot') return Object.keys(value).every(key => base.includes(key)) &&
    (!Object.hasOwn(value, 'label') || typeof value.label === 'string')
  if (value.mode !== 'continuable' || !nonempty(value.label)) return false
  if (!Object.keys(value).every(key => [...base, 'agentProvider', 'agentModel', 'persona', 'toolFilter'].includes(key))) return false
  for (const key of ['agentProvider', 'agentModel', 'persona']) if (Object.hasOwn(value, key) && !nonempty(value[key])) return false
  if (Object.hasOwn(value, 'agentProvider') !== Object.hasOwn(value, 'agentModel')) return false
  if (!Object.hasOwn(value, 'toolFilter')) return true
  const filter = value.toolFilter
  return record(filter) && Object.keys(filter).length > 0 && Object.keys(filter).every(key =>
    ['allow', 'deny'].includes(key) && Array.isArray(filter[key]) && filter[key].every(nonempty))
}

function supportedPackedToolRun(row, placeholdersOnly = true) {
  if (!exactKeys(row, ['type', 'seq0', 'time0', 'data']) || !count(row.seq0) || !integer(row.time0)) return false
  const data = row.data
  if (!exactKeys(data, ['turn', 'step', 'index', 'id', 'dt', 'args'], ['name']) || typeof data.id !== 'string' || (placeholdersOnly && data.id !== '' && data.name !== '' && data.name !== null) ||
      (Object.hasOwn(data, 'name') && data.name !== null && typeof data.name !== 'string')) return false
  if (![data.turn, data.step, data.index].every(count) || !Array.isArray(data.args) || data.args.length === 0 ||
      !data.args.every(value => typeof value === 'string') || !Array.isArray(data.dt) || data.dt.length !== data.args.length - 1 ||
      data.args.length - 1 > Number.MAX_SAFE_INTEGER - row.seq0) return false
  let time = row.time0
  return data.dt.every(gap => integer(gap) && integer(time += gap))
}

const rawChunkEnvelope = row => exactKeys(row, ['type', 'seq', 'time', 'data'], ['ignorable']) && count(row.seq) && integer(row.time) &&
  (!Object.hasOwn(row, 'ignorable') || row.ignorable === true) && exactKeys(row.data, ['turn', 'step', 'chunk']) && [row.data.turn, row.data.step].every(count)
const surfaceEnvelope = row => exactKeys(row, ['type', 'seq', 'time', 'data'], ['surfaceOp', 'sourceEventSeqs', 'ignorable']) && count(row.seq) && integer(row.time) &&
  (!Object.hasOwn(row, 'ignorable') || row.ignorable === true)
const messageShape = value => exactKeys(value, ['role', 'id', 'source', 'content']) && nonempty(value.id) && Array.isArray(value.content)
const toolBlockShape = value => exactKeys(value, ['type', 'id', 'name', 'arguments']) && value.type === 'tool-call' && typeof value.id === 'string' && nonempty(value.name) && typeof value.arguments === 'string'

function supportedPackedTextRun(row) {
  const data = row.data
  return exactKeys(data, ['turn', 'step', 'index', 'dt', 'texts']) && supportedPackedToolRun({ ...row,
    data: { turn: data.turn, step: data.step, index: data.index, id: 'unchanged-text-run', dt: data.dt, args: data.texts } }, false)
}

function supportedNullToolDelta(row) {
  if (!rawChunkEnvelope(row)) return false
  const chunk = row.data.chunk
  return exactKeys(chunk, ['type', 'index', 'id', 'name', 'argumentsDelta']) && chunk.type === 'tool-call-delta' &&
    chunk.name === null && count(chunk.index) && typeof chunk.id === 'string' && typeof chunk.argumentsDelta === 'string'
}

// This is a proof for the published DeepSeek empty-continuation-ID bug, not a
// nearest-call heuristic. Everything is planned before any output is published.
function recoverRecordedToolIdentities(entries) {
  const rawPatches = new Map()
  const packedIds = new Map()
  const refusals = { occurrences: 0, samples: [] }
  let chains = 0
  let fields = 0
  const emptyBlock = block => block?.type === 'tool-call' && block.id === '' || block?.type === 'tool-result' && block.toolCallId === ''
  const emptyDurable = row => row.type === 'tool/call' && row.data?.callId === '' ||
    row.type === 'assistant/chunk' && row.data?.chunk?.type === 'block-end' && emptyBlock(row.data.chunk.block) ||
    ['assistant/message', 'tool/result'].includes(row.type) && (row.data?.message?.content?.some?.(emptyBlock) || row.data?.message?.source?.callId === '')
  const result = () => ({ rawPatches, packedIds, chains, fields, refusals })
  if (!entries.some(entry => emptyDurable(entry.row))) return result()
  const require = (condition, reason) => { if (!condition) throw reason }
  const refuse = (entry, reason) => {
    refusals.occurrences++
    if (refusals.samples.length < 10) refusals.samples.push({ line: entry.index + 1, ...(count(entry.row.seq) ? { seq: entry.row.seq } : {}), reason })
  }
  const logical = []
  let turn, step
  try {
    for (const entry of entries.slice(1)) {
      const row = entry.row
      objectMembers(entry.line, 0) // Sequence identity cannot come from duplicate envelope keys.
      if (row.type === 'turn/start') turn = row.data?.turn
      if (row.type === 'step/start') { turn = row.data?.turn; step = row.data?.step }
      entry.scope = { turn: row.data?.turn ?? turn, step: row.data?.step ?? step }
      if (['tool-call-chunks', 'text-chunks', 'reasoning-chunks'].includes(row.type)) {
        const tool = row.type === 'tool-call-chunks'
        require(tool ? supportedPackedToolRun(row, false) : supportedPackedTextRun(row), 'A packed source has unsupported fields or coordinates.')
        const fragments = tool ? row.data.args : row.data.texts
        let time = row.time0
        for (let offset = 0; offset < fragments.length; offset++) {
          if (offset) time += row.data.dt[offset - 1]
          require(logical.length < maximumBytes / 128, 'The identity proof exceeds its bounded event count.')
          logical.push({ entry, offset, row: { type: 'assistant/chunk', seq: row.seq0 + offset, time,
            data: { turn: row.data.turn, step: row.data.step, chunk: tool ? { type: 'tool-call-delta', index: row.data.index, id: row.data.id,
              ...(Object.hasOwn(row.data, 'name') ? { name: row.data.name } : {}), argumentsDelta: fragments[offset] }
              : { type: row.type === 'text-chunks' ? 'text-delta' : 'reasoning-delta', index: row.data.index, text: fragments[offset] } } } })
        }
      } else logical.push({ entry, row })
      if (row.type === 'step/end') step = undefined
      if (row.type === 'turn/end') { turn = undefined; step = undefined }
    }
    require(logical.every((value, index) => value.row.seq === index && integer(value.row.time)), 'Logical events must have unique complete sequence coordinates and safe times.')
  } catch (error) {
    if (typeof error !== 'string') throw error
    refuse(entries.find(entry => emptyDurable(entry.row)), error)
    return result()
  }
  const sourceSeqs = (row, nonempty = true) => {
    const values = row.sourceEventSeqs
    require(Array.isArray(values), 'Tool history needs explicit, valid assistant chunk provenance.')
    const selected = new Set()
    const hasRanges = values.some(Array.isArray)
    let previous = -1
    for (const member of values) {
      const range = Array.isArray(member)
      require(!range || member.length === 2, 'A provenance range must contain exactly two coordinates.')
      const start = range ? member[0] : member
      const end = range ? member[1] : member
      require(count(start) && count(end) && start <= end && end < row.seq && end - start < logical.length - selected.size,
        'Provenance contains an unsafe, missing or excessive source range.')
      require((!hasRanges && row.type !== 'assistant/message') || start > previous, 'Chunk provenance is not one complete ordered attempt.')
      for (let seq = start; seq <= end; seq++) {
        require(!selected.has(seq), 'Provenance contains duplicate or overlapping source coordinates.')
        selected.add(seq)
      }
      previous = end
    }
    require(!nonempty || selected.size > 0, 'A completed tool stream needs nonempty chunk provenance.')
    // v0 accepts unordered flat sets, but the active v1 -> v2 stage requires an
    // ordered complete attempt. Do not silently rewrite that provenance here.
    return [...selected].sort((left, right) => left - right)
  }
  const sameStep = (left, right) => left?.turn === right?.turn && left?.step === right?.step
  const messages = entries.filter(entry => entry.row.type === 'assistant/message')
  for (const assistant of messages) {
    const row = assistant.row
    const data = row.data
    const content = data?.message?.content
    if (!Array.isArray(content) || !content.some(block => block?.type === 'tool-call') ||
        !entries.some(entry => sameStep(entry.scope, data) && emptyDurable(entry.row))) continue
    try {
      require(surfaceEnvelope(row) && row.surfaceOp === 'append' && exactKeys(data, ['turn', 'step', 'message'], ['usage']) && count(data.turn) && count(data.step) &&
        messageShape(data.message) && data.message.role === 'assistant' && exactKeys(data.message.source, ['kind', 'provider', 'model']) &&
        data.message.source.kind === 'model' && data.message.source.provider === 'deepseek-official' && nonempty(data.message.source.model),
        'Only the uninterrupted recorded DeepSeek append-message shape without owner replay state is supported.')
      const sources = sourceSeqs(row)
      const selected = new Set(sources)
      const stream = sources.map(seq => logical[seq])
      require(stream.every(value => value?.row.type === 'assistant/chunk' && sameStep(value.row.data, data)), 'Chunk provenance cites another event, turn or step.')
      require(logical.slice(sources[0], sources.at(-1) + 1).every(value => value.row.type !== 'assistant/chunk' || !sameStep(value.row.data, data) || selected.has(value.row.seq)),
        'Chunk provenance omits a delta inside this stream attempt.')
      for (const other of messages) if (other !== assistant && Object.hasOwn(other.row, 'sourceEventSeqs')) {
        require(!sourceSeqs(other.row, false).some(seq => selected.has(seq)), 'Two assistant messages share source chunks.')
      }
      const blocks = new Map()
      let finished = false
      let usage
      for (const value of stream) {
        const current = value.row
        const chunk = current.data.chunk
        require(rawChunkEnvelope(current) && record(chunk) && !finished, 'The stream has unsupported fields or chunks after finish.')
        assertUniqueJson(value.entry.line, value.entry.row)
        if (chunk.type === 'usage') {
          require(exactKeys(chunk, ['type', 'usage']) && record(chunk.usage), 'An unrecognized usage chunk prevents identity recovery.')
          usage = chunk.usage
          continue
        }
        if (chunk.type === 'finish') {
          require(exactKeys(chunk, ['type', 'reason']) && exactKeys(chunk.reason, ['kind']) && chunk.reason.kind === 'tool-calls', 'The tool stream is interrupted, truncated or has owner replay state.')
          finished = true
          continue
        }
        require(count(chunk.index), 'A stream block index is unsafe.')
        let block = blocks.get(chunk.index)
        if (chunk.type === 'block-start') {
          require(exactKeys(chunk, ['type', 'index', 'blockType']) && ['text', 'reasoning', 'tool-call'].includes(chunk.blockType) && !block,
            'A block start is repeated or unsupported.')
          blocks.set(chunk.index, { type: chunk.blockType, started: true, text: '', arguments: '', deltas: [] })
          continue
        }
        const type = chunk.type === 'text-delta' ? 'text' : chunk.type === 'reasoning-delta' ? 'reasoning' : chunk.type === 'tool-call-delta' ? 'tool-call' : chunk.block?.type
        require(['text', 'reasoning', 'tool-call'].includes(type), 'A stream chunk has an unsupported block type.')
        if (!block) {
          require(type !== 'tool-call', 'A tool delta or end is missing its block start.')
          block = { type, text: '', arguments: '', deltas: [] }
          blocks.set(chunk.index, block)
        }
        require(block.type === type && !block.end, 'A block changes type, repeats its end or receives a late delta.')
        if (chunk.type === 'block-end') {
          require(exactKeys(chunk, ['type', 'index', 'block']) && record(chunk.block), 'A block end has unsupported fields.')
          block.end = value
          block.content = chunk.block
          if (type === 'tool-call') require(toolBlockShape(chunk.block) &&
            chunk.block.name === block.name && chunk.block.arguments === block.arguments, 'The final tool name or arguments disagree with its deltas.')
          continue
        }
        if (type === 'tool-call') {
          require(exactKeys(chunk, ['type', 'index', 'id', 'argumentsDelta'], ['name']) && typeof chunk.id === 'string' && typeof chunk.argumentsDelta === 'string' &&
            (!Object.hasOwn(chunk, 'name') || chunk.name === null || typeof chunk.name === 'string'), 'A tool delta has unsupported identity or argument fields.')
          if (chunk.name) block.name = chunk.name
          block.arguments += chunk.argumentsDelta
          block.deltas.push(value)
        } else {
          require(exactKeys(chunk, ['type', 'index', 'text']) && typeof chunk.text === 'string', 'A text or reasoning delta has unsupported fields.')
          block.text += chunk.text
        }
      }
      require(finished && blocks.size === content.length, 'The completed stream and assistant block list do not match.')
      require(isDeepStrictEqual(usage, data.usage), 'The completed stream and assistant usage do not match.')
      const ordered = [...blocks.values()]
      const advertised = messages.filter(entry => sameStep(entry.row.data, data)).flatMap(entry => entry.row.data?.message?.content ?? []).filter(block => block?.type === 'tool-call')
      const calls = entries.filter(entry => entry.row.type === 'tool/call' && sameStep(entry.row.data, data))
      const results = entries.filter(entry => entry.row.type === 'tool/result' && sameStep(entry.row.data, data))
      const proposals = []
      const provenChains = []
      for (const [position, block] of ordered.entries()) {
        const actual = block.content ?? { type: block.type, text: block.text }
        const declared = content[position]
        if (block.type !== 'tool-call') {
          require(isDeepStrictEqual(actual, declared), 'Text or reasoning differs between the stream and assistant message.')
          continue
        }
        require(block.end && toolBlockShape(declared) &&
          isDeepStrictEqual({ ...actual, id: '' }, { ...declared, id: '' }), 'The tool block and assistant advertisement disagree.')
        const matchingCalls = calls.filter(entry => entry.row.data.name === actual.name && entry.row.data.arguments === actual.arguments)
        require(matchingCalls.length === 1 && advertised.filter(value => value.name === actual.name && value.arguments === actual.arguments).length === 1,
          'The tool advertisement and execution cannot be paired uniquely by name and arguments.')
        const call = matchingCalls[0]
        require(surfaceEnvelope(call.row) && !Object.hasOwn(call.row, 'surfaceOp') && !Object.hasOwn(call.row, 'sourceEventSeqs') &&
          exactKeys(call.row.data, ['turn', 'step', 'callId', 'name', 'arguments']) && row.seq < call.row.seq && typeof call.row.data.callId === 'string',
          'The tool execution has unsupported fields, precedes its advertisement or has an invalid ID.')
        const matchingResults = results.filter(entry => Object.hasOwn(entry.row, 'sourceEventSeqs') && sourceSeqs(entry.row).includes(call.row.seq))
        let toolResult
        if (matchingResults.length === 1) {
          toolResult = matchingResults[0]
          require(isDeepStrictEqual(sourceSeqs(toolResult.row), [call.row.seq]), 'The result provenance does not select exactly this tool execution.')
        } else {
          require(matchingResults.length === 0 && advertised.length === 1 && calls.length === 1 && results.length === 1 && !Object.hasOwn(results[0].row, 'sourceEventSeqs'),
            'The tool result is missing, ambiguous or has conflicting provenance.')
          require(!logical.some(value => value.row.type === 'assistant/chunk' && sameStep(value.row.data, data) && !selected.has(value.row.seq)),
            'A result without provenance cannot be matched across a retry or another stream attempt.')
          toolResult = results[0]
        }
        const resultMessage = toolResult.row.data?.message
        const resultBlock = resultMessage?.content?.[0]
        require(surfaceEnvelope(toolResult.row) && exactKeys(toolResult.row.data, ['turn', 'step', 'message'], ['error', 'meta']) &&
          call.row.seq < toolResult.row.seq && toolResult.row.surfaceOp === 'append' && messageShape(resultMessage) && resultMessage.role === 'user' &&
          exactKeys(resultMessage.source, ['kind', 'callId']) && resultMessage.source.kind === 'tool' && typeof resultMessage.source.callId === 'string' && resultMessage.content.length === 1 &&
          exactKeys(resultBlock, ['type', 'toolCallId', 'content'], ['isError']) && resultBlock.type === 'tool-result' && typeof resultBlock.toolCallId === 'string' &&
          Array.isArray(resultBlock.content) && (!Object.hasOwn(resultBlock, 'isError') || typeof resultBlock.isError === 'boolean'), 'The tool result is not a complete append result with a single identity.')
        const slots = [
          [block.end, ['data', 'chunk', 'block', 'id'], actual.id],
          [{ entry: assistant, row }, ['data', 'message', 'content', position, 'id'], declared.id],
          [{ entry: call, row: call.row }, ['data', 'callId'], call.row.data.callId],
          [{ entry: toolResult, row: toolResult.row }, ['data', 'message', 'source', 'callId'], resultMessage.source.callId],
          [{ entry: toolResult, row: toolResult.row }, ['data', 'message', 'content', 0, 'toolCallId'], resultMessage.content[0].toolCallId],
        ]
        if (!slots.some(slot => slot[2] === '')) {
          require(slots.every(slot => slot[2] === actual.id) && block.deltas.every(value => value.row.data.chunk.id === '' || value.row.data.chunk.id === actual.id),
            'An already nonempty tool chain contains conflicting recorded identities.')
          provenChains.push(slots)
          continue
        }
        const candidates = new Set(block.deltas.map(value => value.row.data.chunk.id).filter(nonempty))
        require(candidates.size === 1, 'The tool stream has no unique previously recorded provider ID.')
        const candidate = [...candidates][0]
        require(slots.every(slot => slot[2] === '' || slot[2] === candidate), 'A durable ID conflicts with the recorded provider ID.')
        require(advertised.every(value => value === declared || value.id !== candidate), 'The recorded ID is already advertised by another tool in this step.')
        let seen = false
        const deltas = []
        for (const value of block.deltas) {
          if (value.row.data.chunk.id === candidate) seen = true
          else if (seen) deltas.push([value, ['data', 'chunk', 'id'], ''])
        }
        require(deltas.length > 0, 'No empty continuation delta follows the recorded provider ID.')
        provenChains.push(slots)
        proposals.push({ candidate, name: actual.name, assistant, call, toolResult, block, slots: [...deltas, ...slots.filter(slot => slot[2] === '')], sources })
      }
      require(new Set(proposals.map(value => value.candidate)).size === proposals.length, 'Multiple tools claim the same recorded provider ID.')
      for (const proposal of proposals) {
        const owned = new Map()
        const allow = (entry, path) => {
          if (!owned.has(entry)) owned.set(entry, new Set())
          owned.get(entry).add(JSON.stringify(path))
        }
        // Only the exact slots of proven advertisement/call/result chains are
        // owned; another core-looking call is not automatically trustworthy.
        for (const slots of provenChains) for (const [value, path] of slots) allow(value.entry, path)
        const linkedSeqs = new Set([...sources, row.seq, proposal.call.row.seq, proposal.toolResult.row.seq])
        const linkedMessages = new Set([data.message.id, proposal.toolResult.row.data.message.id])
        const cites = value => {
          if (!record(value) && !Array.isArray(value)) return false
          return Object.entries(value).some(([key, child]) => {
            if (key === 'sourceEventSeq' && linkedSeqs.has(child)) return true
            if ((/^(?:messageId|.*MessageId)s?$/.test(key) || key === 'id' && ['assistant', 'user'].includes(value.role)) &&
                (linkedMessages.has(child) || Array.isArray(child) && child.some(id => linkedMessages.has(id)))) return true
            if (key === 'shadowedRange' && record(child) && [...linkedSeqs].some(seq => count(child.start) && count(child.end) && child.start <= seq && seq <= child.end)) return true
            if (['sourceEventSeqs', 'shadowedSeqs', 'shadowedRange'].includes(key) && Array.isArray(child)) {
              if (key === 'shadowedRange' && child.length === 2 && [...linkedSeqs].some(seq => count(child[0]) && count(child[1]) && child[0] <= seq && seq <= child[1])) return true
              if (child.some(member => linkedSeqs.has(member) || Array.isArray(member) && member.length === 2 && [...linkedSeqs].some(seq => count(member[0]) && count(member[1]) && member[0] <= seq && seq <= member[1]))) return true
            }
            return cites(child)
          })
        }
        for (const entry of entries.slice(1)) {
          const value = entry.row
          const replacement = value.surfaceOp
          const surfaces = [row.seq, proposal.toolResult.row.seq]
          const replaces = record(replacement) && replacement.op === 'replace' && surfaces.some(seq => count(replacement.start) && count(replacement.end) && replacement.start <= seq && seq <= replacement.end)
          require(!replaces, 'A surface replacement also owns the affected historical message.')
          const copiesMessage = [data.message.id, proposal.toolResult.row.data.message.id].includes(value.data?.message?.id) && entry !== assistant && entry !== proposal.toolResult
          require(!copiesMessage, 'A copied message identity needs owner-aware recovery.')
          if (!sameStep(entry.scope, data) && !cites(value)) continue
          assertUniqueJson(entry.line, value)
          require(!(value.type === 'approval/asked' && value.data?.toolName === proposal.name &&
            (value.data.callId === undefined || value.data.callId === '' || value.data.callId === proposal.candidate)), 'An associated approval retains the historical tool identity.')
          const visit = (current, path = []) => {
            if (!record(current) && !Array.isArray(current)) return
            for (const [key, child] of Object.entries(current)) {
              const next = [...path, Array.isArray(current) ? Number(key) : key]
              const identity = ['callId', 'toolCallId', 'rootCallId', 'parentCallId', 'subCallId'].includes(key) || key === 'id' && current.type === 'tool-call'
              if (key === 'replayState') require(false, 'Owner replay state needs owner-aware identity recovery.')
              if (identity && (child === '' || child === proposal.candidate || typeof child === 'string' && (child.startsWith(`${proposal.candidate}:code:`) || child.startsWith(':code:')))) {
                require(owned.get(entry)?.has(JSON.stringify(next)), 'An additional approval, dispatch, plugin or copied-content identity reference remains.')
              }
              visit(child, next)
            }
          }
          visit(value)
        }
      }
      for (const proposal of proposals) {
        chains++
        for (const [value, path] of proposal.slots) {
          fields++
          if (value.offset !== undefined) {
            if (!packedIds.has(value.entry.index)) packedIds.set(value.entry.index, new Map())
            packedIds.get(value.entry.index).set(value.offset, proposal.candidate)
          } else {
            if (!rawPatches.has(value.entry.index)) rawPatches.set(value.entry.index, [])
            rawPatches.get(value.entry.index).push({ path, value: proposal.candidate })
          }
        }
      }
    } catch (error) {
      if (typeof error !== 'string') throw error
      refuse(assistant, error)
    }
  }
  if (refusals.occurrences > 0) { rawPatches.clear(); packedIds.clear(); chains = 0; fields = 0 }
  return result()
}

// Diagnose the other concrete legacy shapes reported in #251 without inventing
// descriptor authority or tool associations. This is not a Session validator:
// the owning DSH installation still validates the complete repaired artifact.
function legacyBlockers() {
  const issues = new Map()
  const add = (code, message, row, line, path) => {
    if (!issues.has(code)) issues.set(code, { code, message, occurrences: 0, samples: [] })
    const issue = issues.get(code)
    issue.occurrences++
    const seq = row.seq ?? row.seq0
    if (issue.samples.length < 10) issue.samples.push({ line, type: row.type, ...(count(seq) ? { seq } : {}), path })
  }
  const identifier = (value, row, line, path) => {
    if (value === '') add('empty-durable-tool-id', 'An empty durable tool ID lacks a proven closed chain to its recorded provider identity; new identities are not synthesized.', row, line, path)
  }
  const block = (value, row, line, path) => {
    if (value?.type === 'tool-call') identifier(value.id, row, line, `${path}.id`)
    if (value?.type === 'tool-result') identifier(value.toolCallId, row, line, `${path}.toolCallId`)
  }
  const message = (value, row, line, path) => {
    if (value?.source?.kind === 'tool') identifier(value.source.callId, row, line, `${path}.source.callId`)
    if (Array.isArray(value?.content)) value.content.forEach((value, index) => block(value, row, line, `${path}.content[${index}]`))
  }
  return {
    inspect(row, line) {
      const data = row?.data
      if (row?.type === 'subagent/descriptor' && data?.version !== 3 && !supportedDescriptorV2(data)) {
        add('unsupported-subagent-descriptor', 'Only exact historical v2 descriptors with equivalent v3 composition are supported; this descriptor needs recovery by its original writer.', row, line, 'data.version')
      }
      if (row?.type === 'tool-call-chunks') {
        if ((data?.id === '' || data?.name === '' || data?.name === null) && !supportedPackedToolRun(row)) add('unsupported-packed-tool-chunks', 'The packed row cannot be expanded losslessly: it needs exact fields, string deltas and safe sequence/time coordinates.', row, line, 'data')
      }
      if (row?.type === 'assistant/chunk') {
        if (data?.chunk?.type === 'tool-call-delta' && data.chunk.name === null && !supportedNullToolDelta(row)) {
          add('null-stream-tool-name', 'A null streaming tool name needs exact raw fields, string ID/arguments and safe coordinates before normalization.', row, line, 'data.chunk.name')
        }
        if (data?.chunk?.type === 'block-end') block(data.chunk.block, row, line, 'data.chunk.block')
      }
      if (row?.type === 'assistant/message') {
        message(data?.message, row, line, 'data.message')
        // Frozen v0 also normalizes the older unwrapped assistant shape.
        if (record(data) && !Object.hasOwn(data, 'message') && Object.hasOwn(data, 'content') && Object.hasOwn(data, 'provenance')) {
          message(data, row, line, 'data')
        }
      }
      if (row?.type === 'tool/call') identifier(data?.callId, row, line, 'data.callId')
      if (row?.type === 'tool/result') {
        identifier(data?.callId, row, line, 'data.callId') // Historical unwrapped result.
        message(data?.message, row, line, 'data.message')
      }
    },
    report: () => [...issues.values()],
  }
}

// Locate JSON object members without reserializing message content or numbers.
// JSON.parse validates each complete row before this scanner is used.
function objectMembers(text, start, array = false) {
  let cursor = start
  const space = () => { while (/\s/.test(text[cursor] ?? '') && cursor < text.length) cursor++ }
  const stringEnd = () => {
    cursor++
    while (cursor < text.length) {
      if (text[cursor++] === '"') return
      if (text[cursor - 1] === '\\') cursor++
    }
  }
  const valueEnd = () => {
    if (text[cursor] === '"') return stringEnd()
    if (text[cursor] === '{' || text[cursor] === '[') {
      let depth = 0
      do {
        if (text[cursor] === '"') stringEnd()
        else {
          if ('{['.includes(text[cursor])) depth++
          if ('}]'.includes(text[cursor])) depth--
          cursor++
        }
      } while (depth > 0)
    } else {
      while (cursor < text.length && !/[\s,}\]]/.test(text[cursor])) cursor++
    }
  }
  space()
  if (text[cursor++] !== (array ? '[' : '{')) throw new Error('Expected a JSON container in the legacy message path.')
  const members = []
  const names = new Set()
  space()
  const close = array ? ']' : '}'
  while (text[cursor] !== close) {
    const start = cursor
    let name = members.length
    if (!array) {
      stringEnd()
      name = JSON.parse(text.slice(start, cursor))
      if (names.has(name)) throw new Error('Duplicate JSON property in the legacy message path; refusing ambiguous repair.')
      names.add(name)
      space()
      cursor++ // colon, already validated by JSON.parse
      space()
    }
    const valueStart = cursor
    valueEnd()
    members.push({ name, start, end: cursor, valueStart })
    space()
    if (text[cursor] === close) break
    cursor++ // comma
    space()
  }
  return members
}

function assertUniqueJson(line, row, start = 0) {
  if (!record(row) && !Array.isArray(row)) return
  for (const member of objectMembers(line, start, Array.isArray(row))) assertUniqueJson(line, row[member.name], member.valueStart)
}

function replaceIdentityValues(line, patches) {
  for (const patch of patches) {
    let start = 0
    let member
    for (const key of patch.path) {
      member = objectMembers(line, start, typeof key === 'number').find(value => value.name === key)
      if (!member) throw new Error('An identity patch no longer matches its proven JSON path.')
      start = member.valueStart
    }
    line = line.slice(0, member.valueStart) + JSON.stringify(patch.value) + line.slice(member.end)
  }
  return line
}

function removeSummary(line) {
  const data = objectMembers(line, 0).find(member => member.name === 'data')
  const source = objectMembers(line, data.valueStart).find(member => member.name === 'source')
  const members = objectMembers(line, source.valueStart)
  const index = members.findIndex(member => member.name === 'summary')
  const member = members[index]
  const start = index === members.length - 1 && index > 0 ? members[index - 1].end : member.start
  const end = index < members.length - 1 ? members[index + 1].start : member.end
  return line.slice(0, start) + line.slice(end)
}

function promoteDescriptor(line) {
  const data = objectMembers(line, 0).find(member => member.name === 'data')
  const members = objectMembers(line, data.valueStart)
  const filter = members.find(member => member.name === 'toolFilter')
  if (filter) objectMembers(line, filter.valueStart) // Reject ambiguous nested fields as well.
  const version = members.find(member => member.name === 'version')
  return line.slice(0, version.valueStart) + '3' + line.slice(version.end)
}

function normalizeNullToolName(line) {
  const data = objectMembers(line, 0).find(member => member.name === 'data')
  const chunk = objectMembers(line, data.valueStart).find(member => member.name === 'chunk')
  const name = objectMembers(line, chunk.valueStart).find(member => member.name === 'name')
  // Old/current assemblers only assign truthy names; token timing instead uses
  // name !== undefined. Keep this property: deleting it would change TTFT for
  // an empty argument fragment. Every other raw byte remains unchanged.
  return line.slice(0, name.valueStart) + '""' + line.slice(name.end)
}

function expandPackedToolRun(line, row, retain, recoveredIds) {
  const data = objectMembers(line, 0).find(member => member.name === 'data')
  objectMembers(line, data.valueStart) // Reject duplicate fields before reserializing this row.
  let time = row.time0
  const expanded = []
  for (let index = 0; index < row.data.args.length; index++) {
    if (index > 0) time += row.data.dt[index - 1]
    // Historical packed-row expansion after preserving null-name presence as
    // an empty string. No ID/name inference and
    // no dropped chunks: the active v1 -> v2 migration retains these as raw
    // stream records when their IDs or names are empty strings.
    expanded.push(retain(JSON.stringify({
      type: 'assistant/chunk', seq: row.seq0 + index, time,
      data: { turn: row.data.turn, step: row.data.step, chunk: {
        type: 'tool-call-delta', index: row.data.index, id: recoveredIds?.get(index) ?? row.data.id,
        ...(Object.hasOwn(row.data, 'name') ? { name: row.data.name ?? '' } : {}), argumentsDelta: row.data.args[index],
      } },
    }) + (line.endsWith('\r\n') ? '\r\n' : '\n')))
  }
  return expanded.join('')
}

async function readBounded(path) {
  const chunks = []
  let length = 0
  for await (const chunk of createReadStream(path)) {
    length += chunk.length
    if (length > maximumBytes) throw new Error('Session exceeds the 128 MiB repair limit.')
    chunks.push(chunk)
  }
  return Buffer.concat(chunks)
}

// RFC 8878 framing: reject torn headers, blocks and checksums before Node's
// decoder (which can otherwise accept an unfinished frame) sees the bytes.
function frameLength(input, start) {
  const requireBytes = end => { if (end > input.length) throw new Error('Truncated Zstandard frame; no output written.') }
  requireBytes(start + 5)
  if (input.readUInt32LE(start) !== 0xfd2fb528) throw new Error('Unsupported Zstandard frame; only ordinary DSH frames are accepted.')
  const descriptor = input[start + 4]
  if (descriptor & 0x18) throw new Error('Reserved Zstandard frame header bits are set.')
  const single = (descriptor & 0x20) !== 0
  const sizeFlag = descriptor >>> 6
  const contentSizeBytes = sizeFlag === 0 ? (single ? 1 : 0) : [0, 2, 4, 8][sizeFlag]
  let offset = start + 5 + (single ? 0 : 1) + [0, 1, 2, 4][descriptor & 3] + contentSizeBytes
  requireBytes(offset)
  let last = false
  while (!last) {
    requireBytes(offset + 3)
    const block = input.readUIntLE(offset, 3)
    last = (block & 1) !== 0
    const type = (block >>> 1) & 3
    if (type === 3) throw new Error('Reserved Zstandard block type.')
    offset += 3 + (type === 1 ? 1 : block >>> 3)
    requireBytes(offset)
  }
  offset += descriptor & 4 ? 4 : 0
  requireBytes(offset)
  return offset - start
}

function decodeFrames(input) {
  const chunks = []
  let offset = 0
  let length = 0
  while (offset < input.length) {
    const lengthOnDisk = frameLength(input, offset)
    const { buffer, engine } = zlib.zstdDecompressSync(input.subarray(offset, offset + lengthOnDisk), {
      info: true, maxOutputLength: maximumBytes - length,
    })
    if (engine.bytesWritten !== lengthOnDisk) throw new Error('Invalid Zstandard frame length.')
    offset += engine.bytesWritten
    length += buffer.length
    chunks.push(buffer)
  }
  return Buffer.concat(chunks)
}

function encodeFrames(text) {
  // DSH requires the first frame to contain exactly its header line.
  const end = text.indexOf('\n') + 1
  const options = { params: { [zlib.constants.ZSTD_c_checksumFlag]: 1 } }
  return Buffer.concat([text.slice(0, end), text.slice(end)].filter(Boolean).map(part => zlib.zstdCompressSync(part, options)))
}

export async function repairLegacySession(input, output) {
  const original = await readBounded(input)
  const compressed = original.subarray(0, 4).equals(Buffer.from([0x28, 0xb5, 0x2f, 0xfd]))
  if (compressed && (typeof zlib.zstdDecompressSync !== 'function' || typeof zlib.zstdCompressSync !== 'function')) {
    throw new Error('Zstandard sessions require Node 22.19 or newer (or Node 24+).')
  }
  const decoded = compressed ? decodeFrames(original) : original
  const text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(decoded)
  if (!text.endsWith('\n')) throw new Error('Session must end with a complete newline-terminated JSON record.')
  const lines = text.match(/[^\n]*\n/g) ?? []
  const entries = lines.map((line, index) => {
    try { return { line, index, row: JSON.parse(line) } } catch { throw new Error(`Invalid JSON at line ${index + 1}; no output written.`) }
  })
  const recovery = recoverRecordedToolIdentities(entries)
  let changes = 0
  let descriptors = 0
  let expandedRows = 0
  let expandedChunks = 0
  let normalizedNames = 0
  let outputBytes = 0
  const retain = line => {
    outputBytes += Buffer.byteLength(line)
    if (outputBytes > maximumBytes) throw new Error('Expanded Session exceeds the 128 MiB repair limit; no output written.')
    return line
  }
  const diagnostics = legacyBlockers()
  const repaired = entries.map(entry => {
    const { index } = entry
    let { line, row } = entry
    if (index === 0) {
      objectMembers(line, 0)
      if (row.type !== 'session' || row.version !== 0 || typeof row.id !== 'string' || !row.id ||
          !Number.isSafeInteger(row.createdAt) || row.createdAt < 0 ||
          !Number.isSafeInteger(row.delegationDepth) || row.delegationDepth < 0) {
        throw new Error('Only a legacy DSH format v0 Session header is supported.')
      }
      return retain(line)
    }
    const patches = recovery.rawPatches.get(index)
    if (patches) { line = replaceIdentityValues(line, patches); row = JSON.parse(line) }
    diagnostics.inspect(row, index + 1)
    if (row?.type === 'subagent/descriptor' && supportedDescriptorV2(row.data)) {
      descriptors++
      return retain(promoteDescriptor(line))
    }
    if (row?.type === 'tool-call-chunks' && supportedPackedToolRun(row)) {
      expandedRows++
      expandedChunks += row.data.args.length
      if (row.data.name === null) normalizedNames += row.data.args.length
      return expandPackedToolRun(line, row, retain, recovery.packedIds.get(index))
    }
    if (row?.type === 'assistant/chunk' && supportedNullToolDelta(row)) {
      normalizedNames++
      return retain(normalizeNullToolName(line))
    }
    const source = row?.data?.source
    if (row?.type !== 'user/message' || source?.kind !== 'plugin' || source.plugin !== 'dsh-mnemon' ||
        !Object.hasOwn(summaries, source.form) || !summaries[source.form].includes(source.summary)) return retain(line)
    changes++
    return retain(removeSummary(line))
  }).join('')
  const plain = Buffer.from(repaired)
  // A clean artifact stays byte-identical, including its existing frame layout.
  const result = changes + descriptors + expandedRows + normalizedNames + recovery.fields === 0 ? original : compressed ? encodeFrames(repaired) : plain
  if (result.length > maximumBytes) throw new Error('Repaired Session exceeds the 128 MiB repair limit; no output written.')
  const blockers = diagnostics.report()
  if (output !== undefined && blockers.length === 0) {
    if (resolve(input) === resolve(output)) throw new Error('Output must be a new copy; the input cannot be overwritten.')
    const temporary = await mkdtemp(join(dirname(resolve(output)), '.mnemon-repair-'))
    try {
      const path = join(temporary, 'session')
      const file = await open(path, 'wx', 0o600)
      try { await file.writeFile(result); await file.sync() } finally { await file.close() }
      // Publish exclusively, including when output is a symlink or hard link.
      await link(path, output)
    } finally { await rm(temporary, { recursive: true, force: true }) }
  }
  return {
    mode: output === undefined ? 'preview' : blockers.length === 0 ? 'copy' : 'refused',
    format: 0,
    encoding: compressed ? 'zstd' : 'jsonl',
    repairedMessages: changes,
    repairedDescriptors: descriptors,
    expandedToolChunkRows: expandedRows,
    expandedToolChunks: expandedChunks,
    normalizedToolChunkNames: normalizedNames,
    recoveredToolIdentityChains: recovery.chains,
    recoveredToolIdentityFields: recovery.fields,
    toolIdentityRecoveryRefusals: recovery.refusals,
    inputSha256: sha256(original),
    outputSha256: blockers.length === 0 ? sha256(result) : null,
    originalPreserved: true,
    migrationValidated: false,
    blockers,
  }
}

async function main(args) {
  const usage = 'Usage: dsh-mnemon-repair-session --input FILE [--output NEW_FILE]\nWithout --output, preview the three known Mnemon v0 summary repairs, exact compatible v2 subagent descriptors, packed deltas with empty IDs/names, property-preserving null-to-empty delta names and proven DeepSeek chains with a previously recorded provider ID. Unsupported descriptor/tool shapes are reported with exit status 1 and prevent output. DSH must still validate the repaired copy. Stop DSH and work on a backup copy. The input and existing output files are never overwritten.'
  if (args.length === 1 && args[0] === '--help') { console.log(usage); return }
  const options = new Map()
  for (let index = 0; index < args.length; index += 2) {
    const name = args[index]
    const value = args[index + 1]
    if (!['--input', '--output'].includes(name) || !value || value.startsWith('--') || options.has(name)) throw new Error(usage)
    options.set(name, value)
  }
  if (!options.has('--input')) throw new Error(usage)
  const report = await repairLegacySession(options.get('--input'), options.get('--output'))
  console.log(JSON.stringify(report, null, 2))
  if (report.blockers.length > 0) {
    console.error('Unsupported legacy descriptor/tool data remains; no output written. Review blockers and request recovery from the original DSH writer with a sanitized reproducer.')
    process.exitCode = 1
  }
}

// npm global installs expose this executable through a symlink. Resolve both
// sides while keeping imports from stdin or another entry point side-effect free.
function isEntrypoint() {
  try { return process.argv[1] !== undefined && realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url)) }
  catch { return false }
}

if (isEntrypoint()) {
  main(process.argv.slice(2)).catch(error => { console.error(error.message); process.exitCode = 1 })
}
