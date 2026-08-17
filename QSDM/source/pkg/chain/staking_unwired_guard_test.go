package chain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stakingGuardedArity is the parameter count of each guarded method as declared
// today. Arity narrows a bare name match to something much closer to the real
// method without type-checking the repo:
//
//	Delegate(as, delegator, validator, amount)
//	BeginUnbond(as, delegator, validator, amount, currentHeight, unbondBlocks)
//
// It is also the guard's most dangerous moving part, so it is pinned by
// TestStakingLedger_guardedArityMatchesDeclaredSignatures below. Adding the
// Signature/PublicKey parameters this guard's own failure message recommends
// would change these numbers and, unpinned, would silently stop matching the
// very methods being guarded -- disarming the check at the exact moment someone
// is touching them. Raised by review; it is a silent failure, so it gets a test
// rather than a comment.
var stakingGuardedArity = map[string]int{"Delegate": 4, "BeginUnbond": 6}

// Critical #2 in the capability audit -- "staking txs unauthenticated at apply"
// -- names code that does not exist on main's lineage.
//
// The audit's fix line says to "mirror the wallet_transfer.go pattern in
// ApplyStakingTx", citing staking_tx.go and handlers_staking.go. Neither file
// is on main or on this branch; they belong to agent/multi-mother-hive-release,
// which is not an ancestor of main (audit §0). What ships is StakingLedger, and
// its two value-moving methods -- Delegate and BeginUnbond -- have ZERO
// production callers. There is no staking transaction to authenticate because
// there is no staking transaction.
//
// A reviewer took that further: ValidatorSet.Register (validator.go:109) has
// exactly one production caller in the tree, cmd/qsdm/main.go:1130, which
// registers only this node's own consensusSigner.Address() at startup. So the
// audit's "membership seats anyone with 100 CELL" scenario has no path to occur
// at all, not merely no path through the Sync* functions.
//
// That makes the row unreachable rather than open, and this file is the
// evidence for saying so. But "unreachable today" is exactly the state 2b was
// in before someone wired it: an unauthenticated mutator sitting in the tree,
// waiting for a caller. Delegate and BeginUnbond take a delegator address as a
// plain string and move CELL out of that account with no signature, no public
// key and no nonce bump. Wiring either to a transaction type or an HTTP handler
// without adding authentication first would reintroduce the critical in its
// original form.
//
// So this guard fails the moment a production caller appears.
//
// # Why this parses instead of grepping
//
// The first version scanned `git ls-files` output with a line regex, and a
// reviewer broke it twice in ways that matter:
//
//   - An untracked .go file calling Delegate compiled into the build and the
//     guard passed, because `git ls-files` lists only tracked files. A file is
//     untracked for exactly as long as it takes to write it and forget to add
//     it -- and that window is when someone is wiring something new.
//   - A call split across lines (`s.` on one line, `Delegate(...)` on the next)
//     passed, because the regex matched per line. gofmt itself breaks long
//     chained calls that way, so it was not a contrived case.
//
// Both holes came from inspecting a proxy for the build rather than the build.
// This version walks the filesystem -- what the compiler actually reads -- and
// parses each file, so neither the index nor the line breaks matter.
func TestStakingLedger_valueMoversHaveNoProductionCallers(t *testing.T) {
	guarded := stakingGuardedArity

	var offenders []string
	for _, path := range productionGoFiles(t) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			// A file that does not parse cannot be hiding a compiling call.
			continue
		}
		if !canReachStakingLedger(f) {
			continue
		}

		// Arity is only knowable at a call site. A METHOD VALUE has no
		// arguments to count -- `f := s.Delegate` then `f(as, a, b, amt)` is a
		// real, compiling call that a call-site-only scan never sees, which is
		// how a reviewer walked through the previous version of this test. So
		// collect the call sites first, then treat every remaining reference to
		// the name as a hit regardless of context.
		callArity := map[*ast.SelectorExpr]int{}
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					callArity[sel] = len(call.Args)
				}
			}
			return true
		})

		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			want, guardedName := guarded[sel.Sel.Name]
			if !guardedName {
				return true
			}
			how := "referenced as a value at "
			if got, isCall := callArity[sel]; isCall {
				// A direct call whose arity cannot be this method is some other
				// type's same-named method; leaving those out keeps the guard
				// usable. A non-call reference gets no such benefit of the
				// doubt, because there is no arity to judge it by.
				if got != want {
					return true
				}
				how = "called at "
			}
			pos := fset.Position(sel.Sel.Pos())
			offenders = append(offenders, sel.Sel.Name+" "+how+
				filepath.ToSlash(path)+":"+itoaForStakingGuard(pos.Line))
			return true
		})

		// Reflection defeats every name match above, because the name stops
		// being a name: reflect.ValueOf(s).MethodByName("Delegate").Call(args)
		// compiles, moves CELL, and contains no SelectorExpr called Delegate --
		// only a string literal. A reviewer confirmed the guard passed it.
		//
		// Matched narrowly, on a MethodByName-style call taking a guarded name
		// as a literal, rather than on any string equal to "Delegate" anywhere.
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "MethodByName" && sel.Sel.Name != "FieldByName") {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			name := strings.Trim(lit.Value, "`\"")
			if _, guardedName := guarded[name]; !guardedName {
				return true
			}
			pos := fset.Position(lit.Pos())
			offenders = append(offenders, name+" reached by reflection at "+
				filepath.ToSlash(path)+":"+itoaForStakingGuard(pos.Line))
			return true
		})
	}

	if len(offenders) > 0 {
		t.Fatalf("StakingLedger.Delegate/BeginUnbond now have production callers:\n  %s\n\n"+
			"These methods move CELL out of a delegator account identified by a bare string, "+
			"with no signature check, no public key and no nonce bump. Before wiring them, "+
			"authenticate the caller the way wallet_transfer.go:41-48 and enrollment_apply.go:202 "+
			"do at replay -- carry Signature/PublicKey through to apply and use DebitAndBumpNonce "+
			"(account.go:93). Then update audit critical #2, which this test is the evidence for.\n\n"+
			"If one of these is an unrelated method that merely shares a name and arity, this "+
			"guard is name-based and cannot tell them apart -- narrow it, do not delete it.",
			strings.Join(offenders, "\n  "))
	}
}

// canReachStakingLedger reports whether a file could possibly hold a reference
// to a *StakingLedger method: it is either in package chain itself, or it
// imports the chain package. You cannot obtain a *StakingLedger without one of
// those, so this loses no reachable caller.
//
// It exists because the scan flags non-call references by bare name, with no
// type awareness, across every .go file under the module root -- and Delegate
// is a common name. A reviewer proved the cost: wasmer-go-patched declares its
// own `Delegate = 9` opcode constant (wasmer/config_opcodes.go:18), in a
// separate module that merely happens to sit inside the walked tree. It is
// currently unreferenced, so nothing fails today, but one line of debug code
// writing wasmer.Delegate in a non-call expression would have failed this test
// for reasons with nothing to do with staking. A guard that cries wolf gets
// deleted, which would be a worse outcome than the hole it was closing.
//
// Deliberately NOT done by excluding nested modules: sdk/go is a separate
// module inside this tree that could legitimately import pkg/chain and wire a
// real caller. Filtering on the import keeps that in scope while dropping
// wasmer, which cannot reference chain at all.
func canReachStakingLedger(f *ast.File) bool {
	if f.Name != nil && f.Name.Name == "chain" {
		return true
	}
	for _, imp := range f.Imports {
		if imp.Path == nil {
			continue
		}
		path := strings.Trim(imp.Path.Value, "`\"")
		if path == "github.com/blackbeardONE/QSDM/pkg/chain" ||
			strings.HasSuffix(path, "/pkg/chain") {
			return true
		}
	}
	return false
}

// productionGoFiles walks the module from disk rather than asking git, so a
// file that is new, untracked, ignored or staged-but-uncommitted is still
// scanned. The compiler does not consult the index and neither should this.
func productionGoFiles(t *testing.T) []string {
	t.Helper()

	// Walk up from the package directory to the go.mod rather than shelling out
	// to `go list`: the `go` on PATH in this environment is a trimmed shim that
	// does not work, and a Skip on a guard is indistinguishable from a pass.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot locate module root: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s -- cannot scope the guard", dir)
		}
		dir = parent
	}

	var out []string
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// .cache holds detached worktrees of other branches, which DO carry
			// the staking module. They are not this tree and are not built.
			switch d.Name() {
			case ".cache", ".git", "vendor", "testdata", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Skipf("walk module: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("scanned zero production .go files under %s -- the guard would pass "+
			"vacuously", dir)
	}
	return out
}

// The audit says grep Signature returns 0 hits in the staking files. That was
// measured on a branch carrying staking_tx.go. Pin it for what actually ships,
// so the claim in the audit is checkable rather than inherited.
//
// Bare relative names are safe here: the test binary's working directory is
// always the package source directory, and a rename makes os.ReadFile fail into
// t.Fatalf rather than pass vacuously. Both confirmed by review.
func TestStakingLedger_shippedFilesCarryNoSignatureHandling(t *testing.T) {
	for _, name := range []string{"staking_ledger.go", "staking_persist.go", "staking_registry.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(b), "Signature") {
			t.Errorf("%s now mentions Signature. If authentication was added, say so in "+
				"audit critical #2 and relax TestStakingLedger_valueMoversHaveNoProductionCallers, "+
				"which currently blocks wiring precisely because no such handling exists.", name)
		}
	}
}

func itoaForStakingGuard(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// If the declared signatures drift from stakingGuardedArity, fail here -- where
// the message can say what happened -- instead of letting the caller scan go
// quietly blind.
func TestStakingLedger_guardedArityMatchesDeclaredSignatures(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "staking_ledger.go", nil, 0)
	if err != nil {
		t.Fatalf("parse staking_ledger.go: %v", err)
	}

	found := map[string]int{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Type.Params == nil {
			continue
		}
		if _, guarded := stakingGuardedArity[fn.Name.Name]; !guarded {
			continue
		}
		n := 0
		for _, field := range fn.Type.Params.List {
			// `a, b string` is one field carrying two names.
			if len(field.Names) == 0 {
				n++
				continue
			}
			n += len(field.Names)
		}
		found[fn.Name.Name] = n
	}

	for name, want := range stakingGuardedArity {
		got, ok := found[name]
		if !ok {
			t.Errorf("StakingLedger.%s no longer declared in staking_ledger.go. If it moved or "+
				"was renamed, update stakingGuardedArity -- the caller scan matches on name and "+
				"is silently matching nothing right now.", name)
			continue
		}
		if got != want {
			t.Errorf("StakingLedger.%s now takes %d parameters, stakingGuardedArity says %d. "+
				"TestStakingLedger_valueMoversHaveNoProductionCallers filters direct calls by "+
				"arity, so until this map is updated it will not flag a real call. If the extra "+
				"parameters are Signature/PublicKey, authentication may now exist -- say so in "+
				"audit critical #2 rather than only bumping the number.", name, got, want)
		}
	}
}
