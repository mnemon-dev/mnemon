import { defineConfig } from 'tsdown'

export default defineConfig({
  entry: ['src/external-source.ts', 'src/external-strategy.ts', 'src/external-strategy-extension.ts', 'src/external-provider.ts'],
  outDir: 'lib', format: 'esm', platform: 'node', target: 'es2024',
  dts: true, clean: true, fixedExtension: false, deps: { neverBundle: true },
})
