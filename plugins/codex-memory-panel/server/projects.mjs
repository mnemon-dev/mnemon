// Optional host discovery is isolated here; manual associations remain portable.
import { readFile, writeFile, mkdir, rename, stat, realpath } from 'node:fs/promises';
import { homedir } from 'node:os';
import path from 'node:path';
import { createHash, randomUUID } from 'node:crypto';
import { withFileLock } from './lock.mjs';

const storePattern = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
export const GLOBAL_STORE = 'codex-global';
export function projectKey(directory) {
  const value = path.resolve(directory);
  return createHash('sha256').update(process.platform === 'win32' ? value.toLowerCase() : value).digest('hex').slice(0, 16);
}
export class ProjectRegistry {
  constructor(dataDir, env = process.env) {
    this.file = path.join(dataDir, 'codex-panel', 'projects.json');
    this.hostFile = env.MNEMON_UI_HOST_STATE || path.join(env.CODEX_HOME || path.join(homedir(), '.codex'), '.codex-global-state.json');
    this.roots = async () => [];
    this.queue = Promise.resolve();
  }
  async saved() {
    try {
      const value = JSON.parse(await readFile(this.file, 'utf8'));
      if (value.schemaVersion !== 1 || !Array.isArray(value.projects)) throw new Error('项目关联文件格式已变化。');
      if (value.projects.some(p => !p || typeof p.path !== 'string' || !path.isAbsolute(p.path) || typeof p.name !== 'string' || typeof p.store !== 'string' || !storePattern.test(p.store) || p.store.toLowerCase() === GLOBAL_STORE)) throw new Error('项目关联无效，项目不能绑定全局经验库。');
      return value.projects;
    } catch (error) { if (error.code === 'ENOENT') return []; throw error; }
  }
  async projects(stores = []) {
    const found = new Map(), saved = await this.saved();
    const add = (directory, name, source) => {
      if (typeof directory !== 'string' || !path.isAbsolute(directory)) return;
      const id = projectKey(directory), basename = path.basename(directory);
      found.set(id, { id, path: path.resolve(directory), name: typeof name === 'string' && name.trim() ? name.trim() : basename,
        store: `project-${id}`, source, associated: false });
    };
    try {
      // Codex's saved local-project catalog is optional, version-tolerant, read-only.
      const info = await stat(this.hostFile);
      if (info.size <= 8 * 1024 * 1024) {
        const host = JSON.parse(await readFile(this.hostFile, 'utf8'));
        const projects = host['local-projects'];
        for (const value of Array.isArray(projects) ? projects : Object.values(projects || {})) {
          if (!value || typeof value !== 'object') continue;
          for (const directory of Array.isArray(value.rootPaths) ? value.rootPaths : []) add(directory, value.name, 'codex');
        }
        for (const directory of host['electron-saved-workspace-roots'] || []) add(directory, undefined, 'codex');
      }
    } catch { /* Unsupported/missing host catalog falls back to roots and saved paths. */ }
    for (const root of await this.roots().catch(() => [])) add(root.path, root.name, 'mcp-roots');
    for (const value of saved) {
      const id = projectKey(value.path);
      found.set(id, { ...value, id, source: 'associated', associated: true });
    }
    return [...found.values()].sort((a, b) => a.name.localeCompare(b.name));
  }
  async associate({ directory, name, store }) {
    if (typeof directory !== 'string' || typeof name !== 'string') throw new Error('请提供项目名称和目录。');
    if (!path.isAbsolute(directory)) throw new Error('请输入完整项目目录。');
    const resolved = await realpath(directory);
    if (!(await stat(resolved)).isDirectory()) throw new Error('选定路径不是项目目录。');
    if (!name.trim() || name.length > 100) throw new Error('项目名称长度应为 1–100。');
    if (store !== undefined && (typeof store !== 'string' || !storePattern.test(store))) throw new Error('记忆库名称无效。');
    if (store?.toLowerCase() === GLOBAL_STORE) throw new Error('项目不能绑定全局经验库，请使用项目专属库。');
    const operation = this.queue.then(async () => {
      return withFileLock(`${this.file}.lock`, async () => {
      const projects = await this.saved(), id = projectKey(resolved);
      const previous = projects.find(p => projectKey(p.path) === id);
      const project = { path: resolved, name: name.trim(), store: store || previous?.store || `project-${id}` };
      const next = [...projects.filter(p => projectKey(p.path) !== id), project];
      await mkdir(path.dirname(this.file), { recursive: true });
      const temp = `${this.file}.${randomUUID()}.tmp`;
      await writeFile(temp, JSON.stringify({ schemaVersion: 1, projects: next }, null, 2) + '\n', { mode: 0o600 });
      await rename(temp, this.file);
      return { ...project, id, associated: true };
      });
    });
    this.queue = operation.catch(() => {});
    return operation;
  }
  async resolve(directory, stores = []) {
    if (!path.isAbsolute(directory)) throw new Error('召回需要完整项目目录。');
    const current = await realpath(directory), projects = await this.projects(stores);
    return projects.filter(p => {
      const relative = path.relative(p.path, current);
      return relative === '' || (!relative.startsWith(`..${path.sep}`) && relative !== '..' && !path.isAbsolute(relative));
    }).sort((a, b) => Number(b.associated) - Number(a.associated) || b.path.length - a.path.length)[0] || null;
  }
}
