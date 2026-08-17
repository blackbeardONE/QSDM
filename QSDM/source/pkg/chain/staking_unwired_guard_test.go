package chain

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"
)

// stakingGuardedMethods are the StakingLedger methods that move CELL.
var stakingGuardedMethods = map[string]bool{"Delegate": true, "BeginUnbond": true}

const stakingLedgerType = "github.com/blackbeardONE/QSDM/pkg/chain.StakingLedger"

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
// ValidatorSet.Register (validator.go:109) likewise has exactly one production
// caller, cmd/qsdm/main.go:1130, registering only this node's own signer at
// startup. So the audit's "membership seats anyone with 100 CELL" scenario has
// no path to occur at all, not merely no path through the Sync* functions.
//
// That makes the row unreachable rather than open, and this file is the
// evidence for saying so. But "unreachable today" is exactly the state 2b was
// in before someone wired it: an unauthenticated mutator waiting for a caller.
// Delegate and BeginUnbond take a delegator address as a plain string and move
// CELL out of that account with no signature, no public key and no nonce bump.
//
// # Why this resolves types instead of matching names
//
// Four earlier versions of this guard matched names, and five review rounds
// broke it five times:
//
//  1. an untracked .go file escaped a `git ls-files` scan;
//  2. a call split across lines escaped a line regex;
//  3. a method value (`f := s.Delegate`) escaped a call-site-only AST match;
//  4. reflect.MethodByName("Delegate") escaped every name match, then
//     reflect.Value.Method(i) by INDEX escaped the literal-name match;
//  5. a caller reaching the type through a re-exported alias
//     (`type SL = chain.StakingLedger`) escaped an import-path scope check,
//     because that file never imports pkg/chain at all.
//
// Each fix closed the demonstrated variant and left the next. That is not five
// unlucky misses: an AST scan reasons about IDENTIFIERS -- names, import paths,
// literal arguments -- and every one of these evasions moves the call through a
// layer of the type system that identifiers do not describe. The approach could
// only ever be handed a longer list of shapes to distrust.
//
// So this resolves types. go/packages type-checks the module and every selection
// is compared against the actual declared method on *chain.StakingLedger,
// however the receiver got into scope -- alias, embedding, method value, or a
// plain call. Aliases and embedding stop being special cases because the checker
// has already resolved them.
//
// Known limits, stated rather than implied away:
//   - Reflection is invisible to the type checker, so it keeps a separate
//     name-based scan below.
//   - Only this module is loaded. sdk/go and wasmer-go-patched are separate
//     modules; sdk/go does not import pkg/chain today, verified.
//   - A call through an interface resolves to the interface's method, not to
//     StakingLedger's. No such interface exists in this tree today, and
//     TestStakingLedger_guardedMethodsStillExist would not catch one appearing.
func TestStakingLedger_valueMoversHaveNoProductionCallers(t *testing.T) {
	pkgs := loadModuleForStakingGuard(t)

	var offenders []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.TypesInfo == nil {
			return
		}
		for expr, sel := range p.TypesInfo.Selections {
			fn, ok := sel.Obj().(*types.Func)
			if !ok || !stakingGuardedMethods[fn.Name()] {
				continue
			}
			sig, ok := fn.Type().(*types.Signature)
			if !ok || sig.Recv() == nil {
				continue
			}
			// The receiver is the real thing or it is not; no name heuristics.
			recv := strings.TrimPrefix(sig.Recv().Type().String(), "*")
			if recv != stakingLedgerType {
				continue
			}
			pos := p.Fset.Position(expr.Pos())
			offenders = append(offenders, fn.Name()+" reached in "+p.PkgPath+" at "+
				filepath.ToSlash(pos.Filename)+":"+itoaForStakingGuard(pos.Line))
		}
	})

	if len(offenders) > 0 {
		t.Fatalf("StakingLedger.Delegate/BeginUnbond now have production callers:\n  %s\n\n"+
			"These methods move CELL out of a delegator account identified by a bare string, "+
			"with no signature check, no public key and no nonce bump. Before wiring them, "+
			"authenticate the caller the way wallet_transfer.go:41-48 and enrollment_apply.go:202 "+
			"do at replay -- carry Signature/PublicKey through to apply and use DebitAndBumpNonce "+
			"(account.go:93). Then update audit critical #2, which this test is the evidence for.",
			strings.Join(offenders, "\n  "))
	}
}

// Reflection is the one reach the type checker cannot see: the method name stops
// being a name. Both of these compile and move CELL, and neither leaves a
// resolvable selection behind:
//
//	reflect.ValueOf(s).MethodByName("Delegate").Call(args)
//	reflect.ValueOf(s).Method(idx).Call(args)
//
// So the rule here is inverted relative to the type scan: flag every reflective
// method lookup in a file that could hold a *StakingLedger, and exempt only what
// is PROVABLY harmless -- a string literal naming some other method. A
// non-literal argument cannot be judged and gets no benefit of the doubt.
// Reflection is rare in this tree, so noise is cheap here; silence is not.
func TestStakingLedger_valueMoversNotReachedByReflection(t *testing.T) {
	pkgs := loadModuleForStakingGuard(t)

	var offenders []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if !packageCanHoldStakingLedger(p) {
			return
		}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Method", "MethodByName", "FieldByName":
				default:
					return true
				}
				detail := "a reflective lookup whose argument is not a literal, so it " +
					"cannot be ruled out, at "
				if lit, isLit := call.Args[0].(*ast.BasicLit); isLit && lit.Kind == token.STRING {
					name := strings.Trim(lit.Value, "`\"")
					if !stakingGuardedMethods[name] {
						return true // provably naming some other method
					}
					detail = name + " reached by reflection at "
				}
				pos := p.Fset.Position(call.Lparen)
				offenders = append(offenders, detail+
					filepath.ToSlash(pos.Filename)+":"+itoaForStakingGuard(pos.Line))
				return true
			})
		}
	})

	if len(offenders) > 0 {
		t.Fatalf("reflective method lookups in packages that can hold a *StakingLedger:\n  %s\n\n"+
			"Reflection defeats the type-resolved guard above, so these are flagged on sight. "+
			"If the lookup provably cannot reach Delegate or BeginUnbond, name the method with a "+
			"string literal and this test will exempt it.",
			strings.Join(offenders, "\n  "))
	}
}

// packageCanHoldStakingLedger keeps the reflection scan off packages that could
// not obtain the receiver in the first place. Unlike the import-path check this
// replaces, it asks the type checker, so an alias or an indirect path counts.
func packageCanHoldStakingLedger(p *packages.Package) bool {
	if p.PkgPath == "github.com/blackbeardONE/QSDM/pkg/chain" {
		return true
	}
	for path := range p.Imports {
		if path == "github.com/blackbeardONE/QSDM/pkg/chain" {
			return true
		}
	}
	if p.TypesInfo == nil {
		return false
	}
	for _, tv := range p.TypesInfo.Types {
		if tv.Type == nil {
			continue
		}
		if strings.Contains(strings.TrimPrefix(tv.Type.String(), "*"), stakingLedgerType) {
			return true
		}
	}
	return false
}

// If a guarded method is renamed or removed, the scan above stops matching it
// and goes quiet -- passing for the wrong reason. Fail loudly here instead.
//
// This replaces an arity pin that guarded the old name-and-arity matching. Arity
// no longer decides anything, but existence still does.
func TestStakingLedger_guardedMethodsStillExist(t *testing.T) {
	pkgs := loadModuleForStakingGuard(t)
	var chainPkg *packages.Package
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.PkgPath == "github.com/blackbeardONE/QSDM/pkg/chain" {
			chainPkg = p
		}
	})
	if chainPkg == nil || chainPkg.Types == nil {
		t.Fatal("could not load pkg/chain's types; the guard above would match nothing")
	}

	named, ok := chainPkg.Types.Scope().Lookup("StakingLedger").(*types.TypeName)
	if !ok {
		t.Fatal("StakingLedger no longer declared in pkg/chain -- the caller scan is matching nothing")
	}
	ptr := types.NewPointer(named.Type())
	ms := types.NewMethodSet(ptr)

	found := map[string]bool{}
	for i := 0; i < ms.Len(); i++ {
		found[ms.At(i).Obj().Name()] = true
	}
	for name := range stakingGuardedMethods {
		if !found[name] {
			t.Errorf("StakingLedger.%s no longer exists. If it was renamed, update "+
				"stakingGuardedMethods -- TestStakingLedger_valueMoversHaveNoProductionCallers "+
				"matches on it and is silently matching nothing right now. If the method was "+
				"removed outright, say so in audit critical #2.", name)
		}
	}
}

// loadModuleForStakingGuard type-checks this module.
//
// packages.Load shells out to `go list`, and the `go` on PATH in this
// environment is a trimmed shim that does not work -- so PATH is prefixed with
// the toolchain actually running this test. CGO_CFLAGS/CGO_LDFLAGS are cleared
// for the same reason the build scripts clear them.
//
// Errors are fatal, never skipped: a guard that skips is indistinguishable from
// a guard that passes, and this one is the evidence for closing a critical.
var (
	stakingGuardOnce sync.Once
	stakingGuardPkgs []*packages.Package
	stakingGuardFail string
)

// loadModuleForStakingGuard type-checks the module once per test binary. Three
// guard tests need it, and at several seconds a load, paying three times on
// every `go test ./pkg/chain/` is a cost someone would eventually delete the
// guard to avoid.
func loadModuleForStakingGuard(t *testing.T) []*packages.Package {
	t.Helper()
	stakingGuardOnce.Do(func() {
		stakingGuardPkgs, stakingGuardFail = loadModuleForStakingGuardOnce()
	})
	if stakingGuardFail != "" {
		// Fatal, never Skip: a skipped guard is indistinguishable from a
		// passing one, and this one is the evidence for closing a critical.
		t.Fatal(stakingGuardFail)
	}
	return stakingGuardPkgs
}

func loadModuleForStakingGuardOnce() ([]*packages.Package, string) {
	root, rootErr := stakingGuardModuleRoot()
	if rootErr != "" {
		return nil, rootErr
	}

	env := append(os.Environ(),
		"PATH="+filepath.Join(runtime.GOROOT(), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CGO_CFLAGS=", "CGO_LDFLAGS=")

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Env:        env,
		BuildFlags: []string{"-tags", "dilithium_circl"},
		Dir:        root,
		Tests:      false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, "type-check the module: " + err.Error()
	}
	if len(pkgs) == 0 {
		return nil, "loaded zero packages -- the guard would pass vacuously"
	}

	var loadErrs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, p.PkgPath+": "+e.Error())
		}
	})
	// Coverage is the whole point, so assert it rather than trusting ./... to
	// mean what it looks like it means. The previous version passed Dir: ".."
	// with a comment claiming that was the module root; from the test's cwd of
	// pkg/chain it is pkg/, and cmd/ and internal/ were never type-checked. A
	// plain unauthenticated call added to cmd/qsdm compiled into the shipped
	// binary and the guard passed it. Counting packages is not checking them.
	var sawCmd, sawInternal bool
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		switch {
		case strings.Contains(p.PkgPath, "/QSDM/cmd/"):
			sawCmd = true
		case strings.Contains(p.PkgPath, "/QSDM/internal/"):
			sawInternal = true
		}
	})
	if !sawCmd || !sawInternal {
		return nil, fmt.Sprintf("loaded %d packages but reached cmd/=%v internal/=%v -- the scan is blind to "+
			"the wiring layer, which is exactly where a caller would be added (cmd/qsdm/main.go "+
			"is named in this file as the production entrypoint)", len(pkgs), sawCmd, sawInternal)
	}

	if len(loadErrs) > 0 {
		// A package that fails to type-check contributes no selections, so
		// tolerating errors here would let a caller hide behind an unrelated
		// compile failure.
		return nil, "packages failed to type-check, so the scan would be blind to them:\n  " +
			strings.Join(loadErrs, "\n  ")
	}
	return pkgs, ""
}

// The audit says grep Signature returns 0 hits in the staking files. That was
// measured on a branch carrying staking_tx.go. Pin it for what actually ships,
// so the claim in the audit is checkable rather than inherited.
//
// Bare relative names are safe here: the test binary's working directory is
// always the package source directory, and a rename makes os.ReadFile fail into
// t.Fatalf rather than pass vacuously.
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

// stakingGuardModuleRoot walks up to the go.mod. Passing a relative Dir is what
// broke the previous version: `go test` runs with cwd = the package directory,
// so ".." was pkg/, not the module root.
func stakingGuardModuleRoot() (string, string) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "cannot locate module root: " + err.Error()
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "no go.mod above " + dir + " -- cannot scope the guard"
		}
		dir = parent
	}
}
