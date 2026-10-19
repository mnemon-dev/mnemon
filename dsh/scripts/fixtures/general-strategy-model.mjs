const text = message => typeof message?.content === 'string' ? message.content : (message?.content ?? []).map(block => block.text ?? '').join('\n')
const parse = message => { try { return JSON.parse(text(message)) } catch { return { error: text(message) } } }

export const generalStrategyFact = 'The general-strategy check build uses pnpm 11.'

/** Script model decisions only; the selected general Strategy, its View and the Runtime write stay real. */
export function generalStrategyModel(report) {
  return request => {
    const messages = request.messages ?? []
    const userIndex = messages.findLastIndex(message => message.role === 'user' && text(message).includes('general-strategy-check'))
    if (userIndex < 0) return 'Send general-strategy-check remember, then general-strategy-check recall in the next turn.'
    // DSH titles the session with a separate request that carries no memory protocol.
    if (text(messages[userIndex]).startsWith('Generate the session title')) return 'General strategy check'
    const system = messages.filter(message => message.role === 'system').map(text).join('\n')
    const prompt = messages.map(text).join('\n')
    const calls = messages.slice(userIndex + 1).filter(message => message.role === 'tool')
    // The fixture wire merges the human line with Mnemon's injected context; only the first line is the request.
    if (text(messages[userIndex]).split('\n')[0].includes('remember')) {
      if (calls.length === 0) {
        report({ event: 'composition', protocol: system.includes('MNEMON GENERAL MEMORY PROTOCOL'),
          admitted: [...system.matchAll(/^- (source:\S+) \(/gmu)].map(match => match[1]),
          namedRuntimeTool: /MNEMON VIEW TOOLS[^\n]*mnemon_runtime_memory/u.test(prompt), routes: prompt.includes('MNEMON VIEW ROUTES') })
        return { name: 'mnemon_runtime_memory', args: { action: 'add', target: 'memory', content: generalStrategyFact } }
      }
      const result = parse(calls.at(-1))
      report({ event: 'write', success: result.success === true, receipt: result.memoryReceipt?.status })
      return result.success === true ? 'Saved through the general Strategy.' : 'The general Strategy write failed: ' + JSON.stringify(result)
    }
    const resident = prompt.includes(generalStrategyFact)
    report({ event: 'recall', resident })
    return resident ? 'The build uses pnpm 11, read from resident memory.' : 'The fact is not in resident memory.'
  }
}
