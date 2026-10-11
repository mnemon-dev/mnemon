import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { checkDocuments, documentLinks, headingIds, withoutFences } from '../scripts/verify-docs.mjs'

const directories = []
afterEach(() => { for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true }) })
function fixture(files) {
  const root = mkdtempSync(join(tmpdir(), 'mnemon-docs-test-'))
  directories.push(root)
  for (const [file, text] of Object.entries(files)) { mkdirSync(join(root, file, '..'), { recursive: true }); writeFileSync(join(root, file), text) }
  return checkDocuments(root, Object.keys(files))
}

describe('documentation checks', () => {
  it('keeps line numbers while ignoring backtick and tilde fences', () => {
    expect(withoutFences('a\n```md\n[no](missing)\n```\nb\n~~~\nno\n~~~')).toBe('a\n\n\n\nb\n\n\n')
  })
  it('recognizes Unicode, inline code, links and duplicate headings', () => {
    expect([...headingIds('## 设置：`displayMode`\n## A [link](./b.md)\n## A link\n<a id="explicit"></a>')]).toEqual(['设置displaymode', 'a-link', 'a-link-1', 'explicit'])
  })
  it('recognizes centered HTML headings and entities in document order', () => {
    expect([...headingIds('<h1 align="center">Memory &amp; View</h1>\n## Memory & View\n<h2>中文</h2>')]).toEqual(['memory--view', 'memory--view-1', '中文'])
  })
  it('finds linked images, HTML and reference-style targets without inline-code examples', () => {
    expect(documentLinks('[![x](a.png)](b.md)\n<img src="c.png">\n[x]: d.md\n`[no](missing)`').map(link => link.url).sort()).toEqual(['a.png', 'b.md', 'c.png', 'd.md'])
  })
  it('decodes HTML URL entities once and finds autolinks', () => {
    expect(documentLinks('<img src="https://example.test/badge?metric=downloads&amp;lang=zh">\n[x](./a&#32;b.md)\n<https://example.test/guide>').map(link => link.url)).toEqual(expect.arrayContaining(['https://example.test/badge?metric=downloads&lang=zh', './a b.md', 'https://example.test/guide']))
    expect(documentLinks('<a href="?x=&amp;amp;">').map(link => link.url)).toEqual(['?x=&amp;'])
  })
  it('accepts existing images, directories, encoded paths and local anchors', () => {
    const report = fixture({ 'README.md': '# Home\n[a](#home) [b](./a%20b.md#中文) [c](./dir/) ![d](./dir/a.png)', 'a b.md': '# 中文', 'dir/a.png': 'fixture' })
    expect(report.failures).toEqual([])
    expect(report.anchors).toBe(2)
  })
  it('checks the file and fragment independently of rendering query parameters', () => {
    expect(fixture({ 'README.md': '[a](./a.md?plain=1#中文)', 'a.md': '# 中文' }).failures).toEqual([])
  })
  it('reports missing files and anchors rather than treating them as external', () => {
    const report = fixture({ 'README.md': '[a](./absent.md)\n[b](./a.md#old)', 'a.md': '# New' })
    expect(report.failures).toEqual(['README.md:1: missing ./absent.md', 'README.md:2: missing anchor ./a.md#old'])
  })
  it('reports malformed encoding and missing translation files', () => {
    const report = fixture({ 'docs/en/page.md': '[bad](./%ZZ.md)' })
    expect(report.failures).toHaveLength(2)
    expect(report.failures[1]).toContain('docs/zh-CN/page.md')
  })
  it('does not make network availability a local documentation requirement', () => {
    expect(fixture({ 'README.md': '[web](https://example.invalid/x#y) [mail](mailto:a@example.invalid)' }).failures).toEqual([])
  })
  it('checks current repository URLs used in npm READMEs without rewriting historical links', () => {
    const report = fixture({ 'README.md': '[ok](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/a.md#one) [missing](https://github.com/mnemon-dev/mnemon/tree/master/dsh/absent) [history](https://github.com/omdsh-dev/dsh-mnemon/blob/v0.4.0/old.md)', 'docs/a.md': '# One' })
    expect(report.localLinks).toBe(2)
    expect(report.failures).toEqual(['README.md:1: missing https://github.com/mnemon-dev/mnemon/tree/master/dsh/absent'])
  })
  it('rejects links into the retired repository main branch but keeps pinned history', () => {
    const report = fixture({ 'README.md': '[old](https://github.com/omdsh-dev/dsh-mnemon/blob/main/docs/a.md) ![raw](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/main/docs/x.png) ![pinned](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/x.png)', 'docs/a.md': '# One' })
    expect(report.failures).toEqual([
      'README.md:1: https://github.com/omdsh-dev/dsh-mnemon/blob/main/docs/a.md points at the retired repository; link to dsh/ in mnemon-dev/mnemon',
      'README.md:1: https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/main/docs/x.png points at the retired repository; link to dsh/ in mnemon-dev/mnemon',
    ])
  })
  it.each([['README.md', 'en'], ['README.zh-CN.md', 'zh-CN']])('retains the centered %s header, seven badges and localized diagram', (file, language) => {
    const readme = readFileSync(new URL(`../${file}`, import.meta.url), 'utf8')
    const header = readme.split('\n## ')[0]
    expect(header).toMatch(/^<h1 align="center">dsh-mnemon<\/h1>/u)
    expect([...header.matchAll(/<p align="center">/gu)]).toHaveLength(6)
    const images = [...header.matchAll(/<img\b[^>]*\bsrc="([^"]+)"/gu)].map(match => new URL(match[1].replaceAll('&amp;', '&')))
    const badges = images.filter(url => ['img.shields.io', 'dshfind.com'].includes(url.hostname))
    expect(badges).toHaveLength(7)
    expect(badges.map(url => url.pathname)).toEqual(expect.arrayContaining(['/npm/v/dsh-mnemon', '/npm/dt/dsh-mnemon', '/github/v/release/omdsh-dev/dsh-mnemon', '/github/stars/mnemon-dev/mnemon', '/badge/License-MIT-yellow.svg']))
    const catalog = badges.filter(url => url.hostname === 'dshfind.com')
    expect(catalog.map(url => url.searchParams.get('lang'))).toEqual([language === 'en' ? 'en' : 'zh', language === 'en' ? 'en' : 'zh'])
    expect(catalog.some(url => url.searchParams.get('metric') === 'downloads')).toBe(true)
    expect(readme).toContain(`/docs/assets/diagrams/${language}/composable-memory.png`)
  })
})
