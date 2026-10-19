import { execFileSync } from 'node:child_process'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { resolveGitBranch } from '../src/git-branch.ts'

vi.mock('node:child_process', () => ({ execFileSync: vi.fn() }))

describe('Runtime Git probe process', () => {
  beforeEach(() => { vi.resetAllMocks() })

  it('hides the Windows console while retaining bounded, silent branch detection', () => {
    vi.mocked(execFileSync).mockReturnValue('feature/deep/branch_1\r\n')

    expect(resolveGitBranch('  C:\\workspaces\\project with spaces  ')).toBe('feature/deep/branch_1')
    expect(execFileSync).toHaveBeenCalledOnce()
    expect(execFileSync).toHaveBeenCalledWith('git', [
      '-C', 'C:\\workspaces\\project with spaces', 'branch', '--show-current',
    ], {
      encoding: 'utf8',
      timeout: 2_000,
      stdio: ['ignore', 'pipe', 'ignore'],
      windowsHide: true,
    })
  })

  it('does not launch Git without a workspace root', () => {
    expect(resolveGitBranch()).toBeUndefined()
    expect(resolveGitBranch(' \t\n ')).toBeUndefined()
    expect(execFileSync).not.toHaveBeenCalled()
  })

  it.each(['', '\r\n', ' \t\n '])('returns undefined for empty branch output %j', output => {
    vi.mocked(execFileSync).mockReturnValue(output)
    expect(resolveGitBranch('/workspace')).toBeUndefined()
  })

  it.each([
    ['missing Git', Object.assign(new Error('spawnSync git ENOENT'), { code: 'ENOENT' })],
    ['timeout', Object.assign(new Error('spawnSync git ETIMEDOUT'), { code: 'ETIMEDOUT' })],
    ['failed command', Object.assign(new Error('not a git repository'), { status: 128 })],
  ])('retains the unfiltered fallback after %s', (_reason, error) => {
    vi.mocked(execFileSync).mockImplementation(() => { throw error })
    expect(resolveGitBranch('/workspace')).toBeUndefined()
  })
})
