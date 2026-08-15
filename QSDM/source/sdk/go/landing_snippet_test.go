package qsdm

import (
	"context"
	"testing"
)

// The landing page publishes a Go snippet as the SDK's first impression, and
// every symbol in it was wrong: it advertised the module path
// "github.com/blackbeardONE/QSDM/sdk/go" (the real one is
// ".../QSDM/QSDM/source/sdk/go", so `go get` on the published path fails),
// called qsdm.New (the constructor is NewClient) and client.Status (no such
// method has ever existed). The sample could not compile at any line.
//
// Nothing connected the page to this module, so the drift was invisible. This
// test IS that snippet. It does not make a network call -- the point is purely
// that these identifiers exist with these shapes, so a rename breaks the build
// here and names the file that must change with it.
//
// Source of truth: QSDM/deploy/landing/index.html, the `code-go` <pre> block.
// If you change this test to follow the code, change that block to match.
func TestLandingPageGoSnippetCompiles(t *testing.T) {
	// client := qsdm.NewClient("https://api.qsdm.tech")
	client := NewClient("https://api.qsdm.tech")
	if client == nil {
		t.Fatal("NewClient returned nil; the landing page's constructor call is broken")
	}

	// st, _ := client.GetNodeStatus(ctx)
	// fmt.Println(st.Version)
	//
	// Bound to an already-cancelled context so no request is attempted: this
	// asserts the method's existence and result shape, not the endpoint.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st, err := client.GetNodeStatus(ctx)
	if err == nil && st != nil {
		_ = st.Version // the field the snippet prints
	}
	if st != nil {
		_ = st.Version
	}
}
