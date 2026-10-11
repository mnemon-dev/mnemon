import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { localeExport, readPackageLocales } from './package-locales.mjs'

const result = spawnSync('npm', ['pack', '--dry-run', '--json', '--ignore-scripts'], {
  encoding: 'utf8',
  shell: process.platform === 'win32',
})
if (result.error !== undefined) {
  console.error(result.error.message)
  process.exit(1)
}
if (result.status !== 0) {
  if (result.stderr !== undefined) process.stderr.write(result.stderr)
  process.exit(result.status ?? 1)
}

const parsedPack = JSON.parse(result.stdout)
const pack = Array.isArray(parsedPack) ? parsedPack[0] : Object.values(parsedPack)[0]
const paths = pack.files.map(file => file.path)
const manifest = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'))
const locales = readPackageLocales(fileURLToPath(new URL('../', import.meta.url)), manifest).map(asset => asset.path)
const executables = Object.values(manifest.bin ?? {})
const required = [...executables, ...locales, 'package.json', 'cordis.patch.yml', 'lib/client.js', ...Object.entries(manifest.exports)
  .filter(([name]) => name !== './package.json' && name !== localeExport)
  .flatMap(([, value]) => [value.default.slice(2), value.types.slice(2)])]

const allowedRootFiles = new Set(['package.json', 'cordis.patch.yml', 'LICENSE', 'README.md', 'README.zh-CN.md', 'SECURITY.md', 'THIRD_PARTY_NOTICES.md'])
const missing = required.filter(path => !paths.includes(path))
const unexpected = paths.filter(path => !allowedRootFiles.has(path) && !executables.includes(path) && !locales.includes(path) && !(/^lib\/.+\.(?:js|d\.ts)$/.test(path)))
const clientBundle = readFileSync(new URL('../lib/client.js', import.meta.url), 'utf8')
const hostLeaks = ['require("node:', "require('node:", '#region src/host/version-updates.ts', '#region src/host/rpc.ts']
  .filter(pattern => clientBundle.includes(pattern))
const readmeFiles = ['README.md', 'README.zh-CN.md']
const relativeReadmeImages = readmeFiles.flatMap((path) => {
  const source = readFileSync(new URL(`../${path}`, import.meta.url), 'utf8')
  const markdownImages = [...source.matchAll(/!\[[^\]]*\]\(([^)\s]+)(?:\s+['"][^)]*['"])?\)/g)]
  const htmlImages = [...source.matchAll(/<img\b[^>]*\bsrc=['"]([^'"]+)['"]/gi)]
  return [...markdownImages, ...htmlImages]
    .map(match => match[1])
    .filter(source => !/^https:\/\//.test(source))
    .map(source => `${path}: ${source}`)
})

// Core/Host and the shared page kit only; Source/Provider implementations must
// ship in their own artifacts. Keep a bounded budget, not the old monolith size.
// Includes the bounded legacy Session copy-repair executable and its bilingual
// recovery instructions; Source implementations remain independent artifacts.
// Runtime archive preflight and compensation add bounded Host recovery code.
// The review publication/guard helper adds about 4 KB of Host-only code.
// Audited legacy repair, including recorded-ID chain proofs, stays in the
// maintenance executable. Its measured growth is ~24 KB over the null-name
// repair baseline; retain ~2.2 KB for the separately verified Host grant fix.
// No Source implementation enters the Starter.
// Bounded idle review adds Host checkpoint/receipt handling, budgets and the
// bilingual settings/status UI: measured 1,335,573 bytes (+19,717), with no
// Source implementation included. Retain less than 3 KB of headroom.
// Public DSH session binding, reactive settings context and list-slot guards
// bring the measured package to 1,338,363 bytes (+2,624). Keep less than 1 KB
// of headroom; Source implementations still ship only in their own packages.
// Config-backed settings, retained-backup recovery, composition replay and
// icon/Session compatibility bring the measured baseline to 1,370,778 bytes.
// Explicit Team compatibility, complete user checkpoints and bilingual settings
// add 2,872 bytes, for 1,373,650 total and 2,350 bytes of headroom. Source and
// framework implementations stay external; the artifact still has 49 files.
// The Memory composition board drawn from component declarations, component
// pages with declared options, dependency planning, change feedback with Undo
// and their bilingual copy bring the measured package to 1,441,661 bytes
// (+86,272 over the 1,355,389 measured before it). Keep less
// than 3 KB of headroom; components still ship only in their own packages.
// The component settings region, the shipped components' settings on their
// pages, the shared Apply and apply-at-once helpers and the storage section
// bring it to 1,453,498 bytes (+11,837). The Status card region and the
// Memory System's names drawn from component declarations bring it to
// 1,458,675 bytes (+5,177). Component pages on DSH's row pages, with their
// contribution boundary, bring it to 1,465,659 bytes (+6,984). Keep less
// than 3 KB of headroom.
// Starter dependency preparation adds a small native group entry and its public
// declarations: measured 1,471,089 bytes. Framework and component code stay external.
// The shared page controls (search, select, write receipt, task Agent tag), one
// Save to memory dialog with its receipt, the turn memory bar's items and their
// bilingual copy, and the README's live showcase bring the measured package to
// 1,488,847 bytes (+17,758). Keep less than 3 KB of headroom.
// Desktop windows on the local channels, the remote read-only reasons in both
// languages and the Status version read measure 1,491,026 bytes. The README's
// installation quick start with its recording brings it to 1,492,741 bytes.
// The Starter's component group entry (`dsh-mnemon/bundle`) measures 1,494,791 bytes.
// Idle review's one-layer rule and its runtime memory switch (#319) measure 1,498,810 bytes.
// The conversation tab's workspace from DSH's registry with its task Agents (#326)
// and the user turn a refused subagent step retries with (#327) bring it to 1,504,848 bytes.
// Updating the Starter through DSH's own plugin installer, with the version dialog
// reopened after DSH swaps in the new client, the restart reminder on every Memory
// System page and how an update ended, from the Profile's record (#325), bring it to 1,520,447 bytes.
// The Entities page's two lists, loading placeholders and copy, bundled from the Memory
// Spaces presentation, bring it to 1,524,506 bytes.
const maximumUnpackedBytes = 1_525_500

if (missing.length > 0 || unexpected.length > 0 || hostLeaks.length > 0 || relativeReadmeImages.length > 0 || pack.unpackedSize > maximumUnpackedBytes) {
  if (missing.length > 0) console.error(`Missing package files:\n${missing.map(path => `- ${path}`).join('\n')}`)
  if (unexpected.length > 0) console.error(`Unexpected package files:\n${unexpected.map(path => `- ${path}`).join('\n')}`)
  if (hostLeaks.length > 0) console.error(`Host-only code leaked into lib/client.js:\n${hostLeaks.map(pattern => `- ${pattern}`).join('\n')}`)
  if (relativeReadmeImages.length > 0) console.error(`README images must use absolute HTTPS URLs so npm can render assets excluded from the package:\n${relativeReadmeImages.map(image => `- ${image}`).join('\n')}`)
  if (pack.unpackedSize > maximumUnpackedBytes) console.error(`Unpacked package is ${pack.unpackedSize} bytes; expected at most ${maximumUnpackedBytes}.`)
  process.exit(1)
}

console.log(`Verified package contents: ${pack.entryCount} files, ${pack.size} packed bytes, ${pack.unpackedSize} unpacked bytes.`)
