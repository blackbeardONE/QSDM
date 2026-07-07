(function () {
  'use strict';

  const params = new URLSearchParams(window.location.search);
  const token = params.get('t') || '';
  if (token && window.history.replaceState) {
    window.history.replaceState({}, '', window.location.pathname);
  }

  const $ = (id) => document.getElementById(id);

  function api(path, init) {
    init = init || {};
    init.headers = Object.assign({}, init.headers || {}, {
      'X-QSDM-Token': token,
      'Content-Type': 'application/json',
    });
    return fetch(path, init).then(async (r) => {
      const text = await r.text();
      let body = null;
      try { body = text ? JSON.parse(text) : null; } catch (_) { body = { raw: text }; }
      if (!r.ok) {
        const msg = (body && (body.error || body.raw)) || `HTTP ${r.status}`;
        throw new Error(msg);
      }
      return body;
    });
  }

  function cls(el, state) {
    el.classList.remove('ok', 'warn', 'bad', 'safe');
    el.classList.add(state);
  }

  function text(id, value) {
    $(id).textContent = value === undefined || value === null || value === '' ? '-' : String(value);
  }

  function pids(rows) {
    if (!rows || !rows.length) return '-';
    return rows.map((p) => `${p.name}:${p.pid}`).join(', ');
  }

  function setStatus(id, label, state) {
    const el = $(id);
    el.textContent = label;
    cls(el, state);
  }

  function showToast(msg, ok) {
    const t = $('toast');
    t.hidden = false;
    t.textContent = msg;
    t.style.background = ok ? '#1f7a53' : '#b42318';
    setTimeout(() => { if (t.textContent === msg) t.hidden = true; }, 7000);
  }

  function render(s) {
    text('version', s.gui_version || '');

    const v = s.validator || {};
    const vOK = v.running && v.ready;
    setStatus('validator-status', vOK ? 'ready' : (v.running ? 'starting' : 'stopped'), vOK ? 'ok' : (v.running ? 'warn' : 'bad'));
    text('validator-sub', v.node_id ? `${v.role || 'validator'} ${v.node_id}` : (v.error || 'not reachable'));
    text('validator-height', v.chain_tip);
    text('validator-peers', v.peers);
    text('validator-uptime', v.uptime);
    text('validator-pid', pids(v.processes));

    const m = s.miner || {};
    const svc = m.service || {};
    const minerRunning = (svc.state || '').toUpperCase() === 'RUNNING';
    setStatus('miner-status', minerRunning ? 'running' : (svc.installed ? 'stopped' : 'missing'), minerRunning ? 'ok' : 'warn');
    text('miner-sub', svc.binPath || 'QSDMMiner service');
    text('miner-service', svc.installed ? (svc.state || 'installed') : 'not installed');
    text('miner-pids', pids(m.processes));
    text('miner-log', m.log_path);

    const g = s.gateway || {};
    setStatus('gateway-status', g.running && g.public_ok ? 'public' : (g.running ? 'local' : 'stopped'), g.running && g.public_ok ? 'ok' : (g.running ? 'warn' : 'bad'));
    text('gateway-sub', g.public_error || g.public_url || 'not connected');
    text('gateway-slot', g.slot);
    text('gateway-public', g.public_ok ? `OK height ${g.chain_tip || '-'}` : (g.public_code ? `HTTP ${g.public_code}` : 'offline'));
    text('gateway-pid', pids(g.processes));

    const exp = s.exposure || {};
    text('exposure-summary', exp.summary);
    setStatus('exposure-status', exp.safe ? 'safe' : 'check', exp.safe ? 'ok' : 'bad');
    const pill = $('exposure-pill');
    pill.textContent = exp.safe ? 'localhost only' : 'review exposure';
    cls(pill, exp.safe ? 'safe' : 'bad');

    const listeners = $('listeners');
    listeners.innerHTML = '';
    (exp.listeners || []).forEach((l) => {
      const div = document.createElement('div');
      div.className = 'listener';
      div.innerHTML = `<strong>${escapeHtml(l.address)}:${l.port}</strong><br><span class="${l.local_only ? 'ok' : 'bad'}">${l.local_only ? 'local only' : 'not local only'}</span><br><span class="subtle">pid ${l.pid}</span>`;
      listeners.appendChild(div);
    });
    if (!listeners.children.length) {
      listeners.textContent = 'No validator listeners detected.';
    }

    if (s.links) {
      $('dashboard-link').href = s.links.dashboard || '#';
      $('public-link').href = s.links.public_gateway || '#';
    }

    const needsAdmin = !!(s.admin && s.admin.platform === 'windows' && s.admin.elevated === false);
    $('admin-banner').hidden = !needsAdmin;
    document.querySelectorAll('[data-requires-admin]').forEach((btn) => {
      btn.disabled = needsAdmin;
      btn.title = needsAdmin ? 'Open Admin GUI to control the miner service.' : '';
    });
  }

  function escapeHtml(s) {
    return String(s || '').replace(/[&<>"']/g, (c) =>
      ({ '&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&quot;', "'":'&#39;' })[c]);
  }

  async function refresh() {
    try {
      render(await api('/api/snapshot'));
    } catch (e) {
      showToast(e.message, false);
    }
  }

  async function refreshLog() {
    try {
      const kind = $('log-kind').value;
      const r = await api('/api/log?kind=' + encodeURIComponent(kind));
      text('log-path', r.path);
      $('log-lines').textContent = (r.lines || []).join('\n') || '(empty)';
    } catch (e) {
      $('log-lines').textContent = e.message;
    }
  }

  async function doAction(path, button) {
    const old = button.textContent;
    button.disabled = true;
    button.textContent = 'Working';
    try {
      const r = await api(path, { method: 'POST', body: '{}' });
      showToast((r.output || 'done').trim() || 'done', true);
      await refresh();
      await refreshLog();
    } catch (e) {
      showToast(e.message, false);
      await refresh();
    } finally {
      button.disabled = false;
      button.textContent = old;
    }
  }

  document.querySelectorAll('[data-action]').forEach((btn) => {
    btn.addEventListener('click', () => doAction(btn.getAttribute('data-action'), btn));
  });

  $('refresh').addEventListener('click', refresh);
  $('refresh-log').addEventListener('click', refreshLog);
  $('log-kind').addEventListener('change', refreshLog);
  $('quit').addEventListener('click', () => api('/api/quit', { method: 'POST', body: '{}' }).then(() => window.close()));

  setInterval(() => api('/api/heartbeat').catch(() => {}), 10000);
  setInterval(refresh, 5000);
  refresh();
  refreshLog();
})();
