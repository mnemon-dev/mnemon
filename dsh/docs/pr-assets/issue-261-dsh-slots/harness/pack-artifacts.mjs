import { readFile, writeFile, mkdir, readdir } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { resolve, join } from 'node:path';

const args = new Map();
for (let i = 2; i < process.argv.length; i += 2) args.set(process.argv[i], process.argv[i + 1]);
const source = resolve(args.get('--source'));
const output = resolve(args.get('--output'));
await mkdir(output, { recursive: true });
const git = (...argv) => spawnSync('git', argv, { cwd: source, encoding: 'utf8' }).stdout.trim();
const provenance = { source, commit: git('rev-parse', 'HEAD'), status: git('status', '--porcelain=v1'), capturedAt: new Date().toISOString(), node: process.version };
await writeFile(join(output, 'source-diff.patch'), git('diff', '--binary', 'HEAD') + '\n');
const dirs = [source, ...(await readdir(join(source, 'plugins'))).filter(name => name.startsWith('dsh-mnemon-')).sort().map(name => join(source, 'plugins', name))];
const artifacts = [];
for (const dir of dirs) {
  const manifest = JSON.parse(await readFile(join(dir, 'package.json'), 'utf8'));
  const packed = spawnSync('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', output], { cwd: dir, encoding: 'utf8', maxBuffer: 8 * 1024 * 1024 });
  if (packed.status !== 0) throw new Error(`npm pack ${manifest.name} failed: ${packed.stderr}`);
  const [result] = JSON.parse(packed.stdout);
  const bytes = await readFile(join(output, result.filename));
  const integrity = 'sha512-' + createHash('sha512').update(bytes).digest('base64');
  if (integrity !== result.integrity) throw new Error(`pack integrity mismatch: ${manifest.name}`);
  artifacts.push({ name: manifest.name, version: manifest.version, filename: result.filename, integrity, shasum: createHash('sha1').update(bytes).digest('hex'), sha256: createHash('sha256').update(bytes).digest('hex'), source: dir, manifest, files: result.files });
  console.log(`${manifest.name}@${manifest.version} ${result.filename}`);
}
if (artifacts.length !== 17) throw new Error(`expected 17 artifacts, got ${artifacts.length}`);
await writeFile(join(output, 'artifacts.json'), JSON.stringify({ provenance, artifacts }, null, 2) + '\n');
console.log(`Packed ${artifacts.length} artifacts at ${output}`);
