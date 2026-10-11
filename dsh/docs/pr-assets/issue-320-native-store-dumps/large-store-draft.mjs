// Write the synthetic Mnemon draft used by the issue #320 record: 900 insights of
// about 3.3 KB each, four categories, one tag and one entity per insight.
// Usage: node large-store-draft.mjs <draft.json>
// Import: mnemon --data-dir <dir> --store default import <draft.json> --no-diff
import { writeFileSync } from 'node:fs'

const [target] = process.argv.slice(2)
if (target === undefined) throw new Error('Usage: node large-store-draft.mjs <draft.json>')
const words = 'checkout latency cache queue consumer kafka clickhouse deploy rollback schema index migration payment session token budget review release docs runtime archive space provider recall graph entity edge import export window desktop plugin install'.split(' ')
const pick = (i, n) => Array.from({ length: n }, (_, k) => words[(i * 7 + k * 13) % words.length]).join(' ')
const insights = Array.from({ length: 900 }, (_, i) => ({
  content: `Handoff ${i}: ` + Array.from({ length: 6 }, (_, p) => `Paragraph ${p} for service-${i % 41}: ${pick(i + p, 70)}.`).join(' '),
  category: ['fact', 'decision', 'insight', 'context'][i % 4], importance: 1 + (i % 5), source: 'user',
  tags: [`area-${i % 11}`], entities: [`Service${i % 41}`],
}))
writeFileSync(target, JSON.stringify({ schema_version: '1', source: 'i320-big', insights }))
