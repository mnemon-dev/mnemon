import { createHash } from 'node:crypto';
import { ProjectRegistry, GLOBAL_STORE } from './projects.mjs';

export const validScope = value => typeof value === 'string' && /^(?:[A-Za-z0-9][A-Za-z0-9_-]{0,127}|@global|@all|@project:[a-f0-9]{16})$/.test(value);
export class MemoryScopes {
  constructor(adapter) {
    this.adapter = adapter;
    this.registry = new ProjectRegistry(adapter.dataDir, adapter.env);
    this.defaultStore = adapter.defaultStore === 'default' ? '@global' : adapter.defaultStore;
  }
  async catalog() {
    const stores = await this.adapter.stores();
    const projects = await this.registry.projects(stores);
    return { stores, projects, scopes: [
      { id: '@global', name: '全局经验', group: '通用', store: GLOBAL_STORE },
      { id: '@all', name: '全部项目 · 汇总浏览', group: '通用' },
      ...projects.map(p => ({ id: `@project:${p.id}`, name: p.name, group: '项目', store: p.store, path: p.path, associated: p.associated })),
      ...stores.filter(store => store.toLowerCase() !== GLOBAL_STORE).map(store => ({ id: store, name: `${store} · ${projects.some(p => p.store === store) ? '已关联项目' : '未关联项目'}`, group: '已有记忆库', store })),
    ] };
  }
  async target(scope, catalog) {
    catalog ||= await this.catalog();
    if (!validScope(scope)) throw new Error('记忆范围无效。');
    if (scope === '@all') throw new Error('汇总视图不能写入，请选定项目或全局经验。');
    const target = catalog.scopes.find(s => s.id === scope);
    if (!target) throw new Error('项目或记忆库已不可用，请重新选择。');
    return target;
  }
  async fingerprint(scope) {
    const catalog = await this.catalog();
    const stores = scope === '@all' ? catalog.stores : [(await this.target(scope, catalog)).store];
    const fingerprints = [];
    for (const store of stores) fingerprints.push(catalog.stores.includes(store) ? await this.adapter.fingerprint(store) : 'empty');
    return createHash('sha256').update(JSON.stringify([scope, catalog, fingerprints])).digest('hex');
  }
  async read(scope) {
    const catalog = await this.catalog();
    if (scope === '@all') {
      const results = [], errors = [];
      for (const store of catalog.stores.slice(0, 64)) {
        try { results.push(await this.adapter.read(store)); }
        catch (error) { errors.push(`${store}: ${error.message}`); }
      }
      if (errors.length) throw new Error(`汇总未完成，保留原显示。${errors.join('；')}`);
      const memories = results.flatMap(result => result.memories.map(memory => ({ ...memory, originalId: memory.id, id: `${result.store}:${memory.id}`,
        store: result.store, project: catalog.projects.filter(p => p.store === result.store).map(p => p.name).join(' / ') || (result.store === GLOBAL_STORE ? '全局经验' : result.store) })))
        .sort((a, b) => b.createdAt.localeCompare(a.createdAt) || a.id.localeCompare(b.id));
      const total = results.reduce((sum, result) => sum + result.stats.total, 0);
      const bounded = catalog.stores.length > 64 || total > 5000;
      return { schemaVersion: 1, store: scope, ...catalog, version: await this.adapter.detect(), memories: memories.slice(0, 5000),
        stats: { total, edges: results.every(r => r.stats.edges !== null) ? results.reduce((sum, r) => sum + r.stats.edges, 0) : null },
        truncated: bounded, limit: 5000, storeLimit: 64, dbPath: '', writable: false };
    }
    const target = await this.target(scope, catalog);
    const value = catalog.stores.includes(target.store) ? await this.adapter.read(target.store) : {
      schemaVersion: 1, memories: [], stats: { total: 0, edges: 0 }, version: await this.adapter.detect(), truncated: false, limit: 5000, dbPath: '',
    };
    return { ...value, ...catalog, store: scope, targetStore: target.store, scopeName: target.name, writable: true,
      uninitialized: !catalog.stores.includes(target.store) };
  }
  async remember({ scope, content, category = 'insight', source = 'user' }) {
    const target = await this.target(scope);
    const saved = await this.adapter.remember(target.store, content, category, source);
    return { ...saved, targetStore: target.store, scopeName: target.name };
  }
}
