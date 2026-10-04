/* QSDM web wallet: persistent vault UX layer.
 *
 * Moved verbatim out of the inline <script> in wallet.html (so the page can
 * run under a strict Content-Security-Policy with script-src 'self').
 * The cryptographic primitives (PBKDF2, AES-GCM, ML-DSA-87 sign) are still
 * reached only through window.QSDMWallet / window.qsdm_wallet_* from the
 * SRI-pinned wallet.js + wallet.wasm, and the nonce / submit-signed network
 * calls are unchanged. UI-only changes in this file:
 *   - the Receive tab gets a locally drawn QR code (wallet-ui.js) instead of
 *     a link to a third-party QR service;
 *   - "Sign & send" now shows a review screen (to, amount, fee, nonce) and
 *     only signs + submits after the user confirms;
 *   - view hooks for the compact unlocked layout: QSDMWalletUI.onState,
 *     onBalance and onLocalActivity are told about state changes, balance
 *     refreshes and the local send log (display only; nothing here changes
 *     key handling, signing, nonce or submit).
 */
(function () {
  'use strict';

  // ----- DOM helpers -----
  const $ = (id) => document.getElementById(id);
  function show(el, on) { if (el) el.hidden = !on; }
  function setText(el, t) { if (el) el.textContent = t; }
  function setStatus(elId, msg, cls) {
    const el = $(elId);
    if (!el) return;
    el.innerHTML = cls ? `<span class="${cls}">${msg}</span>` : msg;
  }

  // ----- localStorage keys -----
  const LS_VAULT     = 'qsdm.vault.v1';        // encrypted keystore JSON (verbatim)
  const LS_ACTIVITY  = 'qsdm.vault.activity';  // local-only send log
  const LS_SETTINGS  = 'qsdm.vault.settings';  // { idleMinutes }

  function loadVault() {
    try {
      const raw = localStorage.getItem(LS_VAULT);
      if (!raw) return null;
      return JSON.parse(raw);
    } catch (_) { return null; }
  }
  function saveVault(ks) {
    localStorage.setItem(LS_VAULT, JSON.stringify(ks));
  }
  function wipeVault() {
    localStorage.removeItem(LS_VAULT);
  }
  function loadSettings() {
    try {
      const raw = localStorage.getItem(LS_SETTINGS);
      if (raw) return JSON.parse(raw);
    } catch (_) {}
    return { idleMinutes: 5 };
  }
  function saveSettings(s) {
    localStorage.setItem(LS_SETTINGS, JSON.stringify(s));
  }
  function loadActivity() {
    try {
      const raw = localStorage.getItem(LS_ACTIVITY);
      if (raw) {
        const arr = JSON.parse(raw);
        if (Array.isArray(arr)) return arr;
      }
    } catch (_) {}
    return [];
  }
  function appendActivity(entry) {
    const arr = loadActivity();
    arr.unshift(entry);
    while (arr.length > 20) arr.pop();
    localStorage.setItem(LS_ACTIVITY, JSON.stringify(arr));
    renderActivity();
  }
  function clearActivity() {
    localStorage.removeItem(LS_ACTIVITY);
    renderActivity();
  }

  // ----- session state -----
  // The decrypted private key never leaves this closure. We
  // zero the Uint8Array as soon as locking happens.
  let session = {
    address: null,
    publicKeyHex: null,
    privBytes: null, // Uint8Array(4896) or null
    idleTimer: null,
  };

  function isUnlocked() { return session.privBytes !== null; }

  function lockSession(reason) {
    if (session.privBytes) {
      try { session.privBytes.fill(0); } catch (_) {}
    }
    session.privBytes = null;
    if (session.idleTimer) { clearTimeout(session.idleTimer); session.idleTimer = null; }
    renderState(reason);
  }

  function resetIdleTimer() {
    if (!isUnlocked()) return;
    const s = loadSettings();
    const mins = Math.max(0, Math.min(120, Number(s.idleMinutes) || 0));
    if (session.idleTimer) { clearTimeout(session.idleTimer); session.idleTimer = null; }
    if (mins <= 0) return; // 0 disables idle lock
    session.idleTimer = setTimeout(function () {
      lockSession('Auto-locked after idle timeout.');
    }, mins * 60 * 1000);
  }

  // Re-arm idle timer on any user interaction.
  ['click', 'keydown', 'mousemove', 'scroll', 'touchstart'].forEach(function (ev) {
    window.addEventListener(ev, function () { resetIdleTimer(); }, { passive: true });
  });
  // Tab-hide: re-lock if user hides tab AND idleMinutes > 0
  // (paranoid posture: a backgrounded tab with an unlocked
  // wallet is a phishing-extension's playground).
  document.addEventListener('visibilitychange', function () {
    if (document.hidden && isUnlocked()) {
      const s = loadSettings();
      if ((Number(s.idleMinutes) || 0) > 0) {
        lockSession('Auto-locked: tab hidden.');
      }
    }
  });

  // ----- state rendering -----
  function renderState(banner) {
    const ks = loadVault();
    show($('vault-state-empty'),    !ks && !showingCreateOrImport());
    show($('vault-state-locked'),   !!ks && !isUnlocked() && !showingCreateOrImport());
    show($('vault-state-unlocked'), !!ks &&  isUnlocked());
    if (ks && !isUnlocked()) {
      setText($('vault-locked-addr'), ks.address || '(unknown)');
      if (banner) setStatus('vault-unlock-status', banner, 'warn');
    }
    if (ks && isUnlocked()) {
      setText($('vault-unlocked-addr'), session.address);
      setText($('vault-receive-addr'),  session.address);
      // QR code, explorer link and on-chain activity are drawn locally by
      // wallet-ui.js (no third-party QR service, no address leak).
      if (window.QSDMWalletUI && window.QSDMWalletUI.onAddress) window.QSDMWalletUI.onAddress(session.address);
      renderActivity();
      refreshBalance();
    }
    uiHook('onState', !ks ? (showingCreateOrImport() ? 'flow' : 'empty')
                      : isUnlocked() ? 'unlocked'
                      : showingCreateOrImport() ? 'flow' : 'locked');
  }
  // Optional display hooks in wallet-ui.js; errors there never break the vault.
  function uiHook(name, arg) {
    const UI = window.QSDMWalletUI;
    if (UI && typeof UI[name] === 'function') {
      try { UI[name](arg); } catch (_) {}
    }
  }
  function showingCreateOrImport() {
    return ($('vault-create-flow') && !$('vault-create-flow').hidden)
        || ($('vault-import-flow') && !$('vault-import-flow').hidden);
  }
  function hideAllFlows() {
    show($('vault-create-flow'), false);
    show($('vault-import-flow'), false);
  }

  // ----- balance refresh -----
  let balanceTimer = null;
  async function refreshBalance() {
    if (!isUnlocked()) return;
    const valEl = $('vault-balance-value');
    const srcEl = $('vault-balance-source');
    const QW = window.QSDMWallet;
    try {
      const url = (QW && QW.BALANCE_ENDPOINT ? QW.BALANCE_ENDPOINT
                  : 'https://api.qsdm.tech/api/v1/wallet/balance')
                  + '?address=' + encodeURIComponent(session.address);
      const resp = await fetch(url, { credentials: 'omit', cache: 'no-store' });
      if (!resp.ok) {
        setText(valEl, '—');
        setText(srcEl, 'HTTP ' + resp.status);
        uiHook('onBalance', { ok: false, error: 'HTTP ' + resp.status });
        return;
      }
      const body = await resp.json();
      const bal = typeof body.balance === 'number' ? body.balance : 0;
      setText(valEl, (QW && QW.formatCell) ? QW.formatCell(bal) : (bal + ' CELL'));
      setText(srcEl, body.source ? ('source: ' + body.source) : '');
      uiHook('onBalance', { ok: true, balance: bal, source: body.source || '' });
    } catch (e) {
      setText(valEl, '—');
      setText(srcEl, 'offline');
      uiHook('onBalance', { ok: false, error: 'offline' });
    }
    if (balanceTimer) clearTimeout(balanceTimer);
    balanceTimer = setTimeout(refreshBalance, 30_000); // refresh every 30s
  }

  // ----- activity render -----
  function renderActivity() {
    const list = $('vault-activity-list');
    if (!list) return;
    const arr = loadActivity();
    uiHook('onLocalActivity', arr);
    if (arr.length === 0) {
      list.innerHTML = '<p style="font-size: 13px; color: var(--muted)">No sends from this browser yet.</p>';
      return;
    }
    list.innerHTML = arr.map(function (e) {
      const when = new Date(e.timestamp || Date.now()).toLocaleString();
      const amount = (window.QSDMWallet && window.QSDMWallet.formatCell)
        ? window.QSDMWallet.formatCell(e.amount) : (e.amount + ' CELL');
      const colour = e.status === 'accepted' ? 'var(--success)'
                   : e.status === 'duplicate' ? 'var(--warning)'
                   : e.status === 'failed'   ? 'var(--danger)'  : 'var(--text-2)';
      const recipient = (e.recipient || '').slice(0, 18) + '…';
      const txid = (e.tx_id || '').slice(0, 18) + '…';
      return '<div style="background: rgba(255,255,255,.04); padding: 10px 12px; border-radius: 8px; border: 1px solid var(--border); font-family: var(--mono); font-size: 12px">'
        + '<div style="display:flex; justify-content:space-between; gap:8px"><span>' + amount + ' &rarr; ' + recipient + '</span><span style="color:' + colour + '">' + (e.status || 'unknown') + '</span></div>'
        + '<div style="color: var(--muted); margin-top: 2px">' + when + ' &middot; tx ' + txid + '</div>'
        + '</div>';
    }).join('');
  }

  // ----- buttons -----
  function bindButtons() {
    // Empty state
    if ($('vault-create-btn')) $('vault-create-btn').addEventListener('click', function () {
      hideAllFlows();
      show($('vault-state-empty'), false);
      show($('vault-create-flow'), true);
      setStatus('vault-create-status', (window.QSDMWallet && window.QSDMWallet.isReady()) ? 'Ready.' : 'Waiting for WASM module…', 'ok');
    });
    if ($('vault-import-btn')) $('vault-import-btn').addEventListener('click', function () {
      hideAllFlows();
      show($('vault-state-empty'), false);
      show($('vault-import-flow'), true);
    });
    if ($('vault-create-cancel')) $('vault-create-cancel').addEventListener('click', function () {
      hideAllFlows();
      renderState();
    });
    if ($('vault-import-cancel')) $('vault-import-cancel').addEventListener('click', function () {
      hideAllFlows();
      renderState();
    });

    // Create flow
    if ($('vault-create-go')) $('vault-create-go').addEventListener('click', async function () {
      const QW = window.QSDMWallet;
      if (!QW || !QW.isReady()) {
        setStatus('vault-create-status', 'WASM not ready yet — try again in a second.', 'warn');
        return;
      }
      const p1 = $('vault-create-pass1').value;
      const p2 = $('vault-create-pass2').value;
      if (p1.length < 12) {
        setStatus('vault-create-status', 'Passphrase too short (need 12+ chars).', 'err');
        return;
      }
      if (p1 !== p2) {
        setStatus('vault-create-status', 'Passphrases do not match.', 'err');
        return;
      }
      setStatus('vault-create-status', '<span class="spinner"></span>Generating ML-DSA-87 keypair…');
      try {
        const gen = window.qsdm_wallet_generate();
        if (typeof gen !== 'object' || !gen.public_key_hex || !gen.private_key_hex) {
          throw new Error('keygen failed');
        }
        const addr = window.qsdm_wallet_address_from_public_key(gen.public_key_hex);
        const priv = QW.hexToBytes(gen.private_key_hex);
        const env  = await QW.encryptPrivateKey(priv, p1);
        priv.fill(0);
        const ks   = QW.buildKeystore(addr, gen.public_key_hex, env);
        saveVault(ks);
        // Immediately unlock for the user — they just typed the passphrase.
        const priv2 = await QW.decryptPrivateKey(ks, p1);
        session.address      = addr;
        session.publicKeyHex = gen.public_key_hex;
        session.privBytes    = priv2;
        $('vault-create-pass1').value = '';
        $('vault-create-pass2').value = '';
        hideAllFlows();
        resetIdleTimer();
        renderState();
        setStatus('vault-create-status', '', null);
      } catch (e) {
        setStatus('vault-create-status', 'Generation failed: ' + e.message, 'err');
      }
    });

    // Import flow
    if ($('vault-import-go')) $('vault-import-go').addEventListener('click', async function () {
      const QW = window.QSDMWallet;
      if (!QW) { setStatus('vault-import-status', 'Wallet helpers not loaded.', 'err'); return; }
      const file = $('vault-import-file').files[0];
      const pass = $('vault-import-pass').value;
      if (!file)       { setStatus('vault-import-status', 'Pick a wallet.json file.', 'err'); return; }
      if (pass.length < 1) { setStatus('vault-import-status', 'Enter the passphrase.', 'err'); return; }
      try {
        const text = await file.text();
        let ks;
        try { ks = JSON.parse(text); } catch (e) { throw new Error('not valid JSON'); }
        await QW.validateKeystore(ks);
        const priv = await QW.decryptPrivateKey(ks, pass);
        // Persist + unlock.
        saveVault(ks);
        session.address      = ks.address;
        session.publicKeyHex = ks.public_key;
        session.privBytes    = priv;
        $('vault-import-pass').value = '';
        hideAllFlows();
        resetIdleTimer();
        renderState();
      } catch (e) {
        setStatus('vault-import-status', 'Import failed: ' + e.message, 'err');
      }
    });

    // Locked state
    if ($('vault-unlock-btn')) $('vault-unlock-btn').addEventListener('click', async function () {
      const QW = window.QSDMWallet;
      const ks = loadVault();
      if (!QW || !ks) return;
      const pass = $('vault-unlock-pass').value;
      if (!pass) {
        setStatus('vault-unlock-status', 'Enter your passphrase.', 'err');
        return;
      }
      setStatus('vault-unlock-status', '<span class="spinner"></span>Decrypting…');
      try {
        const priv = await QW.decryptPrivateKey(ks, pass);
        session.address      = ks.address;
        session.publicKeyHex = ks.public_key;
        session.privBytes    = priv;
        $('vault-unlock-pass').value = '';
        resetIdleTimer();
        setStatus('vault-unlock-status', '', null);
        renderState();
      } catch (e) {
        setStatus('vault-unlock-status', e.message, 'err');
      }
    });
    if ($('vault-locked-copy')) $('vault-locked-copy').addEventListener('click', function () {
      const ks = loadVault();
      if (ks && navigator.clipboard) {
        navigator.clipboard.writeText(ks.address);
        setStatus('vault-unlock-status', 'Address copied.', 'ok');
      }
    });
    if ($('vault-forget-btn')) $('vault-forget-btn').addEventListener('click', function () {
      if (!confirm('Wipe the encrypted vault from this browser? Make sure you have wallet.json exported first — this cannot be undone.')) return;
      wipeVault();
      lockSession('Vault wiped.');
      renderState();
    });

    // Unlocked: address + balance
    if ($('vault-unlocked-copy')) $('vault-unlocked-copy').addEventListener('click', function () {
      if (session.address && navigator.clipboard) {
        navigator.clipboard.writeText(session.address);
      }
    });

    // Unlocked: send tab
    if ($('vault-send-btn')) $('vault-send-btn').addEventListener('click', async function () {
      const QW = window.QSDMWallet;
      if (!isUnlocked() || !QW) return;
      const recipient = ($('vault-send-recipient').value || '').trim().toLowerCase();
      const amountStr = ($('vault-send-amount').value || '').trim();
      const feeStr    = ($('vault-send-fee').value    || '0').trim();
      const geotag    = ($('vault-send-geotag').value || 'US').trim().toUpperCase();
      if (!QW.isValidAddress(recipient)) {
        setStatus('vault-send-status', 'Recipient must be 64 lowercase hex chars.', 'err');
        return;
      }
      const amount = Number(amountStr);
      const fee    = Number(feeStr);
      if (!(amount > 0)) {
        setStatus('vault-send-status', 'Amount must be > 0 CELL.', 'err');
        return;
      }
      if (!(fee >= 0)) {
        setStatus('vault-send-status', 'Fee must be ≥ 0 CELL.', 'err');
        return;
      }
      setStatus('vault-send-status', '<span class="spinner"></span>Fetching nonce…');
      // Get nonce (best-effort; validator endpoint).
      let nonce = 0;
      try {
        const nresp = await fetch('https://api.qsdm.tech/api/v1/wallet/nonce?sender=' + encodeURIComponent(session.address), { credentials: 'omit', cache: 'no-store' });
        if (nresp.ok) {
          const nbody = await nresp.json();
          if (typeof nbody.next === 'number') nonce = nbody.next;
        }
      } catch (_) { /* fall through with nonce=0 */ }
      // Review screen: nothing is signed or sent until the user confirms.
      setStatus('vault-send-status', 'Review the transfer in the dialog…');
      let confirmed = false;
      const UI = window.QSDMWalletUI;
      if (UI && UI.reviewSend) {
        confirmed = await UI.reviewSend({
          from: session.address, to: recipient, amount: amount, fee: fee,
          nonce: nonce, geotag: geotag,
        });
      } else {
        confirmed = window.confirm('Send ' + amount + ' CELL (fee ' + fee + ') to ' + recipient + '?');
      }
      if (!confirmed) {
        setStatus('vault-send-status', 'Cancelled. Nothing was signed or sent.', 'warn');
        return;
      }
      if (!isUnlocked()) {
        setStatus('vault-send-status', 'The wallet locked while the review was open. Unlock and try again; nothing was sent.', 'warn');
        return;
      }
      setStatus('vault-send-status', '<span class="spinner"></span>Signing…');
      // Build canonical envelope (same shape the existing Send tab uses).
      const timestamp = new Date().toISOString();
      const txID = (function () {
        // Browser-side deterministic-ish tx_id: sha256 of address|recipient|amount|nonce|now
        // The validator only cares that tx_id is unique per sender; collisions across senders
        // are OK. We do the hash inline so we don't pull a crypto helper.
        return 'tx-' + Math.floor(Math.random() * 1e9).toString(16) + Date.now().toString(16);
      })();
      const envelope = {
        id: txID,
        sender: session.address,
        recipient: recipient,
        amount: amount,
        fee: fee,
        geotag: geotag,
        parent_cells: [],
        timestamp: timestamp,
      };
      if (nonce > 0) envelope.nonce = nonce;
      const signed = window.qsdm_wallet_sign_transaction(
        JSON.stringify(envelope),
        QW.bytesToHex(session.privBytes),
        session.publicKeyHex,
      );
      if (typeof signed !== 'string') {
        setStatus('vault-send-status', 'Sign failed: ' + (signed && signed.error ? signed.error : 'unknown'), 'err');
        return;
      }
      setStatus('vault-send-status', '<span class="spinner"></span>Submitting…');
      let resp, body;
      try {
        resp = await fetch('https://api.qsdm.tech/api/v1/wallet/submit-signed', {
          method: 'POST',
          credentials: 'omit',
          headers: { 'Accept': 'application/json', 'Content-Type': 'application/json' },
          body: signed,
        });
        try { body = await resp.json(); } catch (_) { body = {}; }
      } catch (e) {
        setStatus('vault-send-status', 'Network error: ' + e.message, 'err');
        appendActivity({ timestamp: Date.now(), tx_id: txID, recipient: recipient, amount: amount, status: 'failed' });
        return;
      }
      const ok = resp.status >= 200 && resp.status < 300;
      const status = (body && body.status) || (ok ? 'accepted' : 'failed');
      appendActivity({
        timestamp: Date.now(),
        tx_id: (body && body.tx_id) || txID,
        recipient: recipient,
        amount: amount,
        status: status,
        http: resp.status,
      });
      const cls = ok ? 'ok' : (status === 'duplicate' ? 'warn' : 'err');
      const msg = ok ? (status + ' · tx ' + (((body && body.tx_id) || txID).slice(0, 14)) + '…')
                     : ('HTTP ' + resp.status + ': ' + ((body && (body.error || body.detail || body.message)) || resp.statusText));
      setStatus('vault-send-status', msg, cls);
      if (ok && window.QSDMWalletUI && window.QSDMWalletUI.onSent) {
        window.QSDMWalletUI.onSent((body && body.tx_id) || txID);
      }
      if (ok) {
        $('vault-send-recipient').value = '';
        $('vault-send-amount').value = '';
        $('vault-send-fee').value = '';
        // Trigger a balance refresh.
        setTimeout(refreshBalance, 1500);
      }
    });

    // Receive
    if ($('vault-receive-copy')) $('vault-receive-copy').addEventListener('click', function () {
      if (session.address && navigator.clipboard) {
        navigator.clipboard.writeText(session.address);
        setStatus('vault-receive-status', 'Address copied to clipboard.', 'ok');
      }
    });

    // Activity
    if ($('vault-activity-clear')) $('vault-activity-clear').addEventListener('click', function () {
      if (confirm('Clear the local activity log? The validator still has the confirmed record.')) {
        clearActivity();
      }
    });

    // Settings
    if ($('vault-idle-minutes')) {
      const s = loadSettings();
      $('vault-idle-minutes').value = String(s.idleMinutes || 5);
      $('vault-idle-minutes').addEventListener('change', function () {
        const v = Math.max(0, Math.min(120, parseInt(this.value, 10) || 0));
        saveSettings({ idleMinutes: v });
        resetIdleTimer();
        setStatus('vault-settings-status', 'Idle auto-lock: ' + (v > 0 ? v + ' min' : 'disabled'), 'ok');
      });
    }
    if ($('vault-export-btn')) $('vault-export-btn').addEventListener('click', function () {
      const ks = loadVault();
      if (!ks) return;
      const blob = new Blob([JSON.stringify(ks, null, 2)], { type: 'application/json' });
      const url  = URL.createObjectURL(blob);
      const a    = document.createElement('a');
      a.href = url;
      a.download = 'wallet.json';
      document.body.appendChild(a); a.click(); a.remove();
      setTimeout(function () { URL.revokeObjectURL(url); }, 5000);
    });
    if ($('vault-settings-forget')) $('vault-settings-forget').addEventListener('click', function () {
      if (!confirm('Wipe the encrypted vault from this browser? Make sure you have wallet.json exported first.')) return;
      wipeVault();
      lockSession('Vault wiped.');
      renderState();
    });
    if ($('vault-lock-btn')) $('vault-lock-btn').addEventListener('click', function () {
      lockSession('Locked by user.');
    });

    // V-tabs (Send / Receive / Activity / Settings)
    document.querySelectorAll('[data-vtab]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        document.querySelectorAll('[data-vtab]').forEach(function (b) { b.classList.remove('active'); });
        document.querySelectorAll('[data-vpane]').forEach(function (p) { p.classList.remove('active'); });
        btn.classList.add('active');
        const name = btn.dataset.vtab;
        const pane = document.querySelector('[data-vpane="' + name + '"]');
        if (pane) pane.classList.add('active');
      });
    });
  }

  // ----- bootstrap -----
  function boot() {
    bindButtons();
    renderState();
    // Wait for WASM ready, then refresh status on the create flow.
    (function poll() {
      const QW = window.QSDMWallet;
      if (QW && QW.isReady()) {
        if (!loadVault() && $('vault-create-flow') && !$('vault-create-flow').hidden) {
          setStatus('vault-create-status', 'Ready.', 'ok');
        }
        return;
      }
      setTimeout(poll, 100);
    })();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
