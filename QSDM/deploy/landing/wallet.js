// qsdm.tech/wallet/ — browser-side wallet client.
//
// Threading model:
//   1) wasm_exec.js + wallet.wasm produce 5 globals:
//        qsdm_wallet_generate / _address_from_public_key / _sign / _verify / _version
//      Plus qsdm_wallet_ready === true.
//   2) WebCrypto handles the symmetric envelope (PBKDF2 → AES-256-GCM)
//      with parameters byte-identical to pkg/keystore (the Go package).
//      This file is the source of truth for the JS side of that
//      compatibility; if you bump pkg/keystore's constants, bump them
//      here too.
//   3) The page never POSTs anything. Even crash reports are out of
//      scope — a private-key-bearing page must not have any client-side
//      telemetry.
//
// Keystore format (v1) — must stay byte-for-byte identical to
// pkg/keystore in the QSDM Go source tree:
//
//   {
//     "version": 1, "type": "qsdm-keystore", "algorithm": "ml-dsa-87",
//     "address": "<hex sha256(pubkey)>",
//     "public_key": "<hex 2592>",
//     "kdf": "pbkdf2-sha256",
//     "kdf_params": { "iterations": 600000, "salt": "<hex 16>", "key_len": 32 },
//     "cipher": "aes-256-gcm",
//     "cipher_params": { "nonce": "<hex 12>" },
//     "ciphertext": "<hex AES-GCM ct||tag>",
//     "created_at": "RFC3339 UTC"
//   }
//
// "iterations" can be raised in future builds but never lowered; the
// Validate() check on the Go side rejects anything below 100 000.

(function () {
  'use strict';

  // ----- shared constants (must match pkg/keystore) -----
  const KEYSTORE_VERSION    = 1;
  const KEYSTORE_TYPE       = 'qsdm-keystore';
  const KEYSTORE_ALGO       = 'ml-dsa-87';
  const KEYSTORE_KDF        = 'pbkdf2-sha256';
  const KEYSTORE_CIPHER     = 'aes-256-gcm';
  const PBKDF2_ITERATIONS   = 600_000;
  const PBKDF2_SALT_BYTES   = 16;
  const PBKDF2_KEY_BYTES    = 32; // AES-256 key
  const GCM_NONCE_BYTES     = 12;
  const PUBLIC_KEY_BYTES    = 2592;

  // ----- DOM helpers -----
  const $ = (id) => document.getElementById(id);
  function setStatus(elId, msg, cls) {
    const el = $(elId);
    if (!el) return;
    el.innerHTML = cls ? `<span class="${cls}">${msg}</span>` : msg;
  }
  function setStatusBusy(elId, msg) {
    const el = $(elId);
    if (!el) return;
    el.innerHTML = `<span class="spinner"></span>${msg}`;
  }

  // ----- hex helpers -----
  function bytesToHex(bytes) {
    const arr = new Uint8Array(bytes);
    let s = '';
    for (let i = 0; i < arr.length; i++) {
      s += arr[i].toString(16).padStart(2, '0');
    }
    return s;
  }
  function hexToBytes(hex) {
    if (typeof hex !== 'string' || hex.length % 2 !== 0) {
      throw new Error('hex string has odd length');
    }
    const out = new Uint8Array(hex.length / 2);
    for (let i = 0; i < out.length; i++) {
      const b = parseInt(hex.substr(i * 2, 2), 16);
      if (Number.isNaN(b)) throw new Error('hex string contains non-hex character');
      out[i] = b;
    }
    return out;
  }
  function utf8Encode(s) { return new TextEncoder().encode(s); }

  // ----- WebCrypto envelope -----
  // PBKDF2-derive an AES-256-GCM key from a passphrase + salt. Matches
  // pkg/keystore.Encrypt / pkg/keystore.Decrypt parameters exactly.
  async function deriveKey(passphrase, salt, iterations) {
    const baseKey = await crypto.subtle.importKey(
      'raw', utf8Encode(passphrase), { name: 'PBKDF2' }, false, ['deriveKey']
    );
    return crypto.subtle.deriveKey(
      {
        name: 'PBKDF2',
        salt: salt,
        iterations: iterations,
        hash: 'SHA-256',
      },
      baseKey,
      { name: 'AES-GCM', length: PBKDF2_KEY_BYTES * 8 },
      false,
      ['encrypt', 'decrypt'],
    );
  }

  async function encryptPrivateKey(privateKeyBytes, passphrase) {
    if (!passphrase || passphrase.length === 0) {
      throw new Error('empty passphrase refused');
    }
    const salt  = crypto.getRandomValues(new Uint8Array(PBKDF2_SALT_BYTES));
    const nonce = crypto.getRandomValues(new Uint8Array(GCM_NONCE_BYTES));
    const key   = await deriveKey(passphrase, salt, PBKDF2_ITERATIONS);
    const ct    = await crypto.subtle.encrypt(
      { name: 'AES-GCM', iv: nonce },
      key,
      privateKeyBytes,
    );
    return { salt, nonce, ciphertext: new Uint8Array(ct) };
  }

  async function decryptPrivateKey(keystore, passphrase) {
    if (!passphrase || passphrase.length === 0) {
      throw new Error('empty passphrase refused');
    }
    if (keystore.kdf !== KEYSTORE_KDF) {
      throw new Error(`unsupported kdf "${keystore.kdf}" (want "${KEYSTORE_KDF}")`);
    }
    if (keystore.cipher !== KEYSTORE_CIPHER) {
      throw new Error(`unsupported cipher "${keystore.cipher}" (want "${KEYSTORE_CIPHER}")`);
    }
    if (keystore.kdf_params.iterations < 100_000) {
      throw new Error(`pbkdf2 iterations=${keystore.kdf_params.iterations} is below the 100k floor`);
    }
    const salt  = hexToBytes(keystore.kdf_params.salt);
    const nonce = hexToBytes(keystore.cipher_params.nonce);
    const ct    = hexToBytes(keystore.ciphertext);
    const key   = await deriveKey(passphrase, salt, keystore.kdf_params.iterations);
    let pt;
    try {
      pt = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: nonce }, key, ct);
    } catch (e) {
      // Web crypto OperationError on auth failure / tamper. Collapse
      // to a single message to match pkg/keystore.ErrInvalidPassphrase.
      throw new Error('passphrase does not match (or the keystore is corrupted)');
    }
    return new Uint8Array(pt);
  }

  function buildKeystore(addressHex, publicKeyHex, env) {
    return {
      version:    KEYSTORE_VERSION,
      type:       KEYSTORE_TYPE,
      algorithm:  KEYSTORE_ALGO,
      address:    addressHex,
      public_key: publicKeyHex,
      kdf:        KEYSTORE_KDF,
      kdf_params: {
        iterations: PBKDF2_ITERATIONS,
        salt:       bytesToHex(env.salt),
        key_len:    PBKDF2_KEY_BYTES,
      },
      cipher: KEYSTORE_CIPHER,
      cipher_params: {
        nonce: bytesToHex(env.nonce),
      },
      ciphertext: bytesToHex(env.ciphertext),
      created_at: new Date().toISOString(),
    };
  }

  function validateKeystore(ks) {
    if (!ks || typeof ks !== 'object') throw new Error('not a keystore object');
    if (ks.version !== KEYSTORE_VERSION) throw new Error(`unsupported version ${ks.version}`);
    if (ks.type !== KEYSTORE_TYPE) throw new Error(`bad type "${ks.type}"`);
    if (ks.algorithm !== KEYSTORE_ALGO) throw new Error(`bad algorithm "${ks.algorithm}"`);
    if (typeof ks.public_key !== 'string') throw new Error('public_key missing');
    if (typeof ks.address !== 'string') throw new Error('address missing');
    if (typeof ks.ciphertext !== 'string') throw new Error('ciphertext missing');
    if (!ks.kdf_params || typeof ks.kdf_params.salt !== 'string') throw new Error('kdf_params.salt missing');
    if (!ks.cipher_params || typeof ks.cipher_params.nonce !== 'string') throw new Error('cipher_params.nonce missing');
    const pk = hexToBytes(ks.public_key);
    if (pk.length !== PUBLIC_KEY_BYTES) {
      throw new Error(`public_key is ${pk.length} bytes (want ${PUBLIC_KEY_BYTES})`);
    }
    // Cross-check address ↔ public_key, mirroring pkg/keystore.Validate.
    return crypto.subtle.digest('SHA-256', pk).then((digest) => {
      const recomputed = bytesToHex(digest);
      if (recomputed !== ks.address) {
        throw new Error('address does not match sha256(public_key) — file is mutated');
      }
      return ks;
    });
  }

  // ----- WASM bootstrap -----
  let wasmReady = false;
  async function bootWASM() {
    if (typeof Go === 'undefined') {
      setStatus('gen-status', 'wasm_exec.js failed to load', 'err');
      return;
    }
    const go = new Go();
    try {
      // Subresource Integrity on the WASM fetch.
      // The literal sha384 hash is rewritten in-place by
      // QSDM/scripts/build_wallet_wasm.sh after a clean rebuild
      // (look for the `update_sri_hashes` shell function), so an
      // operator never has to remember to rotate it manually. The
      // browser refuses the fetch if the served bytes don't match
      // — defence-in-depth against a Caddy / CDN swap that would
      // otherwise pair a rogue wallet.wasm with our legitimate
      // wallet.html. A fail-closed at fetch time also produces a
      // visible TypeError in DevTools rather than a silent
      // wrong-key signature.
      const resp = await fetch('/wallet.wasm', {
        integrity: 'sha384-yHrwzrXeXp0uvr4XuFFXM0iPZL5ZEcku33QrczqotHpO+jtnqwqemfADTrcVQHmw',
        // `same-origin` is the implicit default for /wallet.wasm
        // because the page is served from the same origin, but
        // pinning it here means a future move to a CDN sub-domain
        // (e.g. cdn.qsdm.tech) won't silently change the cred
        // behaviour without an explicit recheck of the SRI policy.
        credentials: 'same-origin',
      });
      if (!resp.ok) throw new Error(`wallet.wasm fetch: HTTP ${resp.status}`);
      const buf = await resp.arrayBuffer();
      const result = await WebAssembly.instantiate(buf, go.importObject);
      go.run(result.instance);
    } catch (e) {
      setStatus('gen-status', `WASM init failed: ${e.message}`, 'err');
      return;
    }
    // Poll for the ready flag — Go's main() sets it after registering FuncOfs.
    const t0 = Date.now();
    while (!window.qsdm_wallet_ready) {
      if (Date.now() - t0 > 3000) {
        setStatus('gen-status', 'WASM module did not signal readiness within 3s', 'err');
        return;
      }
      await new Promise(r => setTimeout(r, 20));
    }
    wasmReady = true;
    setStatus('gen-status', 'Ready. Type a passphrase and click Generate.', 'ok');
    const ver = typeof window.qsdm_wallet_version === 'function' ? window.qsdm_wallet_version() : 'unknown';
    const verEl = $('wasm-version');
    if (verEl) verEl.textContent = ver;
  }

  // ----- Read-only balance lookup -----
  //
  // The validator HTTP API exposes `GET /api/v1/wallet/balance?address=<addr>`
  // as a public endpoint (no Authorization header required) — confirmed by
  // `publicPaths` in pkg/api/middleware.go. The response shape is
  // `{ "address": "<hex>", "balance": <number-of-CELL> }` where balance is
  // a float64 of CELL (storage.GetBalance returns float64, not dust). The
  // entire Generate / Open / Sign machinery is unaware of this endpoint and
  // can stand alone with no network access; balance lookup is opt-in via
  // its own tab.
  const BALANCE_ENDPOINT = 'https://api.qsdm.tech/api/v1/wallet/balance';
  let lastAddress = null;

  // Address shape check: lowercase hex, exactly 64 chars (32 bytes —
  // sha256 of the public key). The validator will reject malformed input
  // anyway, but a client-side check produces a useful error before the
  // round trip and stops obvious typos from polluting the validator's
  // HTTP access log.
  function isValidAddress(s) {
    return typeof s === 'string' && /^[0-9a-f]{64}$/i.test(s);
  }
  // Render the API's float64-of-CELL response with fixed 8 decimals
  // (the smallest unit on QSDM is "dust" = 10^-8 CELL). We keep the
  // raw number alongside the formatted version because float64 can
  // lose precision on very large balances and operators may want to
  // see exactly what the API returned.
  function formatCell(n) {
    if (typeof n !== 'number' || !Number.isFinite(n)) return String(n);
    if (n === 0) return '0 CELL';
    const sign = n < 0 ? '-' : '';
    const abs = Math.abs(n);
    return `${sign}${abs.toFixed(8).replace(/0+$/, '').replace(/\.$/, '')} CELL`;
  }

  // Hook called from Generate / Open after a valid address surfaces in
  // this tab. Enables the "Use my last address" shortcut on the
  // Balance pane and prefills the address input if it's currently empty.
  function rememberAddress(addr) {
    if (!isValidAddress(addr)) return;
    lastAddress = addr.toLowerCase();
    const btn = $('bal-use-last');
    if (btn) btn.disabled = false;
    const input = $('bal-addr');
    if (input && !input.value.trim()) input.value = lastAddress;
  }

  // ----- UI wiring -----

  // Tabs
  document.querySelectorAll('.tab').forEach((btn) => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.tab').forEach(b => b.classList.remove('active'));
      document.querySelectorAll('.tab-pane').forEach(p => p.classList.remove('active'));
      btn.classList.add('active');
      const name = btn.dataset.tab;
      document.querySelector(`.tab-pane[data-pane="${name}"]`).classList.add('active');
    });
  });

  // Reveal-passphrase toggle (Generate tab only)
  $('gen-reveal').addEventListener('click', () => {
    const t = $('gen-pass1').type === 'password' ? 'text' : 'password';
    $('gen-pass1').type = t;
    $('gen-pass2').type = t;
  });

  // ----- Generate flow -----
  $('gen-btn').addEventListener('click', async () => {
    if (!wasmReady) {
      setStatus('gen-status', 'WASM not ready yet', 'err');
      return;
    }
    const p1 = $('gen-pass1').value;
    const p2 = $('gen-pass2').value;
    if (!p1) { setStatus('gen-status', 'passphrase is empty', 'err'); return; }
    if (p1 !== p2) { setStatus('gen-status', 'passphrases do not match', 'err'); return; }
    if (p1.length < 8) {
      setStatus('gen-status', 'passphrase shorter than 8 chars (12+ recommended)', 'warn');
      // we warn but proceed — pkg/keystore only refuses zero length.
    }

    setStatusBusy('gen-status', 'generating ML-DSA-87 keypair…');
    // Yield once so the spinner paints.
    await new Promise(r => setTimeout(r, 0));

    const out = window.qsdm_wallet_generate();
    if (out && out.error) {
      setStatus('gen-status', `keygen failed: ${out.error}`, 'err');
      return;
    }

    setStatusBusy('gen-status', `encrypting (PBKDF2 ${PBKDF2_ITERATIONS.toLocaleString()} iters → AES-256-GCM)…`);
    let env;
    try {
      env = await encryptPrivateKey(hexToBytes(out.private_key_hex), p1);
    } catch (e) {
      setStatus('gen-status', `encrypt failed: ${e.message}`, 'err');
      return;
    }
    const keystore = buildKeystore(out.address, out.public_key_hex, env);
    // Zero the plaintext private key reference (browser GC eventually
    // collects, but explicit overwrite reduces the in-memory window).
    out.private_key_hex = '\0'.repeat(out.private_key_hex.length);

    // Render result + download button.
    const blob = new Blob([JSON.stringify(keystore, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const filename = `qsdm-wallet-${keystore.address.slice(0, 12)}.json`;

    const r = $('gen-result');
    r.hidden = false;
    r.innerHTML = `
      <div class="result">
        <h3>Wallet ready</h3>
        <div class="kv">
          <div class="k">address</div><div class="v">${keystore.address}</div>
          <div class="k">algorithm</div><div class="v">${keystore.algorithm}</div>
          <div class="k">public key</div><div class="v long">${keystore.public_key}</div>
          <div class="k">kdf</div><div class="v">${keystore.kdf} (iterations=${keystore.kdf_params.iterations})</div>
          <div class="k">cipher</div><div class="v">${keystore.cipher}</div>
          <div class="k">created</div><div class="v">${keystore.created_at}</div>
        </div>
        <div class="actions" style="margin-top: 14px">
          <a class="btn btn-primary" href="${url}" download="${filename}">Download ${filename}</a>
          <button class="btn btn-ghost" id="gen-copy-addr">Copy address</button>
        </div>
        <div class="status-line" style="margin-top:14px">
          <span class="warn">⚠ Back up this file <strong>and</strong> the passphrase.</span>
          Losing either makes the address unrecoverable.
        </div>
      </div>`;
    $('gen-copy-addr').addEventListener('click', () => {
      navigator.clipboard.writeText(keystore.address).then(() => {
        $('gen-copy-addr').textContent = 'Copied!';
        setTimeout(() => { $('gen-copy-addr').textContent = 'Copy address'; }, 1200);
      });
    });
    setStatus('gen-status', 'Wallet generated. Click Download.', 'ok');
    rememberAddress(keystore.address);
    // Clear passphrase fields after generation so they don't linger.
    $('gen-pass1').value = '';
    $('gen-pass2').value = '';
  });

  // ----- Open flow -----
  async function readKeystoreFromFile(input) {
    if (!input.files || !input.files[0]) throw new Error('no file selected');
    const text = await input.files[0].text();
    const ks = JSON.parse(text);
    await validateKeystore(ks);
    return ks;
  }

  $('open-btn').addEventListener('click', async () => {
    setStatusBusy('open-status', 'reading & decrypting…');
    let ks;
    try {
      ks = await readKeystoreFromFile($('open-file'));
    } catch (e) {
      setStatus('open-status', `keystore: ${e.message}`, 'err');
      return;
    }
    let priv;
    try {
      priv = await decryptPrivateKey(ks, $('open-pass').value);
    } catch (e) {
      setStatus('open-status', e.message, 'err');
      return;
    }
    // Cross-check: derive the public key from the decrypted private and
    // confirm it matches the keystore's public_key field. Until the WASM
    // adds an "extract pubkey from privkey" entry point, we instead
    // round-trip via the address: sign a probe message + verify with the
    // stored public key. If verify is true, the keypair matches.
    const probe = bytesToHex(crypto.getRandomValues(new Uint8Array(16)));
    const sig = window.qsdm_wallet_sign(bytesToHex(priv), probe);
    if (typeof sig !== 'string') {
      setStatus('open-status', `sign probe failed: ${sig.error || 'unknown'}`, 'err');
      return;
    }
    const ok = window.qsdm_wallet_verify(ks.public_key, probe, sig);
    if (ok !== true) {
      setStatus('open-status', 'integrity check failed: decrypted private key does not match stored public_key', 'err');
      return;
    }
    // Zero the priv bytes we still hold.
    priv.fill(0);

    const r = $('open-result');
    r.hidden = false;
    r.innerHTML = `
      <div class="result">
        <h3>Keystore valid</h3>
        <div class="kv">
          <div class="k">address</div><div class="v">${ks.address}</div>
          <div class="k">algorithm</div><div class="v">${ks.algorithm}</div>
          <div class="k">created</div><div class="v">${ks.created_at}</div>
          <div class="k">kdf</div><div class="v">${ks.kdf} (iterations=${ks.kdf_params.iterations})</div>
          <div class="k">integrity</div><div class="v"><span class="ok">✓ private key reproduces the stored public key</span></div>
        </div>
        <div class="actions" style="margin-top: 14px">
          <button class="btn btn-ghost" id="open-copy-addr">Copy address</button>
        </div>
      </div>`;
    $('open-copy-addr').addEventListener('click', () => {
      navigator.clipboard.writeText(ks.address);
      $('open-copy-addr').textContent = 'Copied!';
      setTimeout(() => { $('open-copy-addr').textContent = 'Copy address'; }, 1200);
    });
    setStatus('open-status', 'Keystore decrypted & verified.', 'ok');
    rememberAddress(ks.address);
    $('open-pass').value = '';
  });

  // ----- Sign flow -----
  $('sign-btn').addEventListener('click', async () => {
    setStatusBusy('sign-status', 'decrypting & signing…');
    let ks;
    try {
      ks = await readKeystoreFromFile($('sign-file'));
    } catch (e) {
      setStatus('sign-status', `keystore: ${e.message}`, 'err');
      return;
    }
    let priv;
    try {
      priv = await decryptPrivateKey(ks, $('sign-pass').value);
    } catch (e) {
      setStatus('sign-status', e.message, 'err');
      return;
    }
    const msg = utf8Encode($('sign-msg').value || '');
    if (msg.length === 0) {
      setStatus('sign-status', 'message is empty (refusing to sign nothing)', 'err');
      priv.fill(0);
      return;
    }
    const sig = window.qsdm_wallet_sign(bytesToHex(priv), bytesToHex(msg));
    priv.fill(0);
    if (typeof sig !== 'string') {
      setStatus('sign-status', `sign failed: ${sig.error || 'unknown'}`, 'err');
      return;
    }

    const r = $('sign-result');
    r.hidden = false;
    r.innerHTML = `
      <div class="result">
        <h3>Signed</h3>
        <div class="kv">
          <div class="k">signer</div><div class="v">${ks.address}</div>
          <div class="k">message</div><div class="v">${escapeHtml($('sign-msg').value)}</div>
          <div class="k">signature</div><div class="v long">${sig}</div>
        </div>
        <div class="actions" style="margin-top: 14px">
          <button class="btn btn-ghost" id="sign-copy">Copy signature (hex)</button>
        </div>
      </div>`;
    $('sign-copy').addEventListener('click', () => {
      navigator.clipboard.writeText(sig);
      $('sign-copy').textContent = 'Copied!';
      setTimeout(() => { $('sign-copy').textContent = 'Copy signature (hex)'; }, 1200);
    });
    setStatus('sign-status', `Signed (${sig.length / 2} bytes)`, 'ok');
    $('sign-pass').value = '';
  });

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    })[c]);
  }

  // ----- Balance flow -----
  $('bal-use-last').addEventListener('click', () => {
    if (!lastAddress) return;
    $('bal-addr').value = lastAddress;
    setStatus('bal-status', `Filled in ${lastAddress.slice(0, 12)}…`, 'ok');
  });

  $('bal-btn').addEventListener('click', async () => {
    const addr = ($('bal-addr').value || '').trim().toLowerCase();
    if (!addr) {
      setStatus('bal-status', 'enter an address (64 hex chars) first', 'err');
      return;
    }
    if (!isValidAddress(addr)) {
      setStatus('bal-status', `not a valid QSDM address: expected 64 hex chars, got ${addr.length}`, 'err');
      return;
    }

    setStatusBusy('bal-status', `querying ${BALANCE_ENDPOINT}…`);
    // AbortController gives us a hard 12-second ceiling on the request.
    // If the validator is slow / unreachable we want a clear error in
    // the UI rather than a status line that says "querying…" forever.
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), 12_000);
    let resp;
    try {
      resp = await fetch(`${BALANCE_ENDPOINT}?address=${encodeURIComponent(addr)}`, {
        method: 'GET',
        // Explicitly omit credentials — there's no Authorization
        // header anyway, but this defends against a future CSRF angle
        // if the wallet page is ever embedded as an iframe.
        credentials: 'omit',
        signal: ctl.signal,
        headers: { 'Accept': 'application/json' },
      });
    } catch (e) {
      clearTimeout(timer);
      const reason = e.name === 'AbortError' ? 'timed out after 12s' : e.message;
      setStatus('bal-status', `network error: ${reason}`, 'err');
      return;
    }
    clearTimeout(timer);

    if (!resp.ok) {
      setStatus('bal-status', `HTTP ${resp.status} ${resp.statusText}`, 'err');
      return;
    }
    let body;
    try {
      body = await resp.json();
    } catch (e) {
      setStatus('bal-status', `bad JSON from API: ${e.message}`, 'err');
      return;
    }
    if (typeof body !== 'object' || body === null || !('balance' in body)) {
      setStatus('bal-status', `unexpected API shape: ${JSON.stringify(body).slice(0, 80)}`, 'err');
      return;
    }
    if (body.address && body.address.toLowerCase() !== addr) {
      // Sanity: the validator should echo back the address we sent.
      // If it doesn't, somebody is rewriting the response in flight
      // — SRI doesn't cover dynamic JSON, so we surface this to the
      // user explicitly.
      setStatus(
        'bal-status',
        `address mismatch: asked ${addr.slice(0, 12)}…, got ${String(body.address).slice(0, 12)}…`,
        'err',
      );
      return;
    }

    const r = $('bal-result');
    r.hidden = false;
    r.innerHTML = `
      <div class="result">
        <h3>Balance</h3>
        <div class="kv">
          <div class="k">address</div><div class="v">${escapeHtml(addr)}</div>
          <div class="k">balance</div><div class="v"><strong>${formatCell(body.balance)}</strong></div>
          <div class="k">raw response</div><div class="v"><code>${escapeHtml(JSON.stringify(body))}</code></div>
          <div class="k">source</div><div class="v"><code>${escapeHtml(BALANCE_ENDPOINT)}?address=${escapeHtml(addr.slice(0, 12))}…</code></div>
          <div class="k">checked at</div><div class="v">${new Date().toISOString()}</div>
        </div>
        <div class="status-line" style="margin-top:14px">
          A balance of <code>0 CELL</code> on a freshly-generated address is normal.
          Run the reference miner against it (see <a href="https://github.com/blackbeardONE/QSDM/blob/main/QSDM/docs/docs/MINER_QUICKSTART.md">MINER_QUICKSTART</a>)
          to start earning block rewards.
        </div>
      </div>`;
    setStatus('bal-status', `Balance retrieved: ${formatCell(body.balance)}`, 'ok');
  });

  // ----- Go! -----
  bootWASM();
})();
