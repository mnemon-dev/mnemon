package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The DeepSeek Harness workspace in dsh/ must stay outside the root Go module:
// a nested go.mod excludes it from the module zip that `go install` downloads.
func TestDSHWorkspaceStaysOutsideTheRootModule(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("dsh", "go.mod"))
	if err != nil {
		t.Fatalf("dsh/go.mod must exist so the workspace stays out of the root module zip: %v", err)
	}
	module := ""
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			module = fields[1]
		}
	}
	if module != "github.com/mnemon-dev/mnemon/dsh" {
		t.Fatalf("dsh/go.mod must declare module github.com/mnemon-dev/mnemon/dsh, got %q", module)
	}
	err = filepath.WalkDir("dsh", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "node_modules" {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") {
			t.Errorf("%s: Go code does not belong in the DeepSeek Harness workspace", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk dsh: %v", err)
	}
}
