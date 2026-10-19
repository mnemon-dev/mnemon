#!/usr/bin/env node

const requests = [
  ['OpenViking', 'http://127.0.0.1:1933/health', { headers: { Authorization: `Bearer ${process.env.OPENVIKING_ROOT_API_KEY ?? 'dsh-provider-lab-local-only'}`, 'X-OpenViking-Account': 'dsh-lab', 'X-OpenViking-User': 'demo-user' } }],
  ['Honcho', 'http://127.0.0.1:18000/health'],
  ['Mem0', 'http://127.0.0.1:18888/openapi.json'],
  ['Hindsight', 'http://127.0.0.1:18889/health'],
  ['RetainDB', 'http://127.0.0.1:18990/health'],
  ['Supermemory', 'http://127.0.0.1:18787/'],
]

const results = await Promise.all(requests.map(async ([name, url, init]) => {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 5_000)
  try {
    const response = await fetch(url, { ...(init ?? {}), signal: controller.signal })
    const body = (await response.text()).replace(/\s+/gu, ' ').slice(0, 160)
    return { name, url, healthy: response.ok, status: response.status, body }
  } catch (error) {
    return { name, url, healthy: false, status: 0, body: error instanceof Error ? error.message : String(error) }
  } finally {
    clearTimeout(timer)
  }
}))

for (const result of results) {
  const signal = result.healthy ? 'READY' : 'DOWN '
  console.log(`${signal}  ${result.name.padEnd(12)} ${String(result.status).padStart(3)}  ${result.url}  ${result.body}`)
}

if (results.some(result => !result.healthy)) process.exitCode = 1
