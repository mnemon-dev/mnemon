// Open a running DSH WebUI the way DSH Desktop does, for desktop-only checks such as #310.
// The page lives at dsh-app://app/, a standard, secure, fetch- and stream-capable scheme like
// the official shell registers, and the shell proxies every request to the Host on loopback.
//   <electron> scripts/fixtures/electron-desktop-window.mjs [--remote-debugging-port=<port>]
//   DSH_HOST_URL   the launch URL a Host prints, with its one-time token
//   DSH_TRANSPORT  none (DSH 0.1.7 Desktop, the default) or owns-host (DSH 0.2 Desktop)
//   DSH_OFFSCREEN  1 renders without showing a window, for DevTools-protocol drivers
//   DSH_LANG       the interface language, such as zh-CN; the system language otherwise
// A 0.1.7 window cannot open DSH's WebSocket stream through the scheme, so DSH shows it
// reconnecting; Mnemon's channels are plain HTTP requests and do not depend on it.
import { app, BrowserWindow, protocol, session } from 'electron'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const hostUrl = new URL(process.env.DSH_HOST_URL)
const base = hostUrl.origin
const transport = process.env.DSH_TRANSPORT ?? 'none'
protocol.registerSchemesAsPrivileged([{ scheme: 'dsh-app', privileges: { standard: true, secure: true, corsEnabled: true, supportFetchAPI: true, stream: true, codeCache: true } }])
if (process.env.DSH_LANG) app.commandLine.appendSwitch('lang', process.env.DSH_LANG)

// The shell holds the Host session; the page never sees the token.
const jar = new Map()
const keep = response => {
  for (const line of response.headers.getSetCookie?.() ?? []) {
    const pair = line.split(';', 1)[0]
    const at = pair.indexOf('=')
    if (at > 0) jar.set(pair.slice(0, at).trim(), pair.slice(at + 1).trim())
  }
}
const cookie = () => [...jar].map(([name, value]) => `${name}=${value}`).join('; ')

app.whenReady().then(async () => {
  app.dock?.hide()
  keep(await fetch(hostUrl, { redirect: 'manual' }))
  // A 0.2 shell points the WebSocket stream at the Host itself, with the session and no page Origin.
  for (const [name, value] of jar) await session.defaultSession.cookies.set({ url: base, name, value })
  session.defaultSession.webRequest.onBeforeSendHeaders({ urls: [`${base.replace('http', 'ws')}/*`, `${base}/*`] }, (details, done) => {
    delete details.requestHeaders.Origin
    if (jar.size > 0) details.requestHeaders.Cookie = cookie()
    done({ requestHeaders: details.requestHeaders })
  })
  protocol.handle('dsh-app', async request => {
    const url = new URL(request.url)
    const headers = new Headers(request.headers)
    headers.delete('origin')
    if (jar.size > 0) headers.set('cookie', cookie())
    const init = { method: request.method, headers, redirect: 'manual' }
    if (request.method !== 'GET' && request.method !== 'HEAD' && request.body !== null) { init.body = request.body; init.duplex = 'half' }
    const response = await fetch(base + url.pathname + url.search, init)
    keep(response)
    const location = response.headers.get('location')
    if (location === null || !location.startsWith(base)) return response
    const rewritten = new Headers(response.headers)
    rewritten.set('location', 'dsh-app://app' + location.slice(base.length))
    return new Response(response.body, { status: response.status, statusText: response.statusText, headers: rewritten })
  })
  const offscreen = process.env.DSH_OFFSCREEN === '1'
  const window = new BrowserWindow({
    width: 1280, height: 800, show: !offscreen,
    webPreferences: {
      offscreen,
      preload: join(dirname(fileURLToPath(import.meta.url)), 'electron-desktop-preload.cjs'),
      additionalArguments: transport === 'owns-host' ? [`--dsh-stream-base=${base}/`] : [],
    },
  })
  await window.loadURL('dsh-app://app/')
})
app.on('window-all-closed', () => app.quit())
