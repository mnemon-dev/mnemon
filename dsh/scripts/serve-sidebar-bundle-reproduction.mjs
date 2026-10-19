import { resolve } from 'node:path'
import { servePackedWebFixture } from './fixtures/packed-web-fixture.mjs'

// Exercise published DSH and all packed Mnemon plugins in disposable storage.
// Only the model's choice is scripted; mnemon_status runs through native tools.
await servePackedWebFixture({
  issue: 'sidebar-bundle',
  options: {
    'skin-center': { type: 'string' },
    'skin-market': { type: 'string' },
  },
  prepare({ values }) {
    return {
      packages: [values['skin-center'], values['skin-market']].filter(Boolean).map(path => resolve(path)),
      metadata: { purpose: 'Native Sidebar navigation and independent bundle component activation' },
    }
  },
  complete({ receipts }) {
    if (receipts.length === 0) return { reply: { name: 'mnemon_status', args: {} } }
    return { reply: `Native Mnemon status receipt:\n\n${JSON.stringify(receipts.at(-1).result, null, 2)}` }
  },
})
