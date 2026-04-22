//go:build !custom_wasmer_runtime
// +build !custom_wasmer_runtime

package wasmer

// #cgo CFLAGS: -I${SRCDIR}/packaged/include
//// #cgo LDFLAGS: -lwasmer
//
// #cgo linux,amd64 LDFLAGS: -Wl,-rpath,${SRCDIR}/packaged/lib/linux-amd64 -L${SRCDIR}/packaged/lib/linux-amd64
// //#cgo linux,arm64 LDFLAGS: -Wl,-rpath,${SRCDIR}/packaged/lib/linux-aarch64 -L${SRCDIR}/packaged/lib/linux-aarch64
// #cgo darwin,amd64 LDFLAGS: -Wl,-rpath,${SRCDIR}/packaged/lib/darwin-amd64 -L${SRCDIR}/packaged/lib/darwin-amd64
// #cgo darwin,arm64 LDFLAGS: -Wl,-rpath,${SRCDIR}/packaged/lib/darwin-aarch64 -L${SRCDIR}/packaged/lib/darwin-aarch64
//
// #cgo windows,amd64 CFLAGS: -I${SRCDIR}/packaged/include
// #cgo windows,amd64 LDFLAGS: -L${SRCDIR}/packaged/lib/windows-amd64 -lwasmer_go
//
// #cgo windows,amd64 CFLAGS: -Id:/Projects/QSDM/liboqs_build/include
// #cgo windows,amd64 LDFLAGS: -Ld:/Projects/QSDM/liboqs_build/lib -loqs
//
// #include <wasmer.h>
// #include <oqs/oqs.h>
import "C"

// See https://github.com/golang/go/issues/26366.
import (
	_ "github.com/wasmerio/wasmer-go/wasmer/packaged/include"
	_ "github.com/wasmerio/wasmer-go/wasmer/packaged/lib"
)
