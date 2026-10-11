import assert from 'node:assert/strict'

export const overviewTool = 'mcp__aoci__aoci_overview'
export const overviewChunks = Array.from({ length: 5 }, (_, chunk) => `COMPLETE_OVERVIEW_CHUNK_${chunk + 1}\n` + Array.from({ length: 67 }, (_, index) => `Synthetic module ${chunk * 67 + index + 1}: complete inherited index evidence.`).join('\n'))

/** Independently composed, per-Agent capability; no AOCI server is contacted. */
export const scopedOverviewPlugin = `
export const name = 'review-evidence-fixture'
export const inject = ['agents', 'tools']
export function apply(ctx) {
  const chunks = ${JSON.stringify(overviewChunks)}
  ctx.on('agent/created', ({ agent }) => agent.ctx.tools.register({
    name: ${JSON.stringify(overviewTool)},
    description: 'Read one complete synthetic overview chunk from the isolated fixture.',
    parameters: { type: 'object', properties: { chunk: { type: 'integer', minimum: 0, maximum: 4 } }, required: ['chunk'], additionalProperties: false },
    output: { schema: { type: 'string' }, render: (_args, value) => [{ type: 'text', text: value }] },
    async execute(args) {
      console.log('Review evidence execution: ' + JSON.stringify({ actor: agent.session.header.origin === 'subagent' ? 'child' : 'parent', chunk: args.chunk }))
      return chunks[args.chunk]
    },
  }))
}
`

/** Fixed attempts before and after the fix; only DSH decides whether they execute. */
export function reviewEvidenceModel(report) {
  let parentCalls = 0
  const reviews = new Map()
  return request => {
    const names = (request.tools ?? []).map(tool => tool.function?.name)
    if (!names.length) return 'Inherited overview review'
    // A stable result tool can also be advertised to parents. Only the scoped
    // completion protocol identifies a delegated request, in either protocol.
    const system = (request.messages ?? []).filter(message => message.role === 'system').map(message =>
      typeof message.content === 'string' ? message.content : (message.content ?? []).map(block => block.text ?? '').join('\n')).join('\n')
    const terminal = system.match(/Completion protocol: call `([^`]+)`/u)?.[1]
    const requestId = system.match(/requestId `([^`]+)`/u)?.[1]
    if (terminal !== undefined) {
      const key = requestId ?? terminal
      const stage = reviews.get(key) ?? 0
      reviews.set(key, stage + 1)
      if (stage === 0) {
        const inheritedChunks = overviewChunks.filter(chunk => JSON.stringify(request.messages).includes(JSON.stringify(chunk).slice(1, -1))).length
        assert.equal(inheritedChunks, 5, 'review must inherit all five complete chunks')
        report({ event: 'review-start', inheritedChunks, entries: 335, characters: overviewChunks.join('').length, foreignToolAdvertised: names.includes(overviewTool) })
        return { name: overviewTool, args: { chunk: 0 } }
      }
      if (stage === 1) {
        const receipt = request.messages.findLast(message => message.role === 'tool')
        const content = typeof receipt?.content === 'string' ? receipt.content : JSON.stringify(receipt?.content)
        const executed = content.includes('COMPLETE_OVERVIEW_CHUNK_1')
        assert(executed || content.includes('Mnemon review'), 'foreign attempt must execute or receive the review denial')
        report({ event: 'foreign-attempt-result', executed, denied: !executed })
        return { name: 'mnemon_document_search', args: { query: 'synthetic module overview', limit: 1 } }
      }
      report({ event: 'review-finish', action: 'skipped', boundedDocumentSearch: true })
      const result = { summary: 'Complete inherited overview reviewed; no durable new assertion.', action: 'skipped', memoryBodyIds: [], documentIds: [] }
      return { name: terminal, args: requestId === undefined ? result : { requestId, result } }
    }
    if (parentCalls < 5) return { name: overviewTool, args: { chunk: parentCalls++ } }
    report({ event: 'parent-complete', overviewReads: 5 })
    return 'All 335 synthetic module entries are present in five complete overview chunks. The checkpoint is ready for review; there is no new durable user preference.'
  }
}
