import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import type { JsonValue } from './contracts.ts'
import type { ResolvedMemorySpacesConfig as ResolvedConfig } from './config.ts'
import { ProcessError, runProcess, type ProcessOptions, type ProcessRunner } from './providers/process.ts'
import { withMemoryStorageLock } from 'dsh-mnemon/extension-sdk'
import { findMnemonCommand, isMnemonExecutable, resolveMnemonInvocation } from './native-cli.ts'

export class MnemonCliError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'MnemonCliError'
  }
}

export interface MnemonRunOptions {
  signal?: AbortSignal
  globalFlags?: boolean
  store?: string
  /** Output cap for this call. Whole-store reads raise it; everything else keeps the 2 MiB default. */
  maxOutputBytes?: number
}

export interface MnemonTextCommand {
  args: readonly string[]
  options?: MnemonRunOptions
}

export interface MnemonRunner {
  readonly command: string
  readonly commandFound: boolean
  runJson(args: readonly string[], options?: MnemonRunOptions): Promise<JsonValue>
  runText(args: readonly string[], options?: MnemonRunOptions): Promise<string>
  /** Run related CLI commands consecutively without allowing queued work between them. */
  runTextBatch(commands: readonly MnemonTextCommand[]): Promise<string[]>
  effectiveDataDir(): string
  /** Read Mnemon's persisted active-file selection, ignoring config and environment overrides. */
  persistedStore(): string
  effectiveStore(): string
}

const EMBEDDING_ENVIRONMENT_KEYS = new Set(['MNEMON_EMBED_ENDPOINT', 'MNEMON_EMBED_MODEL', 'MNEMON_EMBED_API_KEY', 'MNEMON_EMBED_PROTOCOL'])

/**
 * Explain a CLI call that produced no result. Only a failed launch means the
 * executable is missing or unusable, so only that case carries the install
 * hint; a timeout, a cancellation or an oversized output names its own cause.
 */
function processFailureMessage(error: unknown, args: readonly string[]): string {
  const detail = error instanceof Error ? error.message : String(error)
  const reason = error instanceof ProcessError ? error.reason : 'launch'
  if (reason === 'output-limit') {
    const command = args.find(arg => !arg.startsWith('-')) ?? 'command'
    return `mnemon ${command} stopped: ${detail.replace(/^mnemon /u, '')}`
  }
  if (reason !== 'launch') return detail
  const hint = process.platform === 'win32'
    ? 'Install the official Mnemon Windows release, ensure mnemon.exe is on PATH or under %LOCALAPPDATA%\\Programs\\mnemon, or set MNEMON_CLI_PATH or mnemon.cliPath to its absolute path.'
    : 'Install Mnemon and ensure "mnemon" is on PATH, or set MNEMON_CLI_PATH or mnemon.cliPath.'
  return `${detail}. ${hint}`
}

/** Preserve the Host environment while making saved embedding overrides authoritative. */
function processEnvironment(config: ResolvedConfig): NodeJS.ProcessEnv | undefined {
  if (!config.embedding.enabled) return undefined
  const inherited = Object.fromEntries(Object.entries(process.env).filter(([key]) => !EMBEDDING_ENVIRONMENT_KEYS.has(key.toUpperCase())))
  return {
    ...inherited,
    MNEMON_EMBED_ENDPOINT: config.embedding.endpoint,
    MNEMON_EMBED_MODEL: config.embedding.model,
    MNEMON_EMBED_API_KEY: config.embedding.apiKey,
    // 'auto' leaves the protocol to Mnemon's /v1 auto-detection.
    ...(config.embedding.protocol === 'auto' ? {} : { MNEMON_EMBED_PROTOCOL: config.embedding.protocol }),
  }
}

export function createRunner(config: ResolvedConfig, processRunner: ProcessRunner = runProcess): MnemonRunner {
  // Installation can change while DSH stays running. Status and execution
  // must resolve the same current executable, not a boot-time availability flag.
  const currentCommand = (): string => findMnemonCommand(config) ?? config.cliPath ?? 'mnemon'
  // Mnemon 0.1.2 runs store migrations while opening the database. Serializing
  // CLI processes prevents parallel status/viz calls during WebUI mount from
  // racing that migration and surfacing a transient SQLITE_BUSY error.

  const globalArgs = (store?: string): string[] => {
    const args = ['--data-dir', effectiveDataDir()]
    if (store !== undefined) args.push('--store', store)
    else if (config.store !== undefined) args.push('--store', config.store)
    return args
  }
  // resolveMemorySpacesConfig always yields a custom scope with an absolute dataDir.
  const effectiveDataDir = (): string => config.dataDir!
  const persistedStore = (): string => {
    const active = join(effectiveDataDir(), 'active')
    if (existsSync(active)) {
      try {
        const value = readFileSync(active, 'utf8').trim()
        if (/^[a-zA-Z0-9][a-zA-Z0-9_-]*$/.test(value)) return value
      } catch {
        // Fall through to Mnemon's own default.
      }
    }
    return 'default'
  }
  const launch = async (
    args: readonly string[],
    options: MnemonRunOptions = {},
  ): Promise<string> => {
    if (options.signal?.aborted === true) throw new MnemonCliError(`mnemon command aborted: ${String(options.signal.reason ?? 'cancelled')}`)
    const argv = options.globalFlags === false ? [...args] : [...globalArgs(options.store), ...args]
    const environment = processEnvironment(config)
    const processOptions: ProcessOptions = {
      timeoutMs: config.timeoutMs,
      ...(environment === undefined ? {} : { env: environment }),
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      ...(options.maxOutputBytes === undefined ? {} : { maxOutputBytes: options.maxOutputBytes }),
    }
    let result
    try {
      const invocation = resolveMnemonInvocation(currentCommand(), argv, environment)
      result = await processRunner(invocation.command, invocation.args,
        { ...processOptions, ...(invocation.env === undefined ? {} : { env: invocation.env }) })
    } catch (error) {
      throw new MnemonCliError(processFailureMessage(error, args))
    }
    if (result.exitCode !== 0) {
      const detail = result.stderr.trim() || result.stdout.trim() || 'no output'
      throw new MnemonCliError(`mnemon ${args.join(' ')} exited ${String(result.exitCode)}: ${detail}`)
    }
    return result.stdout
  }

  const execute = (
    args: readonly string[],
    options: MnemonRunOptions = {},
  ): Promise<string> => {
    return withMemoryStorageLock(effectiveDataDir(), () => launch(args, options))
  }

  return {
    get command() { return currentCommand() },
    get commandFound() {
      const found = findMnemonCommand(config)
      return found !== undefined && isMnemonExecutable(found)
    },
    async runJson(args, options) {
      const stdout = await execute(args, options)
      try {
        return JSON.parse(stdout) as JsonValue
      } catch {
        throw new MnemonCliError(`mnemon ${args.join(' ')} returned invalid JSON`)
      }
    },
    runText: execute,
    runTextBatch(commands) {
      return withMemoryStorageLock(effectiveDataDir(), async () => {
        const outputs: string[] = []
        for (const command of commands) outputs.push(await launch(command.args, command.options))
        return outputs
      })
    },
    effectiveDataDir() {
      return effectiveDataDir()
    },
    persistedStore() {
      return persistedStore()
    },
    effectiveStore() {
      if (config.store !== undefined) return config.store
      const fromEnvironment = process.env.MNEMON_STORE?.trim()
      if (fromEnvironment !== undefined && fromEnvironment !== '') return fromEnvironment
      return persistedStore()
    },
  }
}
