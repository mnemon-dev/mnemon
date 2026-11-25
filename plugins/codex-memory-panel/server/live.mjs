import { createHash } from 'node:crypto';
import { watch } from 'node:fs';
import path from 'node:path';

const hash = value => createHash('sha256').update(JSON.stringify(value)).digest('hex').slice(0, 24);

export class LiveMemoryService {
  constructor(adapter) { this.adapter = adapter; this.entries = new Map(); }

  async entry(store) {
    let entry = this.entries.get(store);
    if (entry) { entry.usedAt = Date.now(); return entry; }
    if (this.entries.size >= 8) {
      const idle = [...this.entries.values()].filter(x => !x.listeners.size && !x.loading).sort((a, b) => a.usedAt - b.usedAt)[0];
      if (!idle) throw new Error('Too many open memory stores. Close an unused panel.');
      this.release(idle);
    }
    entry = { store, usedAt: Date.now(), listeners: new Set(), fingerprint: null, last: null, error: null, revision: null, readAt: 0 };
    this.entries.set(store, entry);
    entry.timer = setInterval(() => {
      if (Date.now() - entry.usedAt > 60000 && !entry.listeners.size && !entry.loading) this.release(entry);
      else this.refresh(entry).catch(() => {});
    }, 2000);
    entry.timer.unref();
    await this.refresh(entry);
    return entry;
  }

  publish(entry, snapshot, error) {
    if (snapshot) entry.last = snapshot;
    entry.error = error;
    const revision = hash({ data: entry.last, error });
    if (revision === entry.revision) return;
    entry.revision = revision;
    entry.changedAt = new Date().toISOString();
    for (const listener of [...entry.listeners]) listener();
  }

  async refresh(entry) {
    if (entry.loading) return entry.loading;
    entry.loading = (async () => {
      try {
        const fingerprint = await this.adapter.fingerprint(entry.store);
        if (fingerprint === entry.fingerprint && !entry.error && Date.now() - entry.readAt < 30000) return;
        const snapshot = await this.adapter.read(entry.store);
        entry.fingerprint = fingerprint;
        entry.readAt = Date.now();
        const directory = snapshot.dbPath ? path.dirname(snapshot.dbPath) : null;
        if (directory && entry.watchPath !== directory) {
          entry.watcher?.close();
          entry.watchPath = directory;
          try {
            entry.watcher = watch(directory, (_, file) => {
              if (file && ![path.basename(snapshot.dbPath), `${path.basename(snapshot.dbPath)}-wal`].includes(String(file))) return;
              clearTimeout(entry.debounce);
              entry.debounce = setTimeout(() => this.refresh(entry).catch(() => {}), 180);
              entry.debounce.unref();
            });
            entry.watcher.on('error', () => { entry.watcher?.close(); entry.watcher = null; entry.watchPath = null; });
            entry.watcher.unref();
          } catch { entry.watchPath = null; }
        }
        this.publish(entry, snapshot, null);
      } catch (error) { this.publish(entry, null, error.message); }
    })().finally(() => { entry.loading = null; });
    return entry.loading;
  }

  async snapshot({ store = this.adapter.defaultStore, revision, waitMs = 0 } = {}, signal) {
    const entry = await this.entry(store);
    await this.refresh(entry);
    if (revision === entry.revision && waitMs > 0 && !signal?.aborted) {
      await new Promise(resolve => {
        let timer;
        const done = () => { clearTimeout(timer); entry.listeners.delete(done); signal?.removeEventListener('abort', done); resolve(); };
        entry.listeners.add(done);
        signal?.addEventListener('abort', done, { once: true });
        timer = setTimeout(done, Math.min(waitMs, 20000));
        if (entry.revision !== revision || signal?.aborted) done();
      });
    }
    const connection = { state: entry.error ? (entry.last ? 'stale' : 'error') : 'live', message: entry.error, updatedAt: entry.changedAt };
    if (revision === entry.revision) return { unchanged: true, revision: entry.revision, store, connection };
    return { ...(entry.last || { schemaVersion: 1, store }), revision: entry.revision, connection };
  }

  release(entry) {
    clearInterval(entry.timer); clearTimeout(entry.debounce); entry.watcher?.close();
    for (const listener of [...entry.listeners]) listener();
    this.entries.delete(entry.store);
  }
  close() { for (const entry of [...this.entries.values()]) this.release(entry); }
}
