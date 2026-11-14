import { mkdir, readFile, writeFile, unlink, rmdir, stat } from 'node:fs/promises';
import path from 'node:path';
import { randomUUID } from 'node:crypto';

// Serializes plugin processes, without locking or altering engine database files.
export async function withFileLock(directory, operation) {
  await mkdir(path.dirname(directory), { recursive: true });
  const file = path.join(directory, 'owner.json'), token = randomUUID();
  let acquired = false;
  for (let attempt = 0; attempt < 100; attempt++) {
    try { await mkdir(directory); acquired = true; break; }
    catch (error) {
      if (error.code !== 'EEXIST') throw error;
      // Serialize stale-owner recovery too: two reclaimers must not remove a
      // fresh owner's file after the first reclaimer released the old lock.
      const recovery = `${directory}.recovery`;
      let reclaiming = false;
      try {
        await mkdir(recovery); reclaiming = true;
        try {
          const owner = JSON.parse(await readFile(file, 'utf8'));
          if (Number.isInteger(owner.pid) && owner.pid > 0) {
            try { process.kill(owner.pid, 0); }
            catch (probe) { if (probe.code === 'ESRCH') { await unlink(file); await rmdir(directory); } }
          }
        } catch (probe) {
          // Recover a crash between mkdir and writing the owner file.
          if (probe.code === 'ENOENT') {
            try { if (Date.now() - (await stat(directory)).mtimeMs > 30000) await rmdir(directory); } catch {}
          }
        }
      } catch { /* Another reclaimer owns recovery, or recovery is unavailable. */ }
      finally { if (reclaiming) await rmdir(recovery).catch(() => {}); }
      await new Promise(resolve => setTimeout(resolve, 100));
    }
  }
  if (!acquired) throw new Error('其他会话正在保存，请稍后核对结果再重试。');
  try {
    await writeFile(file, JSON.stringify({ pid: process.pid, token }), { flag: 'wx', mode: 0o600 });
    return await operation();
  } finally {
    try {
      const owner = JSON.parse(await readFile(file, 'utf8'));
      if (owner.token === token) { await unlink(file); await rmdir(directory); }
    } catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
}
