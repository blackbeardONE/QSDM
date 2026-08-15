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
// Nothing connected the page to this module, so the drift was invisible.
//
// What this test is and is NOT: it pins the IDENTIFIERS the snippet uses, so a
// rename breaks the build here and names the file that must change with it. It
// is not the snippet itself -- a test lives in a func with its own ctx, while
// the page shows a standalone program. A reviewer correctly called out an
// earlier version of this comment for claiming otherwise; the snippet's own
// compilability is a separate property, fixed by making the page show a
// complete `package main` rather than loose statements.
//
// It makes no network call: GetNodeStatus is handed an already-cancelled
// context, so client.do returns the context error before any dial.
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

	// The result is deliberately ignored beyond its shape. A cancelled context
	// means err is always non-nil and st always nil, so any branch guarded on
	// err == nil is dead -- an earlier version had two such branches and a
	// reviewer flagged them as reading like runtime coverage while being none.
	// Referencing the field in a compile-only position keeps st.Version pinned
	// without pretending the call succeeded.
	st, err := client.GetNodeStatus(ctx)
	_ = err
	var _ = func(s *NodeStatus) string {
		if s == nil {
			return ""
		}
		return s.Version // the field the snippet prints
	}
	_ = st
}
