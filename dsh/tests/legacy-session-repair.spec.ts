import { spawnSync } from 'node:child_process'
import { mkdtemp, readFile, readdir, rm, writeFile, mkdir, link, symlink, stat } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { constants, zstdCompressSync, zstdDecompressSync } from 'node:zlib'
import { Context } from '@deepseek-ai/cordis'
import JsonlSessionPersistence from '@deepseek-ai/dsh-session-persistence-jsonl'
import { SessionId } from '@deepseek-ai/dsh-session'
import { assertReleasedEventPayload } from '@deepseek-ai/dsh-session-format-v0-to-v1'
import { BlockAssembler, assistantStreamFirstTokenTime, expandAssistantStream, isTokenDelta } from '@deepseek-ai/dsh-llm'
import { afterEach, describe, expect, it } from 'vitest'

const command = resolve('bin/repair-legacy-session.mjs')
const fixture = await readFile(new URL('./fixtures/issue-223-legacy-v0.jsonl', import.meta.url), 'utf8')
const runtimeFixture = await readFile(new URL('./fixtures/issue-251-legacy-v0.jsonl', import.meta.url), 'utf8')
const toolsFixture = await readFile(new URL('./fixtures/issue-251-valid-tools-v0.jsonl', import.meta.url), 'utf8')
const combinedFixture = await readFile(new URL('./fixtures/issue-251-repairable-v0.jsonl', import.meta.url), 'utf8')
const nullFixture = await readFile(new URL('./fixtures/issue-251-null-name-v0.jsonl', import.meta.url), 'utf8')
const packedNullFixture = await readFile(new URL('./fixtures/issue-251-null-name-packed-v0.jsonl', import.meta.url), 'utf8')
const identityFixture = await readFile(new URL('./fixtures/issue-251-existing-id-v0.jsonl', import.meta.url), 'utf8')
const allLegacyFixture = await readFile(new URL('./fixtures/issue-251-all-legacy-v0.jsonl', import.meta.url), 'utf8')
const roots: string[] = []
afterEach(async () => { for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }) })
async function directory() {
  const root = await mkdtemp(join(tmpdir(), 'mnemon-legacy-repair-'))
  roots.push(root)
  return root
}
function cli(...args: string[]) { return spawnSync(process.execPath, [command, ...args], { encoding: 'utf8' }) }
function compressFrames(text: string) { return Buffer.concat(text.match(/[^\n]+\n/g)!.map(line => zstdCompressSync(line, { params: { [constants.ZSTD_c_checksumFlag]: 1 } }))) }
function decompressFrames(data: Buffer) {
  const chunks: Buffer[] = []
  let offset = 0
  while (offset < data.length) {
    const result = zstdDecompressSync(data.subarray(offset), { info: true }) as unknown as { buffer: Buffer; engine: { bytesWritten: number } }
    chunks.push(result.buffer)
    offset += result.engine.bytesWritten
  }
  return Buffer.concat(chunks).toString()
}
const expected = fixture.replace(',"summary":"Optional memory recall and remember reminder"', '').replace(',"summary":"Memory View snapshot"', '')
const runtimeExpected = runtimeFixture.replace(',"summary":"Optional memory recall and remember reminder"', '').replace(',"summary":"Memory View snapshot"', '').replace(',"summary":"Runtime memory snapshot"', '')
const descriptorFixture = (data: object) => runtimeExpected + JSON.stringify({ type: 'subagent/descriptor', seq: 17, time: 1789030660071, data }) + '\n'
const descriptorV2 = { version: 2, mode: 'continuable', provider: 'in-process', label: 'Synthetic child', agentProvider: 'fixture', agentModel: 'fixture', persona: 'synthetic', toolFilter: { allow: ['synthetic_lookup'], deny: [] } }

function toolStreamFixture(packed: boolean, id: string, name?: string | null) {
  const rows = toolsFixture.trim().split('\n').map(line => JSON.parse(line))
  const first = rows[3]
  const args = ['', '{', '}']
  const dt = [0, 2]
  const deltas = args.map((argumentsDelta, index) => ({ type: 'assistant/chunk', seq: first.seq + index, time: first.time + (index === 2 ? 2 : 0),
    data: { turn: 1, step: 1, chunk: { type: 'tool-call-delta', index: 0, id, ...(name === undefined ? {} : { name }), argumentsDelta } } }))
  const replacement = packed ? [{ type: 'tool-call-chunks', seq0: first.seq, time0: first.time,
    data: { turn: 1, step: 1, index: 0, id, ...(name === undefined ? {} : { name }), dt, args } }] : deltas
  for (const row of rows.slice(4)) {
    row.seq += 2
    row.time += 2
    if (row.type === 'assistant/message') row.sourceEventSeqs = [[2, 5]]
  }
  return [...rows.slice(0, 3), ...replacement, ...rows.slice(4)].map(row => JSON.stringify(row)).join('\n') + '\n'
}

function replayStream(restored: Awaited<ReturnType<typeof publishedLoad>>) {
  const assistant = restored.events.find(event => event.type === 'assistant/message')!
  const stream = (assistant.data as { stream: Parameters<typeof expandAssistantStream>[0] }).stream
  return [...expandAssistantStream(stream)]
}

function identityRawRows() {
  return identityFixture.trim().split('\n').map(line => JSON.parse(line)).flatMap(row => {
    if (row.type !== 'tool-call-chunks') return [row]
    let time = row.time0
    return row.data.args.map((argumentsDelta: string, index: number) => {
      if (index) time += row.data.dt[index - 1]
      return { type: 'assistant/chunk', seq: row.seq0 + index, time, data: { turn: row.data.turn, step: row.data.step,
        chunk: { type: 'tool-call-delta', index: row.data.index, id: row.data.id, name: row.data.name, argumentsDelta } } }
    })
  })
}
const serializeRows = (rows: any[]) => rows.map(row => JSON.stringify(row)).join('\n') + '\n'
function resequenceIdentityRows(rows: any[], pairs?: any[][]) {
  for (const [seq, row] of rows.slice(1).entries()) { row.seq = seq; row.time = 1789030660068 + seq }
  for (const assistant of rows.filter(row => row.type === 'assistant/message')) {
    assistant.sourceEventSeqs = rows.filter(row => row.type === 'assistant/chunk' && row.data.turn === assistant.data.turn && row.data.step === assistant.data.step).map(row => row.seq)
  }
  for (const [call, result] of pairs ?? [[rows.find(row => row.type === 'tool/call'), rows.find(row => row.type === 'tool/result')]]) result.sourceEventSeqs = [call.seq]
  return rows
}

async function publishedLoad(root: string, text: string, mode: 'read' | 'write' = 'read') {
  const { id, cwd }: { id: string; cwd: string } = JSON.parse(text.split('\n')[0]!)
  const sessionDir = join(root, `--${cwd.slice(1).replaceAll('/', '-')}--`, id)
  await mkdir(sessionDir, { recursive: true })
  await writeFile(join(sessionDir, 'session.jsonl'), text)
  const ctx = new Context()
  await ctx.plugin(JsonlSessionPersistence, { root, compression: 'none' })
  try {
    const handle = await ctx.sessionPersistence.open(SessionId(id), mode)
    try { return await handle.read() } finally { await handle.close() }
  } finally { await ctx.fiber.dispose() }
}

describe('legacy Session copy repair', () => {
  it.each(['absent-result-provenance', 'early-placeholder', 'partial-core-ids', 'nonidentity-metadata'] as const)('restores a uniquely proven provider identity with %s', async variant => {
    const root = await directory()
    const rows = identityRawRows()
    let assistant = rows.filter(row => row.type === 'assistant/message').at(-1)!
    if (variant === 'early-placeholder') {
      const first = rows.findIndex(row => row.type === 'assistant/chunk' && row.data.turn === 2 && row.data.chunk.type === 'tool-call-delta')
      const early = structuredClone(rows[first])
      early.data.chunk = { type: 'tool-call-delta', index: 0, id: '', argumentsDelta: '' }
      rows.splice(first, 0, early)
      resequenceIdentityRows(rows)
    }
    assistant = rows.filter(row => row.type === 'assistant/message').at(-1)!
    if (variant === 'absent-result-provenance') delete rows.find(row => row.type === 'tool/result').sourceEventSeqs
    if (variant === 'partial-core-ids') {
      assistant.data.message.content[0].id = 'provider-existing-id-251'
      rows.find(row => row.type === 'tool/result').data.message.source.callId = 'provider-existing-id-251'
    }
    if (variant === 'nonidentity-metadata') {
      // Literal payload text is not an additional structured identity slot.
      rows.find(row => row.type === 'tool/result').data.message.content[0].content[0].text = 'Opaque result text: callId="" remains literal content.'
    }
    const text = serializeRows(rows)
    const input = join(root, 'input')
    const output = join(root, 'output')
    await writeFile(input, text)
    const result = cli('--input', input, '--output', output)
    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({ recoveredToolIdentityChains: 1, blockers: [] })
    const after = await readFile(output, 'utf8')
    const migrated = await publishedLoad(join(root, 'after'), after, 'write')
    expect((await publishedLoad(join(root, 'after'), after)).events).toEqual(migrated.events)
    const restored = migrated.events.filter(event => event.type === 'assistant/message').at(-1)!
    const deltas = expandAssistantStream((restored.data as any).stream).filter(value => value.chunk.type === 'tool-call-delta')
    expect(deltas.map(value => (value.chunk as any).id)).toEqual(variant === 'early-placeholder' ? ['', ...Array(3).fill('provider-existing-id-251')] : Array(3).fill('provider-existing-id-251'))
    expect(await readFile(input, 'utf8')).toBe(text)
    expect(JSON.parse(cli('--input', output).stdout).recoveredToolIdentityChains).toBe(0)
  })

  it.each([...
    (['none', 'zstd'] as const).flatMap(compression => [false, true].map(packedText => ({ compression, packedText, conflictingOther: false }))),
    { compression: 'none', packedText: false, conflictingOther: true },
  ])('proves or refuses multiple-call identities while retaining reasoning and text in stream order (%#)', async ({ compression, packedText, conflictingOther }) => {
    const root = await directory()
    const rows = identityRawRows()
    const assistant = rows.filter(row => row.type === 'assistant/message').at(-1)!
    const firstCall = rows.find(row => row.type === 'tool/call')
    const firstResult = rows.find(row => row.type === 'tool/result')
    const firstChunk = rows.findIndex(row => row.type === 'assistant/chunk' && row.data.turn === 2)
    const chunk = (value: object) => ({ type: 'assistant/chunk', data: { turn: 2, step: 1, chunk: value } })
    const reasoning = { type: 'reasoning', text: 'Synthetic plan.' }
    const textBlock = { type: 'text', text: 'Synthetic narration.' }
    rows.splice(firstChunk, 0,
      chunk({ type: 'block-start', index: 9, blockType: 'reasoning' }), chunk({ type: 'reasoning-delta', index: 9, text: 'Synthetic' }), chunk({ type: 'reasoning-delta', index: 9, text: ' plan.' }),
      chunk({ type: 'block-start', index: 4, blockType: 'text' }), chunk({ type: 'text-delta', index: 4, text: 'Synthetic' }), chunk({ type: 'text-delta', index: 4, text: ' narration.' }),
      chunk({ type: 'block-end', index: 9, block: reasoning }), chunk({ type: 'block-end', index: 4, block: textBlock }))
    const secondBlock = { type: 'tool-call', id: '', name: 'synthetic_second', arguments: '{"second":true}' }
    const usageIndex = rows.findIndex(row => row.type === 'assistant/chunk' && row.data.turn === 2 && row.data.chunk.type === 'usage')
    rows.splice(usageIndex, 0, chunk({ type: 'block-start', index: 2, blockType: 'tool-call' }),
      chunk({ type: 'tool-call-delta', index: 2, id: 'provider-second-id', name: secondBlock.name, argumentsDelta: '{' }),
      chunk({ type: 'tool-call-delta', index: 2, id: '', argumentsDelta: '"second":true}' }), chunk({ type: 'block-end', index: 2, block: secondBlock }))
    assistant.data.message.content = [reasoning, textBlock, ...assistant.data.message.content, secondBlock]
    const secondCall = structuredClone(firstCall)
    Object.assign(secondCall.data, { name: secondBlock.name, arguments: secondBlock.arguments })
    const secondResult = structuredClone(firstResult)
    secondResult.data.message.id += '-second'
    secondResult.data.message.content[0].content[0].text = 'Second synthetic result.'
    rows.splice(rows.indexOf(firstResult), 0, secondCall, secondResult) // Result order differs from advertisement order.
    resequenceIdentityRows(rows, [[firstCall, firstResult], [secondCall, secondResult]])
    if (packedText) for (const kind of ['reasoning', 'text']) {
      const index = rows.findIndex(row => row.type === 'assistant/chunk' && row.data.turn === 2 && row.data.chunk.type === `${kind}-delta`)
      const first = rows[index]
      const second = rows[index + 1]
      rows.splice(index, 2, { type: `${kind}-chunks`, seq0: first.seq, time0: first.time, data: { turn: 2, step: 1, index: first.data.chunk.index,
        dt: [second.time - first.time], texts: [first.data.chunk.text, second.data.chunk.text] } })
    }
    if (conflictingOther) {
      secondBlock.id = 'provider-second-id'
      secondCall.data.callId = 'provider-existing-id-251'
      secondResult.data.message.source.callId = 'provider-second-id'
      secondResult.data.message.content[0].toolCallId = 'provider-second-id'
    }
    const text = serializeRows(rows)
    const input = join(root, 'input')
    const output = join(root, 'output')
    const original = compression === 'none' ? Buffer.from(text) : compressFrames(text)
    await writeFile(input, original)
    const result = cli('--input', input, '--output', output)
    if (conflictingOther) {
      expect(result.status).toBe(1)
      expect(JSON.parse(result.stdout)).toMatchObject({ recoveredToolIdentityChains: 0, mode: 'refused', outputSha256: null })
      expect(await readFile(input)).toEqual(original)
      expect(await readdir(root)).toEqual(['input'])
      return
    }
    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({ recoveredToolIdentityChains: 2, blockers: [] })
    const bytes = await readFile(output)
    const after = compression === 'none' ? bytes.toString() : decompressFrames(bytes)
    if (packedText) for (const line of text.split('\n').filter(line => /"type":"(?:text|reasoning)-chunks"/.test(line))) expect(after.split('\n')).toContain(line)
    const migrated = await publishedLoad(join(root, 'after'), after, 'write')
    const restored = migrated.events.filter(event => event.type === 'assistant/message').at(-1)!
    expect((restored.data as any).message.content).toEqual([reasoning, textBlock,
      { type: 'tool-call', id: 'provider-existing-id-251', name: 'synthetic_lookup', arguments: '{}' }, { ...secondBlock, id: 'provider-second-id' }])
    expect(migrated.events.filter(event => event.type === 'tool/result').map(event => (event.data as any).message.source.callId)).toEqual(['provider-second-id', 'provider-existing-id-251'])
    expect((await publishedLoad(join(root, 'after'), after)).events).toEqual(migrated.events)
    expect(await readFile(input)).toEqual(original)
  })

  it('allows the same provider ID in an independently completed later turn', async () => {
    const root = await directory()
    const rows = identityRawRows()
    const next = structuredClone(rows.slice(rows.findIndex(row => row.type === 'turn/start' && row.data.turn === 2)))
    for (const row of next) {
      row.data.turn = 3
      const chunk = row.data.chunk
      if (chunk?.type === 'tool-call-delta' && chunk.id === '') chunk.id = 'provider-existing-id-251'
      if (chunk?.type === 'block-end') chunk.block.id = 'provider-existing-id-251'
      if (row.type === 'assistant/message') { row.data.message.id += '-later'; row.data.message.content[0].id = 'provider-existing-id-251' }
      if (row.type === 'tool/call') row.data.callId = 'provider-existing-id-251'
      if (row.type === 'tool/result') {
        row.data.message.id += '-later'
        row.data.message.source.callId = 'provider-existing-id-251'
        row.data.message.content[0].toolCallId = 'provider-existing-id-251'
      }
    }
    resequenceIdentityRows([...rows, ...next], [
      [rows.find(row => row.type === 'tool/call'), rows.find(row => row.type === 'tool/result')],
      [next.find(row => row.type === 'tool/call'), next.find(row => row.type === 'tool/result')],
    ])
    const text = serializeRows([...rows, ...next])
    const input = join(root, 'input')
    const output = join(root, 'output')
    await writeFile(input, text)
    const result = cli('--input', input, '--output', output)
    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout).recoveredToolIdentityChains).toBe(1)
    const after = await readFile(output, 'utf8')
    const migrated = await publishedLoad(join(root, 'after'), after, 'write')
    expect(migrated.events.filter(event => event.type === 'tool/call').map(event => (event.data as any).callId)).toEqual(Array(2).fill('provider-existing-id-251'))
    expect((await publishedLoad(join(root, 'after'), after)).events).toEqual(migrated.events)
  })

  it('patches only exact identity tokens while preserving opaque metadata and message UUIDs', async () => {
    const root = await directory()
    const rows = identityRawRows()
    rows.find(row => row.type === 'tool/result').data.meta = { owner: { value: 'large-number', spelling: '\\u4e2d' } }
    const text = serializeRows(rows).replace('"large-number"', '9007199254740993').replaceAll('"id":""', '"id" : ""').replaceAll('\n', '\r\n')
    const expected = text.replaceAll('"id" : ""', '"id" : "provider-existing-id-251"').replaceAll('"callId":""', '"callId":"provider-existing-id-251"')
      .replaceAll('"toolCallId":""', '"toolCallId":"provider-existing-id-251"')
    const input = join(root, 'input')
    const output = join(root, 'output')
    await writeFile(input, text)
    expect(cli('--input', input, '--output', output).status).toBe(0)
    expect(await readFile(output, 'utf8')).toBe(expected)
    expect(await readFile(input, 'utf8')).toBe(text)
    expect(cli('--input', input, '--output', output).status).toBe(1)
    expect(await readFile(output, 'utf8')).toBe(expected)
  })

  it('refuses duplicate keys in provider candidates, provenance and opaque owner metadata', async () => {
    const root = await directory()
    const input = join(root, 'input')
    const base = serializeRows(identityRawRows())
    const rows = identityRawRows()
    rows.find(row => row.type === 'tool/result').data.meta = { owner: { field: 'placeholder' } }
    const sources = JSON.stringify(rows.filter(row => row.type === 'assistant/message').at(-1)!.sourceEventSeqs)
    for (const text of [
      base.replace('"id":"provider-existing-id-251"', '"id":"conflict","id":"provider-existing-id-251"'),
      base.replace(`"sourceEventSeqs":${sources}`, `"sourceEventSeqs":[],"sourceEventSeqs":${sources}`),
      serializeRows(rows).replace('"field":"placeholder"', '"field":0,"field":"placeholder"'),
    ]) {
      await writeFile(input, text)
      const result = cli('--input', input, '--output', join(root, 'output'))
      expect(result.status).toBe(1)
      expect(result.stderr).toContain('Duplicate JSON property')
      expect(await readFile(input, 'utf8')).toBe(text)
      expect(await readdir(root)).toEqual(['input'])
    }
  })

  it.each([
    'no-candidate', 'conflicting-delta-id', 'conflicting-durable-id', 'candidate-on-other-index', 'missing-block-start', 'duplicate-block-end', 'post-end-delta',
    'interrupted-message', 'non-tool-finish', 'missing-finish', 'owner-replay-state', 'usage-mismatch', 'unordered-provenance', 'omitted-delta', 'duplicate-provenance',
    'huge-provenance-range', 'wrong-result-provenance', 'duplicate-call', 'unadvertised-candidate-call', 'invalid-call-owner-shape', 'invalid-result-owner-shape',
    'approval-reference', 'ptc-reference', 'plugin-reference', 'later-linked-reference', 'later-message-reference', 'later-shadowed-range-reference', 'copied-result',
  ])('refuses a provider-ID repair with %s and publishes no partial copy', async variant => {
    const root = await directory()
    const rows = identityRawRows()
    const assistant = rows.filter(row => row.type === 'assistant/message').at(-1)!
    const call = rows.find(row => row.type === 'tool/call')
    const result = rows.find(row => row.type === 'tool/result')
    const deltas = rows.filter(row => row.type === 'assistant/chunk' && row.data.turn === 2 && row.data.chunk.type === 'tool-call-delta')
    const end = rows.find(row => row.type === 'assistant/chunk' && row.data.turn === 2 && row.data.chunk.type === 'block-end')
    const finish = rows.find(row => row.type === 'assistant/chunk' && row.data.turn === 2 && row.data.chunk.type === 'finish')
    let extra
    if (variant === 'no-candidate') deltas[0].data.chunk.id = ''
    if (variant === 'conflicting-delta-id') deltas[1].data.chunk.id = 'another-provider-id'
    if (variant === 'conflicting-durable-id') call.data.callId = 'another-provider-id'
    if (variant === 'candidate-on-other-index') deltas[0].data.chunk.index = 1
    if (variant === 'missing-block-start') rows.splice(rows.findIndex(row => row.type === 'assistant/chunk' && row.data.turn === 2 && row.data.chunk.type === 'block-start'), 1)
    if (variant === 'duplicate-block-end') rows.splice(rows.indexOf(end), 0, structuredClone(end))
    if (variant === 'post-end-delta') rows.splice(rows.indexOf(end) + 1, 0, structuredClone(deltas[1]))
    if (variant === 'interrupted-message') assistant.data.interrupted = true
    if (variant === 'non-tool-finish') finish.data.chunk.reason.kind = 'max-tokens'
    if (variant === 'missing-finish') rows.splice(rows.indexOf(finish), 1)
    if (variant === 'owner-replay-state') assistant.data.message.source.replayState = { response: { untouched: true } }
    if (variant === 'usage-mismatch') assistant.data.usage.outputTokens++
    if (variant === 'duplicate-call') extra = structuredClone(call)
    if (variant === 'unadvertised-candidate-call') { extra = structuredClone(call); Object.assign(extra.data, { callId: 'provider-existing-id-251', name: 'other_lookup', arguments: '{"other":true}' }) }
    if (variant === 'invalid-call-owner-shape') call.data.unknownOwner = true
    if (variant === 'invalid-result-owner-shape') delete result.data.message.content[0].content
    if (variant === 'approval-reference') extra = { type: 'approval/asked', data: { id: 'synthetic-approval', toolName: 'synthetic_lookup', callId: '' } }
    if (variant === 'ptc-reference') extra = { type: 'tool/code-dispatch-start', data: { rootCallId: '', parentCallId: '', subCallId: ':code:1', name: 'nested_lookup', arguments: {} } }
    if (['plugin-reference', 'later-linked-reference', 'later-message-reference', 'later-shadowed-range-reference'].includes(variant)) extra = { type: 'user/message', surfaceOp: 'append', data: { role: 'user', id: 'other-plugin-message', source: { kind: 'plugin', plugin: 'other-plugin' }, content: [{ type: 'owner-metadata', callId: '' }] } }
    if (variant === 'copied-result') { extra = structuredClone(result); extra.data.message.id += '-copy' }
    if (extra) rows.splice(variant.startsWith('later-') ? rows.length - 1 : rows.indexOf(result) + 1, 0, extra)
    resequenceIdentityRows(rows)
    if (variant === 'unordered-provenance') assistant.sourceEventSeqs.reverse()
    if (variant === 'omitted-delta') { deltas[1].data.chunk.id = 'hidden-conflict'; assistant.sourceEventSeqs = assistant.sourceEventSeqs.filter((seq: number) => seq !== deltas[1].seq) }
    if (variant === 'duplicate-provenance') assistant.sourceEventSeqs.push(assistant.sourceEventSeqs[0])
    if (variant === 'huge-provenance-range') assistant.sourceEventSeqs = [[0, Number.MAX_SAFE_INTEGER]]
    if (variant === 'wrong-result-provenance') result.sourceEventSeqs = [assistant.seq]
    if (variant === 'later-linked-reference') extra.data.content[0].sourceEventSeq = call.seq
    if (variant === 'later-message-reference') extra.data.content[0].messageId = assistant.data.message.id
    if (variant === 'later-shadowed-range-reference') extra.data.content[0].shadowedRange = { start: assistant.seq, end: result.seq }
    if (variant === 'copied-result') extra.sourceEventSeqs = [call.seq]
    const text = serializeRows(rows)
    const input = join(root, 'input')
    await writeFile(input, text)
    const report = cli('--input', input, '--output', join(root, 'output'))
    expect(report.status).toBe(1)
    expect(JSON.parse(report.stdout)).toMatchObject({ mode: 'refused', recoveredToolIdentityChains: 0, recoveredToolIdentityFields: 0, outputSha256: null })
    expect(JSON.parse(report.stdout).toolIdentityRecoveryRefusals.occurrences).toBeGreaterThan(0)
    expect(await readFile(input, 'utf8')).toBe(text)
    expect(await readdir(root)).toEqual(['input'])
  })

  it.each([identityFixture, allLegacyFixture].flatMap(text => (['none', 'zstd'] as const).map(compression => ({ text, compression }))))('recovers only the provider ID already recorded in the complete historical tool chain (%#)', async ({ text, compression }) => {
    const root = await directory()
    const input = join(root, 'input')
    const output = join(root, 'output')
    const before = compression === 'none' ? Buffer.from(text) : compressFrames(text)
    await writeFile(input, before)
    await expect(publishedLoad(join(root, 'before'), text)).rejects.toThrow(/source summary requires notice form|id and optional name must be strings/)
    const result = cli('--input', input, '--output', output)
    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({ recoveredToolIdentityChains: 1, recoveredToolIdentityFields: 7, blockers: [] })
    const bytes = await readFile(output)
    const after = compression === 'none' ? bytes.toString() : decompressFrames(bytes)
    const migrated = await publishedLoad(join(root, 'after'), after, 'write')
    const assistant = migrated.events.filter(event => event.type === 'assistant/message').at(-1)!
    const content = (assistant.data as any).message.content
    expect(content).toEqual([{ type: 'tool-call', id: 'provider-existing-id-251', name: 'synthetic_lookup', arguments: '{}' }])
    const stream = expandAssistantStream((assistant.data as any).stream)
    expect(stream.filter(value => value.chunk.type === 'tool-call-delta').map(value => (value.chunk as any).id)).toEqual(Array(3).fill('provider-existing-id-251'))
    expect(new Set(migrated.events.filter(event => event.type === 'tool/call').map(event => (event.data as any).callId))).toEqual(new Set(['provider-existing-id-251']))
    const toolResult = migrated.events.find(event => event.type === 'tool/result')!
    expect((toolResult.data as any).message.source.callId).toBe('provider-existing-id-251')
    expect((toolResult.data as any).message).toMatchObject({ toolCallId: 'provider-existing-id-251', content: [{ type: 'text', text: 'Synthetic tool response with the original provider ID.' }] })
    expect(after.split('\n').find(line => line.includes('synthetic-other-memory-source'))).toBe(text.split('\n').find(line => line.includes('synthetic-other-memory-source')))
    expect((await publishedLoad(join(root, 'after'), after)).events).toEqual(migrated.events)
    expect(await readFile(input)).toEqual(before)
    const repeated = cli('--input', output, '--output', join(root, 'idempotent'))
    expect(repeated.status).toBe(0)
    expect(JSON.parse(repeated.stdout)).toMatchObject({ recoveredToolIdentityChains: 0, recoveredToolIdentityFields: 0 })
    expect(await readFile(join(root, 'idempotent'))).toEqual(bytes)
  })

  it.each(['none', 'zstd'] as const)('repairs all supported shapes together without changing another plugin (%s)', async compression => {
    const root = await directory()
    const input = join(root, 'input')
    const output = join(root, 'output')
    const before = compression === 'none' ? Buffer.from(combinedFixture) : compressFrames(combinedFixture)
    await writeFile(input, before)
    const result = cli('--input', input, '--output', output)
    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({ repairedMessages: 3, repairedDescriptors: 1, expandedToolChunkRows: 1, expandedToolChunks: 3, blockers: [] })
    const after = await readFile(output)
    const text = compression === 'none' ? after.toString() : decompressFrames(after)
    const migrated = await publishedLoad(join(root, 'load'), text, 'write')
    const originalOther = combinedFixture.split('\n').find(line => line.includes('synthetic-other-memory-source'))!
    expect(text.split('\n')).toContain(originalOther)
    expect(migrated.events.filter(event => event.type === 'tool/call')).toHaveLength(1)
    const descriptor = migrated.events.find(event => event.type === 'subagent/descriptor')!
    expect(descriptor.data).toMatchObject({ version: 3, label: 'Synthetic historical child descriptor' })
    expect(await readFile(input)).toEqual(before)
    const repeated = cli('--input', output, '--output', join(root, 'idempotent'))
    expect(repeated.status).toBe(0)
    expect(await readFile(join(root, 'idempotent'))).toEqual(after)
  })

  it.each([
    { id: '', name: undefined },
    { id: '', name: '' },
    { id: 'call-251', name: '' },
  ].flatMap(value => (['none', 'zstd'] as const).map(compression => ({ ...value, compression }))))('losslessly unpacks string placeholders for actual migration and stream replay (%#)', async ({ id, name, compression }) => {
    const root = await directory()
    const text = toolStreamFixture(true, id, name)
    const raw = toolStreamFixture(false, id, name)
    if (id === '') await expect(publishedLoad(join(root, 'before'), text)).rejects.toThrow('id and optional name must be strings')
    else {
      const loaded = await publishedLoad(join(root, 'before'), text)
      expect(() => replayStream(loaded)).toThrow('name must be a non-empty string')
    }
    const input = join(root, 'input')
    const output = join(root, 'output')
    const before = compression === 'none' ? Buffer.from(text) : compressFrames(text)
    await writeFile(input, before)
    const result = cli('--input', input, '--output', output)
    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({ repairedMessages: 0, expandedToolChunkRows: 1, expandedToolChunks: 3, blockers: [] })
    const after = await readFile(output)
    const unpacked = compression === 'none' ? after.toString() : decompressFrames(after)
    expect(unpacked).toBe(raw)
    expect(await readFile(input)).toEqual(before)
    const migrated = await publishedLoad(join(root, 'after'), unpacked, 'write')
    const replay = replayStream(migrated)
    const originalChunks = raw.trim().split('\n').map(line => JSON.parse(line)).filter(row => row.type === 'assistant/chunk').map(row => ({ time: row.time, chunk: row.data.chunk }))
    expect(replay).toEqual(originalChunks)
    expect(replayStream(await publishedLoad(join(root, 'after'), unpacked))).toEqual(originalChunks)
    expect(JSON.parse(cli('--input', output).stdout)).toMatchObject({ expandedToolChunkRows: 0, expandedToolChunks: 0, inputSha256: JSON.parse(result.stdout).outputSha256 })
  })

  it('leaves raw empty-string placeholders and valid tool chains byte-identical', async () => {
    const root = await directory()
    for (const [index, text] of [toolsFixture, toolStreamFixture(false, ''), toolStreamFixture(false, '', ''), toolStreamFixture(false, 'call-251', '')].entries()) {
      const input = join(root, `input-${index}`)
      const output = join(root, `output-${index}`)
      await writeFile(input, text)
      const result = cli('--input', input, '--output', output)
      expect(result.status).toBe(0)
      expect(JSON.parse(result.stdout)).toMatchObject({ repairedMessages: 0, repairedDescriptors: 0, expandedToolChunkRows: 0, blockers: [] })
      expect(await readFile(output, 'utf8')).toBe(text)
      const loaded = await publishedLoad(join(root, `load-${index}`), text)
      expect(replayStream(loaded)).toHaveLength(index === 0 ? 2 : 4)
    }
  })

  it.each([false, true].flatMap(packed => ['', 'call-251'].flatMap(id => (['none', 'zstd'] as const).map(compression => ({ packed, id, compression })))))('normalizes null delta names without changing assembly or first-token timing (%#)', async ({ packed, id, compression }) => {
    const root = await directory()
    const text = toolStreamFixture(packed, id, null)
    const normalized = toolStreamFixture(false, id, '')
    const raw = toolStreamFixture(false, id, null)
    const input = join(root, 'input')
    const output = join(root, 'output')
    const before = compression === 'none' ? Buffer.from(text) : compressFrames(text)
    await writeFile(input, before)
    await expect(publishedLoad(join(root, 'before'), text)).rejects.toThrow(packed ? 'id and optional name must be strings' : 'tool-call-delta name must be a string')
    const preview = cli('--input', input)
    expect(preview.status).toBe(0)
    expect(JSON.parse(preview.stdout)).toMatchObject({ mode: 'preview', normalizedToolChunkNames: 3, blockers: [] })
    expect(await readdir(root)).toEqual(['before', 'input'])
    const result = cli('--input', input, '--output', output)
    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({ mode: 'copy', normalizedToolChunkNames: 3, expandedToolChunkRows: packed ? 1 : 0, expandedToolChunks: packed ? 3 : 0, blockers: [] })
    const after = await readFile(output)
    const repaired = compression === 'none' ? after.toString() : decompressFrames(after)
    expect(repaired).toBe(normalized)
    expect(await readFile(input)).toEqual(before)
    expect(cli('--input', input, '--output', output).status).toBe(1)
    expect(await readFile(output)).toEqual(after)
    const migrated = await publishedLoad(join(root, 'after'), repaired, 'write')
    const stream = replayStream(migrated)
    const originalChunks = raw.trim().split('\n').map(line => JSON.parse(line)).filter(row => row.type === 'assistant/chunk').map(row => ({ time: row.time, chunk: row.data.chunk }))
    expect(stream).toEqual(originalChunks.map(value => ({ ...value, chunk: value.chunk.name === null ? { ...value.chunk, name: '' } : value.chunk })))
    const originalAssembler = new BlockAssembler()
    const normalizedAssembler = new BlockAssembler()
    for (const [index, original] of originalChunks.entries()) {
      originalAssembler.push(original.chunk)
      normalizedAssembler.push(stream[index]!.chunk)
      expect(normalizedAssembler.blocks()).toEqual(originalAssembler.blocks())
      expect(isTokenDelta(stream[index]!.chunk)).toBe(isTokenDelta(original.chunk))
    }
    const migratedAssistant = migrated.events.find(event => event.type === 'assistant/message')!
    const migratedStream = (migratedAssistant.data as { stream: Parameters<typeof assistantStreamFirstTokenTime>[0] }).stream
    expect(assistantStreamFirstTokenTime(migratedStream)).toBe(originalChunks.find(value => isTokenDelta(value.chunk))!.time)
    expect(originalChunks[0]!.chunk.argumentsDelta).toBe('')
    expect(replayStream(await publishedLoad(join(root, 'after'), repaired))).toEqual(stream)
    const repeated = cli('--input', output, '--output', join(root, 'idempotent'))
    expect(repeated.status).toBe(0)
    expect(JSON.parse(repeated.stdout)).toMatchObject({ normalizedToolChunkNames: 0, expandedToolChunkRows: 0 })
    expect(await readFile(join(root, 'idempotent'))).toEqual(after)
  })

  it.each([nullFixture, packedNullFixture])('restores the null-name WebUI fixtures with durable messages and other plugin content intact (%#)', async text => {
    const root = await directory()
    const input = join(root, 'input')
    const output = join(root, 'output')
    await writeFile(input, text)
    await expect(publishedLoad(join(root, 'before'), text)).rejects.toThrow(/name must be a string|id and optional name must be strings/)
    const result = cli('--input', input, '--output', output)
    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({ repairedMessages: 0, repairedDescriptors: 0, normalizedToolChunkNames: 3, blockers: [] })
    const after = await readFile(output, 'utf8')
    const migrated = await publishedLoad(join(root, 'after'), after, 'write')
    const originals = nullFixture.trim().split('\n').map(line => JSON.parse(line))
    const reference = await publishedLoad(join(root, 'reference'), nullFixture.replaceAll('"name":null', '"name":""'))
    for (const type of ['user/message', 'tool/call', 'tool/result', 'assistant/message']) {
      expect(migrated.events.filter(event => event.type === type).map(event => event.data)).toEqual(reference.events.filter(event => event.type === type).map(event => event.data))
    }
    const assistants = migrated.events.filter(event => event.type === 'assistant/message')
    expect(assistants.flatMap(event => expandAssistantStream((event.data as any).stream))).toEqual(originals.filter(event => event.type === 'assistant/chunk')
      .map(event => ({ time: event.time, chunk: event.data.chunk.name === null ? { ...event.data.chunk, name: '' } : event.data.chunk })))
    expect((await publishedLoad(join(root, 'after'), after)).events).toEqual(migrated.events)
    expect(await readFile(input, 'utf8')).toBe(text)
  })

  it('changes only the raw null token, retaining name presence and unrelated null metadata', async () => {
    const root = await directory()
    const input = join(root, 'input')
    const output = join(root, 'output')
    const row = '  {"type":"assistant/chunk", "seq":2e0, "time":1.2300e+3, "ignorable":true, "data":{"turn":1,"step":1,"chunk":{"type":"tool-call-delta", "index":0, "id":"\\u0063all-251", "na\\u006de" : null, "argumentsDelta":"\\\"name\\\":null"}}}\r\n'
    const text = toolsFixture.split('\n')[0] + '\r\n' + row + '{"type":"owner/opaque","data":{"name":null,"n":9007199254740993}}\r\n'
    await writeFile(input, text)
    const result = cli('--input', input, '--output', output)
    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout).normalizedToolChunkNames).toBe(1)
    expect(await readFile(output, 'utf8')).toBe(text.replace(' : null', ' : ""'))
    expect(await readFile(input, 'utf8')).toBe(text)
  })

  it('preserves every assembler prefix after an earlier name and across block closure', async () => {
    const root = await directory()
    const rows = toolStreamFixture(false, 'call-251', null).trim().split('\n').map(line => JSON.parse(line))
    const first = rows.find(row => row.type === 'assistant/chunk')
    const extra = [
      { ...first.data.chunk, name: 'synthetic_lookup', argumentsDelta: '' },
      ...rows.filter(row => row.type === 'assistant/chunk').map(row => row.data.chunk),
      { ...first.data.chunk, name: null, argumentsDelta: 'ignored after block-end' },
      { type: 'usage', usage: { inputTokens: 10, outputTokens: 5 } },
      { type: 'finish', reason: { kind: 'tool-calls' }, replayState: { response: { name: null, owner: 'untouched' }, blocks: [{ name: null }] } },
    ]
    const text = JSON.stringify(rows[0]) + '\n' + extra.map((chunk, index) => JSON.stringify({ type: 'assistant/chunk', seq: index, time: 1000 + index, data: { turn: 1, step: 1, chunk } })).join('\n') + '\n'
    const input = join(root, 'input')
    const output = join(root, 'output')
    await writeFile(input, text)
    expect(cli('--input', input, '--output', output).status).toBe(0)
    const normalized = (await readFile(output, 'utf8')).trim().split('\n').slice(1).map(line => JSON.parse(line).data.chunk)
    const before = new BlockAssembler()
    const after = new BlockAssembler()
    const state = (assembler: BlockAssembler) => ({ blocks: assembler.blocks(), interrupted: assembler.interruptedBlocks(), usage: assembler.usage, finish: assembler.finish, replayState: assembler.replayState })
    for (const [index, chunk] of extra.entries()) {
      before.push(chunk)
      after.push(normalized[index])
      expect(state(after)).toEqual(state(before))
      expect(isTokenDelta(normalized[index])).toBe(isTokenDelta(chunk))
    }
    expect(normalized.at(-1)).toEqual(extra.at(-1))
  })

  it('refuses unknown raw null-name shapes and invalid coordinates instead of publishing a partial repair', async () => {
    const root = await directory()
    const input = join(root, 'input')
    const output = join(root, 'output')
    for (const mutate of [
      (row: any) => { row.extra = true },
      (row: any) => { row.ignorable = false },
      (row: any) => { row.data.extra = true },
      (row: any) => { row.data.chunk.extra = true },
      (row: any) => { row.seq = Number.MAX_SAFE_INTEGER + 1 },
      (row: any) => { row.time = Number.MAX_SAFE_INTEGER + 1 },
      (row: any) => { row.time = 0.5 },
      (row: any) => { row.data.turn = -1 },
      (row: any) => { row.data.step = 0.5 },
      (row: any) => { row.data.chunk.index = -1 },
      (row: any) => { row.data.chunk.id = null },
      (row: any) => { row.data.chunk.argumentsDelta = null },
      (row: any) => { delete row.data.chunk.argumentsDelta },
    ]) {
      const rows = toolStreamFixture(false, 'call-251', null).trim().split('\n').map(line => JSON.parse(line))
      mutate(rows.find(row => row.type === 'assistant/chunk'))
      const text = rows.map(row => JSON.stringify(row)).join('\n') + '\n'
      await writeFile(input, text)
      const result = cli('--input', input, '--output', output)
      expect(result.status).toBe(1)
      expect(JSON.parse(result.stdout)).toMatchObject({ mode: 'refused', outputSha256: null, blockers: [{ code: 'null-stream-tool-name', occurrences: 1 }] })
      expect(await readFile(input, 'utf8')).toBe(text)
      expect(await readdir(root)).toEqual(['input'])
    }
    await writeFile(input, toolStreamFixture(false, 'call-251', null).replace('"index":0', '"index":-0'))
    expect(cli('--input', input, '--output', output).status).toBe(1)
    expect(await readdir(root)).toEqual(['input'])
  })

  it('rejects duplicate properties along raw and packed null-name paths', async () => {
    const root = await directory()
    const input = join(root, 'input')
    const raw = toolStreamFixture(false, 'call-251', null)
    for (const text of [
      raw.replace('"type":"assistant/chunk"', '"type":"other","type":"assistant/chunk"'),
      raw.replace('"chunk":{"type":"tool-call-delta"', '"chunk":{},"chunk":{"type":"tool-call-delta"'),
      raw.replace('"name":null', '"name":"other","na\\u006de":null'),
      toolStreamFixture(true, 'call-251', null).replace('"name":null', '"name":"other","name":null'),
    ]) {
      await writeFile(input, text)
      const result = cli('--input', input, '--output', join(root, 'output'))
      expect(result.status).toBe(1)
      expect(result.stderr).toContain('Duplicate JSON property')
      expect(await readFile(input, 'utf8')).toBe(text)
      expect(await readdir(root)).toEqual(['input'])
    }
  })

  it.each([false, true])('refuses empty durable tool identities, including ambiguous advertisements (%s)', async ambiguous => {
    const root = await directory()
    let text = toolsFixture.replaceAll('"call-251"', '""')
    if (ambiguous) {
      const rows = text.trim().split('\n').map(line => JSON.parse(line))
      rows.find(row => row.type === 'assistant/message').data.message.content.push({ type: 'tool-call', id: '', name: 'other_lookup', arguments: '{}' })
      text = rows.map(row => JSON.stringify(row)).join('\n') + '\n'
    }
    const input = join(root, 'input')
    await writeFile(input, text)
    await expect(publishedLoad(join(root, 'before'), text)).rejects.toThrow('id must be a non-empty string')
    const result = cli('--input', input, '--output', join(root, 'output'))
    expect(result.status).toBe(1)
    const report = JSON.parse(result.stdout)
    expect(report).toMatchObject({ mode: 'refused', outputSha256: null, blockers: [{ code: 'empty-durable-tool-id', occurrences: ambiguous ? 6 : 5 }] })
    expect(report.blockers[0].samples.map((sample: { path: string }) => sample.path)).toContain('data.message.source.callId')
    expect(report.blockers[0].samples.map((sample: { path: string }) => sample.path)).toContain('data.message.content[0].toolCallId')
    expect(await readFile(input, 'utf8')).toBe(text)
    expect(await readdir(root)).toEqual(['before', 'input'])
  })

  it.each(['assistant/message', 'tool/result'])('also diagnoses empty identities in historical unwrapped %s', async type => {
    const root = await directory()
    const rows = toolsFixture.trim().split('\n').map(line => JSON.parse(line))
    const row = rows.find(row => row.type === type)
    row.data = type === 'assistant/message'
      ? { turn: 1, step: 1, content: row.data.message.content, provenance: { provider: 'fixture', model: 'fixture' } }
      : { turn: 1, step: 1, callId: 'call-251', content: row.data.message.content[0].content, isError: false }
    const valid = rows.map(row => JSON.stringify(row)).join('\n') + '\n'
    await expect(publishedLoad(join(root, 'valid'), valid)).resolves.toBeDefined()
    const input = join(root, 'input')
    await writeFile(input, valid)
    const clean = cli('--input', input, '--output', join(root, 'valid-copy'))
    expect(clean.status).toBe(0)
    expect(await readFile(join(root, 'valid-copy'), 'utf8')).toBe(valid)
    if (type === 'assistant/message') row.data.content[0].id = ''
    else row.data.callId = ''
    const text = rows.map(row => JSON.stringify(row)).join('\n') + '\n'
    await expect(publishedLoad(join(root, 'invalid'), text)).rejects.toThrow('must be a non-empty string')
    await writeFile(input, text)
    const result = cli('--input', input, '--output', join(root, 'output'))
    expect(result.status).toBe(1)
    expect(JSON.parse(result.stdout)).toMatchObject({ mode: 'refused', blockers: [{ code: 'empty-durable-tool-id', occurrences: 1,
      samples: [{ path: type === 'assistant/message' ? 'data.content[0].id' : 'data.callId' }] }] })
    expect(await readFile(input, 'utf8')).toBe(text)
    expect(await readdir(root)).toEqual(['input', 'invalid', 'valid', 'valid-copy'])
  })

  it.each([
    (row: any) => { row.extra = true },
    (row: any) => { row.data.extra = true },
    (row: any) => { row.seq0 = Number.MAX_SAFE_INTEGER },
    (row: any) => { row.seq0 = Number.MAX_SAFE_INTEGER; row.data.args = ['', '']; row.data.dt = [0] },
    (row: any) => { row.time0 = Number.MAX_SAFE_INTEGER },
    (row: any) => { row.data.dt = [0] },
    (row: any) => { row.data.dt = [0, 0.5] },
    (row: any) => { row.data.args = [] },
    (row: any) => { row.data.args[0] = {} },
    (row: any) => { row.data.index = -1 },
  ].flatMap(mutate => [undefined, null].map(name => ({ mutate, name }))))('refuses packed expansion when fields or coordinates would lose information (%#)', async ({ mutate, name }) => {
    const root = await directory()
    const rows = toolStreamFixture(true, '', name).trim().split('\n').map(line => JSON.parse(line))
    mutate(rows.find(row => row.type === 'tool-call-chunks'))
    const text = rows.map(row => JSON.stringify(row)).join('\n') + '\n'
    const input = join(root, 'input')
    await writeFile(input, text)
    const result = cli('--input', input, '--output', join(root, 'output'))
    expect(result.status).toBe(1)
    expect(JSON.parse(result.stdout)).toMatchObject({ expandedToolChunkRows: 0, blockers: [{ code: 'unsupported-packed-tool-chunks' }] })
    expect(await readFile(input, 'utf8')).toBe(text)
    expect(await readdir(root)).toEqual(['input'])
  })

  it('rejects duplicate packed fields and bounds expanded output before publication', async () => {
    const root = await directory()
    const input = join(root, 'input')
    const output = join(root, 'output')
    await writeFile(input, toolStreamFixture(true, '').replace('"id":"","dt"', '"id":"other","id":"","dt"'))
    const duplicate = cli('--input', input, '--output', output)
    expect(duplicate.status).toBe(1)
    expect(duplicate.stderr).toContain('Duplicate JSON property')
    const rows = toolStreamFixture(true, '').trim().split('\n').map(line => JSON.parse(line))
    const packed = rows.find(row => row.type === 'tool-call-chunks')
    packed.data.args = Array(1_000_000).fill('')
    packed.data.dt = Array(999_999).fill(0)
    await writeFile(input, rows.map(row => JSON.stringify(row)).join('\n') + '\n')
    const oversized = cli('--input', input, '--output', output)
    expect(oversized.status).toBe(1)
    expect(oversized.stderr).toContain('Expanded Session exceeds the 128 MiB repair limit')
    expect(await readdir(root)).toEqual(['input'])
  })

  it('refuses negative zero packed coordinates without normalizing their identity', async () => {
    const root = await directory()
    const input = join(root, 'input')
    const text = toolStreamFixture(true, '').replace('"seq0":2', '"seq0":-0')
    await writeFile(input, text)
    const result = cli('--input', input, '--output', join(root, 'output'))
    expect(result.status).toBe(1)
    expect(JSON.parse(result.stdout).blockers[0].code).toBe('unsupported-packed-tool-chunks')
    expect(await readFile(input, 'utf8')).toBe(text)
    expect(await readdir(root)).toEqual(['input'])
  })

  it('reports all known blocker classes with bounded location samples and publishes no partial repair', async () => {
    const root = await directory()
    const rows = descriptorFixture({ ...descriptorV2, extra: true }).replace(runtimeExpected, runtimeFixture).trim().split('\n').map(line => JSON.parse(line))
    for (let index = 0; index < 15; index++) rows.push({ type: 'assistant/chunk', seq: 18 + index, time: 1789030660071,
      data: { turn: 1, step: 1, chunk: { type: 'tool-call-delta', index: 0, id: '', name: null, argumentsDelta: 'private fixture payload', extra: 'private fixture payload' } } })
    rows.find(row => row.type === 'assistant/chunk' && row.data.chunk.name === null).seq = { malformed: 'private fixture payload' }
    rows.push({ type: 'tool/call', seq: 33, time: 1789030660071, data: { turn: 1, step: 1, callId: '', name: 'synthetic_lookup', arguments: '{}' } })
    const text = rows.map(row => JSON.stringify(row)).join('\n') + '\n'
    const input = join(root, 'input')
    await writeFile(input, text)
    const result = cli('--input', input, '--output', join(root, 'output'))
    expect(result.status).toBe(1)
    const report = JSON.parse(result.stdout)
    expect(report.mode).toBe('refused')
    expect(report.repairedMessages).toBe(3)
    expect(report.blockers.map((issue: { code: string; occurrences: number }) => [issue.code, issue.occurrences])).toEqual([
      ['unsupported-subagent-descriptor', 1], ['null-stream-tool-name', 15], ['empty-durable-tool-id', 1],
    ])
    expect(report.blockers[1].samples).toHaveLength(10)
    expect(result.stdout).not.toContain('private fixture payload')
    expect(await readFile(input, 'utf8')).toBe(text)
    expect(await readdir(root)).toEqual(['input'])
  })

  it.each([
    { version: 2, mode: 'one-shot', provider: 'in-process' },
    { version: 2, mode: 'one-shot', provider: 'in-process', label: '' },
    { version: 2, mode: 'continuable', provider: 'in-process', label: 'Synthetic child' },
    descriptorV2,
  ])('promotes only the equivalent declared v2 descriptor composition (%#)', async descriptor => {
    const root = await directory()
    const text = descriptorFixture(descriptor)
    const input = join(root, 'input')
    const output = join(root, 'output')
    await writeFile(input, text)
    await expect(publishedLoad(join(root, 'before'), text)).rejects.toThrow('uses unsupported descriptor version 2')
    const result = cli('--input', input, '--output', output)
    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({ repairedMessages: 0, repairedDescriptors: 1, blockers: [] })
    const after = await readFile(output, 'utf8')
    expect(after).toBe(text.replace('"version":2', '"version":3'))
    expect(await readFile(input, 'utf8')).toBe(text)
    const migrated = await publishedLoad(join(root, 'after'), after, 'write')
    const restored = migrated.events.find(event => event.type === 'subagent/descriptor')!
    expect(restored.data).toEqual({ ...descriptor, version: 3 })
    expect(() => assertReleasedEventPayload(JSON.parse(JSON.stringify(restored)), 0)).not.toThrow()
    expect(JSON.parse(cli('--input', output).stdout)).toMatchObject({ repairedDescriptors: 0, inputSha256: JSON.parse(result.stdout).outputSha256 })
  })

  it.each([
    { ...descriptorV2, version: 1 },
    { ...descriptorV2, agentReasoningEffort: 'high' },
    { ...descriptorV2, extra: true },
    { ...descriptorV2, provider: '' },
    { ...descriptorV2, label: '' },
    { ...descriptorV2, agentProvider: '' },
    { ...descriptorV2, agentModel: undefined },
    { ...descriptorV2, persona: '' },
    { ...descriptorV2, toolFilter: {} },
    { ...descriptorV2, toolFilter: { allow: [''] } },
    { ...descriptorV2, toolFilter: { deny: [], extra: [] } },
    { version: 2, mode: 'one-shot', provider: 'in-process', persona: 'synthetic' },
  ])('diagnoses unsupported descriptor composition and refuses a copy (%#)', async descriptor => {
    const root = await directory()
    const text = descriptorFixture(descriptor)
    const input = join(root, 'input')
    const output = join(root, 'output')
    await writeFile(input, text)
    const preview = cli('--input', input)
    expect(preview.status).toBe(1)
    expect(JSON.parse(preview.stdout)).toMatchObject({ mode: 'preview', migrationValidated: false, outputSha256: null, repairedDescriptors: 0,
      blockers: [{ code: 'unsupported-subagent-descriptor', occurrences: 1, samples: [{ line: 19, type: 'subagent/descriptor', seq: 17, path: 'data.version' }] }] })
    const result = cli('--input', input, '--output', output)
    expect(result.status).toBe(1)
    expect(JSON.parse(result.stdout).mode).toBe('refused')
    expect(result.stderr).toContain('no output written')
    expect(await readdir(root)).toEqual(['input'])
    expect(await readFile(input, 'utf8')).toBe(text)
  })

  it('refuses duplicate keys along the descriptor and nested toolFilter paths', async () => {
    const root = await directory()
    for (const text of [
      descriptorFixture(descriptorV2).replace('"version":2', '"version":1,"version":2'),
      descriptorFixture(descriptorV2).replace('"allow":["synthetic_lookup"]', '"allow":["other"],"allow":["synthetic_lookup"]'),
    ]) {
      const input = join(root, 'input')
      await writeFile(input, text)
      const result = cli('--input', input, '--output', join(root, 'output'))
      expect(result.status).toBe(1)
      expect(result.stderr).toContain('Duplicate JSON property')
      expect(await readdir(root)).toEqual(['input'])
      expect(await readFile(input, 'utf8')).toBe(text)
    }
  })

  it.each(['none', 'zstd'] as const)('repairs all three historical Mnemon summaries and retains another plugin (%s)', async compression => {
    const root = await directory()
    const input = join(root, 'input')
    const output = join(root, 'output')
    const before = compression === 'none' ? Buffer.from(runtimeFixture) : compressFrames(runtimeFixture)
    await writeFile(input, before)
    const preview = cli('--input', input)
    expect(preview.status).toBe(0)
    expect(JSON.parse(preview.stdout)).toMatchObject({ mode: 'preview', repairedMessages: 3 })
    expect(await readdir(root)).toEqual(['input'])
    expect(cli('--input', input, '--output', output).status).toBe(0)
    const after = await readFile(output)
    expect(compression === 'none' ? after.toString() : decompressFrames(after)).toBe(runtimeExpected)
    expect(await readFile(input)).toEqual(before)
    const repeated = cli('--input', output, '--output', join(root, 'idempotent'))
    expect(repeated.status).toBe(0)
    expect(JSON.parse(repeated.stdout).repairedMessages).toBe(0)
    expect(await readFile(join(root, 'idempotent'))).toEqual(after)
  })
  it('previews and exclusively writes the two known repairs without changing the original', async () => {
    const root = await directory()
    const input = join(root, 'session.jsonl')
    const output = join(root, 'repaired.jsonl')
    await writeFile(input, fixture)
    const preview = cli('--input', input)
    expect(preview.status).toBe(0)
    expect(JSON.parse(preview.stdout)).toMatchObject({ mode: 'preview', repairedMessages: 2, originalPreserved: true })
    expect(await readdir(root)).toEqual(['session.jsonl'])
    const copied = cli('--input', input, '--output', output)
    expect(copied.status).toBe(0)
    expect(JSON.parse(copied.stdout)).toMatchObject({ ...JSON.parse(preview.stdout), mode: 'copy' })
    expect(await readFile(input, 'utf8')).toBe(fixture)
    expect(await readFile(output, 'utf8')).toBe(expected)
    if (process.platform !== 'win32') expect((await stat(output)).mode & 0o777).toBe(0o600)
    for (const target of [input, output]) expect(cli('--input', input, '--output', target).status).toBe(1)
    await link(input, join(root, 'hard-link'))
    expect(cli('--input', input, '--output', join(root, 'hard-link')).status).toBe(1)
    if (process.platform !== 'win32') {
      await symlink(input, join(root, 'symlink'))
      expect(cli('--input', input, '--output', join(root, 'symlink')).status).toBe(1)
    }
    expect(await readFile(input, 'utf8')).toBe(fixture)
    expect((await readdir(root)).some(name => name.startsWith('.mnemon-repair-'))).toBe(false)
  })

  it.each(['first', 'middle', 'last'] as const)('preserves every other byte when summary is the %s source property', async position => {
    const root = await directory()
    const input = join(root, 'input')
    const output = join(root, 'output')
    const keys = ['"kind": "plugin"', '"plugin":"dsh-mnemon"', '"form":"recall"']
    keys.splice(position === 'first' ? 0 : position === 'middle' ? 1 : 3, 0, '"summary" : "Memory View snapshot"')
    const row = '  { "type":"user/message", "seq":9007199254740993, "data":{"source":{ ' + keys.join(',  ') + ' }, "content":[{"text":"\\\"summary\\\": {} [] \\u4e2d", "n":1.2300e+42}]}}\r\n'
    const text = fixture.split('\n')[0] + '\r\n' + row
    await writeFile(input, text)
    expect(cli('--input', input, '--output', output).status).toBe(0)
    const result = await readFile(output, 'utf8')
    const removed = position === 'last' ? ',  "summary" : "Memory View snapshot"' : '"summary" : "Memory View snapshot",  '
    expect(result).toBe(text.replace(removed, ''))
  })

  it('leaves clean, unrelated and unknown metadata byte-identical', async () => {
    const root = await directory()
    for (const [index, text] of [expected, fixture.replaceAll('dsh-mnemon', 'other-plugin'), fixture.replaceAll('summary', 'differentField'), fixture.replaceAll('Memory View snapshot', 'unrecognized summary').replaceAll('Optional memory recall and remember reminder', 'another summary'), fixture.replaceAll('"form":"recall"', '"form":"notice"').replaceAll('"form":"instructions"', '"form":"notice"')].entries()) {
      const input = join(root, `${index}.jsonl`)
      const output = join(root, `${index}-copy.jsonl`)
      await writeFile(input, text)
      const result = cli('--input', input, '--output', output)
      expect(result.status).toBe(0)
      expect(JSON.parse(result.stdout).repairedMessages).toBe(0)
      expect(await readFile(output, 'utf8')).toBe(text)
    }
  })

  it('handles actual concatenated Zstandard frames and preserves a clean compressed artifact', async () => {
    const root = await directory()
    const input = join(root, 'session.jsonl.zstd')
    const output = join(root, 'copy.jsonl.zstd')
    const compressed = compressFrames(fixture)
    await writeFile(input, compressed)
    const result = cli('--input', input, '--output', output)
    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toMatchObject({ encoding: 'zstd', repairedMessages: 2 })
    expect(decompressFrames(await readFile(output))).toBe(expected)
    expect(await readFile(input)).toEqual(compressed)
    const clean = cli('--input', output, '--output', join(root, 'clean.zstd'))
    expect(clean.status).toBe(0)
    expect(await readFile(join(root, 'clean.zstd'))).toEqual(await readFile(output))
    const corrupt = Buffer.from(compressed)
    corrupt[corrupt.length - 1] = corrupt[corrupt.length - 1]! ^ 0xff
    await writeFile(input, corrupt)
    expect(cli('--input', input).status).toBe(1)
    await writeFile(input, compressed.subarray(0, compressed.length - 5))
    expect(cli('--input', input).status).toBe(1)
  })

  it.each([
    fixture.replace('"version":0', '"version":3'),
    fixture.replace(/"createdAt":\d+/, '"createdAt":-1'),
    fixture.slice(0, -1),
    fixture + '{invalid}\n',
    fixture.replace('"plugin":"dsh-mnemon"', '"plugin":"other","plugin":"dsh-mnemon"'),
    Buffer.concat([Buffer.from(fixture), Buffer.from([0xff, 0x0a])]),
  ])('refuses invalid or ambiguous input without publishing output (%#)', async text => {
    const root = await directory()
    const input = join(root, 'input')
    const output = join(root, 'output')
    await writeFile(input, text)
    const before = await readFile(input)
    expect(cli('--input', input, '--output', output).status).toBe(1)
    expect(await readFile(input)).toEqual(before)
    expect(await readdir(root)).toEqual(['input'])
  })

  it('bounds decompressed input before parsing', async () => {
    const root = await directory()
    const input = join(root, 'oversized.zstd')
    await writeFile(input, zstdCompressSync(Buffer.alloc(128 * 1024 * 1024 + 1, 0x20)))
    expect(cli('--input', input).status).toBe(1)
  })

  it.runIf(process.platform !== 'win32')('runs through the executable symlink used by npm global installs', async () => {
    const root = await directory()
    const executable = join(root, 'dsh-mnemon-repair-session')
    await symlink(command, executable)
    const help = spawnSync(executable, ['--help'], { encoding: 'utf8' })
    expect(help.status).toBe(0)
    expect(help.stdout).toContain('Usage: dsh-mnemon-repair-session')
    const input = join(root, 'session.jsonl')
    await writeFile(input, fixture)
    const preview = spawnSync(executable, ['--input', input], { encoding: 'utf8' })
    expect(preview.status).toBe(0)
    expect(JSON.parse(preview.stdout)).toMatchObject({ mode: 'preview', repairedMessages: 2 })
    expect(await readFile(input, 'utf8')).toBe(fixture)
  })

  it('rejects incomplete or duplicate CLI options', () => {
    expect(cli('--help').status).toBe(0)
    for (const args of [[], ['--input'], ['--wat', 'file'], ['--input', 'file', '--input', 'file']]) expect(cli(...args).status).toBe(1)
  })

  it.each([
    { name: 'original Mnemon summaries', fixture, expected },
    { name: 'runtime summary and independent plugin', fixture: runtimeFixture, expected: runtimeExpected },
  ].flatMap(value => (['none', 'zstd'] as const).map(compression => ({ ...value, compression }))))('unblocks the real published DSH migration and cold reopen ($name, $compression)', async ({ fixture, expected, compression }) => {
    const root = await directory()
    const reference = (await publishedLoad(join(root, 'reference'), expected)).events
      .filter(event => event.type === 'user/message').map(event => event.data)
    const { id, cwd }: { id: string; cwd: string } = JSON.parse(fixture.split('\n')[0]!)
    const suffix = compression === 'none' ? '.jsonl' : '.jsonl.zstd'
    const sessionDir = join(root, `--${cwd.slice(1).replaceAll('/', '-')}--`, id)
    await mkdir(sessionDir, { recursive: true })
    const original = join(sessionDir, `session${suffix}`)
    const before = compression === 'none' ? Buffer.from(fixture) : compressFrames(fixture)
    await writeFile(original, before)
    async function backend() {
      const ctx = new Context()
      await ctx.plugin(JsonlSessionPersistence, { root, compression })
      return ctx
    }
    let ctx = await backend()
    try {
      await expect(ctx.sessionPersistence.open(SessionId(id), 'read')).rejects.toThrow('source summary requires notice form')
    } finally { await ctx.fiber.dispose() }
    const copy = join(root, `repaired${suffix}`)
    expect(cli('--input', original, '--output', copy).status).toBe(0)
    expect(await readFile(original)).toEqual(before)
    // Explicit operator replacement in this disposable fixture only. The CLI
    // leaves this decision to the owner; the original backup stays intact.
    await writeFile(join(root, `backup${suffix}`), before)
    await writeFile(original, await readFile(copy))
    const repairedV0 = await readFile(original)
    ctx = await backend()
    try {
      const handle = await ctx.sessionPersistence.open(SessionId(id), 'write')
      const restored = await handle.read()
      expect(restored.events.filter(event => event.type === 'user/message').map(event => event.data)).toEqual(reference)
      await handle.close()
      // DSH writes the migrated generation beside the untouched source file.
      expect((await readdir(sessionDir)).some(name => /^session\.v\d+\./u.test(name))).toBe(true)
    } finally { await ctx.fiber.dispose() }
    ctx = await backend()
    try {
      const handle = await ctx.sessionPersistence.open(SessionId(id), 'read')
      expect((await handle.read()).events.filter(event => event.type === 'user/message').map(event => event.data)).toEqual(reference)
      await handle.close()
    } finally { await ctx.fiber.dispose() }
    expect(await readFile(original)).toEqual(repairedV0)
    expect(await readFile(join(root, `backup${suffix}`))).toEqual(before)
  })
})
