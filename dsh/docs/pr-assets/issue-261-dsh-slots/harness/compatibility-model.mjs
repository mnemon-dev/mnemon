import assert from 'node:assert/strict';

export const fixtureMemory = 'Issue 261 compatibility: the isolated published DSH cohort uses real Mnemon runtime memory.';
const ready = 'Isolated Issue 261 compatibility fixture ready. Send compatibility-261 to add runtime memory and inspect Mnemon status.';
export const contentText = content => typeof content === 'string' ? content : Array.isArray(content) ? content.filter(block => block.type === 'text' || typeof block.text === 'string').map(block => block.text ?? '').join('\n') : '';
const parseReceipt = content => { const value = contentText(content); try { return JSON.parse(value); } catch { return { error: value }; } };

export function normalizeRequest(request) {
  const messages = request.messages ?? [];
  const system = [contentText(request.system), ...messages.filter(message => message.role === 'system').map(message => contentText(message.content))].filter(Boolean).join('\n');
  const toolNames = (request.tools ?? []).map(tool => tool.function?.name ?? tool.name).filter(Boolean);
  const isMarker = message => message.role === 'user' && (typeof message.content === 'string' ? [message.content] : (message.content ?? []).filter(block => block.type === 'text').map(block => block.text ?? '')).some(value => /^compatibility-261(?:\s|$)/u.test(value.trim()));
  const userIndex = messages.findLastIndex(isMarker);
  const receipts = messages.slice(userIndex + 1).flatMap(message => message.role === 'tool' ? [parseReceipt(message.content)] : message.role === 'user' && Array.isArray(message.content) ? message.content.filter(block => block.type === 'tool_result').map(block => parseReceipt(block.content)) : []);
  return { messages, system, toolNames, userIndex, receipts };
}

export function compatibilityModel(report) {
  let calls = 0;
  const childCounts = new Map();
  return request => {
    calls += 1;
    if (calls > 200) return 'Compatibility fixture request budget reached. Stop this test run.';
    const { system, toolNames: names, userIndex, receipts } = normalizeRequest(request);
    const toolNames = new Set(names);
    // Title requests quote the marker, but have no tool inventory or authority to write memory.
    if (toolNames.size === 0) return /title/iu.test(system) ? 'Issue 261 compatibility' : ready;
    const completionName = system.match(/Completion protocol: call `([^`]+)`/u)?.[1];
    if (completionName !== undefined) {
      assert(toolNames.has(completionName), 'child completion tool is visible');
      const requestId = system.match(/requestId `([^`]+)`/u)?.[1];
      const key = requestId ?? completionName;
      const count = (childCounts.get(key) ?? 0) + 1;
      childCounts.set(key, count);
      if (count > 3) return 'No further maintenance actions are permitted in this bounded compatibility fixture.';
      const result = { action: 'skipped', summary: 'No child maintenance writes in the Issue 261 compatibility fixture.', memoryBodyIds: [] };
      report({ event: 'child-skipped', completionName, requestId, count });
      return { name: completionName, args: requestId === undefined ? result : { requestId, result } };
    }
    if (userIndex < 0) return ready;
    assert(toolNames.has('mnemon_runtime_memory') && toolNames.has('mnemon_status'), 'real Mnemon runtime and status tools are visible');
    if (receipts.length === 0) {
      report({ event: 'runtime-add-requested', tools: [...toolNames].filter(name => name.startsWith('mnemon_')).sort() });
      return { name: 'mnemon_runtime_memory', args: { action: 'add', target: 'memory', content: fixtureMemory, importance: 'normal' } };
    }
    if (receipts.length === 1) {
      const first = receipts[0];
      if (first?.success !== true) {
        report({ event: 'runtime-add-failed', receipt: first });
        return 'Compatibility fixture stopped because the real runtime-memory tool did not report success: ' + JSON.stringify(first);
      }
      report({ event: 'runtime-add-committed', receipt: first });
      return { name: 'mnemon_status', args: {} };
    }
    report({ event: 'compatibility-complete', receipts: receipts.slice(0, 2) });
    return 'Issue 261 compatibility complete: real runtime memory added and Mnemon status inspected.';
  };
}

export function responseProtocol(request, requestPath = '') {
  return requestPath.includes('/anthropic/') || /\/messages(?:\?|$)/u.test(requestPath) || request.system !== undefined || request.tools?.some(tool => tool.input_schema !== undefined) ? 'anthropic' : 'openai';
}

export function sendModelReply(response, request, requestPath, reply, id) {
  const protocol = responseProtocol(request, requestPath);
  const isText = typeof reply === 'string';
  const model = request.model ?? 'issue261-fixture';
  if (protocol === 'anthropic') {
    const message = { id, type: 'message', role: 'assistant', model, content: isText ? [{ type: 'text', text: reply }] : [{ type: 'tool_use', id: `tool-${id}`, name: reply.name, input: reply.args }], stop_reason: isText ? 'end_turn' : 'tool_use', stop_sequence: null, usage: { input_tokens: 100, output_tokens: 20 } };
    if (request.stream === false) { response.writeHead(200, { 'content-type': 'application/json' }); response.end(JSON.stringify(message)); return; }
    response.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
    const event = value => response.write(`event: ${value.type}\ndata: ${JSON.stringify(value)}\n\n`);
    event({ type: 'message_start', message: { ...message, content: [], stop_reason: null, usage: { input_tokens: 100, output_tokens: 0 } } });
    event({ type: 'content_block_start', index: 0, content_block: isText ? { type: 'text', text: '' } : { type: 'tool_use', id: `tool-${id}`, name: reply.name, input: {} } });
    event({ type: 'content_block_delta', index: 0, delta: isText ? { type: 'text_delta', text: reply } : { type: 'input_json_delta', partial_json: JSON.stringify(reply.args) } });
    event({ type: 'content_block_stop', index: 0 });
    event({ type: 'message_delta', delta: { stop_reason: message.stop_reason, stop_sequence: null }, usage: { output_tokens: 20 } });
    event({ type: 'message_stop' });
    response.end();
    return;
  }
  const delta = isText ? { role: 'assistant', content: reply } : { role: 'assistant', tool_calls: [{ index: 0, id: `tool-${id}`, type: 'function', function: { name: reply.name, arguments: JSON.stringify(reply.args) } }] };
  if (request.stream === false) { response.writeHead(200, { 'content-type': 'application/json' }); response.end(JSON.stringify({ id, object: 'chat.completion', model, choices: [{ index: 0, message: delta, finish_reason: isText ? 'stop' : 'tool_calls' }], usage: { prompt_tokens: 100, completion_tokens: 20, total_tokens: 120 } })); return; }
  response.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
  response.write(`data: ${JSON.stringify({ id, object: 'chat.completion.chunk', model, choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`);
  response.write(`data: ${JSON.stringify({ id, object: 'chat.completion.chunk', model, choices: [{ index: 0, delta: {}, finish_reason: isText ? 'stop' : 'tool_calls' }] })}\n\n`);
  response.write(`data: ${JSON.stringify({ id, object: 'chat.completion.chunk', model, choices: [], usage: { prompt_tokens: 100, completion_tokens: 20, total_tokens: 120 } })}\n\n`);
  response.end('data: [DONE]\n\n');
}
