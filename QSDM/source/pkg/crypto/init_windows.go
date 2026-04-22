//go:build windows && cgo
// +build windows,cgo

package crypto

// This file is intentionally minimal.
// DLL preloading is now handled in dilithium.go's NewDilithium() function
// to avoid crashes during package initialization (init() functions run before
// CGO is fully ready, which can cause STATUS_ACCESS_VIOLATION).
//
// The preload_openssl_dlls() function in dilithium.go is called just before
// OQS_SIG_new(), which is a safer time to load DLLs.

