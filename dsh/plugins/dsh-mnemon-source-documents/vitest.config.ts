import { defineConfig } from 'vitest/config'

export default defineConfig({
  resolve: { dedupe: ['react', 'react-dom'] },
  // The independent-package check runs this real filesystem/UI suite beside
  // the 4096-document pressure fixture. Keep a bounded timeout without making
  // host scheduler contention look like a functional failure.
  test: { testTimeout: 15_000, server: { deps: { inline: ['@deepseek-ai/dsh-client-ui-primitives'] } } },
})
