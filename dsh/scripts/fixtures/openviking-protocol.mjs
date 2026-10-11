// Deterministic protocol fixture for issue 233, not a live OpenViking backend.
// It models a cosmetic extraction update that never stores the submitted text.
import { createServer } from 'node:http'
import { pathToFileURL } from 'node:url'

export function openVikingProtocolFixture() {
  const files = new Map()
  const requests = []
  const state = { vectorStatus: 'complete', failDelete: false }
  const server = createServer(async (request, response) => {
    let raw = ''
    for await (const chunk of request) raw += chunk
    const body = JSON.parse(raw || '{}')
    const url = new URL(request.url, 'http://fixture.local')
    const account = request.headers['x-openviking-account'] || 'default'
    const user = request.headers['x-openviking-user'] || 'default'
    const method = request.method
    requests.push({ method, path: url.pathname, account, user, body })
    const key = uri => `${account}:${uri}`
    const root = `viking://user/${user}/memories`
    const canonical = uri => String(uri || '').replace(/^viking:\/\/user\/memories(?=\/|$)/u, root)
    let result
    let status = 200
    if (url.pathname === '/health') result = { healthy: true }
    else if (url.pathname === '/__fixture') result = { fixture: 'deterministic-openviking-protocol', files: [...files.values()], requests }
    else if (url.pathname === '/api/v1/system/status') result = { initialized: true, user }
    else if (url.pathname === '/api/v1/admin/accounts') result = [{ account_id: 'default' }]
    else if (url.pathname.endsWith('/users')) result = [{ user_id: 'default', display_name: 'Issue 233 protocol fixture' }, { user_id: 'other', display_name: 'Other synthetic namespace' }]
    else if (url.pathname === '/api/v1/sessions') result = { session_id: body.session_id }
    else if (url.pathname.endsWith('/messages')) result = { message_id: 'fixture-message' }
    else if (url.pathname.endsWith('/commit')) result = { task_id: 'fixture-extraction', status: 'accepted' }
    else if (url.pathname === '/api/v1/tasks/fixture-extraction') result = { status: 'completed', result: { memories_extracted: { memory_update: 1 } } }
    else if (url.pathname === '/api/v1/content/write') {
      const uri = canonical(body.uri)
      if (body.mode === 'create' && files.has(key(uri))) status = 409
      else {
        files.set(key(uri), { uri, account, content: body.content })
        result = { uri, root_uri: uri.slice(0, uri.lastIndexOf('/')), context_type: 'memory', mode: body.mode,
          written_bytes: Buffer.byteLength(body.content), content_updated: true, semantic_status: 'skipped',
          vector_status: state.vectorStatus, queue_status: { Embedding: { error_count: state.vectorStatus === 'failed' ? 1 : 0, errors: [] } } }
      }
    } else if (url.pathname === '/api/v1/content/read') {
      result = files.get(key(canonical(url.searchParams.get('uri'))))?.content
      if (result === undefined) status = 404
    } else if (url.pathname === '/api/v1/fs/ls') {
      const requestedRoot = canonical(url.searchParams.get('uri'))
      result = [...files.values()].filter(file => file.account === account && file.uri.startsWith(requestedRoot + '/'))
        .map(file => ({ uri: file.uri, isDir: false, abstract: 'Synthetic summary; the Provider must read the full body.' }))
    } else if (url.pathname === '/api/v1/search/find') {
      const requestedRoot = canonical(body.target_uri)
      result = { memories: [...files.values()].filter(file => file.account === account && file.uri.startsWith(requestedRoot + '/') && file.content.includes(body.query))
        .map(file => ({ uri: file.uri, abstract: 'Synthetic summary', score: 0.99 })) }
    } else if (url.pathname === '/api/v1/fs' && method === 'DELETE') {
      const uri = canonical(url.searchParams.get('uri'))
      if (state.failDelete) status = 503
      else result = { uri, estimated_deleted_count: Number(files.delete(key(uri))) }
    } else status = 404
    response.writeHead(status, { 'content-type': 'application/json' })
    response.end(JSON.stringify(status === 200 ? { status: 'ok', result } : { status: 'error', error: { message: `Synthetic OpenViking HTTP ${status}` } }))
  })
  return { server, files, requests, state }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const fixture = openVikingProtocolFixture()
  fixture.server.listen(Number(process.env.MNEMON_OPENVIKING_FIXTURE_PORT || 19335), '127.0.0.1', () => {
    console.log(`Deterministic OpenViking protocol fixture: http://127.0.0.1:${fixture.server.address().port}`)
  })
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => fixture.server.close())
}
