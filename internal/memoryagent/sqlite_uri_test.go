package memoryagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The DSNs of the budget file, the model cache and the overlay are what they
// have always been for a POSIX path, parameters included: the budget takes the
// write lock when a transaction begins (a deferred one can deadlock two
// processes), a read-only overlay opens mode=ro. Windows paths are
// records.SQLiteURI's (its tests make them URIs); here the stores must use it.
func TestFileDSNsAreWhatTheyWere(t *testing.T) {
	const dir = "/var/lib/mnemon-aml/1/"
	const spaced = "/tmp/my dir/100%/"
	for _, c := range []struct{ got, want string }{
		{budgetDSN(dir + "model-budget.db"), "file://" + dir + "model-budget.db?_pragma=busy_timeout%2810000%29&_txlock=immediate"},
		{budgetDSN(spaced + "budget.db"), "file:///tmp/my%20dir/100%25/budget.db?_pragma=busy_timeout%2810000%29&_txlock=immediate"},
		{modelCacheDSN(dir + "model-cache.db"), "file://" + dir + "model-cache.db?_pragma=busy_timeout%2810000%29"},
		{modelCacheDSN(spaced + "cache.db"), "file:///tmp/my%20dir/100%25/cache.db?_pragma=busy_timeout%2810000%29"},
		{overlayDSN(dir+"memory-index.db", false), "file://" + dir + "memory-index.db?_pragma=busy_timeout%2810000%29"},
		{overlayDSN(dir+"memory-index.db", true), "file://" + dir + "memory-index.db?_pragma=busy_timeout%2810000%29&mode=ro"},
		{overlayDSN(spaced+"overlay.db", true), "file:///tmp/my%20dir/100%25/overlay.db?_pragma=busy_timeout%2810000%29&mode=ro"},
	} {
		if c.got != c.want {
			t.Errorf("DSN\n%s\nwant\n%s", c.got, c.want)
		}
	}
}

// Every SQLite URI of this package is made by records.SQLiteURI: one built by
// hand from a path is only right where paths are POSIX paths (Windows drives
// are the URI's authority then, which SQLite refuses).
func TestSQLiteURIsAreMadeByTheRecordsHelper(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.CompositeLit:
				if selector, ok := node.Type.(*ast.SelectorExpr); ok && selector.Sel.Name == "URL" {
					for _, element := range node.Elts {
						pair, ok := element.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						if key, ok := pair.Key.(*ast.Ident); ok && key.Name == "Scheme" {
							if value, ok := pair.Value.(*ast.BasicLit); ok && value.Value == `"file"` {
								t.Errorf("%s builds a file: URI by hand: use records.SQLiteURI", fset.Position(node.Pos()))
							}
						}
					}
				}
			case *ast.BasicLit:
				if node.Kind == token.STRING && strings.HasPrefix(node.Value, `"file:`) {
					t.Errorf("%s builds a file: URI by hand: use records.SQLiteURI", fset.Position(node.Pos()))
				}
			}
			return true
		})
	}
}
