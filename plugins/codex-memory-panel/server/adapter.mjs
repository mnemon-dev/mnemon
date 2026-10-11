import { execFile, spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdtemp, rm, stat } from 'node:fs/promises';
import { homedir, tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { withFileLock } from './lock.mjs';

const execute = promisify(execFile);
const helper = fileURLToPath(new URL('./snapshot.py', import.meta.url));
const MAX_MEMORIES = 5000;
const opts = { encoding: 'utf8', timeout: 15000, maxBuffer: 24 * 1024 * 1024, windowsHide: true };
const text = (v, fallback = '') => typeof v === 'string' ? v : fallback;
const strings = v => Array.isArray(v) ? v.filter(x => typeof x === 'string') : [];

export function validStore(store) {
  if (typeof store !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(store)) throw new Error('Invalid memory store name.');
  return store;
}

export function normalize(rows, stats, version, store, stores) {
  if (!Array.isArray(rows) || !stats || !Number.isInteger(stats.total_insights) || stats.total_insights < 0) {
    throw new Error('Mnemon JSON interface changed: expected memory array and total_insights.');
  }
  const memories = rows.slice(0, MAX_MEMORIES).map(row => {
    if (!row || typeof row.id !== 'string' || typeof row.content !== 'string') throw new Error('Mnemon memory interface changed: missing id/content.');
    return { id: row.id, content: row.content, category: text(row.category, 'unknown'), importance: Number.isFinite(row.importance) ? row.importance : null,
      tags: strings(row.tags), entities: strings(row.entities), source: text(row.source), createdAt: text(row.created_at), updatedAt: text(row.updated_at) };
  }).sort((a, b) => b.createdAt.localeCompare(a.createdAt) || a.id.localeCompare(b.id));
  return { schemaVersion: 1, store, stores, version, memories,
    stats: { total: stats.total_insights, edges: Number.isFinite(stats.edge_count) ? stats.edge_count : null,
      deleted: Number.isFinite(stats.deleted_insights) ? stats.deleted_insights : null, bytes: Number.isFinite(stats.db_size_bytes) ? stats.db_size_bytes : null },
    truncated: stats.total_insights > memories.length, limit: MAX_MEMORIES,
    compatibility: version === '0.2.10' ? 'validated-version' : 'interface-validated',
    syncMethod: 'sqlite-online-backup', dbPath: text(stats.db_path) };
}

export class MnemonAdapter {
  constructor(env = process.env) {
    this.env = env;
    this.dataDir = env.MNEMON_UI_DATA_DIR || env.MNEMON_DATA_DIR || path.join(homedir(), '.mnemon');
    this.defaultStore = validStore(env.MNEMON_UI_STORE || env.MNEMON_STORE || 'default');
    this.paths = new Map();
    this.versionChecked = 0;
    const configured = env.MNEMON_UI_COMMAND;
    const npmCli = process.platform === 'win32' ? path.join(env.APPDATA || path.join(homedir(), 'AppData', 'Roaming'), 'npm', 'node_modules', '@mnemon-dev', 'mnemon', 'bin', 'mnemon.js') : '';
    const command = configured || (npmCli && existsSync(npmCli) ? npmCli : 'mnemon');
    this.launch = /\.[cm]?js$/i.test(command) ? { command: process.execPath, prefix: [command] } : { command, prefix: [] };
    this.python = env.MNEMON_UI_PYTHON || (process.platform === 'win32' ? 'python' : 'python3');
  }

  async cli(args, dataDir = this.dataDir, store) {
    const flags = ['--data-dir', dataDir, ...(store ? ['--store', validStore(store)] : [])];
    try {
      return (await execute(this.launch.command, [...this.launch.prefix, ...flags, ...args], opts)).stdout.trim();
    } catch (error) {
      const detail = text(error.stderr).trim().split('\n')[0] || error.message;
      throw new Error(`Mnemon: ${detail}`);
    }
  }

  async detect() {
    if (Date.now() - this.versionChecked < 60000) return this.version;
    const version = (await this.cli(['--version'])).match(/\d+\.\d+\.\d+(?:[-+][\w.-]+)?/)?.[0];
    if (!version) throw new Error('Cannot detect Mnemon version.');
    if (this.version !== version) {
      const help = await this.cli(['--help']);
      if (!help.includes('--readonly')) throw new Error('This Mnemon version has no safe readonly CLI. Adapter update required.');
      this.paths.clear();
    }
    this.version = version;
    this.versionChecked = Date.now();
    return version;
  }

  async stores() {
    const raw = await this.cli(['--readonly', 'store', 'list']);
    // store list is a human-readable public command in 0.2.x; keep this parser isolated.
    const stores = raw.split(/\r?\n/).map(line => line.trim().replace(/^\*\s*/, '')).filter(line => /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(line));
    if (!stores.length && !raw.includes('no stores yet')) throw new Error('Memory store list format changed.');
    return stores;
  }

  async remember(store, content, category = 'insight', source = 'user') {
    validStore(store);
    if (typeof content !== 'string' || !content.trim() || Buffer.byteLength(content, 'utf8') > 8000) throw new Error('记忆内容须为 1–8000 UTF-8 字节。');
    if (!['insight', 'decision', 'preference', 'context', 'fact', 'general'].includes(category)) throw new Error('记忆分类无效。');
    if (!['user', 'agent'].includes(source)) throw new Error('记忆来源无效。');
    await this.detect();
    const lockName = process.platform === 'win32' ? store.toLowerCase() : store;
    const output = await withFileLock(path.join(this.dataDir, 'codex-panel', `write-${lockName}.lock`), async () =>
      JSON.parse(await this.cli(['remember', '--cat', category, '--source', source, '--', content.trim()], this.dataDir, store)));
    if (typeof output.id !== 'string' && typeof output.action !== 'string') throw new Error('写入返回格式变化，请先核对 Mnemon，勿重复提交。');
    this.paths.delete(store);
    return output;
  }

  async databasePath(store) {
    if (this.paths.has(store)) return this.paths.get(store);
    // Used only for path discovery. Counts from immutable live-store access are never displayed.
    const data = JSON.parse(await this.cli(['--readonly', 'status'], this.dataDir, store));
    if (typeof data.db_path !== 'string' || !path.isAbsolute(data.db_path)) throw new Error('Mnemon no longer exposes a local SQLite path. Adapter update required.');
    this.paths.set(store, data.db_path);
    return data.db_path;
  }

  async fingerprint(store) {
    await this.detect();
    const db = await this.databasePath(store);
    const values = await Promise.all([db, `${db}-wal`].map(async file => {
      try { const s = await stat(file, { bigint: true }); return `${s.size}:${s.mtimeNs}`; }
      catch (e) { if (e.code === 'ENOENT') return 'missing'; throw e; }
    }));
    return [this.version, ...values].join('|');
  }

  async backup(source, target) {
    await new Promise((resolve, reject) => {
      const child = spawn(this.python, [helper], { windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
      let stderr = '';
      const timer = setTimeout(() => { child.kill(); reject(new Error('Live snapshot timed out.')); }, 12000);
      child.stdout.resume();
      child.stderr.on('data', chunk => { stderr = (stderr + chunk.toString('utf8')).slice(-2000); });
      child.stdin.on('error', () => {});
      child.on('error', error => { clearTimeout(timer); reject(new Error(`Snapshot requires Python 3: ${error.message}`)); });
      child.on('close', code => { clearTimeout(timer); code === 0 ? resolve() : reject(new Error(`Cannot read live snapshot: ${stderr.trim() || code}`)); });
      child.stdin.end(JSON.stringify({ source, target }));
    });
  }

  async read(store, query = '') {
    validStore(store);
    const version = await this.detect();
    const stores = await this.stores();
    if (!stores.includes(store)) throw new Error(`Memory store '${store}' is unavailable.`);
    const dbPath = await this.databasePath(store);
    const folder = await mkdtemp(path.join(tmpdir(), 'mnemon-ui-'));
    try {
      await this.backup(dbPath, path.join(folder, 'data', 'default', 'mnemon.db'));
      const [stats, rows] = await Promise.all([
        this.cli(['--readonly', 'status'], folder, 'default'),
        this.cli(['--readonly', 'recall', query, '--basic', '--limit', String(query ? 8 : MAX_MEMORIES + 1)], folder, 'default'),
      ]);
      const snapshot = normalize(JSON.parse(rows), JSON.parse(stats), version, store, stores);
      snapshot.dbPath = dbPath;
      return snapshot;
    } finally { await rm(folder, { recursive: true, force: true, maxRetries: 3, retryDelay: 100 }); }
  }
}
