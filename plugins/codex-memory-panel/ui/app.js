(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  const labels = { all: '全部', insight: '经验', decision: '决策', preference: '偏好', context: '上下文', fact: '事实' };
  let snapshot = null, selected = null, category = 'all', generation = 0, currentStore, controller, nextId = 1;
  const pending = new Map();
  const preview = window.parent === window;
  let ready;
  let catalog = null, writingScope;
  const formatTime = value => { const date = new Date(value); return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false }); };
  function el(tag, className, text) { const e = document.createElement(tag); if (className) e.className = className; if (text !== undefined) e.textContent = text; return e; }

  function request(method, params, timeout = 35000) {
    const id = nextId++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { pending.delete(id); reject(new Error('Codex 面板连接超时，正在重连。')); }, timeout);
      pending.set(id, { resolve, reject, timer });
      window.parent.postMessage({ jsonrpc: '2.0', id, method, params }, '*');
    });
  }
  function initializeBridge() {
    if (preview) return Promise.resolve();
    if (!ready) {
      ready = request('ui/initialize', { appInfo: { name: 'Mnemon Memory Panel', version: '0.2.3' }, appCapabilities: {}, protocolVersion: '2026-01-26' }, 15000).then(() => {
        window.parent.postMessage({ jsonrpc: '2.0', method: 'ui/notifications/initialized' }, '*');
      }).catch(error => { ready = undefined; throw error; });
    }
    return ready;
  }
  window.addEventListener('message', event => {
    if (event.source !== window.parent || !event.data || event.data.jsonrpc !== '2.0') return;
    const message = event.data;
    if (message.id !== undefined && pending.has(message.id)) {
      const handler = pending.get(message.id); pending.delete(message.id); clearTimeout(handler.timer);
      message.error ? handler.reject(new Error(message.error.message || 'Codex bridge error')) : handler.resolve(message.result);
    } else if (message.method === 'ui/notifications/tool-result' && message.params?.structuredContent?.schemaVersion === 1) {
      const value = message.params.structuredContent;
      if (!currentStore) { currentStore = value.store; render(value); }
    } else if (message.method === 'ui/notifications/host-context-changed') {
      const theme = message.params?.theme;
      if (theme === 'light' || theme === 'dark') document.documentElement.style.colorScheme = theme;
    }
  });

  function setConnection(state, message) {
    const node = $('connection'); node.className = `connection ${state}`;
    node.lastElementChild.textContent = ({ live: '实时同步', stale: '连接中断 · 保留已有内容', error: '暂时无法读取' })[state] || '正在连接';
    $('notice').hidden = !message; $('notice').textContent = message || '';
  }

  function render(value) {
    if (value.schemaVersion !== 1) { setConnection('stale', '面板数据接口版本已变化，需要更新适配层。'); return; }
    if (!Array.isArray(value.memories)) {
      setConnection(value.connection?.state || 'error', value.connection?.message || '尚未取得记忆数据。');
      if (!snapshot) $('list').replaceChildren(el('div', 'empty', '连接恢复后，记忆会自动显示。'));
      return;
    }
    const previousIds = new Set(snapshot?.store === value.store ? snapshot.memories.map(x => x.id) : value.memories.map(x => x.id));
    const added = value.memories.filter(x => !previousIds.has(x.id)).map(x => x.id);
    snapshot = value;
    $('total').textContent = String(value.stats.total); $('edges').textContent = value.stats.edges ?? '—';
    $('version').textContent = `Mnemon ${value.version}`;
    $('synced').textContent = new Date(value.connection.updatedAt).toLocaleTimeString('zh-CN', { hour12: false });
    if (value.scopes) { catalog = value; renderScopes(); }
    $('store').value = value.store;
    $('add-memory').disabled = value.writable === false;
    $('add-memory').title = value.writable === false ? '请先选择一个项目或全局经验。' : '';
    $('data-path').textContent = value.dbPath ? `本地 · ${value.dbPath}` : '本地记忆';
    $('data-path').title = $('data-path').textContent;
    $('update-note').textContent = added.length ? `新增 ${added.length} 条记忆` : '';
    const warning = value.truncated ? `已显示前 ${value.limit} 条记忆${value.storeLimit ? `，最多汇总 ${value.storeLimit} 个库` : ''}，已统计 ${value.stats.total} 条。` : '';
    setConnection(value.connection.state, value.connection.message || warning);
    if (selected && !value.memories.some(x => x.id === selected)) selected = null;
    renderCategories(); renderList(new Set(added)); renderDetail();
  }

  function renderCategories() {
    const keys = ['all', ...new Set(snapshot.memories.map(x => x.category))];
    if (!keys.includes(category)) category = 'all';
    $('categories').replaceChildren(...keys.map(key => {
      const count = key === 'all' ? snapshot.memories.length : snapshot.memories.filter(x => x.category === key).length;
      const button = el('button', key === category ? 'active' : '', `${labels[key] || key} ${count}`);
      button.type = 'button'; button.setAttribute('aria-pressed', String(key === category));
      button.onclick = () => { category = key; renderCategories(); renderList(); };
      return button;
    }));
  }

  function renderList(added = new Set()) {
    if (!snapshot) return;
    const query = $('search').value.trim().toLocaleLowerCase();
    const rows = snapshot.memories.filter(x => (category === 'all' || category === x.category) && [x.content, x.source, ...x.tags, ...x.entities].join(' ').toLocaleLowerCase().includes(query));
    $('matched').textContent = `${rows.length} 条`;
    const scroll = $('list').scrollTop;
    const nodes = rows.map(memory => {
      const card = el('button', `memory-card${selected === memory.id ? ' selected' : ''}${added.has(memory.id) ? ' new-memory' : ''}`);
      card.type = 'button'; card.setAttribute('aria-pressed', String(selected === memory.id));
      const top = el('div', 'card-top'); top.append(el('span', 'category-badge', labels[memory.category] || memory.category), el('span', '', memory.project || formatTime(memory.createdAt).split(' ')[0]));
      const tags = el('div', 'card-tags'); memory.tags.slice(0, 3).forEach(tag => tags.append(el('span', '', `#${tag}`)));
      card.append(top, el('div', 'card-content', memory.content), tags);
      card.onclick = () => { selected = memory.id; $('detail').scrollTop = 0; renderList(); renderDetail(); };
      return card;
    });
    if (!nodes.length) nodes.push(el('div', 'empty', snapshot.memories.length ? '没有匹配的记忆，试试其他关键词。' : '这里还没有记忆，新经验写入后会自动出现。'));
    $('list').replaceChildren(...nodes); $('list').scrollTop = scroll;
  }

  function renderDetail() {
    const memory = snapshot?.memories.find(x => x.id === selected);
    if (!memory) {
      const empty = el('div', 'detail-empty'); empty.append(el('span', 'detail-symbol', '◎'), el('h2', '', '每条经验，都有来处'), el('p', '', '选择一条记忆，查看完整内容和来源。'));
      $('detail').replaceChildren(empty); return;
    }
    const scroll = $('detail').scrollTop, header = el('div', 'detail-header'), copy = el('button', 'copy', '复制内容');
    copy.type = 'button'; copy.onclick = async () => {
      try { await navigator.clipboard.writeText(memory.content); copy.textContent = '已复制'; }
      catch { copy.textContent = '请选中文字复制'; }
    };
    header.append(el('span', 'category-badge', labels[memory.category] || memory.category), copy);
    const chips = el('div', 'chips'); [...new Set([...memory.tags, ...memory.entities])].forEach(tag => chips.append(el('span', 'chip', tag)));
    const meta = el('dl', 'metadata');
    for (const [name, content] of [['记忆范围', memory.project || snapshot.scopeName || snapshot.targetStore || snapshot.store], ['来源', memory.source || '未提供'], ['重要性', memory.importance === null ? '未提供' : `${memory.importance} / 5`], ['创建时间', formatTime(memory.createdAt)], ['更新时间', formatTime(memory.updatedAt)], ['记忆 ID', memory.originalId || memory.id]]) {
      meta.append(el('dt', '', name), el('dd', name === '记忆 ID' ? 'memory-id' : '', content));
    }
    $('detail').replaceChildren(header, el('h2', 'detail-title', '完整记忆'), el('div', 'full-content', memory.content), chips, meta);
    $('detail').scrollTop = scroll;
  }

  async function getSnapshot(args) {
    if (preview) {
      controller = new AbortController();
      const response = await fetch(`/api/snapshot?${new URLSearchParams(Object.entries(args).filter(([, v]) => v !== undefined))}`, { signal: controller.signal });
      const value = await response.json(); if (!response.ok) throw new Error(value.error || '连接失败'); return value;
    }
    await initializeBridge();
    const result = await request('tools/call', { name: 'mnemon_snapshot', arguments: args });
    if (result.isError) throw new Error(result.content?.find(x => x.type === 'text')?.text || '读取失败');
    return result.structuredContent;
  }

  function renderScopes() {
    const signature = JSON.stringify(catalog.scopes);
    if ($('store').dataset.options === signature) return;
    const groups = new Map();
    for (const scope of catalog.scopes) {
      if (!groups.has(scope.group)) { const group = el('optgroup'); group.label = scope.group; groups.set(scope.group, group); }
      const option = el('option', '', scope.name); option.value = scope.id; groups.get(scope.group).append(option);
    }
    const choose = el('option', '', '＋ 选定项目…'); choose.value = '@choose';
    $('store').replaceChildren(...groups.values(), choose);
    $('store').dataset.options = signature;
  }
  async function callTool(name, args = {}) {
    if (preview) {
      const routes = { mnemon_projects: '/api/projects', mnemon_associate_project: '/api/associate', mnemon_remember: '/api/remember' };
      const read = name === 'mnemon_projects';
      const response = await fetch(routes[name], read ? {} : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(args) });
      const value = await response.json(); if (!response.ok) throw new Error(value.error || '操作失败'); return value;
    }
    await initializeBridge();
    const value = await request('tools/call', { name, arguments: args });
    if (value.isError) throw new Error(value.content?.find(x => x.type === 'text')?.text || '操作失败');
    return value.structuredContent;
  }
  async function openProjects() {
    $('project-error').textContent = ''; $('project-dialog').showModal();
    try {
      catalog = await callTool('mnemon_projects'); renderScopes(); $('store').value = currentStore;
      $('project-choice').replaceChildren(el('option', '', '手动添加项目目录'), ...catalog.projects.map(p => { const option = el('option', '', p.name); option.value = p.id; return option; }));
      $('project-choice').firstElementChild.value = '';
      $('project-store').replaceChildren(el('option', '', '自动创建项目专属库'), ...catalog.stores.filter(store => store.toLowerCase() !== 'codex-global').map(store => { const option = el('option', '', store); option.value = store; return option; }));
      $('project-store').firstElementChild.value = '';
    } catch (error) { $('project-error').textContent = error.message; }
  }
  $('project-choice').onchange = () => {
    const project = catalog.projects.find(p => p.id === $('project-choice').value);
    $('project-name').value = project?.name || ''; $('project-path').value = project?.path || '';
    $('project-store').value = project && catalog.stores.includes(project.store) ? project.store : '';
  };
  $('choose-project').onclick = openProjects;
  $('close-project').onclick = () => $('project-dialog').close();
  $('project-form').onsubmit = async event => {
    event.preventDefault(); $('save-project').disabled = true; $('project-error').textContent = '正在保存项目关联…';
    try {
      const value = await callTool('mnemon_associate_project', { directory: $('project-path').value.trim(), name: $('project-name').value.trim(), store: $('project-store').value || undefined });
      catalog = await callTool('mnemon_projects'); renderScopes(); $('project-dialog').close(); switchStore(`@project:${value.id}`);
    } catch (error) { $('project-error').textContent = error.message; }
    finally { $('save-project').disabled = false; }
  };
  $('add-memory').onclick = () => {
    if (!snapshot || snapshot.writable === false) return;
    writingScope = currentStore; $('memory-target').textContent = `保存到：${snapshot.scopeName || snapshot.targetStore || currentStore}`;
    $('memory-error').textContent = ''; $('memory-content').value = ''; $('memory-dialog').showModal();
  };
  $('close-memory').onclick = () => $('memory-dialog').close();
  $('memory-form').onsubmit = async event => {
    event.preventDefault(); $('save-memory').disabled = true; $('memory-error').textContent = '正在保存，请勿重复提交…';
    try {
      const value = await callTool('mnemon_remember', { scope: writingScope, content: $('memory-content').value, category: $('memory-category').value, source: 'user' });
      $('memory-error').textContent = value.action === 'skipped' ? '这条内容已经存在，未重复保存。' : `已保存到 ${value.scopeName}`;
      $('memory-content').value = '';
    } catch (error) { $('memory-error').textContent = `${error.message} 请核对记忆列表后再决定是否重试。`; }
    finally { $('save-memory').disabled = false; }
  };

  async function synchronize(token) {
    let revision, failures = 0;
    while (token === generation) {
      try {
        const value = await getSnapshot({ store: currentStore, revision, waitMs: revision ? 20000 : 0 });
        if (token !== generation) return;
        if (!value || (currentStore && value.store !== currentStore)) throw new Error('记忆体响应不匹配。');
        currentStore = value.store; revision = value.revision; failures = 0;
        if (!value.unchanged) render(value); else setConnection(value.connection.state, value.connection.message || (snapshot?.truncated ? `已显示前 ${snapshot.limit} 条记忆。` : ''));
        if (value.connection.state !== 'live') await new Promise(resolve => setTimeout(resolve, 2000));
      } catch (error) {
        if (token !== generation) return;
        setConnection(snapshot ? 'stale' : 'error', `${error.message} 自动重连中…`);
        await new Promise(resolve => setTimeout(resolve, Math.min(10000, 1000 * 2 ** Math.min(failures++, 4))));
      }
    }
  }
  $('search').addEventListener('input', () => renderList());
  function switchStore(store) {
    currentStore = store; $('store').value = store; generation++; controller?.abort();
    snapshot = null; selected = null; category = 'all'; $('search').value = '';
    $('total').textContent = '—'; $('edges').textContent = '—'; $('synced').textContent = '—';
    $('categories').replaceChildren(); $('list').replaceChildren(el('div', 'empty', '正在切换记忆体…')); renderDetail();
    setConnection('connecting'); synchronize(generation);
  }
  $('store').addEventListener('change', () => {
    if ($('store').value === '@choose') { $('store').value = currentStore; openProjects(); }
    else switchStore($('store').value);
  });
  window.addEventListener('pagehide', () => { generation++; controller?.abort(); for (const p of pending.values()) { clearTimeout(p.timer); p.reject(new Error('Panel closed')); } pending.clear(); });
  synchronize(generation);
})();
