import { readFileSync, readdirSync, writeFileSync, existsSync, unlinkSync, rmdirSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

// Link only this package's public self-references to its emitted declarations.
// Independent plugins keep their public package imports and separate artifacts.
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const output = join(root, 'lib', 'types')
const config = JSON.parse(readFileSync(join(root, 'tsconfig.json'), 'utf8'))
const aliases = new Map(Object.entries(config.compilerOptions.paths).filter(([specifier]) => specifier.startsWith('dsh-mnemon/')).map(([specifier, paths]) => {
  if (specifier.includes('*') || paths.length !== 1) throw new Error(`Expected one exact declaration alias: ${specifier}`)
  const target = resolve(output, paths[0].replace(/\.[cm]?tsx?$/u, '.d.ts'))
  if (!existsSync(target)) throw new Error(`Missing bundled declaration for ${specifier}: ${target}`)
  return [specifier, target]
}))

const declarations = new Set()
function link(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const filename = join(directory, entry.name)
    if (entry.isDirectory()) { link(filename); continue }
    if (!entry.name.endsWith('.d.ts')) continue
    declarations.add(filename)
    const source = readFileSync(filename, 'utf8')
    const tree = ts.createSourceFile(filename, source, ts.ScriptTarget.Latest, true)
    const edits = []
    function visit(node) {
      const literal = ts.isImportDeclaration(node) || ts.isExportDeclaration(node)
        ? node.moduleSpecifier
        : ts.isImportTypeNode(node) && ts.isLiteralTypeNode(node.argument)
          ? node.argument.literal : undefined
      if (literal !== undefined && ts.isStringLiteral(literal) && aliases.has(literal.text)) {
        let target = relative(dirname(filename), aliases.get(literal.text)).replaceAll('\\', '/').replace(/\.d\.ts$/u, '.js')
        if (!target.startsWith('.')) target = `./${target}`
        edits.push({ start: literal.getStart(tree), end: literal.end, text: JSON.stringify(target) })
      }
      ts.forEachChild(node, visit)
    }
    visit(tree)
    let linked = source
    for (const edit of edits.sort((a, b) => b.start - a.start)) linked = linked.slice(0, edit.start) + edit.text + linked.slice(edit.end)
    if (linked !== source) writeFileSync(filename, linked)
  }
}

link(output)

// tsc emits declarations for the complete development workspace, including
// independently published plugin entries that the aggregate does not expose.
// Ship the transitive type graph of public entries, not these unrelated roots.
const manifest = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'))
const retained = new Set()
function retain(filename) {
  if (retained.has(filename)) return
  if (!declarations.has(filename)) throw new Error(`Missing public type dependency: ${filename}`)
  retained.add(filename)
  const preprocessed = ts.preProcessFile(readFileSync(filename, 'utf8'), true, true)
  for (const { fileName } of [...preprocessed.importedFiles, ...preprocessed.referencedFiles]) {
    if (!fileName.startsWith('.')) continue
    const target = resolve(dirname(filename), fileName.replace(/(?<!\.d)\.[cm]?[jt]sx?$/u, '.d.ts'))
    retain(target)
  }
}
function visitExport(value) {
  if (typeof value !== 'object' || value === null) return
  for (const [key, entry] of Object.entries(value)) {
    if (key === 'types' && typeof entry === 'string') retain(resolve(root, entry))
    else visitExport(entry)
  }
}
visitExport(manifest.exports)
if (typeof manifest.types === 'string') retain(resolve(root, manifest.types))
for (const filename of declarations) if (!retained.has(filename)) unlinkSync(filename)
function removeEmptyDirectories(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (entry.isDirectory()) removeEmptyDirectories(join(directory, entry.name))
  }
  if (directory !== output && readdirSync(directory).length === 0) rmdirSync(directory)
}
removeEmptyDirectories(output)
console.log(`Linked public declarations; retained ${retained.size}/${declarations.size} public type dependencies.`)
