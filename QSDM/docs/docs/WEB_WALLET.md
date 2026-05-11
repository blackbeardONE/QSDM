# QSDM Web Wallet — Self-Custody Reference

Public URL: <https://qsdm.tech/wallet/>
Source:    `QSDM/source/wasm_modules/wallet/cmd/qsdm-wallet/main.go`
            `QSDM/deploy/landing/wallet.html`
            `QSDM/deploy/landing/wallet.js`
Sibling:   `qsdmcli wallet new|show|inspect|sign` (CLI · same keystore format)

The web wallet is a static page that generates and operates ML-DSA-87
(FIPS 204) wallets entirely client-side. The QSDM validators play no
role in keystore creation or storage; the only network traffic from
`/wallet/` is the GET of `wallet.html`, `wasm_exec.js`, `wallet.wasm`,
and `wallet.js`.

This document is the reference for what the page does, how, and why.

---

## 1. Architecture

```
   Browser tab
   ───────────
                ┌────────────────────────────────────────┐
                │ wallet.html  (UI: 3 tabs)              │
                │   Generate · Open · Sign               │
                └───────────────────┬────────────────────┘
                                    │ DOM events
                                    ▼
   ┌──────────────────────────────────────────────┐
   │ wallet.js  (~600 LOC)                        │
   │   • WebCrypto envelope (PBKDF2 + AES-GCM)    │
   │   • Keystore (de)serialise to/from JSON      │
   │   • UI state machine                         │
   └──────────────┬────────────────┬──────────────┘
                  │                │
                  ▼                ▼
   ┌──────────────────────┐ ┌──────────────────────────────┐
   │ window.crypto.subtle │ │ wallet.wasm  (Go via Circl)  │
   │  PBKDF2-SHA-256      │ │   • mldsa87.GenerateKey      │
   │  AES-256-GCM         │ │   • mldsa87.SignTo           │
   │  getRandomValues     │ │   • mldsa87.Verify           │
   └──────────────────────┘ └──────────────────────────────┘
```

**Why split the crypto?** Each side is doing what it is designed for:

- **WebCrypto** is a battle-tested browser primitive for password-based
  symmetric encryption. Using it instead of a WASM-side PBKDF2/AES
  saves ~500 KB of WASM, eliminates a side-channel surface, and matches
  the OWASP guidance to use the platform's native primitives when
  available.
- **WASM** is the only practical way to ship a FIPS-204 ML-DSA-87
  signer in a browser. WebCrypto does not expose post-quantum
  signature schemes (as of late 2026), so Go-compiled-to-WASM is the
  reference path. The same `cloudflare/circl` ML-DSA-87 implementation
  is what the validator side uses, so the browser cannot accidentally
  produce a signature the network refuses.

---

## 2. Keystore JSON format (v1)

Defined by `pkg/keystore` in the Go source tree. Both the CLI and the
browser emit byte-identical files:

```json
{
  "version":      1,
  "type":         "qsdm-keystore",
  "algorithm":    "ml-dsa-87",
  "address":      "<hex sha256(public_key)>",
  "public_key":   "<hex 2592-byte FIPS 204 ML-DSA-87 public key>",
  "kdf":          "pbkdf2-sha256",
  "kdf_params":   { "iterations": 600000, "salt": "<hex 16>", "key_len": 32 },
  "cipher":       "aes-256-gcm",
  "cipher_params":{ "nonce": "<hex 12>" },
  "ciphertext":   "<hex AES-256-GCM(private_key) with appended 16-byte tag>",
  "created_at":   "RFC 3339 UTC"
}
```

Cross-compatibility is enforced by `QSDM/source/pkg/keystore/keystore_test.go`
plus an offline Node.js test that decrypts a CLI-produced keystore using
WebCrypto and signs via WASM (`_tmp_xcompat.js` in the repo root —
`.gitignored`; runnable manually).

Bumping `iterations` is forward-compatible (a future build can produce
700 k-iter keystores and the Go-side `Validate` only complains if the
value is below 100 000). Lowering iterations would be a regression and
is enforced against.

---

## 3. Threat model

What the wallet **does** protect against:

1. **Server-side custody risk.** No QSDM operator, validator, or
   third-party service ever holds the private key. The validator's
   `POST /api/v1/wallet/create` endpoint is intentionally **not** used by
   this page — that route returns a "ghost" address with no recoverable
   key, useful only as a write-only sink. The web wallet replaces it.
2. **Network observers** (passive and active). The page never POSTs the
   passphrase, the private key, or even the public address. Confirm in
   DevTools → Network: the only requests are the four static GETs above.
3. **Disk-resident attackers (offline).** The keystore on disk is
   AES-256-GCM-encrypted under a PBKDF2-derived key (600 k iterations,
   SHA-256). At commodity hardware speeds (~10⁵ guesses/sec on a
   single GPU), a 12-character alphanumeric passphrase has ~10²¹
   combinations — comfortably out of reach. A 6-character passphrase is
   trivially crackable; the page warns at 8 characters but does not
   refuse (only a zero-byte passphrase is rejected outright).
4. **Tampered keystore JSON.** The Go-side `keystore.Validate` enforces
   that `sha256(public_key) == address`; the browser-side equivalent
   (`validateKeystore` in `wallet.js`) does the same check before
   prompting for a passphrase. A flipped byte in the ciphertext fails the
   AES-GCM tag verification at decrypt time.

What the wallet **does NOT** protect against (and explicit warnings in
the UI call this out):

5. **A compromised QSDM web server.** If an attacker replaces
   `wallet.wasm` or `wallet.js` on the qsdm.tech CDN, the page can be
   modified to exfiltrate the private key the moment it's generated.
   Mitigations:
   - The repo publishes the WASM artefact at a known path with a
     git-tracked SHA-256 (see `RELEASE_NOTES`).
   - The CLI (`qsdmcli wallet new`) is the cold-storage path that
     bypasses the website entirely.
   - Subresource Integrity (SRI) for `wallet.wasm` is a planned
     follow-up; the WebAssembly fetch needs the hash baked into the
     HTML for that to be useful, so it requires a deploy-time
     rewrite step.
6. **A compromised endpoint** (your laptop's browser, OS, or
   keyboard logger). The wallet runs in-tab; if the OS is compromised,
   nothing here helps.
7. **A weak passphrase.** PBKDF2-600 k is OWASP-compliant but
   purely a delay against offline guessing — a passphrase like
   `password123` is still cracked in seconds. Pick a passphrase
   long enough that brute-force is infeasible.

---

## 4. Verifying the build (deployer checklist)

A QSDM operator (you, on `blackbeardONE/QSDM`) ships the wallet by:

1. Running `./QSDM/scripts/build_wallet_wasm.sh`. Output:
   - `QSDM/deploy/landing/wallet.wasm` (~3 MB)
   - `QSDM/deploy/landing/wasm_exec.js` (copied from `$GOROOT/lib/wasm/`)
2. Committing both files to the repo. The SHA-256 of `wallet.wasm` is
   reproducible from the same Go toolchain version + `go.sum`-pinned
   `cloudflare/circl` version, so two independent builds agree byte-for-byte
   (modulo `-trimpath`-suppressed file paths).
3. Verifying locally:
   - `python3 -m http.server -d QSDM/deploy/landing 8088`
   - Open `http://127.0.0.1:8088/wallet.html`
   - Confirm DevTools → Network shows GETs only for the four static files
     and nothing else after pressing Generate.
4. Confirming the published build matches the repo:
   - `curl -s https://qsdm.tech/wallet.wasm | sha256sum`
   - Compare to the value in `RELEASE_NOTES_v0.3.x.md` under "wallet WASM SHA-256".

---

## 5. Practical recipes

### 5a. Generate an address and pipe it into a miner

```bash
# CLI path — quietest, scriptable
./qsdmcli wallet new --passphrase-file ./pass.txt --out ~/.qsdm/wallet.json
ADDR="$(./qsdmcli wallet show --in ~/.qsdm/wallet.json | awk '/^address/{print $2}')"
./qsdmminer --validator=https://api.qsdm.tech --address="$ADDR" --batch-count=1
```

### 5b. Read an address out of a keystore without revealing the private key

```bash
./qsdmcli wallet show --in ~/.qsdm/wallet.json
# Address + public-key fields are plaintext in the keystore JSON.
# The private key remains AES-256-GCM-encrypted; no passphrase prompted.
```

### 5c. Verify keystore integrity end-to-end

```bash
./qsdmcli wallet inspect --in ~/.qsdm/wallet.json --passphrase-file ./pass.txt
# Decrypts, then reconstructs the public key from the recovered private key
# and compares against the stored public_key. Fails loudly if the keystore
# was edited after encryption.
```

### 5d. Sign an arbitrary message (e.g., a transaction envelope)

```bash
echo -n '{"sender":"0xabc","amount":100}' \
  | ./qsdmcli wallet sign --in ~/.qsdm/wallet.json --passphrase-file ./pass.txt --message-file -
# Stdout: hex ML-DSA-87 signature (4627 bytes).
```

Browser equivalent: the **Sign message** tab on `qsdm.tech/wallet/` does
exactly the same thing for short UTF-8 messages.

---

## 6. Known limitations / roadmap

- The wallet page does not currently broadcast signed transactions; it
  produces the signature and leaves transport to the operator (curl,
  qsdmcli tx, SDK). A "send transaction" tab is planned for v0.4.0
  once the v2 mining payload format settles.
- WebCrypto's PBKDF2 implementation is constant-iteration; on slower
  hardware (mobile browsers) the encrypt step can take 2-3 seconds.
  The page spinners through it but the UX is not great. Argon2
  (faster + memory-hard) would be the upgrade path — it isn't in
  WebCrypto as of late 2026, so this would require a WASM-side
  KDF, which is itself an attack surface. Holding for upstream.
- No keystore "rename / move passphrase" flow yet. Implementable: open,
  re-encrypt with a new passphrase, save. Trivial follow-up.
- No browser-side mnemonic seed phrase. ML-DSA-87 keys do not have a
  deterministic / BIP-39 representation in the way Ed25519 keys do;
  encoding 4896 bytes of secret material into a wordlist would
  produce an unwieldy ~480-word phrase. The encrypted JSON keystore is
  the recovery artefact instead.
