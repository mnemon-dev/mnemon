import { execFile } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'
import { describe, expect, it } from 'vitest'

const run = promisify(execFile)
const fixture = fileURLToPath(new URL('./fixtures/bundle-activation.mjs', import.meta.url))
// The pinned development host by default; set the variable to check another installation.
const profile = process.env.MNEMON_BUNDLE_TEST_PROFILE ?? fileURLToPath(new URL('..', import.meta.url))
// A DSH host never inherits the NODE_PATH that `pnpm exec` sets. DSH 0.2
// routes plugin packages through Node's search paths, NODE_PATH included, so
// that path would reach this workspace's real plugins instead of the fixture's.
const env = { ...process.env, NODE_PATH: undefined }

describe('published Starter activation contracts', () => {
  it('activates isolated transitive components after a cold start without the bundle selected', async () => {
    const { stdout } = await run(process.execPath, ['--expose-internals', fixture, profile, 'manager', '--isolated'], { env, timeout: 30_000 })
    expect(stdout).toContain('"manager":true,"result":"passed"')
  }, 30_000)

  it('retains dependency routes of another bundle disabled since startup', async () => {
    const { stdout } = await run(process.execPath, ['--expose-internals', fixture, profile, 'manager', '--isolated', '--other-bundle'], { env, timeout: 30_000 })
    expect(stdout).toContain('"manager":true,"result":"passed"')
  }, 30_000)

  it('retains the legacy mnemon gate and independent component choices on the pinned DSH', async () => {
    const { stdout } = await run(process.execPath, ['--expose-internals', fixture], { env, timeout: 30_000 })
    expect(stdout).toContain('"result":"passed"')
  }, 30_000)

  it('persists real component and bundle toggles without bypassing the core gate', async () => {
    const { stdout } = await run(process.execPath, ['--expose-internals', fixture, profile, 'manager'], { env, timeout: 30_000 })
    expect(stdout).toContain('"manager":true,"result":"passed"')
  }, 30_000)
})
