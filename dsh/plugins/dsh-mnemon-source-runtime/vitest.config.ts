import { defineConfig } from 'vitest/config'

export default defineConfig({
  resolve: { dedupe: ['react', 'react-dom'] },
  // The independent-package check runs these real Source/UI tests beside other
  // packages' suites. Keep a bounded timeout without making host scheduler
  // contention look like a functional failure.
  test: { testTimeout: 15_000, server: { deps: { inline: ['@deepseek-ai/dsh-client-ui-primitives'] } } },
})
