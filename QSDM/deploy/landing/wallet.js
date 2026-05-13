// qsdm.tech/wallet/ — browser-side wallet client.
//
// Threading model:
//   1) wasm_exec.js + wallet.wasm produce 6 globals:
//        qsdm_wallet_generate / _address_from_public_key / _sign / _verify /
//        _sign_transaction / _version
//      Plus qsdm_wallet_ready === true.
//      `_sign_transaction` was added in v0.4.0 Phase B (Session 96) to back
//      the new Send tab. It bundles canonical-payload marshalling +
//      ML-DSA-87 signing into a single call so the canonical bytes are
//      produced by Go's json.Marshal (matches the server-side
//      pkg/api/handlers.go::SubmitSignedTransaction canonicalisation
//      byte-for-byte, sidestepping JS/Go float-format drift).
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
        integrity: 'sha384-XKMSFMnk27ul5OLXqm2zFMPtsdSVUGNXK8sChbKc/Y2nIqVLEB330Ll+UDhz0Eb6',
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
          To start earning block rewards you need an enrolled NVIDIA GPU on the
          live mainnet (v2 only): bond <strong>10 CELL</strong> with
          <code>qsdmcli enroll</code>, then run
          <code>qsdmminer-console --protocol=v2</code>.
          See <a href="https://github.com/blackbeardONE/QSDM/blob/main/QSDM/docs/docs/MINER_QUICKSTART.md">MINER_QUICKSTART</a>
          (and <a href="https://github.com/blackbeardONE/QSDM/blob/main/QSDM/docs/docs/MINER_QUICKSTART.md#appendix-b-enrollment-funding-status">Appendix&nbsp;B</a>
          for the funding caveat &mdash; v0.4.0 has no public faucet yet).
        </div>
      </div>`;
    setStatus('bal-status', `Balance retrieved: ${formatCell(body.balance)}`, 'ok');
  });

  // ----- Send transaction flow (v0.4.0 Phase B, Session 96) -----
  //
  // The Send tab is the only tab that BOTH decrypts the private key
  // AND talks to the network. The flow:
  //
  //   1. Read + decrypt keystore (same path as Open / Sign).
  //   2. Validate recipient / amount / fee / geotag client-side.
  //   3. Derive sender = hex(sha256(public_key)) — matches what the
  //      server checks (HTTP 400 sender_mismatch on disagreement).
  //   4. Build an envelope object (no signature, no public_key — the
  //      WASM helper fills those in after signing).
  //   5. Call qsdm_wallet_sign_transaction — it canonicalises with
  //      Go's json.Marshal (byte-identical to what the server
  //      canonicalises against), ML-DSA-87-signs, and returns the
  //      final envelope JSON.
  //   6. Zero the decrypted private key buffer.
  //   7. POST the signed envelope to /api/v1/wallet/submit-signed
  //      and render the result.
  //
  // The server-side semantics are documented in
  // QSDM/docs/docs/V040_WALLET_SEND_DESIGN.md and audited via the
  // api-06 row in pkg/audit/checklist.go. v0.4.0 ships with two
  // intentional gaps:
  //   - no per-account nonce → cross-tx_id replay possible
  //   - storage debit isn't atomic with the balance check
  // Both are scheduled for v0.4.1. The user-facing warning text on
  // the Send tab calls this out.
  const SEND_ENDPOINT_DEFAULT = 'https://api.qsdm.tech/api/v1/wallet/submit-signed';

  // tx_id derivation matches pkg/wallet.WalletService.CreateTransaction:
  //   sha256(sender || recipient || timestamp.UnixNano)
  // first 16 bytes hex. We use Date.now() * 1e6 + sub-millisecond jitter
  // as a nanosecond-timestamp surrogate; the only consensus requirement
  // is that distinct submissions from the same sender produce distinct
  // tx_ids (server's idempotency check is keyed on tx_id), and a
  // collision would just return 409 Duplicate from the validator — not
  // a security issue, just a UX one. JS doesn't have a real ns-clock so
  // we splice 6 hex digits of crypto.getRandomValues to push the
  // collision probability into oblivion.
  async function deriveTxID(senderHex, recipientHex, timestampISO) {
    const nsHash = await crypto.subtle.digest(
      'SHA-256',
      utf8Encode(senderHex + recipientHex + timestampISO + bytesToHex(crypto.getRandomValues(new Uint8Array(3)))),
    );
    return bytesToHex(new Uint8Array(nsHash).slice(0, 16));
  }

  // Permissive amount parser: accepts "1", "1.5", "0.01", " 12.3 " etc.
  // Returns a finite, non-negative number or throws with a useful
  // message. Caller is responsible for the amount > 0 / fee >= 0 split.
  function parseCellAmount(label, raw) {
    const s = (raw || '').trim();
    if (!s) throw new Error(`${label} is required`);
    const n = Number(s);
    if (!Number.isFinite(n)) throw new Error(`${label} is not a number: "${s}"`);
    if (n < 0) throw new Error(`${label} cannot be negative`);
    return n;
  }

  $('send-btn').addEventListener('click', async () => {
    if (!wasmReady) {
      setStatus('send-status', 'WASM not ready yet', 'err');
      return;
    }
    if (typeof window.qsdm_wallet_sign_transaction !== 'function') {
      setStatus(
        'send-status',
        'this WASM build is missing qsdm_wallet_sign_transaction — rebuild wallet.wasm with the v0.4.0+ source',
        'err',
      );
      return;
    }

    // --- input validation (before we touch the keystore) ---
    const recipient = ($('send-recipient').value || '').trim().toLowerCase();
    if (!isValidAddress(recipient)) {
      setStatus('send-status', `recipient is not a valid 64-hex-char address (got ${recipient.length} chars)`, 'err');
      return;
    }
    let amount, fee;
    try {
      amount = parseCellAmount('amount', $('send-amount').value);
      fee = parseCellAmount('fee', $('send-fee').value);
    } catch (e) {
      setStatus('send-status', e.message, 'err');
      return;
    }
    if (amount <= 0) {
      setStatus('send-status', 'amount must be > 0', 'err');
      return;
    }
    const geotag = ($('send-geotag').value || '').trim().toUpperCase();
    if (!/^[A-Z]{2,3}$/.test(geotag)) {
      setStatus('send-status', `geotag must be 2–3 letters (got "${geotag}")`, 'err');
      return;
    }
    // parent_cells is optional; if empty we send two placeholder
    // hex64s so the validator's ValidateParentCells gate is satisfied.
    // pkg/wallet.CreateTransaction does the same thing server-side
    // when called with fewer than 2 parents (the "parent1"/"parent2"
    // strings on that path are NOT 64-hex though, so we use real
    // hex-shaped placeholders here — the validator's regex is
    // shape-only, no on-chain existence check).
    let parentCells = ($('send-parents').value || '')
      .split(/[,\n\s]+/).map(s => s.trim()).filter(Boolean);
    if (parentCells.length === 0) {
      parentCells = [
        '00000000000000000000000000000000000000000000000000000000000000a1',
        '00000000000000000000000000000000000000000000000000000000000000a2',
      ];
    }
    for (const p of parentCells) {
      if (!/^[0-9a-f]{32,128}$/i.test(p)) {
        setStatus('send-status', `parent cell "${p.slice(0, 16)}…" is not hex (32–128 chars)`, 'err');
        return;
      }
    }
    const endpoint = ($('send-endpoint').value || '').trim() || SEND_ENDPOINT_DEFAULT;

    // --- decrypt + sign ---
    setStatusBusy('send-status', 'reading & decrypting keystore…');
    let ks;
    try {
      ks = await readKeystoreFromFile($('send-file'));
    } catch (e) {
      setStatus('send-status', `keystore: ${e.message}`, 'err');
      return;
    }
    let priv;
    try {
      priv = await decryptPrivateKey(ks, $('send-pass').value);
    } catch (e) {
      setStatus('send-status', e.message, 'err');
      return;
    }
    // Sender = hex(sha256(public_key)). The keystore stores the
    // public_key in cleartext (it's not a secret), and we verified
    // address == sha256(public_key) during validateKeystore. So
    // ks.address IS the sender — no extra hashing needed here.
    const sender = ks.address;
    if (sender === recipient) {
      setStatus('send-status', 'sender == recipient (refusing to send to yourself)', 'err');
      priv.fill(0);
      return;
    }

    setStatusBusy('send-status', 'signing transaction (ML-DSA-87)…');
    await new Promise(r => setTimeout(r, 0)); // yield for spinner paint

    const timestamp = new Date().toISOString();
    let txID;
    try {
      txID = await deriveTxID(sender, recipient, timestamp);
    } catch (e) {
      setStatus('send-status', `tx_id derivation failed: ${e.message}`, 'err');
      priv.fill(0);
      return;
    }

    // The envelope we hand to WASM. WASM canonicalises + signs +
    // re-marshals; we don't have to set signature/public_key here
    // (and shouldn't — WASM strips them defensively before signing).
    // Field order in this object is irrelevant to the canonical
    // bytes because WASM re-marshals through the Go struct.
    const envelope = {
      id: txID,
      sender: sender,
      recipient: recipient,
      amount: amount,
      fee: fee,
      geotag: geotag,
      parent_cells: parentCells,
      timestamp: timestamp,
    };

    const signed = window.qsdm_wallet_sign_transaction(
      JSON.stringify(envelope),
      bytesToHex(priv),
      ks.public_key,
    );
    // Zero the decrypted private key reference before anything else.
    priv.fill(0);

    if (typeof signed !== 'string') {
      setStatus('send-status', `signing failed: ${signed && signed.error ? signed.error : 'unknown'}`, 'err');
      return;
    }

    // --- POST the signed envelope ---
    setStatusBusy('send-status', `submitting to ${endpoint}…`);
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), 30_000);
    let resp;
    try {
      resp = await fetch(endpoint, {
        method: 'POST',
        credentials: 'omit',
        signal: ctl.signal,
        headers: {
          'Accept': 'application/json',
          'Content-Type': 'application/json',
        },
        body: signed,
      });
    } catch (e) {
      clearTimeout(timer);
      const reason = e.name === 'AbortError' ? 'timed out after 30s' : e.message;
      setStatus('send-status', `network error: ${reason}`, 'err');
      return;
    }
    clearTimeout(timer);

    let body, bodyText;
    try {
      bodyText = await resp.text();
      body = bodyText ? JSON.parse(bodyText) : {};
    } catch (e) {
      setStatus('send-status', `bad JSON from API: ${e.message} (raw: ${(bodyText || '').slice(0, 120)})`, 'err');
      return;
    }

    // Server result paths:
    //   200 + status:"accepted" + transaction_id + broadcast
    //   409 + status:"duplicate" + transaction_id  (idempotent retry)
    //   400 / 402 / 422 / 500 + {error: "..."}
    const ok = resp.ok || resp.status === 409;
    const statusLabel = ok ? (body.status || 'accepted') : 'error';
    const colour = ok ? 'ok' : 'err';

    const r = $('send-result');
    r.hidden = false;
    const renderedTxID = body.transaction_id || txID;
    r.innerHTML = `
      <div class="result" style="${ok ? '' : 'border-color: rgba(255,107,107,.5); background: rgba(255,107,107,.05);'}">
        <h3 style="${ok ? '' : 'color: var(--danger)'}">${ok ? 'Submitted' : 'Rejected'}</h3>
        <div class="kv">
          <div class="k">tx_id</div><div class="v">${escapeHtml(renderedTxID)}</div>
          <div class="k">status</div><div class="v"><strong class="${colour}">${escapeHtml(statusLabel)}</strong></div>
          <div class="k">broadcast</div><div class="v">${escapeHtml(body.broadcast || '—')}</div>
          <div class="k">sender</div><div class="v">${escapeHtml(sender)}</div>
          <div class="k">recipient</div><div class="v">${escapeHtml(recipient)}</div>
          <div class="k">amount</div><div class="v"><strong>${escapeHtml(formatCell(amount))}</strong></div>
          <div class="k">fee</div><div class="v">${escapeHtml(formatCell(fee))}</div>
          <div class="k">HTTP</div><div class="v">${resp.status} ${escapeHtml(resp.statusText)}</div>
          <div class="k">raw response</div><div class="v long"><code>${escapeHtml(bodyText.slice(0, 800))}</code></div>
        </div>
        ${ok ? `
          <div class="status-line" style="margin-top:14px">
            The envelope is on the wire. <strong>Until v0.4.1 closes the
            atomicity gap</strong>, double-check your balance after a
            few seconds to confirm the debit landed cleanly.
          </div>
        ` : ''}
      </div>`;
    setStatus('send-status', ok
      ? `${statusLabel}: ${formatCell(amount)} → ${recipient.slice(0, 12)}… (tx ${renderedTxID.slice(0, 12)}…)`
      : `HTTP ${resp.status}: ${(body && (body.error || body.detail || body.message)) || resp.statusText}`,
      colour);
    // Wipe the passphrase field on success or terminal error.
    $('send-pass').value = '';
  });

  // ----- Go! -----
  bootWASM();
})();
