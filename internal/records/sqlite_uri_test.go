package records

import (
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// uriParams are the parameters of the strictest open the stores make
// (OpenReaderDB's quiescent file), as the URIs below carry them.
var uriParams = url.Values{"_pragma": {"busy_timeout(10000)"}, "mode": {"ro"}, "immutable": {"1"}}

const uriQuery = "?_pragma=busy_timeout%2810000%29&immutable=1&mode=ro"

// A Windows path is the path of a file: URI the way SQLite reads one: its
// backslashes are slashes and its drive follows a slash, so that the drive is
// part of the path and not the URI's authority. The expected URIs are what
// SQLite's own documentation gives (file:///C:/Documents%20and%20Settings/...)
// and what the native store builds (internal/memory/store/db.go).
func TestSQLiteURIOfWindowsPaths(t *testing.T) {
	for _, c := range []struct{ name, path, want string }{
		{"a drive", `C:\a\b.db`, "file:///C:/a/b.db"},
		{"a lowercase drive", `c:\a\b.db`, "file:///c:/a/b.db"},
		{"a temporary directory", `C:\Users\runneradmin\AppData\Local\Temp\TestOpen123\001\budget.db`,
			"file:///C:/Users/runneradmin/AppData/Local/Temp/TestOpen123/001/budget.db"},
		{"spaces and a percent sign", `D:\Program Files\100%\a b.db`, "file:///D:/Program%20Files/100%25/a%20b.db"},
		{"slashes already", `C:/a/b.db`, "file:///C:/a/b.db"},
		{"both slashes", `C:\a/b\c.db`, "file:///C:/a/b/c.db"},
		{"a UNC path", `\\server\share\dir\a.db`, "file:////server/share/dir/a.db"},
		{"a UNC path with spaces", `\\server\my share\a b.db`, "file:////server/my%20share/a%20b.db"},
		{"the characters of a URI", `E:\q#x&y=1+2;3\a.db`, "file:///E:/q%23x&y=1+2;3/a.db"},
		{"non-ASCII", `C:\用户\a.db`, "file:///C:/%E7%94%A8%E6%88%B7/a.db"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := sqliteURI(c.path, true, uriParams); got != c.want+uriQuery {
				t.Errorf("sqliteURI(%q) =\n%s\nwant\n%s", c.path, got, c.want+uriQuery)
			}
			// Without parameters there is no query at all.
			if got := sqliteURI(c.path, true, nil); got != c.want {
				t.Errorf("sqliteURI(%q) without parameters = %s, want %s", c.path, got, c.want)
			}
		})
	}
}

// legacySQLiteURI is how the stores built their URIs before the helper: the
// path as the path of a url.URL, whatever its syntax.
func legacySQLiteURI(path string, query url.Values) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	for key, values := range query {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Where paths are POSIX paths, the URI is what it always was, byte for byte:
// a store opened by one binary and read by another must name the same file,
// and the parameters (mode=ro, immutable=1) are as they were.
func TestSQLiteURIOfPOSIXPathsIsWhatItAlwaysWas(t *testing.T) {
	queries := []url.Values{
		nil,
		uriParams,
		{"_pragma": {"busy_timeout(10000)"}, "_txlock": {"immediate"}},
		{"_pragma": {"busy_timeout(10000)"}, "_txlock": {"immediate"}, "mode": {"ro"}},
		{"_pragma": {"busy_timeout(10000)"}},
		{"_pragma": {"busy_timeout(10000)"}, "mode": {"ro"}},
	}
	for _, path := range []string{
		"/var/lib/mnemon-aml/1/data/users/0123456789abcdef0123456789abcdef/records.db",
		"/tmp/with space/x.db",
		"/tmp/100%/a b.db",
		"/tmp/a?b#c.db",
		"/tmp/q&x=1+2;3,4$5@6/a.db",
		"/tmp/用户/a.db",
		`/tmp/back\slash/a.db`,
		`/tmp/C:\Users\x.db`,
		"/C:/a/b.db",
		"//double/slash.db",
		"/a//b/../c/./d.db",
		"/trailing/",
		"/",
		// Not absolute: not a path any store opens, and as refused as ever.
		"rel/x.db",
		"x.db",
		"C:/a/b.db",
		`C:\a\b.db`,
		`\\server\share\a.db`,
		"",
	} {
		for _, query := range queries {
			if got, want := sqliteURI(path, false, query), legacySQLiteURI(path, query); got != want {
				t.Errorf("sqliteURI(%q, POSIX) = %s, want what it was: %s", path, got, want)
			}
		}
	}
	// Spelled out, for the paths the stores really have.
	for _, c := range []struct {
		path  string
		query url.Values
		want  string
	}{
		{"/var/lib/mnemon-aml/1/data/users/k/records.db", url.Values{"_pragma": {"busy_timeout(10000)"}, "_txlock": {"immediate"}},
			"file:///var/lib/mnemon-aml/1/data/users/k/records.db?_pragma=busy_timeout%2810000%29&_txlock=immediate"},
		{"/var/lib/mnemon-aml/1/data/users/k/records.db", uriParams,
			"file:///var/lib/mnemon-aml/1/data/users/k/records.db" + uriQuery},
		{"/tmp/my dir/100%/model-budget.db", url.Values{"_pragma": {"busy_timeout(10000)"}},
			"file:///tmp/my%20dir/100%25/model-budget.db?_pragma=busy_timeout%2810000%29"},
	} {
		if got := sqliteURI(c.path, false, c.query); got != c.want {
			t.Errorf("sqliteURI(%q, POSIX) = %s, want %s", c.path, got, c.want)
		}
	}
}

// The DSNs the stores and their readers open by are what they have always
// been for a POSIX path, parameters included: the writer's busy timeout and
// immediate transactions, a read-only store's mode=ro, a reader's mode=ro and,
// for a quiescent file, immutable=1 (and no transaction mode: a reader takes
// no write lock).
func TestStoreDSNsAreWhatTheyWere(t *testing.T) {
	const dir = "/var/lib/mnemon-aml/1/data/users/0123456789abcdef0123456789abcdef/"
	const spaced = "/tmp/my dir/100%/"
	for _, c := range []struct{ got, want string }{
		{sqliteDSN(dir+"records.db", false), "file://" + dir + "records.db?_pragma=busy_timeout%2810000%29&_txlock=immediate"},
		{sqliteDSN(dir+"records.db", true), "file://" + dir + "records.db?_pragma=busy_timeout%2810000%29&_txlock=immediate&mode=ro"},
		{sqliteDSN(spaced+"records.db", false), "file:///tmp/my%20dir/100%25/records.db?_pragma=busy_timeout%2810000%29&_txlock=immediate"},
		{readerDSN(dir+"records.db", false), "file://" + dir + "records.db?_pragma=busy_timeout%2810000%29&mode=ro"},
		{readerDSN(dir+"records.db", true), "file://" + dir + "records.db?_pragma=busy_timeout%2810000%29&immutable=1&mode=ro"},
		{readerDSN(spaced+"memory-index.db", true), "file:///tmp/my%20dir/100%25/memory-index.db?_pragma=busy_timeout%2810000%29&immutable=1&mode=ro"},
	} {
		if c.got != c.want {
			t.Errorf("DSN\n%s\nwant\n%s", c.got, c.want)
		}
	}
}

// SQLiteURI follows the host it runs on.
func TestSQLiteURIFollowsTheHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x y", "a.db")
	if got, want := SQLiteURI(path, uriParams), sqliteURI(path, runtime.GOOS == "windows", uriParams); got != want {
		t.Errorf("SQLiteURI(%q) = %s, want %s", path, got, want)
	}
}

// openCode is the SQLite result code (primary) of opening the file a DSN
// names and using it: 0 when it works.
func openCode(t *testing.T, dsn string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Ping(); err == nil {
		return 0
	}
	var failure *sqlite.Error
	if !errors.As(err, &failure) {
		t.Fatalf("%s: %v", dsn, err)
	}
	return failure.Code() & 0xff
}

// SQLite's URI parser takes the URIs made of Windows paths. The files do not
// exist here, so the open fails at the file (SQLITE_CANTOPEN), after the URI
// has been read; the URI the stores used to build for such a path, with its
// drive as the authority (file://C:%5Ca%5Cb.db), fails in the parser itself
// (SQLITE_ERROR) on every host. Directories that do not exist keep the test
// from reaching a network share on Windows.
func TestSQLiteAcceptsTheURIsOfWindowsPaths(t *testing.T) {
	paths := []string{`C:\no-such-dir\sqlite uri\100%\a.db`}
	if runtime.GOOS != "windows" { // a UNC path is a network share there
		paths = append(paths, `\\no-such-host\share\a b.db`)
	}
	for _, path := range paths {
		if code := openCode(t, sqliteURI(path, true, uriParams)); code != sqlite3.SQLITE_CANTOPEN {
			t.Errorf("%s: opening gave result code %d, want %d (SQLITE_CANTOPEN: the URI was read)", path, code, sqlite3.SQLITE_CANTOPEN)
		}
	}
}

// The file the URI names is the file at the path, whatever characters its
// directory has, and the parameters take effect: a database made through the
// helper is read through a read-only, immutable one, which cannot write.
func TestSQLiteURIOpensTheFileAtThePath(t *testing.T) {
	name := "a b%c#d&e=f+g;h 用户"
	if runtime.GOOS != "windows" { // ? is not a character of a Windows file name
		name += "?i"
	}
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "x.db")
	write, err := sql.Open("sqlite", SQLiteURI(path, url.Values{"_pragma": {"busy_timeout(10000)"}, "_txlock": {"immediate"}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = write.Exec(`CREATE TABLE t(x TEXT); INSERT INTO t VALUES ('kept')`); err != nil {
		t.Fatal(err)
	}
	if err = write.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("the URI names another file than the path:", err)
	}
	read, err := sql.Open("sqlite", SQLiteURI(path, uriParams))
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	var got string
	if err = read.QueryRow(`SELECT x FROM t`).Scan(&got); err != nil || got != "kept" {
		t.Fatalf("read %q, %v", got, err)
	}
	if _, err = read.Exec(`INSERT INTO t VALUES ('refused')`); err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("a mode=ro connection wrote: %v", err)
	}
}

// Every SQLite URI of this package is made by SQLiteURI: one built by hand
// from a path is only right where paths are POSIX paths, as every one was
// before Windows had to open a store.
func TestSQLiteURIsAreMadeByTheHelper(t *testing.T) {
	assertNoHandMadeSQLiteURI(t, ".", "sqlite_uri.go")
}

// assertNoHandMadeSQLiteURI fails for each file of the package in dir, but the
// ones named in except and the tests, that builds a file: URI itself: a
// url.URL with the scheme file, or a string that starts with "file:".
func assertNoHandMadeSQLiteURI(t *testing.T, dir string, except ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || slices.Contains(except, name) {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
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
