import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import { join } from 'node:path';

/** Snapshot physically installed public framework packages; never optional platforms from a lockfile. */
export async function frameworkPins(root, expectedVersion) {
  const pins = {};
  for (const directory of await readdir(join(root, 'node_modules/@deepseek-ai'))) {
    let manifest;
    try { manifest = JSON.parse(await readFile(join(root, 'node_modules/@deepseek-ai', directory, 'package.json'), 'utf8')); }
    catch (error) { if (error.code === 'ENOENT') continue; throw error; }
    if (manifest.name.startsWith('@deepseek-ai/dsh')) assert.equal(manifest.version, expectedVersion, `mixed framework package: ${directory}`);
    pins[`@deepseek-ai/${directory}`] = manifest.version;
  }
  const lock = JSON.parse(await readFile(join(root, 'package-lock.json'), 'utf8'));
  for (const [path, entry] of Object.entries(lock.packages)) {
    if (!path.startsWith('node_modules/') || path.includes('/node_modules/') || !entry.peer) continue;
    const name = path.slice('node_modules/'.length);
    if (name.startsWith('@deepseek-ai/')) continue;
    try { pins[name] = JSON.parse(await readFile(join(root, path, 'package.json'), 'utf8')).version; }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
  return pins;
}
