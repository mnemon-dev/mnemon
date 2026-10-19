import childProcess from 'node:child_process'
import { appendFileSync } from 'node:fs'
import { syncBuiltinESMExports } from 'node:module'

const execute = childProcess.execFileSync
childProcess.execFileSync = function (file, args, options) {
  const observed = file === 'git' && Array.isArray(args)
    && args.length === 4 && args[0] === '-C'
    && args[2] === 'branch' && args[3] === '--show-current'
  const record = {
    phase: process.env.MNEMON_GIT_PROBE_PHASE,
    platform: process.platform,
    pid: process.pid,
    timestamp: new Date().toISOString(),
    windowsHide: options?.windowsHide ?? null,
    timeout: options?.timeout ?? null,
    stdio: options?.stdio ?? null,
  }
  try {
    const output = Reflect.apply(execute, this, [file, args, options])
    if (observed && process.env.MNEMON_GIT_PROBE_TRACE) {
      appendFileSync(process.env.MNEMON_GIT_PROBE_TRACE, JSON.stringify({
        ...record, outcome: 'success', branch: String(output).trim(),
      }) + '\n')
    }
    return output
  } catch (error) {
    if (observed && process.env.MNEMON_GIT_PROBE_TRACE) {
      appendFileSync(process.env.MNEMON_GIT_PROBE_TRACE, JSON.stringify({
        ...record, outcome: 'error', code: error?.code ?? null,
      }) + '\n')
    }
    throw error
  }
}
syncBuiltinESMExports()
