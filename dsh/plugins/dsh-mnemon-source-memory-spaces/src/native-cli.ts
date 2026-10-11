import { accessSync, constants, existsSync, readFileSync, realpathSync, statSync } from 'node:fs'
import { homedir } from 'node:os'
import { createRequire } from 'node:module'
import { dirname, join, posix, win32 } from 'node:path'

interface NpmManifest { name?: string; version?: string; optionalDependencies?: Record<string, string> }
function manifest(path: string): NpmManifest | undefined {
  try { return JSON.parse(readFileSync(path, 'utf8')) as NpmManifest } catch { return undefined }
}
function samePath(left: string, right: string): boolean {
  try { return realpathSync(left) === realpathSync(right) } catch { return false }
}
const MNEMON_NPM_PACKAGE = '@mnemon-dev/mnemon'

/** Execute JavaScript with the Host binary even when it is Electron's GUI executable. */
export function nodeLauncherEnvironment(environment: NodeJS.ProcessEnv = process.env): NodeJS.ProcessEnv {
  // Windows treats environment keys case-insensitively. Emit a single spelling,
  // and do not restore keys deliberately removed by saved embedding overrides.
  const inherited = Object.fromEntries(Object.entries(environment).filter(([key]) => key.toUpperCase() !== 'ELECTRON_RUN_AS_NODE'))
  return { ...inherited, ELECTRON_RUN_AS_NODE: '1' }
}

export function mnemonNpmLauncher(command: string): string | undefined {
  let realCommand = command
  try { realCommand = realpathSync(command) } catch {}
  const root = dirname(dirname(realCommand))
  if (manifest(join(root, 'package.json'))?.name === MNEMON_NPM_PACKAGE
    && samePath(realCommand, join(root, 'bin/mnemon.js'))) return realCommand
  // npm's Windows shim is a sibling of node_modules and cannot be spawned
  // directly with shell:false. Validate it, then use the JavaScript launcher.
  if (/\.cmd$/i.test(command)) {
    const launcher = join(dirname(command), 'node_modules', MNEMON_NPM_PACKAGE, 'bin/mnemon.js')
    try {
      if (manifest(join(dirname(dirname(launcher)), 'package.json'))?.name === MNEMON_NPM_PACKAGE
        && existsSync(launcher) && readFileSync(command, 'utf8').replaceAll('\\', '/').includes('node_modules/@mnemon-dev/mnemon/bin/mnemon.js')) return launcher
    } catch {}
  }
  return undefined
}

/** Resolve only the platform package pinned by the recognized official launcher. */
function npmNativeBinary(launcher: string): string | undefined {
  const pkg = manifest(join(dirname(dirname(launcher)), 'package.json'))
  const target = `${process.platform}-${process.arch}`
  const alias = `${MNEMON_NPM_PACKAGE}-${target}`
  const version = typeof pkg?.version === 'string' ? `${pkg.version}-${target}` : undefined
  if (version === undefined || pkg?.optionalDependencies?.[alias] !== `npm:${MNEMON_NPM_PACKAGE}@${version}`) return undefined
  try {
    // Resolve from this launcher (including nested/pnpm installs), never from
    // the Host's node_modules or a guessed global npm prefix.
    // Avoid require.resolve's cached symlink target after a package-manager
    // update while DSH is still running.
    const packagePath = createRequire(launcher).resolve.paths(alias)?.map(path => join(path, alias, 'package.json')).find(existsSync)
    if (packagePath === undefined) return undefined
    const native = manifest(packagePath)
    if (native?.name !== MNEMON_NPM_PACKAGE || native.version !== version) return undefined
    const binary = join(dirname(packagePath), 'bin', process.platform === 'win32' ? 'mnemon.exe' : 'mnemon')
    return isMnemonExecutable(binary) ? binary : undefined
  } catch {
    // Preserve the launcher's diagnostics for missing optional dependencies or
    // a future npm layout we do not recognize.
    return undefined
  }
}

/** Keep CLI subprocesses under our windowsHide, timeout and cancellation policy. */
export function resolveMnemonInvocation(command: string, args: readonly string[], env?: NodeJS.ProcessEnv): {
  command: string; args: string[]; env?: NodeJS.ProcessEnv
} {
  const launcher = mnemonNpmLauncher(command)
  if (launcher !== undefined) {
    // The npm wrapper's nested spawn does not hide Windows console windows.
    // Its update command owns npm provenance, so retain that special entry.
    const binary = args.length === 1 && args[0] === 'update' ? undefined : npmNativeBinary(launcher)
    if (binary === undefined) return { command: process.execPath, args: [launcher, ...args], env: nodeLauncherEnvironment(env) }
    command = binary
  }
  return { command, args: [...args], ...(env === undefined ? {} : { env }) }
}

const UNIX_COMMON_CLI_PATHS = [
  '~/.local/bin/mnemon',
  '/opt/homebrew/bin/mnemon',
  '/usr/local/bin/mnemon',
  '/usr/bin/mnemon',
] as const

export interface CommandDiscoveryOptions {
  platform?: NodeJS.Platform
  env?: NodeJS.ProcessEnv
  home?: string
  isExecutable?: (path: string) => boolean
}

function pathApi(platform: NodeJS.Platform): typeof posix | typeof win32 {
  return platform === 'win32' ? win32 : posix
}

function expandHome(path: string, home = homedir(), platform = process.platform): string {
  if (path === '~') return home
  return path.startsWith('~/') || path.startsWith('~\\') ? pathApi(platform).join(home, path.slice(2)) : path
}

function envValue(env: NodeJS.ProcessEnv, name: string, platform: NodeJS.Platform): string | undefined {
  if (platform !== 'win32') return env[name]
  const key = Object.keys(env).find(candidate => candidate.toLowerCase() === name.toLowerCase())
  return key === undefined ? undefined : env[key]
}

export function isMnemonExecutable(path: string, platform = process.platform): boolean {
  if (platform === 'win32' && win32.extname(path).toLowerCase() !== '.exe' && mnemonNpmLauncher(path) === undefined) return false
  try {
    accessSync(path, platform === 'win32' ? constants.F_OK : constants.X_OK)
    return statSync(path).isFile()
  } catch {
    return false
  }
}

function windowsCommonCliPaths(env: NodeJS.ProcessEnv, home: string): string[] {
  const candidates: string[] = []
  const goBin = envValue(env, 'GOBIN', 'win32')?.trim()
  if (goBin !== undefined && win32.isAbsolute(goBin)) candidates.push(win32.join(goBin, 'mnemon.exe'))

  const goPath = envValue(env, 'GOPATH', 'win32')?.trim()
  const goPathRoot = goPath?.split(win32.delimiter).map(candidate => candidate.trim())
    .find(candidate => candidate !== '' && win32.isAbsolute(candidate))
  const goInstallRoot = goPathRoot ?? win32.join(home, 'go')
  candidates.push(win32.join(goInstallRoot, 'bin', 'mnemon.exe'))

  const localAppData = envValue(env, 'LOCALAPPDATA', 'win32')?.trim()
  if (localAppData !== undefined && win32.isAbsolute(localAppData)) {
    candidates.push(win32.join(localAppData, 'Programs', 'mnemon', 'mnemon.exe'))
  }
  const programFiles = envValue(env, 'ProgramFiles', 'win32')?.trim()
  if (programFiles !== undefined && win32.isAbsolute(programFiles)) {
    candidates.push(win32.join(programFiles, 'mnemon', 'mnemon.exe'))
  }
  return [...new Set(candidates)]
}

function commonCliPaths(platform: NodeJS.Platform, env: NodeJS.ProcessEnv, home: string): string[] {
  if (platform === 'win32') return windowsCommonCliPaths(env, home)
  return UNIX_COMMON_CLI_PATHS.map(candidate => expandHome(candidate, home, platform))
}

/** Locate the local Mnemon binary without invoking a shell. */
export function findMnemonCommand(
  config: { cliPath?: string | undefined },
  options: CommandDiscoveryOptions = {},
): string | undefined {
  const platform = options.platform ?? process.platform
  const env = options.env ?? process.env
  const home = options.home ?? homedir()
  const isExecutable = options.isExecutable ?? (path => isMnemonExecutable(path, platform))
  const paths = pathApi(platform)
  const resolveCommand = (command: string): string | undefined => {
    const expanded = expandHome(command, home, platform)
    // Explicit paths remain authoritative even when missing, so execution
    // reports the configured path instead of silently choosing another CLI.
    if (expanded.includes('/') || expanded.includes('\\')) return expanded
    const names = platform === 'win32' && paths.extname(expanded) === '' ? [`${expanded}.exe`, `${expanded}.cmd`] : [expanded]
    for (const directory of (envValue(env, 'PATH', platform) ?? '').split(paths.delimiter)) {
      if (directory === '') continue
      for (const name of names) {
        const path = paths.join(directory, name)
        if (/\.cmd$/i.test(name) && mnemonNpmLauncher(path) === undefined) continue
        if (isExecutable(path)) return path
      }
    }
    return undefined
  }
  if (config.cliPath !== undefined) return resolveCommand(config.cliPath)
  const envPath = envValue(env, 'MNEMON_CLI_PATH', platform)?.trim()
  if (envPath !== undefined && envPath !== '') {
    const path = resolveCommand(envPath)
    if (path !== undefined && isExecutable(path)) return path
  }
  const fromPath = resolveCommand('mnemon')
  if (fromPath !== undefined) return fromPath
  for (const path of commonCliPaths(platform, env, home)) {
    if (isExecutable(path)) return path
  }
  return undefined
}
