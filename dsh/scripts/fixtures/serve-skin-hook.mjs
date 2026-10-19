import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { servePackedWebFixture } from './packed-web-fixture.mjs'

const packageName = 'issue273-mnemon-skin-fixture'
const selector = '[data-dsh-plugin="dsh-mnemon"][data-dsh-part="mnemon-view"]'
const css = `${selector} {
  --mn-bg: #e0f7ed;
  --mn-backdrop: #c4eadc;
  --mn-surface: linear-gradient(115deg, rgba(35, 166, 136, .18), rgba(238, 195, 88, .16)) #e0f7ed;
}\n`

await servePackedWebFixture({
  issue: 273,
  options: { 'skin-order': { type: 'string', default: 'before' } },
  async prepare({ values, state, privateFile, run }) {
    const order = values['skin-order']
    assert(['before', 'after', 'none'].includes(order), '--skin-order must be before, after or none')
    if (order === 'none') return { metadata: { skin: { order, installed: false } } }
    const directory = join(state, 'skin-plugin')
    await mkdir(directory, { mode: 0o700 })
    await writeFile(join(directory, 'package.json'), JSON.stringify({
      name: packageName, version: '0.0.0', private: true, type: 'module',
      main: './index.js', exports: { '.': './index.js', './client': './client.js', './package.json': './package.json' },
      files: ['index.js', 'client.js', 'skin.css', 'cordis.patch.yml'],
      dsh: { bundle: { patch: './cordis.patch.yml' }, client: { inject: ['dsh-mnemon'], external: ['dsh-mnemon'], platform: 'web' } },
    }, null, 2) + '\n')
    await writeFile(join(directory, 'index.js'), 'export function apply() {}\n')
    await writeFile(join(directory, 'skin.css'), css)
    await writeFile(join(directory, 'cordis.patch.yml'), `- insert:\n    - id: issue273-skin\n      name: ${packageName}\n`)
    // A normal published Client bundle owns this style and removes it on
    // disposal. The public package dependency materializes Mnemon first;
    // prepend/append then exercise both ordinary cascade source orders.
    await writeFile(join(directory, 'client.js'), `window.__ModuleLoader__.load({
  id: ${JSON.stringify(packageName)},
  factory: require => {
    require('dsh-mnemon');
    return { apply(ctx) {
      ctx.effect(() => {
        const style = document.createElement('style');
        style.dataset.plugin = ${JSON.stringify(packageName)};
        style.dataset.pluginCss = ${JSON.stringify(packageName + '/skin.css')};
        style.dataset.fixtureStyleOrder = ${JSON.stringify(order)};
        style.textContent = ${JSON.stringify(css)};
        document.head.${order === 'before' ? 'prepend' : 'append'}(style);
        return () => style.remove();
      }, 'Issue 273 installed skin stylesheet');
    } };
  },
});\n`)
    const packed = JSON.parse(await run('npm', ['pack', '--ignore-scripts', '--json'], { cwd: directory }))[0]
    const tarball = join(directory, packed.filename)
    await privateFile('skin-manifest.json', JSON.stringify({ package: packageName, order, selector, css, sha256: createHash('sha256').update(await readFile(tarball)).digest('hex') }, null, 2) + '\n')
    return { packages: [tarball], metadata: { skin: { package: packageName, order, selector, installed: true } } }
  },
  complete() {
    return { reply: 'The fixture conversation is working. Open Mnemon to inspect the installed skin. The model makes no visual claims; compare the real view and its declared surface tokens.' }
  },
})
