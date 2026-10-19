// Run the published DSH CLI inside a real Electron main process for WebUI tests.
const { app } = require('electron')
const { join } = require('node:path')
const { pathToFileURL } = require('node:url')

if (!process.env.DSH_HOME) throw new Error('An isolated DSH_HOME is required')
app.setPath('userData', join(process.env.DSH_HOME, 'electron'))
app.on('window-all-closed', () => {})
// Electron accepts the flag but retains it in argv instead of execArgv. Let the
// published Cordis loader use its explicit internals path without native addons.
process.execArgv.push('--expose-internals')
process.argv = [process.execPath, ...process.argv.slice(process.argv.indexOf(__filename) + 1)]
app.whenReady().then(async () => {
  app.dock?.hide()
  console.log('Electron fixture Host: ' + JSON.stringify({
    electron: process.versions.electron, node: process.versions.node,
    type: process.type, runAsNode: process.env.ELECTRON_RUN_AS_NODE ?? null,
  }))
  const cli = await import(pathToFileURL(process.argv[1]).href)
  // Published DSH 0.1.7-rc.2 only starts automatically for import.meta.main.
  // This fixture imports it from Electron's main script instead.
  if (typeof cli.runCli === 'function') await cli.runCli()
}).catch(error => { console.error(error); app.exit(1) })
