import { configDefaults, defineConfig } from 'vitest/config'
import { fileURLToPath } from 'node:url'
import { clientCssPlugin } from './tsdown.config.ts'

export default defineConfig({
  // Compile the real class maps; JSDOM does not implement the browser CSS layout engine.
  plugins: [{ ...clientCssPlugin(false), enforce: 'pre' } as never],
  resolve: {
    alias: Object.entries({
      'dsh-mnemon/client': fileURLToPath(new URL('./src/client/extension-sdk.ts', import.meta.url)),
      'dsh-mnemon-source-runtime/client': fileURLToPath(new URL('./plugins/dsh-mnemon-source-runtime/src/client.ts', import.meta.url)),
      'dsh-mnemon-source-documents/client': fileURLToPath(new URL('./plugins/dsh-mnemon-source-documents/src/client.ts', import.meta.url)),
      'dsh-mnemon-source-memory-spaces/client': fileURLToPath(new URL('./plugins/dsh-mnemon-source-memory-spaces/src/client.ts', import.meta.url)),
      'dsh-mnemon-source-memory-spaces/testing': fileURLToPath(new URL('./plugins/dsh-mnemon-source-memory-spaces/src/testing.ts', import.meta.url)),
      'dsh-mnemon-source-memory-spaces/native-cli': fileURLToPath(new URL('./plugins/dsh-mnemon-source-memory-spaces/src/native-cli.ts', import.meta.url)),
      'dsh-mnemon-source-memory-spaces/provider-sdk': fileURLToPath(new URL('./plugins/dsh-mnemon-source-memory-spaces/src/provider-sdk.ts', import.meta.url)),
      'dsh-mnemon/extension-sdk': fileURLToPath(new URL('./src/sdk/index.ts', import.meta.url)),
      'dsh-mnemon/contracts': fileURLToPath(new URL('./src/core/contracts/index.ts', import.meta.url)),
      'dsh-mnemon/testing': fileURLToPath(new URL('./src/sdk/testing.ts', import.meta.url)),
      'dsh-mnemon-source-runtime/contracts': fileURLToPath(new URL('./plugins/dsh-mnemon-source-runtime/src/contracts.ts', import.meta.url)),
      'dsh-mnemon-source-documents/contracts': fileURLToPath(new URL('./plugins/dsh-mnemon-source-documents/src/contracts.ts', import.meta.url)),
      'dsh-mnemon-source-memory-spaces/contracts': fileURLToPath(new URL('./plugins/dsh-mnemon-source-memory-spaces/src/contracts.ts', import.meta.url)),
      'dsh-mnemon-source-runtime': fileURLToPath(new URL('./plugins/dsh-mnemon-source-runtime/src/index.ts', import.meta.url)),
      'dsh-mnemon-source-documents': fileURLToPath(new URL('./plugins/dsh-mnemon-source-documents/src/index.ts', import.meta.url)),
      'dsh-mnemon-source-memory-spaces': fileURLToPath(new URL('./plugins/dsh-mnemon-source-memory-spaces/src/index.ts', import.meta.url)),
      'dsh-mnemon-strategy-default-three-tier/extension-sdk': fileURLToPath(new URL('./plugins/dsh-mnemon-strategy-default-three-tier/src/extension-sdk.ts', import.meta.url)),
      'dsh-mnemon-strategy-scoped': fileURLToPath(new URL('./plugins/dsh-mnemon-strategy-scoped/src/index.ts', import.meta.url)),
      'dsh-mnemon-strategy-light-context': fileURLToPath(new URL('./plugins/dsh-mnemon-strategy-light-context/src/index.ts', import.meta.url)),
      'dsh-mnemon-strategy-auto-capture': fileURLToPath(new URL('./plugins/dsh-mnemon-strategy-auto-capture/src/index.ts', import.meta.url)),
      'dsh-mnemon-strategy-default-three-tier': fileURLToPath(new URL('./plugins/dsh-mnemon-strategy-default-three-tier/src/index.ts', import.meta.url)),
      'dsh-mnemon-strategy-general': fileURLToPath(new URL('./plugins/dsh-mnemon-strategy-general/src/index.ts', import.meta.url)),
      'dsh-mnemon-provider-mnemon-native': fileURLToPath(new URL('./plugins/dsh-mnemon-provider-mnemon-native/src/index.ts', import.meta.url)),
      'dsh-mnemon-provider-openviking': fileURLToPath(new URL('./plugins/dsh-mnemon-provider-openviking/src/index.ts', import.meta.url)),
      'dsh-mnemon-provider-honcho': fileURLToPath(new URL('./plugins/dsh-mnemon-provider-honcho/src/index.ts', import.meta.url)),
      'dsh-mnemon-provider-mem0': fileURLToPath(new URL('./plugins/dsh-mnemon-provider-mem0/src/index.ts', import.meta.url)),
      'dsh-mnemon-provider-hindsight': fileURLToPath(new URL('./plugins/dsh-mnemon-provider-hindsight/src/index.ts', import.meta.url)),
      'dsh-mnemon-provider-holographic': fileURLToPath(new URL('./plugins/dsh-mnemon-provider-holographic/src/index.ts', import.meta.url)),
      'dsh-mnemon-provider-retaindb': fileURLToPath(new URL('./plugins/dsh-mnemon-provider-retaindb/src/index.ts', import.meta.url)),
      'dsh-mnemon-provider-byterover': fileURLToPath(new URL('./plugins/dsh-mnemon-provider-byterover/src/index.ts', import.meta.url)),
      'dsh-mnemon-provider-supermemory': fileURLToPath(new URL('./plugins/dsh-mnemon-provider-supermemory/src/index.ts', import.meta.url)),
    }).map(([name, replacement]) => ({ find: new RegExp('^' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$'), replacement })),
    // Keep UI packages on Mnemon's React instance just as the browser bundle does.
    dedupe: ['react', 'react-dom'],
  },
  test: {
    environment: 'node',
    // These tests must execute outside the workspace, against packed artifacts.
    exclude: [...configDefaults.exclude, 'scripts/fixtures/**'],
    coverage: { enabled: false },
    server: {
      deps: {
        inline: ['@deepseek-ai/dsh-client-ui-primitives'],
      },
    },
  },
})
