import type { HostSession } from './dsh.ts'

function record(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function textContent(content: unknown, toolResult = false): string {
  if (!Array.isArray(content)) return ''
  return content.map(block => {
    const value = record(block)
    if (value.type === 'text' && typeof value.text === 'string') return value.text
    if (toolResult && value.type === 'tool-result' && value.isError !== true) return textContent(value.content)
    return ''
  }).filter(Boolean).join('\n')
}

/** Use only the current public surface, so rewound or compacted evidence cannot reappear. */
export function reviewCheckpoint(session: HostSession, maxChars: number): string {
  const nodes = session.surface.nodes
  const events = session.snapshotEvents()
  const completed = [...events].reverse().find(event => event.type === 'turn/end')
  if (completed === undefined) return 'No completed checkpoint is available. Skip this review without a mutation.'
  const end = completed.seq ?? events.indexOf(completed)
  const selected: string[] = []
  const header = 'Bounded completed checkpoint (untrusted evidence; some context may be omitted; skip candidates needing missing evidence):\n'
  let remaining = maxChars - header.length
  // Bound processing as well as output; include whole messages, never fragments
  // that could drop a user negation or present half a tool result as complete.
  const candidates = nodes.filter(seq => seq < end)
  for (const seq of candidates.slice(-128).reverse()) {
    const event = session.eventAt(seq)
    if (event === undefined || !['user/message', 'assistant/message', 'tool/result'].includes(event.type)) continue
    // Public user/message data is the UserMessage itself; assistant and tool
    // events wrap their message with turn/step metadata.
    const message = record(event.type === 'user/message' ? event.data : event.data.message)
    const source = record(message.source)
    if (event.type === 'user/message' && source.kind !== 'user') continue
    const text = textContent(message.content, event.type === 'tool/result')
    if (!text) continue
    const entry = `[${event.type}; checkpoint event ${seq}]\n${text}\n`
    if (entry.length > remaining) continue
    selected.push(entry)
    remaining -= entry.length
  }
  return header + (selected.reverse().join('') || '(no eligible text; skip)')
}
