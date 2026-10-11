import { execFileSync } from 'node:child_process'
// This projection concern belongs to the Runtime Source, not the Mnemon Core.

const GIT_BRANCH_TIMEOUT_MS = 2_000

/**
 * Resolve the current git branch of a workspace root. Returns undefined when
 * the directory is not a git working tree, HEAD is detached, or the probe
 * fails or times out, so callers fall back to the unfiltered view.
 */
export function resolveGitBranch(cwd?: string): string | undefined {
  const root = cwd?.trim()
  if (root === undefined || root === '') return undefined
  try {
    const output = execFileSync('git', ['-C', root, 'branch', '--show-current'], {
      encoding: 'utf8',
      timeout: GIT_BRANCH_TIMEOUT_MS,
      stdio: ['ignore', 'pipe', 'ignore'],
      windowsHide: true,
    })
    const branch = output.trim()
    return branch === '' ? undefined : branch
  } catch {
    return undefined
  }
}
