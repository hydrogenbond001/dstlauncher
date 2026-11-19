'use strict';

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

let state = { profiles: [], dst: [] };
let view = 'frp';
let current = { kind: null, id: null };
let logTimer = null;
let logOffset = 0;
let logSource = 'launcher';
let dstConfigData = null;
let stopped = false;
let steamBusy = false;
let steamLogDismissed = false;
let steamDownloading = false;
let logViews = {};
let logSourcesSig = "";
let logForceBottom = false;

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

async function api(url, opts = {}) {
  const res = await fetch(url, opts);
  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch { data = text; }
  }
  if (!res.ok) {
    const msg = data && data.error ? data.error : `${res.status} ${res.statusText}`;
    throw new Error(msg);
  }
  return data;
}

function banner(msg, ok = false) {
  const el = $('#banner');
  if (!msg) { el.hidden = true; return; }
  el.hidden = false;
  el.textContent = msg;
  el.classList.toggle('ok', ok);
}

function listFor(v) {
  return v === 'frp' ? state.profiles : (state.dst || []);
}

async function refresh(showMissing = true) {
  if (stopped) return;
  try {
    const data = await api('/api/state');
    data.dst = data.dst || [];
    state = data;
    $('#version').textContent = 'v' + state.version;
    $('#root').textContent = state.root;
    $('#listen').textContent = state.listen;
    renderList();
    if (showMissing) {
      const missing = [];
      if (!state.bin.frpc) missing.push('frpc');
      if (!state.bin.frps) missing.push('frps');
      banner(missing.length ? `未找到 ${missing.join(' / ')}：放入 bin/ 或与 exe 同级目录后点刷新。` : '');
    }
    if (current.kind && current.id) syncDetail();
  } catch (err) {
    banner('面板连接失败：' + err.message);
  }
}

function renderList() {
  const wrap = $('#list');
  const items = listFor(view);
  if (!items.length) {
    wrap.innerHTML = `<p class="empty">${view === 'frp'
      ? '还没有 frp 实例，点右上角新建。'
      : '还没有 DST 服务器，点右上角新建。'}</p>`;
    return;
  }
  wrap.innerHTML = items.map(p => (view === 'frp' ? frpCard(p) : dstCard(p))).join('');
}

function actionButtons(kind, p) {
  return `${p.running
    ? `<button class="ghost" data-act="stop" data-kind="${kind}" data-id="${esc(p.id)}">停止</button>
       <button class="ghost" data-act="restart" data-kind="${kind}" data-id="${esc(p.id)}">重启</button>`
    : `<button class="primary" data-act="start" data-kind="${kind}" data-id="${esc(p.id)}">启动</button>`}
    <button class="ghost" data-act="detail" data-kind="${kind}" data-id="${esc(p.id)}">详情</button>
    <button class="ghost danger" data-act="delete" data-kind="${kind}" data-id="${esc(p.id)}">删除</button>`;
}

function frpCard(p) {
  const meta = [
    esc(p.id),
    p.running ? 'pid ' + p.pid : '',
    p.webPort ? 'dashboard :' + p.webPort : '',
  ].filter(Boolean).join(' · ');
  return `<article class="card">
    <div class="card-top">
      <span class="dot ${p.running ? 'on' : ''}"></span>
      <strong title="${esc(p.name)}">${esc(p.name)}</strong>
      <span class="chip">${p.kind === 'server' ? 'frps' : 'frpc'}</span>
    </div>
    <div class="small muted">${meta}</div>
    ${p.binError ? `<div class="small warn">${esc(p.binError)}</div>` : ''}
    <div class="card-actions">${actionButtons('frp', p)}</div>
  </article>`;
}

function dstCard(p) {
  const shards = (p.shards || []).map(s => `${s.name}:${s.port}`).join(' · ');
  const meta = [esc(p.id), p.running ? 'pid ' + p.pid : ''].filter(Boolean).join(' · ');
  const runningShards = (p.shards || []).filter(s => s.running).length;
  return `<article class="card">
    <div class="card-top">
      <span class="dot ${p.running ? 'on' : ''}"></span>
      <strong title="${esc(p.name)}">${esc(p.name)}</strong>
      <span class="chip">DST</span>
    </div>
    <div class="small muted">${meta}${runningShards ? ' · 运行分片 ' + runningShards : ''}</div>
    <div class="small muted mono ellipsis" title="${esc(p.clusterDir)}">${esc(p.clusterDir)}</div>
    ${p.clusterDirOk ? '' : '<div class="small warn">集群目录不存在</div>'}
    ${shards ? `<div class="small muted ellipsis">${esc(shards)}</div>` : '<div class="small warn">未识别到世界</div>'}
    <div class="card-actions">${actionButtons('dst', p)}</div>
  </article>`;
}

$('#list').addEventListener('click', async (e) => {
  const btn = e.target.closest('button[data-act]');
  if (!btn) return;
  const { act, kind, id } = btn.dataset;
  const base = kind === 'frp' ? `/api/profiles/${id}` : `/api/dst/${id}`;
  btn.disabled = true;
  try {
    if (act === 'detail') {
      await openDetail(kind, id);
      return;
    }
    if (act === 'delete') {
      const label = kind === 'frp' ? '实例' : 'DST 服务器';
      if (!confirm(`删除${label} ${id}？${kind === 'dst' ? '（不会删除集群目录）' : '（配置与日志会删除）'}`)) return;
      await api(base, { method: 'DELETE' });
      if (current.kind === kind && current.id === id) closeDetail();
    } else {
      await api(`${base}/${act}`, { method: 'POST' });
      if (kind === 'dst' && act !== 'stop' && current.kind === 'dst' && current.id === id) {
        // A new run writes a fresh server_log.txt; drop cached offsets/text.
        logViews = {};
        logSourcesSig = '';
        logOffset = 0;
        logForceBottom = true;
        $('#logText').textContent = '';
        if (!$('#tab-logs').hidden) startLogs();
      }
    }
    await refresh();
  } catch (err) {
    banner(err.message);
  } finally {
    btn.disabled = false;
  }
});

function switchView(next) {
  if (view === next) return;
  view = next;
  $$('.nav-item').forEach(b => b.classList.toggle('active', b.dataset.view === next));
  $('#viewTitle').textContent = next === 'frp' ? 'frp 实例' : 'DST 服务器';
  $('#viewHint').textContent = next === 'frp'
    ? '管理 frpc / frps 进程、配置与 dashboard'
    : '集群目录、bat 启动、日志与常用配置';
  $('#newBtn').textContent = next === 'frp' ? '+ 新建 frp 实例' : '+ 新建 DST 服务器';
  $('#steamBar').hidden = next !== 'dst';
  closeDetail();
  renderList();
  if (next === 'dst') {
    loadSettings();
    loadSteamStatus();
  }
}

$$('.nav-item').forEach(btn => btn.addEventListener('click', () => switchView(btn.dataset.view)));

const TABS = [['config', '配置'], ['logs', '日志'], ['status', '状态']];

async function openDetail(kind, id) {
  const item = listFor(kind === 'frp' ? 'frp' : 'dst').find(x => x.id === id);
  if (!item) return;
  current = { kind, id };
  logViews = {};
  logSourcesSig = '';
  logSource = 'launcher';
  logOffset = 0;
  $('#logText').textContent = '';
  $('#placeholder').hidden = true;
  $('#detail').hidden = false;
  $('#detailTitle').textContent = item.name || id;
  syncDetail();
  buildTabs();

  if (kind === 'frp') {
    $('#frpConfig').hidden = false;
    $('#dstConfig').hidden = true;
    $('#configText').value = '';
    $('#configPath').textContent = '';
    try {
      const data = await api(`/api/profiles/${id}/config`);
      $('#configPath').textContent = data.path;
      $('#configText').value = data.content;
    } catch (err) {
      banner('读取配置失败：' + err.message);
    }
  } else {
    $('#frpConfig').hidden = true;
    $('#dstConfig').hidden = false;
    logSource = ((item.shards || []).find(s => s.isMaster) || {}).name || 'launcher';
    renderLogSources(item);
    try {
      await loadDstConfig(id);
    } catch (err) {
      banner('读取配置失败：' + err.message);
    }
  }
  switchTab('config');
}

function syncDetail() {
  if (!current.kind) return;
  const item = listFor(current.kind === 'frp' ? 'frp' : 'dst').find(x => x.id === current.id);
  if (!item) return;
  $('#detailTitle').textContent = item.name || current.id;
  $('#detailSub').textContent = current.kind === 'frp' ? (item.configPath || '') : (item.clusterDir || '');
  const dash = $('#dashLink');
  if (current.kind === 'frp' && item.webUrl) {
    dash.hidden = false;
    dash.dataset.url = item.webUrl;
  } else {
    dash.hidden = true;
    dash.dataset.url = '';
  }
  if (current.kind === 'dst') renderLogSources(item);
}

function buildTabs() {
  $('#tabs').innerHTML = TABS
    .map(([id, label], i) => `<button class="tab ${i === 0 ? 'active' : ''}" data-tab="${id}">${label}</button>`)
    .join('');
  $$('.tab').forEach(t => t.addEventListener('click', () => switchTab(t.dataset.tab)));
}

function switchTab(name) {
  $$('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === name));
  ['config', 'logs', 'status'].forEach(id => {
    $('#tab-' + id).hidden = id !== name;
  });
  if (name === 'logs') {
    startLogs();
  } else {
    saveLogView();
    stopLogs();
  }
  if (name === 'status') loadStatus();
}

function closeDetail() {
  current = { kind: null, id: null };
  stopLogs();
  $('#detail').hidden = true;
  $('#placeholder').hidden = false;
}

/* ---- logs ---- */

function renderLogSources(item) {
  const wrap = $('#logSources');
  if (current.kind !== 'dst') {
    wrap.innerHTML = '';
    logSourcesSig = '';
    return;
  }
  const sources = [{ id: 'launcher', label: '启动日志' }]
    .concat((item.shards || []).map(s => ({ id: s.name, label: s.name })));
  if (!sources.some(s => s.id === logSource)) logSource = 'launcher';
  const sig = sources.map(s => s.id).join('|');
  if (sig !== logSourcesSig) {
    logSourcesSig = sig;
    wrap.innerHTML = sources.map(s =>
      `<button class="src" data-src="${esc(s.id)}">${esc(s.label)}</button>`
    ).join('');
    $$('.src', wrap).forEach(b => b.addEventListener('click', () => {
      saveLogView();
      logSource = b.dataset.src;
      logForceBottom = true;
      renderLogSources(item);
      if (!$('#tab-logs').hidden) startLogs();
    }));
  }
  $$('.src', wrap).forEach(b => b.classList.toggle('active', b.dataset.src === logSource));
}

function saveLogView() {
  if (current.kind !== 'dst') return;
  logViews[logSource] = { offset: logOffset, text: $('#logText').textContent };
}

function startLogs() {
  stopLogs();
  const view = logViews[logSource] || { offset: 0, text: '' };
  logOffset = view.offset;
  const pre = $('#logText');
  pre.textContent = view.text;
  pre.scrollTop = pre.scrollHeight;
  logForceBottom = true;
  loadLogs();
  logTimer = setInterval(loadLogs, 1500);
}

function stopLogs() {
  if (logTimer) {
    clearInterval(logTimer);
    logTimer = null;
  }
}

async function loadLogs() {
  if (!current.kind) return;
  const base = current.kind === 'frp' ? '/api/profiles/' : '/api/dst/';
  let url = `${base}${current.id}/logs?offset=${logOffset}`;
  if (current.kind === 'dst') url += `&source=${encodeURIComponent(logSource)}`;
  try {
    const data = await api(url);
    const pre = $('#logText');
    const lines = data.lines || [];
    if (data.reset) {
      pre.textContent = '';
      logForceBottom = true;
    }
    if (lines.length) {
      const nearBottom = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 40;
      pre.textContent += lines.join('\n') + '\n';
      if (logForceBottom || nearBottom) {
        pre.scrollTop = pre.scrollHeight;
        logForceBottom = false;
      }
    } else if (logForceBottom) {
      pre.scrollTop = pre.scrollHeight;
      logForceBottom = false;
    }
    logOffset = data.next;
    if (current.kind === 'dst') {
      logViews[logSource] = { offset: logOffset, text: pre.textContent };
    }
  } catch {
    // log file may not exist yet
  }
}

async function loadStatus() {
  if (!current.kind) return;
  const base = current.kind === 'frp' ? '/api/profiles/' : '/api/dst/';
  try {
    const st = await api(`${base}${current.id}/status`);
    $('#statusText').textContent = JSON.stringify(st, null, 2);
  } catch (err) {
    $('#statusText').textContent = String(err.message);
  }
}

/* ---- frp config ---- */

$('#saveConfig').addEventListener('click', async () => {
  if (current.kind !== 'frp') return;
  const btn = $('#saveConfig');
  btn.disabled = true;
  try {
    const res = await api(`/api/profiles/${current.id}/config`, {
      method: 'PUT',
      headers: { 'Content-Type': 'text/plain' },
      body: $('#configText').value,
    });
    const running = !!(state.profiles.find(p => p.id === current.id) || {}).running;
    if (res && res.reloadError) {
      banner('已保存，但热重载失败：' + res.reloadError);
    } else {
      banner('配置已保存并通过 frp 校验' + (running ? '，已热重载' : ''), true);
    }
    await refresh(false);
  } catch (err) {
    banner('保存失败：' + err.message);
  } finally {
    btn.disabled = false;
  }
});

/* ---- DST config ---- */

async function loadDstConfig(id) {
  const data = await api(`/api/dst/${id}/config`);
  dstConfigData = data;
  renderDstConfig(data);
}

function renderDstConfig(cfg) {
  const c = cfg.cluster;
  const gameModes = ['survival', 'endless', 'wilderness'];
  $('#dstConfigFields').innerHTML = `
    <section class="cfg-section">
      <h4>集群 cluster.ini</h4>
      <div class="row">
        <label>房间名 <input data-cluster="name" value="${esc(c.name)}"></label>
        <label>密码 <input data-cluster="password" value="${esc(c.password)}"></label>
        <label>语言 <input data-cluster="language" value="${esc(c.language)}"></label>
        <label>描述 <input data-cluster="description" value="${esc(c.description)}"></label>
        <label>游戏模式
          <select data-cluster="gameMode">
            ${gameModes.map(m => `<option value="${m}" ${m === c.gameMode ? 'selected' : ''}>${m}</option>`).join('')}
          </select>
        </label>
        <label>最大玩家 <input type="number" data-cluster="maxPlayers" min="1" max="64" value="${c.maxPlayers}"></label>
        <label>master_port <input type="number" data-cluster="masterPort" value="${c.masterPort}"></label>
        <label>cluster_key <input data-cluster="clusterKey" value="${esc(c.clusterKey)}"></label>
        <label class="check-label"><span>PVP</span><input type="checkbox" data-cluster="pvp" ${c.pvp ? 'checked' : ''}></label>
        <label class="check-label"><span>空服暂停</span><input type="checkbox" data-cluster="pauseWhenEmpty" ${c.pauseWhenEmpty ? 'checked' : ''}></label>
        <label class="check-label"><span>分片</span><input type="checkbox" data-cluster="shardEnabled" ${c.shardEnabled ? 'checked' : ''}></label>
        <label class="check-label"><span>控制台</span><input type="checkbox" data-cluster="consoleEnabled" ${c.consoleEnabled ? 'checked' : ''}></label>
      </div>
    </section>

    <section class="cfg-section">
      <h4>世界 server.ini（自动识别）</h4>
      <div class="world-grid">
        ${(cfg.shards || []).map(sh => `
          <div class="world-card" data-shard="${esc(sh.name)}">
            <div class="card-top"><strong>${esc(sh.name)}</strong>
              <span class="chip">${sh.isMaster ? '主世界' : '洞穴'}</span></div>
            <label>server_port <input type="number" data-shard-key="port" value="${sh.port}"></label>
            <label>master_server_port <input type="number" data-shard-key="masterServerPort" value="${sh.masterServerPort}"></label>
            <label>authentication_port <input type="number" data-shard-key="authPort" value="${sh.authPort}"></label>
            <label class="check-label"><span>is_master</span><input type="checkbox" data-shard-key="isMaster" ${sh.isMaster ? 'checked' : ''}></label>
          </div>`).join('')}
      </div>
    </section>

    <section class="cfg-section">
      <h4>cluster_token.txt</h4>
      <textarea class="short" data-text="token">${esc(cfg.token)}</textarea>
      <h4>adminlist.txt</h4>
      <textarea class="short" data-text="adminList">${esc(cfg.adminList)}</textarea>
      <div class="small muted mono">${(cfg.files || []).map(esc).join('<br>')}</div>
    </section>`;
}

function collectDstConfig() {
  const cfg = JSON.parse(JSON.stringify(dstConfigData));
  $$('#dstConfigFields [data-cluster]').forEach(el => {
    const key = el.dataset.cluster;
    cfg.cluster[key] = el.type === 'checkbox' ? el.checked
      : el.type === 'number' ? Number(el.value) : el.value;
  });
  $$('#dstConfigFields .world-card').forEach(card => {
    const sh = (cfg.shards || []).find(x => x.name === card.dataset.shard);
    if (!sh) return;
    $$('[data-shard-key]', card).forEach(el => {
      const key = el.dataset.shardKey;
      sh[key] = el.type === 'checkbox' ? el.checked
        : el.type === 'number' ? Number(el.value) : el.value;
    });
  });
  $$('#dstConfigFields [data-text]').forEach(el => { cfg[el.dataset.text] = el.value; });
  return cfg;
}

$('#saveDstConfig').addEventListener('click', async () => {
  if (current.kind !== 'dst' || !dstConfigData) return;
  const btn = $('#saveDstConfig');
  btn.disabled = true;
  try {
    const fresh = await api(`/api/dst/${current.id}/config`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(collectDstConfig()),
    });
    dstConfigData = fresh;
    renderDstConfig(fresh);
    banner('配置已写回集群目录', true);
  } catch (err) {
    banner('保存失败：' + err.message);
  } finally {
    btn.disabled = false;
  }
});

$('#dashLink').addEventListener('click', () => {
  const url = $('#dashLink').dataset.url;
  if (url) window.open(url, '_blank', 'noopener');
});

$('#closeDetail').addEventListener('click', closeDetail);

/* ---- create dialog ---- */

function markRequired(root) {
  $$('[required]', root).forEach(el => { el.dataset.required = '1'; });
}

function setRequired(root, on) {
  $$('[data-required="1"]', root).forEach(el => { el.required = on; });
}

function openCreate() {
  $('#createTitle').textContent = view === 'frp' ? '新建 frp 实例' : '新建 DST 服务器';
  $('#frpFields').hidden = view !== 'frp';
  $('#dstFields').hidden = view !== 'dst';
  setRequired($('#frpFields'), view === 'frp');
  setRequired($('#dstFields'), view === 'dst');
  $('#createHint').textContent = '';
  $('#createDialog').showModal();
}

function addProxyRow() {
  const row = $('#proxyRow').content.firstElementChild.cloneNode(true);
  $('.p-type', row).addEventListener('change', () => syncProxyRow(row));
  $('.remove', row).addEventListener('click', () => row.remove());
  syncProxyRow(row);
  $('#proxies').append(row);
}

function syncProxyRow(row) {
  const type = $('.p-type', row).value;
  $('.p-remote', row).hidden = !(type === 'tcp' || type === 'udp');
  $('.p-domains', row).hidden = !(type === 'http' || type === 'https' || type === 'tcpmux');
  $('.p-secret', row).hidden = !(type === 'stcp' || type === 'xtcp' || type === 'sudp');
}

function proxyPayload() {
  return $$('#proxies .proxy-row').map(row => {
    const type = $('.p-type', row).value;
    const item = {
      name: $('.p-name', row).value.trim(),
      type,
      localIP: $('.p-ip', row).value.trim() || '127.0.0.1',
      localPort: Number($('.p-local', row).value) || 0,
    };
    if (type === 'tcp' || type === 'udp') {
      item.remotePort = Number($('.p-remote', row).value) || 0;
    }
    if (type === 'http' || type === 'https' || type === 'tcpmux') {
      item.customDomains = $('.p-domains', row).value.split(',').map(s => s.trim()).filter(Boolean);
    }
    if (type === 'stcp' || type === 'xtcp' || type === 'sudp') {
      item.secretKey = $('.p-secret', row).value.trim();
    }
    return item;
  }).filter(p => p.name);
}

function parsePorts(text) {
  return text.split(/[,\s]+/).map(s => s.trim()).filter(Boolean).map(s => {
    const [a, b] = s.split('-').map(Number);
    return b === undefined ? { single: a } : { start: a, end: b };
  });
}

$('#createForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const form = e.target;
  const el = form.elements;
  try {
    $('#createHint').textContent = '创建中…';
    let created;
    if (view === 'frp') {
      const body = { name: el.name.value.trim(), kind: el.kind.value };
      if (el.kind.value === 'client') {
        body.client = {
          serverAddr: el.serverAddr.value.trim(),
          serverPort: Number(el.serverPort.value),
          token: el.token.value,
          webPort: Number(el.webPort.value),
          webUser: 'admin',
          webPass: el.webPass.value,
          proxies: proxyPayload(),
        };
      } else {
        body.server = {
          bindPort: Number(el.bindPort.value),
          token: el.tokenS.value,
          webPort: Number(el.webPortS.value),
          webUser: 'admin',
          webPass: el.webPassS.value,
          allowPorts: parsePorts(el.allowPorts.value),
        };
      }
      created = await api('/api/profiles', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
    } else {
      created = await api('/api/dst', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: el.dstName.value.trim(),
          clusterDir: el.clusterDir.value.trim(),
        }),
      });
    }
    $('#createDialog').close();
    await refresh();
    await openDetail(view === 'frp' ? 'frp' : 'dst', created.id);
  } catch (err) {
    $('#createHint').textContent = '';
    banner('创建失败：' + err.message);
  }
});

$('#newBtn').addEventListener('click', openCreate);
$('#closeCreate').addEventListener('click', () => $('#createDialog').close());
$('#addProxy').addEventListener('click', addProxyRow);

$('#kind').addEventListener('change', () => {
  const client = $('#kind').value === 'client';
  $('#clientFields').hidden = !client;
  $('#serverFields').hidden = client;
});

/* ---- steamcmd ---- */

async function loadSettings() {
  try {
    const s = await api('/api/settings');
    const input = $('#steamcmdDir');
    if (document.activeElement !== input) input.value = s.steamcmdDir || '';
  } catch (err) {
    banner(err.message);
  }
}

async function loadSteamStatus() {
  if (view !== 'dst' || stopped || steamDownloading) return;
  try {
    const st = await api('/api/steamcmd/status');
    const status = $('#steamStatus');
    status.className = 'hint';
    status.title = '';
    if (st.running) {
      steamLogDismissed = false;
      status.textContent = `更新中… ${st.elapsed || 0}s`;
      status.classList.add('warn');
    } else if (st.success) {
      status.classList.add('ok-text');
      if (steamLogDismissed) {
        status.textContent = 'Success! 更新完成';
      } else {
        status.textContent = 'Success! 更新完成（点击关闭日志）';
        status.classList.add('clickable');
        status.title = '点击关闭日志窗口';
      }
    } else if (st.error) {
      status.textContent = st.error;
      status.classList.add('warn');
    } else if (st.serverFound) {
      status.textContent = '服务端已找到';
    } else if (st.steamcmdError) {
      status.textContent = st.steamcmdError;
      status.classList.add('warn');
    } else if (st.serverError) {
      status.textContent = st.serverError;
      status.classList.add('warn');
    } else {
      status.textContent = '';
    }

    const log = $('#steamLog');
    const lines = st.lines || [];
    if (lines.length && !steamLogDismissed) {
      log.hidden = false;
      log.textContent = lines.join('\n');
      log.scrollTop = log.scrollHeight;
    } else if (steamLogDismissed) {
      log.hidden = true;
    }
    if (!st.running && steamBusy) {
      steamBusy = false;
      await refresh(false);
    }
  } catch {
    // panel may be shutting down
  }
}

$('#saveSteamcmd').addEventListener('click', async () => {
  try {
    await api('/api/settings', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ steamcmdDir: $('#steamcmdDir').value.trim() }),
    });
    banner('steamcmd 目录已保存', true);
    await loadSteamStatus();
  } catch (err) {
    banner(err.message);
  }
});

$('#steamStatus').addEventListener('click', async () => {
  const status = $('#steamStatus');
  if (!status.classList.contains('clickable')) return;
  try {
    await api('/api/steamcmd/clear', { method: 'POST' });
  } catch (err) {
    banner(err.message);
    return;
  }
  steamLogDismissed = true;
  $('#steamLog').hidden = true;
  status.classList.remove('clickable');
  status.title = '';
  status.textContent = 'Success! 更新完成';
});

$('#downloadSteamcmd').addEventListener('click', async () => {
  const dir = $('#steamcmdDir').value.trim();
  if (!dir) {
    banner('请先填写 steamcmd 目录');
    return;
  }
  const btn = $('#downloadSteamcmd');
  const status = $('#steamStatus');
  btn.disabled = true;
  steamDownloading = true;
  status.className = 'hint warn';
  status.textContent = '下载 steamcmd…';
  try {
    const res = await api('/api/steamcmd/install', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ dir }),
    });
    banner(res.already ? 'steamcmd 已存在，未重复下载' : 'steamcmd 下载完成', true);
    await loadSettings();
  } catch (err) {
    banner('下载失败：' + err.message);
  } finally {
    steamDownloading = false;
    btn.disabled = false;
    await loadSteamStatus();
  }
});

$('#updateSteamcmd').addEventListener('click', async () => {
  if (!confirm('将运行 steamcmd 更新 343050（可能较久，期间请勿关闭），继续？')) return;
  try {
    steamBusy = true;
    steamLogDismissed = false;
    $('#steamLog').hidden = false;
    $('#steamLog').textContent = '等待 steamcmd 启动...';
    await api('/api/steamcmd/update', { method: 'POST' });
    await loadSteamStatus();
  } catch (err) {
    steamBusy = false;
    banner(err.message);
  }
});

$('#quitPanel').addEventListener('click', async () => {
  if (!confirm('退出面板会停止所有正在运行的实例，确定？')) return;
  stopped = true;
  clearInterval(refreshTimer);
  clearInterval(steamTimer);
  stopLogs();
  try {
    await api('/api/quit', { method: 'POST' });
  } catch {
    // server may close the connection right after replying
  }
  banner('面板已退出，所有实例已停止，可以关闭此页面。', true);
  $$('button').forEach(b => { b.disabled = true; });
});

function randomPass() {
  const chars = 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789';
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return [...bytes].map(v => chars[v % chars.length]).join('');
}

markRequired($('#frpFields'));
markRequired($('#dstFields'));
setRequired($('#dstFields'), false);
$('#webPass').value = randomPass();
$('#webPassS').value = randomPass();
addProxyRow();
refresh();
loadSettings();
const refreshTimer = setInterval(() => refresh(false), 4000);
const steamTimer = setInterval(loadSteamStatus, 2000);
