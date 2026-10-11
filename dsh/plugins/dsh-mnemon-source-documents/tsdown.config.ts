import { readFile } from 'node:fs/promises'
import { basename, dirname, resolve } from 'node:path'
import { transform } from 'lightningcss'
import { defineConfig } from 'tsdown'

export default defineConfig([
{
  entry: ['src/contracts.ts', 'src/index.ts'], outDir: 'lib', format: 'esm', platform: 'node',
  target: 'es2024', dts: true, clean: true, fixedExtension: false, deps: { neverBundle: true },
},
{
  entry: { client: 'src/client.ts' }, outDir: 'lib', format: 'cjs', platform: 'browser',
  target: 'es2022', dts: false, clean: false, fixedExtension: false,
  outExtensions: () => ({ js: '.js', dts: '.d.ts' }),
  deps: { neverBundle: true, alwaysBundle: ['markdown-to-jsx'], onlyBundle: ['markdown-to-jsx'] },
  define: { 'process.env.NODE_ENV': JSON.stringify('production') },

  plugins: [{
    name: 'source-presentation',
    resolveId(source: string, importer: string | undefined) {
      if (!source.endsWith('.module.css') || importer === undefined) return null
      return '\0source-css:' + resolve(dirname(importer), source) + '.mjs'
    },
    async load(id: string) {
      if (!id.startsWith('\0source-css:')) return null
      const path = id.slice('\0source-css:'.length, -'.mjs'.length)
      this.addWatchFile(path)
      const file = basename(path)
      const filename = file === 'page.module.css' ? 'src/client/MnemonView.module.css' : 'src/client/MnemonSidebarView.module.css'
      const { code, exports } = transform({ filename, code: await readFile(path), cssModules: { pattern: '[hash]_[local]' }, minify: true })
      const classes = Object.fromEntries(Object.entries(exports ?? {}).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0).map(([key, value]) => [key, value.name]))
      return `const css = ${JSON.stringify(code.toString())};
const id = ${JSON.stringify('dsh-mnemon-source-documents/presentation/' + file)};
if (typeof document !== 'undefined') {
  let tag = document.querySelector('style[data-plugin-css=' + JSON.stringify(id) + ']');
  if (!tag) { tag = document.createElement('style'); tag.dataset.pluginCss = id; document.head.appendChild(tag); }
  if (tag.textContent !== css) tag.textContent = css;
}
export default ${JSON.stringify(classes)};`
    },
  }],
  outputOptions: {
    banner: 'window.__ModuleLoader__.load({ id: "dsh-mnemon-source-documents", factory: (require) => {',
    intro: 'var module = { exports: {} }; var exports = module.exports;',
    footer: 'return module.exports; } });',
  },
},
])
