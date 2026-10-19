import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { memoryPlugin as documents } from 'dsh-mnemon-source-documents'
import { memoryPlugin as spaces } from 'dsh-mnemon-source-memory-spaces'
import { memoryPlugin as runtime } from 'dsh-mnemon-source-runtime'
import { memoryPlugin as capture } from 'dsh-mnemon-strategy-auto-capture'
import { memoryPlugin as threeTier } from 'dsh-mnemon-strategy-default-three-tier'
import { memoryPlugin as general } from 'dsh-mnemon-strategy-general'
import { memoryPlugin as light } from 'dsh-mnemon-strategy-light-context'
import { memoryPlugin as scoped } from 'dsh-mnemon-strategy-scoped'
import { STARTER_COMPONENT_ROWS } from '../src/client/starter-rows.ts'

const components = [runtime, documents, spaces, threeTier, general, capture, light, scoped]
const read = (path: string): Record<string, unknown> => JSON.parse(readFileSync(new URL(`../${path}`, import.meta.url), 'utf8')) as Record<string, unknown>

describe('component package metadata', () => {
  // DSH names a package's rows from its locale files; the board names them from the declaration.
  it.each(components.map(plugin => [plugin.packageName, plugin] as const))('%s shows DSH the name and description it declares', (packageName, plugin) => {
    for (const [file, language] of [['en', 'en'], ['zh', 'zh-CN']] as const) {
      expect(read(`plugins/${packageName}/locale/${file}.json`)).toEqual({ meta: { title: plugin.label?.[language], description: plugin.description?.[language] } })
    }
    const manifest = read(`plugins/${packageName}/package.json`) as { exports: Record<string, unknown>; files: string[] }
    expect(manifest.exports['./locale/*.json']).toBe('./locale/*.json')
    expect(manifest.files).toContain('locale')
  })

  it('gives every component row the Starter declares a page of its own', () => {
    // Row ids and module names in the bundle patch, for the rows that name a memory component.
    const patch = readFileSync(new URL('../cordis.patch.yml', import.meta.url), 'utf8')
    const rows = [...patch.matchAll(/- id: ([\w-]+)\n\s+name: (dsh-mnemon-(?:source|strategy)-[\w-]+)/gu)]
    expect(rows.map(row => ({ rowId: row[1], packageName: row[2] }))).toEqual(STARTER_COMPONENT_ROWS)
    expect(rows.map(row => row[2]).sort()).toEqual(components.map(plugin => plugin.packageName).sort())
  })
})
