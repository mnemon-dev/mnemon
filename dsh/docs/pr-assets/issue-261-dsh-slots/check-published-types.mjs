// Validate production sources against a clean, exact published DSH cohort.
// Usage: node check-published-types.mjs <mnemon-checkout> <sdk-install-root> <report.json>
// Install the matching dsh-client-store and dsh-client-ui-slots contracts too.
import assert from 'node:assert/strict'
import { readdir, readFile, writeFile, realpath } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { resolve, join } from 'node:path'
assert.equal(process.argv.length, 5, 'Expected Mnemon checkout, public SDK installation root, and JSON report path')
const [rootArg, sdkArg, report] = process.argv.slice(2).map(value => resolve(value))
const root = await realpath(rootArg)
const sdkRoot = await realpath(sdkArg)
const require = createRequire(join(root, 'package.json'))
const ts = require('typescript')
const base = JSON.parse(await readFile(join(root, 'tsconfig.json'), 'utf8'))
const paths = Object.fromEntries(Object.entries(base.compilerOptions.paths).map(([key, values]) => [key, values.map(value => resolve(root, value))]))
const packages = []
for (const name of await readdir(join(sdkRoot, 'node_modules/@deepseek-ai'))) {
  const directory = join(sdkRoot, 'node_modules/@deepseek-ai', name)
  const pkg = JSON.parse(await readFile(join(directory, 'package.json'), 'utf8'))
  packages.push({name:pkg.name, version:pkg.version})
  for (const [key, target] of Object.entries(pkg.exports ?? {})) {
    if (typeof target !== 'object' || target === null || typeof target.types !== 'string') continue
    assert(!target.types.includes('/src/'), 'Use only published declarations')
    paths[pkg.name + (key === '.' ? '' : key.slice(1))] = [resolve(directory, target.types)]
  }
  if (!paths[pkg.name] && pkg.types) paths[pkg.name] = [resolve(directory, pkg.types)]
}
const cohort = packages.find(pkg => pkg.name === '@deepseek-ai/dsh')?.version
assert(cohort, 'The SDK root must include the published DSH CLI')
assert(packages.every(pkg => !pkg.name.startsWith('@deepseek-ai/dsh') || pkg.version === cohort), 'The SDK root contains mixed DSH cohorts')
const config = ts.parseJsonConfigFileContent({
  ...base,
  compilerOptions: {...base.compilerOptions, paths, types:['node'], noEmit:true, typeRoots:[join(root,'node_modules/@types')]},
  include:['src/**/*.ts','src/**/*.tsx','plugins/*/src/**/*.ts','plugins/*/src/**/*.tsx']
}, ts.sys, root)
const program = ts.createProgram(config.fileNames, config.options)
const diagnostics = ts.getPreEmitDiagnostics(program)
const sourceFiles = program.getSourceFiles().map(file => file.fileName)
const leaked = sourceFiles.filter(file => file.includes('node_modules') && /@deepseek-ai(?:\+|\/)(?:dsh|cordis)/.test(file) && !file.startsWith(sdkRoot + '/'))
const rendered = ts.formatDiagnosticsWithColorAndContext(diagnostics, {getCanonicalFileName:value=>value,getCurrentDirectory:()=>root,getNewLine:()=> '\n'})
await writeFile(report, JSON.stringify({root,sdkRoot,packages,diagnosticCount:diagnostics.length,diagnostics:ts.formatDiagnostics(diagnostics,{getCanonicalFileName:value=>value,getCurrentDirectory:()=>root,getNewLine:()=> '\n'}), leaked,sourceFiles},null,2)+'\n')
console.log(rendered)
console.log(JSON.stringify({sdkRoot, productionFiles:config.fileNames.length,diagnostics:diagnostics.length, foreignDshDeclarations:leaked.length,report}))
process.exitCode = diagnostics.length || leaked.length ? 1 : 0
