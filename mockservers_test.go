package vlatte

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A mock server of this module gives its admin API its scenarios, and runs the scenario suite from its tests: a new
// server that skips either fails here instead of shipping without scenarios. A mock server is a package that builds
// adminapi.Capabilities.

func TestMockServers_SupportScenarios(t *testing.T) {
	servers := make(map[string]bool)
	runsSuite := make(map[string]bool)

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if path != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}

		dir := filepath.Dir(path)
		if strings.HasSuffix(path, "_test.go") {
			ast.Inspect(file, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && isSelector(call.Fun, "scenariotest", "Run") {
					runsSuite[dir] = true
				}

				return true
			})

			return nil
		}

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isSelector(lit.Type, "adminapi", "Capabilities") {
				return true
			}

			servers[dir] = true
			require.True(t, setsField(lit, "Scenarios"), "%s gives its admin API no Scenarios", path)

			return true
		})

		return nil
	})
	require.NoError(t, err)

	require.NotEmpty(t, servers, "found no mock server: the test no longer finds adminapi.Capabilities")
	for dir := range servers {
		require.True(t, runsSuite[dir], "%s doesn't run scenariotest.Run from its tests", dir)
	}
}

// isSelector reports whether expr is pkg.name.
func isSelector(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	x, ok := sel.X.(*ast.Ident)

	return ok && x.Name == pkg && sel.Sel.Name == name
}

// setsField reports whether lit sets the field key to something other than nil.
func setsField(lit *ast.CompositeLit, key string) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}

		if id, ok := kv.Key.(*ast.Ident); ok && id.Name == key {
			v, isIdent := kv.Value.(*ast.Ident)
			return !isIdent || v.Name != "nil"
		}
	}

	return false
}
