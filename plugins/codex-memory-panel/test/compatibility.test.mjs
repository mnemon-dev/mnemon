import test from 'node:test';
import assert from 'node:assert/strict';
import { normalize } from '../server/adapter.mjs';

test('optional and additional engine fields remain compatible', () => {
  const result = normalize([{ id: 'fixture', content: 'Example knowledge', future_field: true }], { total_insights: 1, future_metric: 2 }, '0.2.99', 'example', ['example']);
  assert.equal(result.compatibility, 'interface-validated');
  assert.equal(result.memories[0].category, 'unknown');
  assert.deepEqual(result.memories[0].tags, []);
  assert.equal(result.stats.edges, null);
});

test('missing required engine fields are rejected rather than rendered empty', () => {
  assert.throws(() => normalize([], {}, '0.2.10', 'example', ['example']), /interface changed/);
  assert.throws(() => normalize([{ id: 'fixture' }], { total_insights: 1 }, '0.2.10', 'example', ['example']), /missing id\/content/);
});
