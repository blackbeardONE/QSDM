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
	// Arity is part of the signature, so checking it narrows a bare name match
	// to something much closer to the real method without type-checking the
	// whole repo. Delegate(as, delegator, validator, amount);
	// BeginUnbond(as, delegator, validator, amount, currentHeight, unbondBlocks).
	guarded := map[string]int{"Delegate": 4, "BeginUnbond": 6}

	var offenders []string
	for _, path := range productionGoFiles(t) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			// A file that does not parse cannot be hiding a compiling call.
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			want, guardedName := guarded[sel.Sel.Name]
			if !guardedName || len(call.Args) != want {
				return true
			}
			pos := fset.Position(call.Lparen)
			offenders = append(offenders, sel.Sel.Name+" called at "+
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
