//go:build !js || !wasm
// +build !js !wasm

package wallet

// Placeholder for non-WASM builds so `go build ./...` includes this module.
// Browser/WASM entrypoints live in wallet_wasm.go (js && wasm).
