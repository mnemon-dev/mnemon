import assert from 'node:assert/strict'
import { readFileSync, readdirSync, realpathSync } from 'node:fs'
import { createRequire } from 'node:module'
import { resolve } from 'node:path'

export const localeExport = './locale/*.json'

/** Expand DSH metadata resources without treating JSON as executable Node entries. */
export function readPackageLocales(directory, manifest) {
  if (!Object.hasOwn(manifest.exports ?? {}, localeExport)) return []
  assert.equal(manifest.exports[localeExport], localeExport, 'Locale exports must resolve inside ./locale/')
  const require = createRequire(resolve(directory, 'package.json'))
  const files = readdirSync(resolve(directory, 'locale'), { withFileTypes: true })
    .filter(entry => entry.name.endsWith('.json')).sort((a, b) => a.name.localeCompare(b.name, 'en'))
  assert(files.some(entry => entry.name === 'en.json'), 'DSH metadata requires locale/en.json')
  const languages = new Set()
  return files.map(entry => {
    const language = entry.name.slice(0, -5)
    assert(entry.isFile() && /^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$/u.test(language), `Invalid locale resource: ${entry.name}`)
    assert(!languages.has(language.toLowerCase()), `Duplicate locale: ${language}`)
    languages.add(language.toLowerCase())
    const path = `locale/${entry.name}`
    const filename = require.resolve(`${manifest.name}/${path}`)
    assert.equal(realpathSync(filename), realpathSync(resolve(directory, path)), `${path} must resolve to the package's own resource`)
    const dictionary = JSON.parse(readFileSync(filename, 'utf8'))
    assert(dictionary && typeof dictionary === 'object' && !Array.isArray(dictionary), `${path} must contain an object`)
    const meta = dictionary.meta === undefined ? {} : dictionary.meta
    assert(meta && typeof meta === 'object' && !Array.isArray(meta), `${path}: meta must be an object`)
    for (const field of ['title', 'description']) {
      assert(meta[field] === undefined || (typeof meta[field] === 'string' && meta[field].trim()), `${path}: meta.${field} must be a non-empty string`)
    }
    return { path, dictionary }
  })
}
