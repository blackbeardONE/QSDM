//go:build js && wasm
// +build js,wasm

// qsdm-wallet — WebAssembly entry point for the browser wallet served at
// qsdm.tech/wallet/. Exposes a tiny, byte-only API surface to JavaScript:
//
//   - qsdm_wallet_generate()                             → {address, public_key_hex, private_key_hex}
//   - qsdm_wallet_address_from_public_key(public_key_hex)→ "<hex sha256>"
//   - qsdm_wallet_sign(private_key_hex, message_hex)     → "<hex 4627-byte signature>"
//   - qsdm_wallet_verify(public_key_hex, message_hex, signature_hex) → boolean
//   - qsdm_wallet_sign_transaction(envelope_json, private_key_hex, public_key_hex) → signed_envelope_json
//   - qsdm_wallet_version()                              → "qsdm-wallet v2 / ml-dsa-87 / circl"
//
// What this module does NOT do (deliberately):
//
//   - It does not perform passphrase derivation or symmetric encryption.
//     Both PBKDF2 and AES-GCM are exposed by the browser's WebCrypto
//     API; doing them in WASM would bloat the binary by ~5x for no
//     security benefit. The companion wallet.js calls WebCrypto with
//     the exact parameters pkg/keystore uses (PBKDF2-HMAC-SHA-256,
//     600_000 iterations, 16-byte salt, AES-256-GCM with a 12-byte
//     nonce), so the keystore JSON written by the browser is
//     byte-identical to one written by `qsdmcli wallet new`.
//
//   - It does not maintain a process-wide singleton wallet. The previous
//     iteration of this module ran walletcrypto.GenerateKeyPair() in
//     init() and stored the result in a package-level variable; that
//     was the right shape for a server-side wallet and the wrong shape
//     for self-custody (a freshly-loaded WASM page would silently mint a
//     new key the user had to discard, plus every navigation would
//     "lose" the previous wallet). The new API is stateless: callers
//     pass the key material in, get the result back.
//
// Build:
//
//	cd QSDM/source
//	GOOS=js GOARCH=wasm go build -o ../../deploy/landing/wallet.wasm ./wasm_modules/wallet/cmd/qsdm-wallet
//
// Then serve `deploy/landing/wallet.wasm` next to `wasm_exec.js` (copied
// from `$(go env GOROOT)/misc/wasm/wasm_exec.js` or
// `$(go env GOROOT)/lib/wasm/wasm_exec.js` depending on Go version) and
// the companion wallet.html + wallet.js.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"syscall/js"

	"github.com/blackbeardONE/QSDM/wasm_modules/wallet/walletcrypto"
)

// apiVersion is surfaced to the JS side. Bump when the WASM API shape
// changes (not when the underlying crypto changes — backend rotation
// is masked by walletcrypto). The JS side displays this so bug reports
// have a stable identifier for the loaded binary.
//
// v1 (sessions 82–94): generate / address_from_public_key / sign / verify / version.
// v2 (session 96, v0.4.0 Phase B): adds sign_transaction for the
// browser-side self-custody send flow (POST /api/v1/wallet/submit-signed).
const apiVersion = "qsdm-wallet v2 / ml-dsa-87 / circl"

func main() {
	js.Global().Set("qsdm_wallet_generate", js.FuncOf(walletGenerate))
	js.Global().Set("qsdm_wallet_address_from_public_key", js.FuncOf(walletAddressFromPublicKey))
	js.Global().Set("qsdm_wallet_sign", js.FuncOf(walletSign))
	js.Global().Set("qsdm_wallet_verify", js.FuncOf(walletVerify))
	js.Global().Set("qsdm_wallet_sign_transaction", js.FuncOf(walletSignTransaction))
	js.Global().Set("qsdm_wallet_version", js.FuncOf(walletVersion))
	// Signal readiness to the page so the UI can disable the loading spinner.
	js.Global().Set("qsdm_wallet_ready", js.ValueOf(true))
	// Park the goroutine; the Go runtime tears the process down when
	// main() returns, which would unregister every js.FuncOf above.
	// `select{}` blocks forever with zero CPU cost.
	select {}
}

// walletGenerate is the only stateful entry point: it produces a fresh
// ML-DSA-87 keypair and returns the address plus both raw keys as hex.
// The JS side immediately PBKDF2+AES-GCM-encrypts the private_key_hex
// and discards the plaintext; nothing about the keypair persists in
// WASM memory between calls.
//
// Return shape: a JS object {address, public_key_hex, private_key_hex}.
// On failure (which would only happen if crypto/rand fails — i.e.
// browser refuses to expose getRandomValues) returns {error: "..."}.
func walletGenerate(this js.Value, args []js.Value) interface{} {
	kp, err := walletcrypto.GenerateKeyPair()
	if err != nil {
		return errorResult(err)
	}
	sum := sha256.Sum256(kp.PublicKey)
	return map[string]interface{}{
		"address":         hex.EncodeToString(sum[:]),
		"public_key_hex":  hex.EncodeToString(kp.PublicKey),
		"private_key_hex": hex.EncodeToString(kp.PrivateKey),
	}
}

// walletAddressFromPublicKey derives the canonical QSDM address (hex
// SHA-256 of the packed public key) from a hex public key. Useful when
// the caller has a public key from a keystore but wants the address
// without re-running keystore validation.
func walletAddressFromPublicKey(this js.Value, args []js.Value) interface{} {
	if len(args) != 1 {
		return errorResult(errors.New("qsdm_wallet_address_from_public_key(public_key_hex)"))
	}
	pkHex := args[0].String()
	pk, err := hex.DecodeString(pkHex)
	if err != nil {
		return errorResult(fmt.Errorf("public_key_hex not hex: %w", err))
	}
	sum := sha256.Sum256(pk)
	return hex.EncodeToString(sum[:])
}

// walletSign(private_key_hex, message_hex) → signature_hex.
//
// The private key is passed in (not stored anywhere): it is the JS
// caller's responsibility to keep it in memory only for the duration of
// the sign call and to clear the variable afterwards. The
// 4627-byte ML-DSA-87 signature is returned as hex.
func walletSign(this js.Value, args []js.Value) interface{} {
	if len(args) != 2 {
		return errorResult(errors.New("qsdm_wallet_sign(private_key_hex, message_hex)"))
	}
	sk, err := hex.DecodeString(args[0].String())
	if err != nil {
		return errorResult(fmt.Errorf("private_key_hex: %w", err))
	}
	msg, err := hex.DecodeString(args[1].String())
	if err != nil {
		return errorResult(fmt.Errorf("message_hex: %w", err))
	}
	kp, err := walletcrypto.FromBytes(sk, nil)
	if err != nil {
		return errorResult(err)
	}
	sig, err := kp.Sign(msg)
	if err != nil {
		return errorResult(err)
	}
	return hex.EncodeToString(sig)
}

// walletVerify(public_key_hex, message_hex, signature_hex) → boolean.
//
// Returns a plain bool (not the {result, error} object) on the happy
// path — callers want to write `if (qsdm_wallet_verify(...)) { ... }`
// without unwrapping. Parse errors come back as an {error: ...} object;
// JS can distinguish via `typeof`.
func walletVerify(this js.Value, args []js.Value) interface{} {
	if len(args) != 3 {
		return errorResult(errors.New("qsdm_wallet_verify(public_key_hex, message_hex, signature_hex)"))
	}
	pk, err := hex.DecodeString(args[0].String())
	if err != nil {
		return errorResult(fmt.Errorf("public_key_hex: %w", err))
	}
	msg, err := hex.DecodeString(args[1].String())
	if err != nil {
		return errorResult(fmt.Errorf("message_hex: %w", err))
	}
	sig, err := hex.DecodeString(args[2].String())
	if err != nil {
		return errorResult(fmt.Errorf("signature_hex: %w", err))
	}
	kp, err := walletcrypto.FromBytes(nil, pk)
	if err != nil {
		return errorResult(err)
	}
	ok, err := kp.Verify(msg, sig)
	if err != nil {
		return errorResult(err)
	}
	return ok
}

// txEnvelope mirrors pkg/wallet.TransactionData on the wire. It MUST
// stay byte-shape-identical to the server-side struct so the canonical
// payload we sign here is the same one POST /api/v1/wallet/submit-signed
// reconstructs and verifies. If pkg/wallet.TransactionData ever gains a
// new field, add it here in the same struct position (json.Marshal
// emits fields in struct-declaration order, and the server canonicalises
// by parse → re-marshal, so the order is the contract).
//
// `omitempty` on PublicKey matches the server side and is what makes the
// canonical payload exclude `public_key` entirely when we clear it
// before signing. Signature is NOT omitempty — when cleared it emits as
// `"signature":""`, which the server's strip-and-remarshal step also
// produces.
type txEnvelope struct {
	ID          string   `json:"id"`
	Sender      string   `json:"sender"`
	Recipient   string   `json:"recipient"`
	Amount      float64  `json:"amount"`
	Fee         float64  `json:"fee"`
	GeoTag      string   `json:"geotag"`
	ParentCells []string `json:"parent_cells"`
	Signature   string   `json:"signature"`
	PublicKey   string   `json:"public_key,omitempty"`
	Timestamp   string   `json:"timestamp"`
}

// walletSignTransaction is the v0.4.0 Phase B (Session 96) entry point
// for the browser "Send" tab. It takes a JSON-encoded transaction
// envelope (no signature, no public_key — those are filled in here),
// the user's hex-encoded ML-DSA-87 private key, and the matching
// hex-encoded public key. It returns the fully-signed envelope JSON
// ready to POST to /api/v1/wallet/submit-signed.
//
// Why does WASM do the canonical-bytes marshalling instead of JS?
// Because Go's json.Marshal and JavaScript's JSON.stringify can disagree
// on float64 representation for some inputs (e.g. 1e-7: Go emits
// "1e-07", JS emits "1e-7"). The server canonicalises by parsing the
// posted envelope into wallet.TransactionData then json.Marshal-ing it
// with the signature+public_key fields cleared — that's the bytes it
// then verifies the signature against. To guarantee byte-equality with
// the server's canonicalisation, we MUST produce the canonical bytes
// using Go's own json.Marshal. Running it inside the WASM module is
// the simplest way to do that without re-implementing the float-format
// rules.
//
// Returns the final signed envelope as a JSON string on success. On
// failure returns {error: "..."} (the WASM convention for typed
// errors).
func walletSignTransaction(this js.Value, args []js.Value) interface{} {
	if len(args) != 3 {
		return errorResult(errors.New("qsdm_wallet_sign_transaction(envelope_json, private_key_hex, public_key_hex)"))
	}
	envJSON := args[0].String()
	skHex := args[1].String()
	pkHex := args[2].String()

	sk, err := hex.DecodeString(skHex)
	if err != nil {
		return errorResult(fmt.Errorf("private_key_hex: %w", err))
	}
	pk, err := hex.DecodeString(pkHex)
	if err != nil {
		return errorResult(fmt.Errorf("public_key_hex: %w", err))
	}

	// Parse the caller-supplied envelope. We accept any field-order in
	// the input JSON because the immediate re-marshal below normalises
	// to Go-struct order regardless of input ordering. This makes the
	// JS side trivial: it can build the object in any field-order it
	// likes.
	var env txEnvelope
	if err := json.Unmarshal([]byte(envJSON), &env); err != nil {
		return errorResult(fmt.Errorf("envelope_json: %w", err))
	}

	// Defensive: ignore any signature / public_key the caller already
	// put on the envelope. The server requires the canonical payload
	// to be the envelope with both cleared, so we always clear them
	// before signing.
	env.Signature = ""
	env.PublicKey = ""

	// Client-side sender ↔ pubkey bind. The server enforces this too
	// (and rejects with 400 sender_mismatch if it disagrees), but
	// failing here gives the user a friendlier error before the
	// round trip. JS already knows pubkey when calling this helper —
	// it should have set env.Sender to hex(sha256(pubkey)) — but a
	// typo / mismatched keystore would otherwise produce a confusing
	// HTTP error.
	sum := sha256.Sum256(pk)
	derived := hex.EncodeToString(sum[:])
	if env.Sender != derived {
		return errorResult(fmt.Errorf(
			"envelope.sender (%s) does not match hex(sha256(public_key)) (%s) — "+
				"the keystore's public_key and the envelope sender don't agree",
			env.Sender, derived,
		))
	}

	// Canonical bytes for the signature. This is the EXACT byte string
	// the server will re-marshal and verify against in
	// pkg/api/handlers.go::SubmitSignedTransaction.
	canonical, err := json.Marshal(env)
	if err != nil {
		return errorResult(fmt.Errorf("marshal canonical payload: %w", err))
	}

	kp, err := walletcrypto.FromBytes(sk, pk)
	if err != nil {
		return errorResult(err)
	}
	sig, err := kp.Sign(canonical)
	if err != nil {
		return errorResult(err)
	}

	// Attach signature + public_key, then re-marshal as the final wire
	// envelope. The server will strip these back off, re-marshal, and
	// verify against the resulting bytes — which equals `canonical`
	// above by construction.
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = pkHex
	final, err := json.Marshal(env)
	if err != nil {
		return errorResult(fmt.Errorf("marshal final envelope: %w", err))
	}
	return string(final)
}

func walletVersion(this js.Value, args []js.Value) interface{} {
	return apiVersion
}

// errorResult is the conventional failure shape: a JS object with a
// single "error" field. JS callers test `typeof result === 'object'
// && result.error` to detect failure without juggling exceptions
// across the WASM/JS boundary (which would be lost as a generic
// "Go program exited" by wasm_exec.js).
func errorResult(err error) map[string]interface{} {
	return map[string]interface{}{"error": err.Error()}
}
