import { defineConfig } from 'tsdown'

export default defineConfig({
  entry: ['src/index.ts', 'src/descriptor.ts'], outDir: 'lib', format: 'esm', platform: 'node',
  target: 'es2024', dts: true, clean: true, fixedExtension: false, deps: { neverBundle: true },
})
