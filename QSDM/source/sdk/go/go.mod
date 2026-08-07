// The Go SDK is its own module so `go get` can resolve it.
//
// The parent module declares `module github.com/blackbeardONE/QSDM` while
// its go.mod lives at QSDM/source/go.mod — two directories below the repo
// root. Go requires a module in a subdirectory to declare the path the
// subdirectory actually has, so the parent's declared path does not match
// its location and `go get github.com/blackbeardONE/QSDM/sdk/go` cannot
// resolve. That made the published Go SDK unusable by consumers.
//
// Fixing the parent would mean renaming the module path across ~130k LOC.
// Giving the SDK its own module is self-contained, changes no existing
// import (nothing in the parent module imports the SDK — only two comments
// mention it), and makes the documented consumer path work:
//
//	go get github.com/blackbeardONE/QSDM/QSDM/source/sdk/go
//
// The client depends only on the standard library, so this module needs no
// requires and pulls in nothing transitively.
module github.com/blackbeardONE/QSDM/QSDM/source/sdk/go

go 1.25
