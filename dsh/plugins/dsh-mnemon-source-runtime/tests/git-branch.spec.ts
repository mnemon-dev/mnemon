import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { resolveGitBranch } from '../src/git-branch.ts'

const gitAvailable = (() => {
  try {
    execFileSync('git', ['--version'], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
})()

const directories: string[] = []
const gitRepository = () => {
  const directory = mkdtempSync(join(tmpdir(), 'dsh-mnemon-git-'))
  directories.push(directory)
  execFileSync('git', ['-c', 'init.defaultBranch=main', 'init', '-q', directory], { stdio: 'ignore' })
  execFileSync('git', ['-C', directory, 'config', 'user.name', 'Test'], { stdio: 'ignore' })
  execFileSync('git', ['-C', directory, 'config', 'user.email', 'test@example.invalid'], { stdio: 'ignore' })
  writeFileSync(join(directory, 'file.txt'), 'seed\n')
  execFileSync('git', ['-C', directory, 'add', 'file.txt'], { stdio: 'ignore' })
  execFileSync('git', ['-C', directory, 'commit', '-q', '-m', 'seed'], { stdio: 'ignore' })
  return directory
}

afterEach(() => {
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

describe('resolveGitBranch', () => {
  it('returns undefined for missing, blank, or non-git directories', () => {
    expect(resolveGitBranch()).toBeUndefined()
    expect(resolveGitBranch('   ')).toBeUndefined()
    const directory = mkdtempSync(join(tmpdir(), 'dsh-mnemon-nongit-'))
    directories.push(directory)
    expect(resolveGitBranch(directory)).toBeUndefined()
  })

  it('never throws when the path does not exist', () => {
    expect(() => resolveGitBranch(join(tmpdir(), 'dsh-mnemon-missing-404'))).not.toThrow()
    expect(resolveGitBranch(join(tmpdir(), 'dsh-mnemon-missing-404'))).toBeUndefined()
  })

  if (gitAvailable) {
    it('returns the current branch, follows branch switches, and returns undefined on detached HEAD', () => {
      const repository = gitRepository()
      expect(resolveGitBranch(repository)).toBe('main')

      execFileSync('git', ['-C', repository, 'checkout', '-q', '-b', 'feature/deep/branch_1'], { stdio: 'ignore' })
      expect(resolveGitBranch(repository)).toBe('feature/deep/branch_1')

      execFileSync('git', ['-C', repository, 'checkout', '-q', '--detach'], { stdio: 'ignore' })
      expect(resolveGitBranch(repository)).toBeUndefined()

      execFileSync('git', ['-C', repository, 'checkout', '-q', 'main'], { stdio: 'ignore' })
      expect(resolveGitBranch(repository)).toBe('main')
    })

    it('resolves from a nested working tree directory', () => {
      const repository = gitRepository()
      execFileSync('git', ['-C', repository, 'checkout', '-q', '-b', 'nested-branch'], { stdio: 'ignore' })
      const nested = join(repository, 'sub', 'dir')
      mkdirSync(nested, { recursive: true })
      expect(resolveGitBranch(nested)).toBe('nested-branch')
    })
  } else {
    it('skips git-backed assertions when git is unavailable', () => {
      expect(true).toBe(true)
    })
  }
})
