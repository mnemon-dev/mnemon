import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { join, resolve } from 'node:path';
import { frameworkPins } from '../../issue-261-dsh-slots/harness/framework-pins.mjs';

const args = new Map();
for (let i = 2; i < process.argv.length; i += 2) args.set(process.argv[i], process.argv[i + 1]);
assert(args.has('--root'), 'supply a disposable --root outside the source checkout');
const root = resolve(args.get('--root'));
const version = args.get('--framework-version') ?? '0.1.7-alpha.1';
assert(['0.1.5-rc.2', '0.1.6-alpha.2', '0.1.7-alpha.1'].includes(version));
const current = version === '0.1.7-alpha.1';
await mkdir(root, { recursive: true });
const manifest = {
  name: 'issue267-clean-public-framework', version: '0.0.0', private: true, type: 'module',
  dependencies: {
    '@deepseek-ai/dsh': version,
    '@deepseek-ai/dsh-web-app': version,
    '@deepseek-ai/cordis': current ? '4.0.3' : '4.0.2',
    ...(current ? { '@deepseek-ai/schemastery': '3.18.3' } : {}),
    react: '18.3.1', 'react-dom': '18.3.1',
  },
};
await writeFile(join(root, 'package.json'), JSON.stringify(manifest, null, 2) + '\n');
const env = { ...process.env };
for (const key of ['NODE_PATH', 'NODE_OPTIONS', 'npm_config_legacy_peer_deps', 'NPM_CONFIG_LEGACY_PEER_DEPS']) delete env[key];
const registry = 'https://registry.npmjs.org';
const argv = ['install', '--registry', registry, '--legacy-peer-deps=false', '--no-audit', '--no-fund'];
const log = [];
await new Promise((done, reject) => {
  const child = spawn('npm', argv, { cwd: root, env, stdio: ['ignore', 'pipe', 'pipe'] });
  child.stdout.on('data', chunk => log.push(chunk));
  child.stderr.on('data', chunk => log.push(chunk));
  child.once('error', reject);
  child.once('exit', async code => {
    await writeFile(join(root, 'install.log'), Buffer.concat(log));
    code === 0 ? done() : reject(new Error(`Public framework install exited ${code}; see ${join(root, 'install.log')}`));
  });
});
const pins = await frameworkPins(root, version);
const dsh = Object.entries(pins).filter(([name]) => name.startsWith('@deepseek-ai/dsh'));
assert(dsh.length > 200);
const lockBytes = await readFile(join(root, 'package-lock.json'));
const provenance = {
  version, root, argv, legacyPeers: false, registry,
  dshPackageCount: dsh.length,
  cordis: pins['@deepseek-ai/cordis'], schemastery: pins['@deepseek-ai/schemastery'],
  lockSha256: createHash('sha256').update(lockBytes).digest('hex'),
  node: process.version, platform: process.platform, arch: process.arch,
  completedAt: new Date().toISOString(),
};
await writeFile(join(root, 'framework-pins.json'), JSON.stringify(pins, null, 2) + '\n');
await writeFile(join(root, 'framework-provenance.json'), JSON.stringify(provenance, null, 2) + '\n');
console.log(JSON.stringify(provenance));
