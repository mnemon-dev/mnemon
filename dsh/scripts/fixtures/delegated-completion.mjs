import assert from 'node:assert/strict'

/** Read fixture completion instructions; the stable tool is also visible on parent requests. */
export function delegatedCompletion(request) {
  const system = (request.messages ?? []).filter(message => message.role === 'system').map(message =>
    typeof message.content === 'string' ? message.content : (message.content ?? []).map(block => block.text ?? '').join('\n')).join('\n')
  const name = system.match(/Completion protocol: call `([^`]+)`/u)?.[1]
  if (name === undefined) return undefined
  const tool = (request.tools ?? []).find(tool => tool.function?.name === name)?.function
  assert(tool, 'completion tool must be in the child allowlist')
  const requestId = system.match(/requestId `([^`]+)`/u)?.[1]
  const schema = requestId === undefined ? tool.parameters : JSON.parse(system.match(/matching this JSON schema:\n([^\n]+)/u)?.[1] ?? 'null')
  assert(schema, 'completion instructions must provide the per-run result schema')
  return { name, key: requestId ?? name, parameters: schema, wrap: result => requestId === undefined ? result : { requestId, result } }
}
