//go:build !unix

package aml

import "os"

// fileOwner tells no owner where files have no POSIX owner (Windows): the
// owner rule is not checked there (ruling R89).
func fileOwner(os.FileInfo) (owner, process int, ok bool) { return 0, 0, false }
