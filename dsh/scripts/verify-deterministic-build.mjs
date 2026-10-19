import { createHash } from 'node:crypto'
import { readdir, readFile } from 'node:fs/promises'
import { dirname, join, relative, resolve } from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const outputRoot = join(root, 'lib')

function build() {
  const npmExecPath = process.env.npm_execpath
  if (npmExecPath === undefined) throw new Error('Run this check through the package manager')
  const result = spawnSync(process.execPath, [npmExecPath, 'run', 'build'], { cwd: root, stdio: 'inherit' })
  if (result.status !== 0) process.exit(result.status ?? 1)
}

async function outputHashes(directory = outputRoot) {
  const hashes = new Map()
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) {
      for (const [name, hash] of await outputHashes(path)) hashes.set(name, hash)
    } else if (entry.isFile()) {
      const name = relative(outputRoot, path).replaceAll('\\', '/')
      hashes.set(name, createHash('sha256').update(await readFile(path)).digest('hex'))
    }
  }
  return hashes
}

build()
const first = await outputHashes()
build()
const second = await outputHashes()

const names = [...new Set([...first.keys(), ...second.keys()])].sort()
const changed = names.filter(name => first.get(name) !== second.get(name))
if (changed.length > 0) {
  console.error(`Build output is not deterministic:\n${changed.map(name => `- ${name}`).join('\n')}`)
  process.exit(1)
}

console.log(`Verified deterministic output for ${names.length} files.`)
