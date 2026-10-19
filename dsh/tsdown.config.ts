import { readFile } from 'node:fs/promises'
import { dirname, relative, resolve as resolvePath } from 'node:path'
import { fileURLToPath } from 'node:url'
import type { UserConfig, TsdownPlugin } from 'tsdown'
import { transform } from 'lightningcss'
import ts from 'typescript'

const PLUGIN_ID = 'dsh-mnemon'
const PROJECT_ROOT = dirname(fileURLToPath(import.meta.url))
const CLIENT_EXTERNALS = [
  /^react(?:\/.*)?$/,
  /^react-dom(?:\/.*)?$/,
  /^@deepseek-ai\/dsh-client-ui-primitives(?:\/.*)?$/,
]
const CSS_VIRTUAL_PREFIX = '\0dsh-mnemon-css:'
const CSS_VIRTUAL_SUFFIX = '.mjs'

const host: UserConfig = {
  name: PLUGIN_ID,
  entry: {
    index: 'src/index.ts', starter: 'src/starter.ts', bundle: 'src/bundle.ts', core: 'src/core/plugin.ts', contracts: 'src/core/contracts/index.ts',
    'extension-sdk': 'src/sdk/index.ts', testing: 'src/sdk/testing.ts',
  },
  outDir: 'lib',
  format: ['esm'],
  platform: 'node',
  target: 'es2024',
  fixedExtension: false,
  dts: false,
  clean: true,
  deps: { neverBundle: true },
  plugins: [standardDecoratorsPlugin()],
}

const client: UserConfig = {
  name: `${PLUGIN_ID}/client`,
  entry: { client: 'src/client/index.ts' },
  outDir: 'lib',
  format: 'cjs',
  fixedExtension: false,
  outExtensions: () => ({ js: '.js' }),
  platform: 'browser',
  target: 'es2022',
  dts: false,
  sourcemap: false,
  clean: false,
  deps: {
    neverBundle: CLIENT_EXTERNALS,
    alwaysBundle: [/^dsh-mnemon-source-[^/]+\/presentation\//],
  },
  define: {
    'process.env.NODE_ENV': JSON.stringify(process.env.NODE_ENV ?? 'production'),
  },
  plugins: [clientCssPlugin()],
  outputOptions: {
    banner: `window.__ModuleLoader__.load({ id: ${JSON.stringify(PLUGIN_ID)}, factory: (require) => {`,
    footer: 'return module.exports; } });',
    intro: 'var module = { exports: {} }; var exports = module.exports;',
  },
}

export function clientCssPlugin(injectStyles = true): TsdownPlugin {
  return {
    name: 'dsh-mnemon-css-modules-inline',
    resolveId(source: string, importer: string | undefined) {
      if (source === 'dsh-mnemon/client') return resolvePath(PROJECT_ROOT, 'src/client/extension-sdk.ts')
      if (!source.endsWith('.module.css')) return null
      const absolute = source.startsWith('dsh-mnemon-source-')
        ? fileURLToPath(import.meta.resolve(source))
        : importer === undefined ? source : resolveAssetPath(source, importer)
      return CSS_VIRTUAL_PREFIX + absolute + CSS_VIRTUAL_SUFFIX
    },
    async load(virtualId: string) {
      if (!virtualId.startsWith(CSS_VIRTUAL_PREFIX)) return null
      const fileId = virtualId.slice(CSS_VIRTUAL_PREFIX.length, -CSS_VIRTUAL_SUFFIX.length)
      this.addWatchFile(fileId)
      const source = await readFile(fileId)
      const filename = portableRelativePath(PROJECT_ROOT, fileId)
      const { code, exports: cssExports } = transform({
        // Default Source assets share the established page-kit class namespace.
        filename: presentationNamespace(filename),
        code: source,
        cssModules: { pattern: '[hash]_[local]' },
        minify: true,
      })
      const classMap = stableCssClassMap(cssExports)
      const tagId = filename.match(/dsh-mnemon-source-[^/]+\/presentation\/[^/]+$/)?.[0] ?? `${PLUGIN_ID}/${filename}`
      return [
        `const css = ${JSON.stringify(code.toString())};`,
        `const tagId = ${JSON.stringify(tagId)};`,
        `if (${injectStyles} && typeof document !== "undefined") {`,
        '  let tag = document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]");',
        '  if (!tag) { tag = document.createElement("style"); tag.dataset.pluginCss = tagId; document.head.appendChild(tag); }',
        '  if (tag.textContent !== css) tag.textContent = css;',
        '}',
        `export default ${JSON.stringify(classMap)};`,
      ].join('\n')
    },
  }
}

/** Lower standard decorators because Rolldown currently preserves their syntax. */
function standardDecoratorsPlugin(): TsdownPlugin {
  return {
    name: 'dsh-mnemon-standard-decorators',
    transform(code: string, id: string) {
      if (!id.replaceAll('\\', '/').endsWith('/src/host/remote-rpc.ts')) return null
      const output = ts.transpileModule(code, {
        fileName: id,
        compilerOptions: {
          target: ts.ScriptTarget.ES2022,
          module: ts.ModuleKind.ESNext,
          verbatimModuleSyntax: true,
        },
      })
      return { code: output.outputText, map: null }
    },
  }
}

function resolveAssetPath(source: string, importer: string): string {
  return resolvePath(dirname(importer), source)
}

export function portableRelativePath(root: string, path: string): string {
  return relative(root, path).replaceAll('\\', '/')
}

export function presentationNamespace(filename: string): string {
  if (/dsh-mnemon-source-[^/]+\/presentation\/page\.module\.css$/.test(filename)) return 'src/client/MnemonView.module.css'
  if (/dsh-mnemon-source-[^/]+\/presentation\/sidebar\.module\.css$/.test(filename)) return 'src/client/MnemonSidebarView.module.css'
  return filename
}

export function stableCssClassMap(cssExports: Record<string, { name: string }> | void): Record<string, string> {
  return Object.fromEntries(Object.entries(cssExports ?? {})
    .sort(([left], [right]) => left < right ? -1 : left > right ? 1 : 0)
    .map(([local, exported]) => [local, exported.name]))
}

export default [host, client]
