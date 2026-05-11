//go:build js && wasm
// +build js,wasm

// qsdm-wallet — WebAssembly entry point for the browser wallet served at
// qsdm.tech/wallet/. Exposes a tiny, byte-only API surface to JavaScript:
//
//   - qsdm_wallet_generate()                             → {address, public_key_hex, private_key_hex}
//   - qsdm_wallet_address_from_public_key(public_key_hex)→ "<hex sha256>"
//   - qsdm_wallet_sign(private_key_hex, message_hex)     → "<hex 4627-byte signature>"
//   - qsdm_wallet_verify(public_key_hex, message_hex, signature_hex) → boolean
//   - qsdm_wallet_version()                              → "qsdm-wallet v1 / ml-dsa-87 / circl"
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
	"errors"
	"fmt"
	"syscall/js"

	"github.com/blackbeardONE/QSDM/wasm_modules/wallet/walletcrypto"
)

// apiVersion is surfaced to the JS side. Bump when the WASM API shape
// changes (not when the underlying crypto changes — backend rotation
// is masked by walletcrypto). The JS side displays this so bug reports
// have a stable identifier for the loaded binary.
const apiVersion = "qsdm-wallet v1 / ml-dsa-87 / circl"

func main() {
	js.Global().Set("qsdm_wallet_generate", js.FuncOf(walletGenerate))
	js.Global().Set("qsdm_wallet_address_from_public_key", js.FuncOf(walletAddressFromPublicKey))
	js.Global().Set("qsdm_wallet_sign", js.FuncOf(walletSign))
	js.Global().Set("qsdm_wallet_verify", js.FuncOf(walletVerify))
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
