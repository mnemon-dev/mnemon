import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { evaluatePluginCompatibility } from '@deepseek-ai/dsh-app-boot'
import { describe, expect, it } from 'vitest'

/** The pinned development baseline and DSH 0.2's release candidates; 0.2.0-rc.2 is npm `latest` and `next`. */
const SUPPORTED_DSH_RUNTIMES = ['0.1.7-rc.2', '0.2.0-rc.1', '0.2.0-rc.2']

const root = join(import.meta.dirname, '..')
const manifests = [
  join(root, 'package.json'),
  ...readdirSync(join(root, 'plugins')).map(name => join(root, 'plugins', name, 'package.json')),
].map(path => JSON.parse(readFileSync(path, 'utf8')) as { name: string; version: string })

describe('DSH host compatibility', () => {
  // DSH refuses to install or load a plugin whose @deepseek-ai/dsh or
  // @deepseek-ai/dsh-* peers exclude its runtime; this is the check it runs.
  it.each(manifests.map(manifest => [manifest.name, manifest] as const))('%s installs on every supported DSH runtime', (_name, manifest) => {
    for (const runtime of SUPPORTED_DSH_RUNTIMES) {
      expect(evaluatePluginCompatibility(manifest, {}, runtime), `${manifest.name} on DSH ${runtime}`).toBeUndefined()
    }
  })
})
