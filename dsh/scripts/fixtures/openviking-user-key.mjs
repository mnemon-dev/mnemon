// Deterministic user-key protocol fixture for issue 271, not a cloud account.
import { createServer } from 'node:http'

export function openVikingUserKeyFixture(identities) {
  const files = new Map()
  const requests = []
  const state = { denyData: false }
  const server = createServer(async (request, response) => {
    let raw = ''
    for await (const chunk of request) raw += chunk
    const body = JSON.parse(raw || '{}')
    const url = new URL(request.url, 'http://fixture.local')
    const path = url.pathname.replace(/^\/openviking/u, '')
    const identity = identities.get(String(request.headers.authorization ?? '').replace(/^Bearer /u, ''))
    const identityHeaders = request.headers['x-openviking-account'] !== undefined || request.headers['x-openviking-user'] !== undefined
    const root = `viking://user/${identity?.user}/memories`
    const uri = body.uri ?? body.target_uri ?? url.searchParams.get('uri')
    const own = typeof uri === 'string' && (uri === root || uri.startsWith(root + '/'))
    const key = value => `${identity?.account}:${value}`
    const send = (status, result) => {
      response.writeHead(status, { 'content-type': 'application/json' })
      response.end(JSON.stringify(status === 200 ? { status: 'ok', result } : { status: 'error', error: { message: result } }))
    }
    // Credentials and request headers are deliberately omitted from evidence.
    requests.push({ method: request.method, path, account: identity?.account, user: identity?.user, identityHeaders, uri })
    if (path === '/__fixture') return send(200, { fixture: 'deterministic-openviking-user-key', requests, files: [...files.values()] })
    if (path.startsWith('/api/v1/admin/')) return send(403, 'API is currently unavailable due to access restrictions')
    if (path === '/health') return send(200, { healthy: true })
    if (!identity) return send(401, 'Synthetic user key is invalid')
    if (identityHeaders) return send(403, 'Identity headers are not accepted in api_key mode')
    if (state.denyData || !own) return send(403, 'Synthetic user key cannot access this memory namespace')
    if (path === '/api/v1/fs/ls') return send(200, [...files.values()]
      .filter(file => file.account === identity.account && file.uri.startsWith(root + '/'))
      .map(file => ({ uri: file.uri, isDir: false })))
    if (path === '/api/v1/search/find') return send(200, { memories: [...files.values()]
      .filter(file => file.account === identity.account && file.uri.startsWith(root + '/') && file.content.includes(body.query))
      .map(file => ({ uri: file.uri, score: 0.99 })) })
    if (path === '/api/v1/content/read') {
      const file = files.get(key(uri))
      return file ? send(200, file.content) : send(404, 'Synthetic memory is missing')
    }
    if (path === '/api/v1/content/write') {
      if (body.mode !== 'create' || files.has(key(uri))) return send(409, 'Synthetic memory already exists')
      files.set(key(uri), { account: identity.account, uri, content: body.content })
      return send(200, { uri, content_updated: true, written_bytes: Buffer.byteLength(body.content), mode: 'create', context_type: 'memory', vector_status: 'complete', semantic_status: 'skipped' })
    }
    if (path === '/api/v1/fs' && request.method === 'DELETE') return send(200, { uri, estimated_deleted_count: Number(files.delete(key(uri))) })
    send(404, 'Synthetic route is unavailable')
  })
  return { server, files, requests, state }
}
