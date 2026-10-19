import { readFileSync } from 'node:fs'
import { afterEach, describe, expect, it, vi } from 'vitest'

const workflow = readFileSync(new URL('../.github/workflows/issue-template-enforcer.yml', import.meta.url), 'utf8')
const script = workflow.match(/^          script: \|\n((?: {12}.*\n|\n)*)/m)?.[1]
if (!script) throw new Error('Issue template workflow must contain an inline script')
const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor
const runWorkflow = new AsyncFunction('context', 'github', script.replace(/^ {12}/gm, ''))

const baseSections = [
  ['提交前查重 / Duplicate check', '- [x] I searched open and closed issues.'],
  ['涉及区域 / Affected area', '运行时记忆 / Runtime Memory'],
  ['Issue 类型 / Issue type', 'Bug 报告 / Bug report'],
  ['摘要 / Summary', 'Archiving fails after runtime memory reaches its capacity.'],
  ['预期结果 / Expected outcome', 'Archive the entries and complete the write.'],
  ['详情或复现步骤 / Details or reproduction steps', 'Fill runtime memory and add another entry. The write fails.'],
  ['环境信息 / Environment', 'macOS, Node.js 24, two active Memory Spaces.'],
]
const body = (sections) => sections.map(([heading, value]) => `### ${heading}\n\n${value}`).join('\n\n')
const bugBody = body(baseSections)
const questionBody = bugBody.replace('Bug 报告 / Bug report', '问题 / Question')
const issue = (overrides = {}) => ({ number: 235, state: 'open', body: bugBody, labels: [{ name: 'bug' }], ...overrides })
const missingLabel = () => issue({ labels: [] })
const incomplete = () => issue({ body: bugBody.replace(baseSections[6][1], '_No response_') })
const marker = '<!-- dsh-mnemon:issue-template-check -->'
const botComment = (body) => ({ id: 123, body, user: { login: 'github-actions[bot]', type: 'Bot' } })
const checks = []

afterEach(() => {
  for (const check of checks.splice(0)) {
    expect(check.update, 'template checks must never change issue state').not.toHaveBeenCalled()
  }
})

function harness(eventIssue, { snapshots = [eventIssue], comments = [], action = 'opened' } = {}) {
  let reads = 0
  const get = vi.fn(async () => {
    const snapshot = snapshots[Math.min(reads++, snapshots.length - 1)]
    if (snapshot instanceof Error) throw snapshot
    return { data: structuredClone(snapshot) }
  })
  const update = vi.fn(async () => ({ data: {} }))
  const addLabels = vi.fn(async () => ({ data: [{ name: 'bug' }] }))
  const createComment = vi.fn(async () => ({ data: {} }))
  const updateComment = vi.fn(async () => ({ data: {} }))
  const listComments = vi.fn(async () => ({ data: comments }))
  const paginate = vi.fn(async (method, params) => (await method(params)).data)
  const context = { repo: { owner: 'omdsh-dev', repo: 'dsh-mnemon' }, payload: { issue: eventIssue, action } }
  const check = {
    get, update, addLabels, createComment, updateComment, listComments, paginate,
    run: () => runWorkflow(context, { paginate, rest: { issues: { get, update, addLabels, createComment, updateComment, listComments } } }),
  }
  checks.push(check)
  return check
}

describe('advisory issue template checks', () => {
  it('accepts a report without separate evidence, smoke tests, code references or a patch', async () => {
    const check = harness(issue())
    await check.run()
    expect(check.createComment).not.toHaveBeenCalled()
    expect(check.addLabels).not.toHaveBeenCalled()
  })

  it.each([
    ['JSON logs (#210)', '```json\n{"code":"CONTEXT_WINDOW_EXCEEDED","n_prompt_tokens":145508,"n_ctx":98304}\n```'],
    ['plain-text errors (#217)', 'Error: Multiple memory entries contain "X"; use a unique substring.'],
    ['short errors', 'Error: X'],
    ['other code block languages', '```typescript\nthrow new Error("Failed to archive")\n```'],
  ])('accepts %s as supporting information', async (_name, evidence) => {
    const check = harness(issue({ body: body([...baseSections, ['Bug 证据 / Bug evidence', evidence]]) }))
    await check.run()
    expect(check.createComment).not.toHaveBeenCalled()
  })

  it('accepts evidence in the reproduction steps without repeating it in another section (#211)', async () => {
    const reproduction = baseSections.map(([heading, value]) => [heading, heading === '详情或复现步骤 / Details or reproduction steps'
      ? value + '\n\n```text\naoci_overview: 6, glob: 1, read: 1; overview body_bytes: 112388\n```' : value])
    const check = harness(issue({ body: body(reproduction) }))
    await check.run()
    expect(check.createComment).not.toHaveBeenCalled()
  })

  it('adds the bug label for a report submitted without label permissions', async () => {
    const check = harness(issue({ labels: [{ name: 'help wanted' }] }))
    await check.run()
    expect(check.addLabels).toHaveBeenCalledExactlyOnceWith({
      owner: 'omdsh-dev', repo: 'dsh-mnemon', issue_number: 235, labels: ['bug'],
    })
    expect(check.createComment).not.toHaveBeenCalled()
  })

  it.each(['before the job starts', 'during the job'])('uses a bug label added %s (#235)', async (timing) => {
    const check = harness(missingLabel(), { snapshots: timing === 'during the job' ? [missingLabel(), issue()] : [issue()] })
    await check.run()
    expect(check.addLabels).not.toHaveBeenCalled()
    expect(check.createComment).not.toHaveBeenCalled()
  })

  it('uses the latest report type when the author changes a bug to a question', async () => {
    const check = harness(missingLabel(), { snapshots: [missingLabel(), issue({ body: questionBody, labels: [] })] })
    await check.run()
    expect(check.addLabels).not.toHaveBeenCalled()
    expect(check.createComment).not.toHaveBeenCalled()
  })

  it('accepts required information filled in while comments are being loaded', async () => {
    const check = harness(incomplete(), { snapshots: [incomplete(), issue()] })
    await check.run()
    expect(check.createComment).not.toHaveBeenCalled()
  })

  it('rechecks required information after adding the bug label', async () => {
    const missing = { ...incomplete(), labels: [] }
    const check = harness(missing, { snapshots: [missing, missing, issue()] })
    await check.run()
    expect(check.addLabels).toHaveBeenCalledTimes(1)
    expect(check.createComment).not.toHaveBeenCalled()
  })

  it.each(['before the job starts', 'during the job', 'while adding the label'])('skips an issue closed %s', async (timing) => {
    const missing = { ...incomplete(), labels: [] }
    const closed = { ...missing, state: 'closed' }
    const snapshots = timing === 'before the job starts' ? [closed]
      : timing === 'during the job' ? [missing, closed] : [missing, missing, closed]
    const check = harness(missing, { snapshots })
    await check.run()
    expect(check.createComment).not.toHaveBeenCalled()
    expect(check.updateComment).not.toHaveBeenCalled()
    if (timing !== 'while adding the label') expect(check.addLabels).not.toHaveBeenCalled()
  })

  it.each(['opened', 'reopened', 'edited'])('requests missing information without closing an %s issue', async (action) => {
    const check = harness(incomplete(), { action })
    await check.run()
    expect(check.createComment).toHaveBeenCalledTimes(1)
    const comment = check.createComment.mock.calls[0][0].body
    expect(comment).toContain('环境信息 / Environment')
    expect(comment).toContain('The issue remains open')
    expect(comment).toContain(marker)
  })

  it('requests duplicate-search confirmation without closing the report', async () => {
    const check = harness(issue({ body: bugBody.replace('- [x]', '- [ ]') }))
    await check.run()
    expect(check.createComment.mock.calls[0][0].body).toContain('confirm that you searched')
  })

  it('handles an empty API-created report without assuming it is a bug', async () => {
    const check = harness(issue({ body: null, labels: [] }))
    await check.run()
    expect(check.addLabels).not.toHaveBeenCalled()
    expect(check.createComment.mock.calls[0][0].body).toContain('摘要 / Summary')
  })

  it('updates its existing reminder instead of posting another one', async () => {
    const check = harness(incomplete(), { comments: [botComment(marker + '\nOld reminder')] })
    await check.run()
    expect(check.createComment).not.toHaveBeenCalled()
    expect(check.updateComment).toHaveBeenCalledExactlyOnceWith({
      owner: 'omdsh-dev', repo: 'dsh-mnemon', comment_id: 123, body: expect.stringContaining('环境信息 / Environment'),
    })
  })

  it('marks its reminder resolved after the author supplies the missing information', async () => {
    const check = harness(issue(), { comments: [botComment(marker + '\nOld reminder')], action: 'edited' })
    await check.run()
    expect(check.createComment).not.toHaveBeenCalled()
    expect(check.updateComment.mock.calls[0][0].body).toContain('Required report information is present')
  })

  it('leaves an unchanged reminder alone when an issue is reopened again', async () => {
    const first = harness(incomplete())
    await first.run()
    const comment = botComment(first.createComment.mock.calls[0][0].body)
    const next = harness(incomplete(), { comments: [comment], action: 'reopened' })
    await next.run()
    expect(next.createComment).not.toHaveBeenCalled()
    expect(next.updateComment).not.toHaveBeenCalled()
  })

  it.each([
    { login: 'reporter', type: 'User' },
    { login: 'other-automation[bot]', type: 'Bot' },
  ])('does not edit a marker copied by $login', async (user) => {
    const check = harness(incomplete(), { comments: [{ ...botComment(marker), user }] })
    await check.run()
    expect(check.updateComment).not.toHaveBeenCalled()
    expect(check.createComment).toHaveBeenCalledTimes(1)
  })

  it.each(['initial read', 'latest read'])('makes no changes when the %s fails', async (timing) => {
    const failure = new Error('GitHub is unavailable')
    const check = harness(incomplete(), { snapshots: timing === 'initial read' ? [failure] : [incomplete(), failure] })
    await expect(check.run()).rejects.toThrow('GitHub is unavailable')
    expect(check.addLabels).not.toHaveBeenCalled()
    expect(check.createComment).not.toHaveBeenCalled()
    expect(check.updateComment).not.toHaveBeenCalled()
  })

  it('does not post duplicate advice when comment lookup fails', async () => {
    const check = harness(incomplete())
    check.listComments.mockRejectedValueOnce(new Error('Comment lookup failed'))
    await expect(check.run()).rejects.toThrow('Comment lookup failed')
    expect(check.createComment).not.toHaveBeenCalled()
    expect(check.updateComment).not.toHaveBeenCalled()
  })

  it('reports label API failures without closing or blaming the reporter', async () => {
    const check = harness(missingLabel())
    check.addLabels.mockRejectedValueOnce(new Error('Label request failed'))
    await expect(check.run()).rejects.toThrow('Label request failed')
    expect(check.createComment).not.toHaveBeenCalled()
  })

  it.each([undefined, issue({ pull_request: {} })])('ignores events without a regular issue', async (eventIssue) => {
    const check = harness(eventIssue)
    await check.run()
    expect(check.get).not.toHaveBeenCalled()
    expect(check.addLabels).not.toHaveBeenCalled()
    expect(check.createComment).not.toHaveBeenCalled()
  })
})
