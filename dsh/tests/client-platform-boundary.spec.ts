import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import ts from 'typescript'

describe('browser bundle platform boundary', () => {
  const root = fileURLToPath(new URL('../', import.meta.url))
  const directories = ['src/client', ...['runtime', 'documents', 'memory-spaces'].map(group => `plugins/dsh-mnemon-source-${group}/src/client`)]
  const clientSources = () => directories.flatMap(directory => {
    const directoryPath = join(root, directory)
    return readdirSync(directoryPath, { recursive: true })
      .filter(path => /\.[jt]sx?$/.test(String(path)))
      .map(path => ({ path: join(directory, String(path)), source: readFileSync(join(directoryPath, String(path)), 'utf8') }))
  })

  it('keeps all default and independent Client imports inside browser code or data-only contracts', () => {
    const violations: string[] = []
    for (const { path, source } of clientSources()) {
      const ast = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
      for (const node of ast.statements) {
        if ((!ts.isImportDeclaration(node) && !ts.isExportDeclaration(node)) || node.moduleSpecifier === undefined || !ts.isStringLiteral(node.moduleSpecifier)) continue
        const specifier = node.moduleSpecifier.text
        if (/^(?:node:|dsh-mnemon\/(?:core|kernel|extension-sdk)|dsh-mnemon-source-[^/]+$)/u.test(specifier)) violations.push(`${path}: ${specifier}`)
        if (!specifier.startsWith('.')) continue
        const target = resolve(root, dirname(path), specifier)
        const browserFile = directories.some(directory => target.startsWith(join(root, directory) + sep))
        const normalized = target.split(sep).join('/')
        const contract = normalized.endsWith('/contracts.ts') || normalized.endsWith('/host/protocol.ts')
        const presentation = /\/presentation\/[^/]+\.(?:json|module\.css)$/.test(normalized)
        if (ts.isExportDeclaration(node) && node.isTypeOnly || ts.isImportDeclaration(node) && node.importClause?.isTypeOnly) continue
        if (!browserFile && !contract && !presentation) violations.push(`${path}: ${specifier}`)
        if (contract && /(?:from|import)\s*['"]node:/u.test(readFileSync(target, 'utf8'))) violations.push(`${path}: Node contract ${specifier}`)
      }
    }
    expect(violations).toEqual([])
  })

  it('keeps the shared browser contract free of Node runtime imports', () => {
    const contract = readFileSync(new URL('../src/host/protocol.ts', import.meta.url), 'utf8')
    expect(contract).not.toMatch(/from\s*['"]node:/u)
  })

  it('delegates deployment URLs and transport to the host connection', () => {
    const forbidden = /\bfetch\s*\(|\bXMLHttpRequest\b|\bWebSocket\b|\bEventSource\b|['"]\/(?:api|m\/api|plugins|assets)(?:\/|['"])/gu
    const violations = clientSources().flatMap(({ path, source }) => {
      return [...source.matchAll(forbidden)].map(match => `${path}: ${match[0]}`)
    })
    expect(violations).toEqual([])
  })
})
