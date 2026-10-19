import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { localeExport, readPackageLocales } from '../scripts/package-locales.mjs'

const temporary = []

function fixture({ exports = { [localeExport]: localeExport }, locales = { 'en.json': { meta: {} } } } = {}) {
  const directory = mkdtempSync(join(tmpdir(), 'mnemon-package-locales-'))
  temporary.push(directory)
  const manifest = { name: 'mnemon-locale-fixture', type: 'module', exports }
  writeFileSync(join(directory, 'package.json'), JSON.stringify(manifest))
  mkdirSync(join(directory, 'locale'))
  for (const [filename, dictionary] of Object.entries(locales)) {
    writeFileSync(join(directory, 'locale', filename), JSON.stringify(dictionary))
  }
  return { directory, manifest, read: () => readPackageLocales(directory, manifest) }
}

afterEach(() => {
  for (const directory of temporary.splice(0)) rmSync(directory, { recursive: true, force: true })
})

describe('published DSH locale metadata', () => {
  it('resolves concrete locale resources while allowing the English manifest fallback', () => {
    const english = { meta: {} }
    const chinese = { meta: { title: '三级记忆', description: '召回与写入' } }
    const packageFixture = fixture({ locales: { 'zh.json': chinese, 'en.json': english } })
    expect(packageFixture.read()).toEqual([
      { path: 'locale/en.json', dictionary: english },
      { path: 'locale/zh.json', dictionary: chinese },
    ])
  })

  it('allows ordinary dictionaries without metadata and packages without locale exports', () => {
    expect(fixture({ locales: { 'en.json': { greeting: 'Hello' } } }).read()).toEqual([
      { path: 'locale/en.json', dictionary: { greeting: 'Hello' } },
    ])
    expect(fixture({ exports: {} }).read()).toEqual([])
  })

  it('rejects a locale export mapped outside its declared resource directory', () => {
    const packageFixture = fixture({ exports: { [localeExport]: './assets/*.json' } })
    expect(packageFixture.read).toThrow('Locale exports must resolve inside ./locale/')
  })

  it('rejects a concrete export that overrides the wildcard with a different resource', () => {
    const packageFixture = fixture({ exports: {
      [localeExport]: localeExport,
      './locale/en.json': './other-en.json',
    } })
    writeFileSync(join(packageFixture.directory, 'other-en.json'), '{"meta":{}}')
    expect(packageFixture.read).toThrow("locale/en.json must resolve to the package's own resource")
  })

  it('rejects a locale set that DSH cannot discover because en.json is absent', () => {
    const packageFixture = fixture({ locales: { 'zh.json': { meta: { title: '三级记忆' } } } })
    expect(packageFixture.read).toThrow('DSH metadata requires locale/en.json')
  })

  it('rejects directories masquerading as locale files and malformed locale names', () => {
    const directoryFixture = fixture()
    mkdirSync(join(directoryFixture.directory, 'locale/zh.json'))
    expect(directoryFixture.read).toThrow('Invalid locale resource: zh.json')
    const nameFixture = fixture({ locales: { 'en.json': {}, 'zh_CN.json': {} } })
    expect(nameFixture.read).toThrow('Invalid locale resource: zh_CN.json')
  })

  it('rejects malformed JSON instead of silently using the manifest fallback', () => {
    const packageFixture = fixture()
    writeFileSync(join(packageFixture.directory, 'locale/en.json'), '{"meta":')
    expect(packageFixture.read).toThrow(SyntaxError)
  })

  it.each([null, [], 'translation', 42])('rejects a non-object dictionary: %j', dictionary => {
    expect(fixture({ locales: { 'en.json': dictionary } }).read).toThrow('locale/en.json must contain an object')
  })

  it.each([null, [], 'translation', 42])('rejects a non-object meta field: %j', meta => {
    expect(fixture({ locales: { 'en.json': { meta } } }).read).toThrow('locale/en.json: meta must be an object')
  })

  it.each([
    ['title', ''], ['title', '   '], ['title', 42], ['title', null],
    ['description', ''], ['description', '\n\t'], ['description', false], ['description', {}],
  ])('rejects an unusable meta.%s value: %j', (field, value) => {
    const packageFixture = fixture({ locales: { 'en.json': { meta: { [field]: value } } } })
    expect(packageFixture.read).toThrow(`locale/en.json: meta.${field} must be a non-empty string`)
  })
})
