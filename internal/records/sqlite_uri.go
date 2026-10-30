package records

import (
	"net/url"
	"runtime"
	"strings"
)

// SQLiteURI is the file: URI under which SQLite opens the file at path, an
// absolute path in the syntax of the host, with the query's parameters: the
// driver's (_pragma, _txlock) and SQLite's own (mode, immutable), which only a
// file: URI carries. Every SQLite file of the AML stores opens by it: the
// records stores and their readers, the model budget, the model cache and the
// overlay.
//
// A POSIX path is the path of the URI as it is, escaped. A Windows path is
// made the way SQLite reads one: its backslashes become slashes and its drive
// gets a slash before it, so that the drive belongs to the path and not to the
// URI's authority. C:\a\b.db is file:///C:/a/b.db, and the UNC path
// \\host\share\a.db is file:////host/share/a.db. A url.URL given the path as
// it is puts the drive after "file://", where the authority is, and SQLite
// refuses the URI on every host ("invalid uri authority"). The native store
// builds its read-only URIs the same way (internal/memory/store/db.go). The
// escaping is url.URL's: the characters a URI cannot take in a path (space, %,
// ?, #, non-ASCII bytes) become %XX, which SQLite decodes.
//
// It lives here because this is the lowest package the AML stores share:
// memoryagent imports records, and the promoted package graph
// (test/mnemond/architecture) lets neither import the native store.
func SQLiteURI(path string, query url.Values) string {
	return sqliteURI(path, runtime.GOOS == "windows", query)
}

// sqliteURI is SQLiteURI for a host whose paths are Windows paths, or not. The
// syntax is an argument so that either is tested on any host, and so that a
// POSIX path stays what it was whatever it starts with: a name that starts
// with "C:" is a relative path there. A path that is not absolute is not made
// absolute, and SQLite refuses its URI as it always did: no store opens one.
func sqliteURI(path string, windows bool, query url.Values) string {
	if windows {
		path = strings.ReplaceAll(path, `\`, "/") // filepath.ToSlash there
		if hasDrive(path) {
			path = "/" + path
		}
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}
	return u.String()
}

// hasDrive reports whether path starts with a Windows drive: a letter and a
// colon.
func hasDrive(path string) bool {
	return len(path) >= 2 && path[1] == ':' && (path[0] >= 'a' && path[0] <= 'z' || path[0] >= 'A' && path[0] <= 'Z')
}
