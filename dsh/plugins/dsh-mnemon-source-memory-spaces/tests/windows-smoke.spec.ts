import { existsSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { delimiter, dirname, join } from 'node:path'
import { afterAll, describe, expect, it } from 'vitest'
import { resolveMemorySpacesConfig } from "../src/config.ts"
import { createRunner } from '../src/runner.ts'
import { findMnemonCommand } from '../src/native-cli.ts'

const enabled = process.platform === 'win32' && process.env.RUN_WINDOWS_MNEMON_SMOKE === '1'
const temporaryDirectories: string[] = []

afterAll(() => {
  for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

describe.skipIf(!enabled)('Windows Mnemon integration', () => {
  it('discovers the verified release binary outside PATH and runs status', async () => {
    const expected = process.env.MNEMON_WINDOWS_SMOKE_PATH
    if (expected === undefined || expected === '') throw new Error('MNEMON_WINDOWS_SMOKE_PATH is required')
    expect(existsSync(expected)).toBe(true)
    expect((process.env.PATH ?? '').split(delimiter).some(directory => directory.toLowerCase() === dirname(expected).toLowerCase())).toBe(false)

    const command = findMnemonCommand({})
    expect(command?.toLowerCase()).toBe(expected.toLowerCase())

    const dataDir = mkdtempSync(join(tmpdir(), 'dsh-mnemon-windows-smoke-'))
    temporaryDirectories.push(dataDir)
    const runner = createRunner(resolveMemorySpacesConfig({ dataDir }))
    expect(runner.commandFound).toBe(true)
    await expect(runner.runText(['--version'], { globalFlags: false }))
      .resolves.toContain(`mnemon version ${process.env.MNEMON_WINDOWS_SMOKE_VERSION ?? '0.2.3'}`)
    await expect(runner.runJson(['status'])).resolves.toMatchObject({ total_insights: 0 })
  })
})
