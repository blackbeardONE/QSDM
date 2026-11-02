package main

// Source-level wiring checks for main() (design rev 4 §2 cmd/qsdm (a)-(g),
// §5). main() cannot run end to end in a unit test, so these tests pin the
// order and placement of the HL1 calls in its syntax tree.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

type hl1MainSource struct {
	fset *token.FileSet
	file *ast.File
	main *ast.FuncDecl
}

func hl1ParseMain(t *testing.T) *hl1MainSource {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "main" && fd.Recv == nil {
			return &hl1MainSource{fset: fset, file: f, main: fd}
		}
	}
	t.Fatal("func main not found")
	return nil
}

// hl1CallName returns "Fn", "pkg.Fn" or "x.y.Method" for a call.
func hl1CallName(c *ast.CallExpr) string { return hl1ExprName(c.Fun) }

func hl1ExprName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return hl1ExprName(x.X) + "." + x.Sel.Name
	}
	return "?"
}

// calls returns the positions of every call named name inside node.
func (s *hl1MainSource) calls(node ast.Node, name string) []token.Pos {
	var out []token.Pos
	ast.Inspect(node, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && hl1CallName(c) == name {
			out = append(out, c.Pos())
		}
		return true
	})
	return out
}

func (s *hl1MainSource) one(t *testing.T, name string) token.Pos {
	t.Helper()
	ps := s.calls(s.main, name)
	if len(ps) != 1 {
		t.Fatalf("main() calls %s %d times, want 1", name, len(ps))
	}
	return ps[0]
}

func (s *hl1MainSource) line(p token.Pos) int { return s.fset.Position(p).Line }

func (s *hl1MainSource) before(t *testing.T, a, b string) {
	t.Helper()
	pa, pb := s.one(t, a), s.one(t, b)
	if pa >= pb {
		t.Fatalf("%s (line %d) must come before %s (line %d)", a, s.line(pa), b, s.line(pb))
	}
}

func TestHL1WiringStartupOrder(t *testing.T) {
	s := hl1ParseMain(t)
	if n := len(s.calls(s.main, "chain.AcquireStateLock")); n != 0 {
		t.Fatalf("main() calls chain.AcquireStateLock directly %d time(s); S0 must use hl1AcquireStateLock (exit 78)", n)
	}
	// S0 -> S1-S3 -> S4 prefix check, before the first other state access.
	s.before(t, "hl1AcquireStateLock", "hl1BootS1S3")
	s.before(t, "hl1BootS1S3", "hl1LockedTransitionPreflight")
	s.before(t, "hl1LockedTransitionPreflight", "chain.LoadOrCreateBFTSigner")
	s.before(t, "hl1BootS1S3", "setupNetwork")
	// S4: the journal newline check precedes the journal load.
	nl := s.calls(s.main, "hl1RequireFinalNewline")
	if len(nl) != 2 {
		t.Fatalf("hl1RequireFinalNewline called %d times, want 2 (journal, receipts)", len(nl))
	}
	if load := s.one(t, "chain.LoadChainNDJSON"); nl[0] >= load {
		t.Fatal("the journal newline check must precede LoadChainNDJSON")
	}
	if load := s.one(t, "adminReceipts.LoadNDJSON"); nl[1] >= load {
		t.Fatal("the receipts newline check must precede LoadNDJSON")
	}
	// S5 after the whole restore block (including every receipts load and
	// the migration) and before OpenChainJournal and the reserve parse.
	s5 := s.one(t, "hl1StartupWatermark")
	for _, name := range []string{"adminReceipts.LoadNDJSON", "adminReceipts.Load", "adminReceipts.AppendBlockNDJSON", "adminProducer.RestoreChain"} {
		for _, p := range s.calls(s.main, name) {
			if p >= s5 {
				t.Fatalf("%s (line %d) runs after S5 (line %d)", name, s.line(p), s.line(s5))
			}
		}
	}
	s.before(t, "hl1StartupWatermark", "hl1OpenChainJournal")
	s.before(t, "hl1OpenChainJournal", "hl1PersistenceReserve")
	// S7-S14 before S15 (SyncFunderNonce, Start) and S16 after Start.
	s.before(t, "hl1ReconcileCanary", "blockDriver.SyncFunderNonce")
	s.before(t, "blockDriver.Start", "hl1Mining.guard.Activate")
	s.before(t, "hl1ReconcileCanary", "miningsvc.New")
	// HL2 WP-C S14b: operator keys after S7-S14, before S15 and S16.
	s.before(t, "hl1ReconcileCanary", "hl2HydrateOperatorKeys")
	s.before(t, "hl2HydrateOperatorKeys", "blockDriver.SyncFunderNonce")
	s.before(t, "hl2HydrateOperatorKeys", "hl1Mining.guard.Activate")
	s.before(t, "miningsvc.New", "api.NewServer")
}

func TestHL1WiringNoExit1InRestoreBlock(t *testing.T) {
	s := hl1ParseMain(t)
	var from, to token.Pos
	ast.Inspect(s.main, func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok && len(vs.Names) == 1 && vs.Names[0].Name == "hl1RestoredBlocks" {
			from = vs.Pos()
		}
		return true
	})
	to = s.one(t, "hl1PersistenceReserve")
	if from == 0 || from >= to {
		t.Fatal("restore block markers not found")
	}
	for _, fn := range []string{"log.Fatal", "log.Fatalf", "log.Fatalln", "os.Exit"} {
		for _, p := range s.calls(s.main, fn) {
			if p > from && p < to {
				t.Fatalf("%s at line %d is inside the restore block/S5/journal/reserve span; it must be fatalRestore (exit 78)", fn, s.line(p))
			}
		}
	}
	if n := len(s.calls(s.main, "fatalRestore")); n < 16 {
		t.Fatalf("only %d fatalRestore calls in main()", n)
	}
}

func TestHL1WiringExternalAppendAndGossipGated(t *testing.T) {
	s := hl1ParseMain(t)
	// Every TryAppendExternalBlock reference in main() is the argument of
	// hl1ExternalAppend.
	allowed := map[token.Pos]bool{}
	for _, p := range s.calls(s.main, "hl1ExternalAppend") {
		ast.Inspect(s.main, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok && c.Pos() == p {
				for _, a := range c.Args {
					allowed[a.Pos()] = true
				}
			}
			return true
		})
	}
	refs := 0
	ast.Inspect(s.main, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "TryAppendExternalBlock" {
			refs++
			if !allowed[sel.Pos()] {
				t.Fatalf("TryAppendExternalBlock at line %d is not gated by hl1ExternalAppend", s.line(sel.Pos()))
			}
		}
		return true
	})
	if refs != 2 {
		t.Fatalf("%d TryAppendExternalBlock references in main(), want 2 (block gossip, BFT commit)", refs)
	}
	// HTTP chain sync only through hl1ShouldStartChainSync.
	var gated bool
	ast.Inspect(s.main, func(n ast.Node) bool {
		if is, ok := n.(*ast.IfStmt); ok {
			if c, ok := is.Cond.(*ast.CallExpr); ok && hl1CallName(c) == "hl1ShouldStartChainSync" {
				gated = len(s.calls(is.Body, "startHTTPChainSync")) == 1
			}
		}
		return true
	})
	if !gated || len(s.calls(s.main, "startHTTPChainSync")) != 1 {
		t.Fatal("startHTTPChainSync is not gated by hl1ShouldStartChainSync")
	}
	// The tx topic is wired only by wireTxGossip.
	for _, name := range []string{"net.SetTxGossipIngress", "net.SetMessageHandler"} {
		if n := len(s.calls(s.main, name)); n != 0 {
			t.Fatalf("main() calls %s directly; use wireTxGossip", name)
		}
	}
	s.one(t, "wireTxGossip")
}

func TestHL1WiringPOLMovedToH10(t *testing.T) {
	s := hl1ParseMain(t)
	var onSealed ast.Node
	ast.Inspect(s.main, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == 1 {
			if sel, ok := as.Lhs[0].(*ast.SelectorExpr); ok && sel.Sel.Name == "OnSealed" {
				onSealed = as.Rhs[0]
			}
		}
		return true
	})
	if onSealed == nil {
		t.Fatal("adminProducer.OnSealed assignment not found")
	}
	if n := len(s.calls(onSealed, "networking.PublishPolAfterBlockSeal")); n != 0 {
		t.Fatal("OnSealed still publishes POL before the block is durable")
	}
	for _, keep := range []string{"stakingLedger.ProcessCommittedHeight", "adminFinality.UpdateTip"} {
		if len(s.calls(onSealed, keep)) != 1 {
			t.Fatalf("OnSealed lost %s", keep)
		}
	}
	// The only POL publish is the hook's PublishPol (H10).
	var inHook int
	ast.Inspect(s.main, func(n ast.Node) bool {
		if kv, ok := n.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "PublishPol" {
				inHook += len(s.calls(kv.Value, "networking.PublishPolAfterBlockSeal"))
			}
		}
		return true
	})
	if inHook != 1 || len(s.calls(s.main, "networking.PublishPolAfterBlockSeal")) != 1 {
		t.Fatal("POL must be published exactly once, from the hook's PublishPol (H10)")
	}
	// The hook is installed as OnSealedBlock.
	var installed bool
	ast.Inspect(s.main, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == 1 {
			if sel, ok := as.Lhs[0].(*ast.SelectorExpr); ok && sel.Sel.Name == "OnSealedBlock" {
				if r, ok := as.Rhs[0].(*ast.SelectorExpr); ok && r.Sel.Name == "OnSealedBlock" {
					if x, ok := r.X.(*ast.Ident); ok && x.Name == "hl1Hook" {
						installed = true
					}
				}
			}
		}
		return true
	})
	if !installed {
		t.Fatal("adminProducer.OnSealedBlock is not hl1Hook.OnSealedBlock")
	}
}

func TestHL1WiringNoRewardSink(t *testing.T) {
	s := hl1ParseMain(t)
	ast.Inspect(s.file, func(n ast.Node) bool {
		if kv, ok := n.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "RewardSink" {
				t.Fatalf("RewardSink is wired at line %d", s.line(kv.Pos()))
			}
		}
		return true
	})
}
