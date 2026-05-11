//go:build !js || !wasm
// +build !js !wasm

// Placeholder so `go build ./...` doesn't fail outside the WASM build
// target. The real entry point is in main.go (//go:build js && wasm).
package main

func main() {}
