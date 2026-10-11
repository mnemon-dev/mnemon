import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { portableRelativePath, stableCssClassMap } from '../tsdown.config.ts'

describe('build configuration', () => {
  it('uses portable project-relative CSS module filenames', () => {
    const root = resolve('fixture')
    expect(portableRelativePath(root, join(root, 'src', 'client', 'View.module.css')))
      .toBe('src/client/View.module.css')
  })

  it('serializes CSS module exports in a stable order', () => {
    expect(stableCssClassMap({ zeta: { name: 'z' }, alpha: { name: 'a' } }))
      .toEqual({ alpha: 'a', zeta: 'z' })
  })
})
