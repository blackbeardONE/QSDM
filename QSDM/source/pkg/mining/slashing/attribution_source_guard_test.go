package slashing

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHMACAttributionGateHasNoProductionCallers(t *testing.T) {
	root := sourceRootForAttributionGuard(t)
	var callers []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".cache", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if callsSetHMACAttribution(call) {
				pos := fset.Position(call.Pos())
				callers = append(callers, pos.String())
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan source: %v", err)
	}
	if len(callers) > 0 {
		t.Fatalf("SetHMACAttestationAttributable has production callers:\n  %s\n\n"+
			"Do not enable this gate until enrollment stores an asymmetric public key "+
			"instead of a public symmetric HMAC key.", strings.Join(callers, "\n  "))
	}
}

func callsSetHMACAttribution(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name == "SetHMACAttestationAttributable"
	case *ast.SelectorExpr:
		return fn.Sel.Name == "SetHMACAttestationAttributable"
	default:
		return false
	}
}

func sourceRootForAttributionGuard(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate source go.mod")
		}
		dir = parent
	}
}
