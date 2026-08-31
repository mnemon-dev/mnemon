package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestMnemondArchitecture(t *testing.T) {
	root := repositoryRoot(t)
	t.Run("package dependencies match the promoted graph", func(t *testing.T) {
		assertMnemondPackageGraph(t, root)
	})
	t.Run("native memory stays independent from agency", func(t *testing.T) {
		assertNativeMemoryDoesNotImportAgency(t, root)
	})
	t.Run("collaboration cases stay out of production", func(t *testing.T) {
		assertNoCaseKindsInProduction(t, root)
	})
	t.Run("attachments have one interactive issuer", func(t *testing.T) {
		assertInteractiveAttachmentOnly(t, root)
	})
	t.Run("case semantics stay in fixtures", func(t *testing.T) {
		assertCaseFixturesAreDataOnly(t, root)
	})
	t.Run("production does not depend on fixture paths", func(t *testing.T) {
		assertNoFixturePathsInProduction(t, root)
	})
}

func assertNativeMemoryDoesNotImportAgency(t *testing.T, root string) {
	t.Helper()
	agency := map[string]struct{}{
		"cmd/agency":      {},
		"internal/agency": {},
		"internal/daemon": {},
	}
	for _, component := range []string{"cmd/memory", "internal/memory"} {
		forEachComponentGoFile(t, root, component, func(path string, file *ast.File) {
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Errorf("%s: unquote import: %v", path, err)
					continue
				}
				const prefix = modulePath + "/"
				if !strings.HasPrefix(importPath, prefix) {
					continue
				}
				dependency := strings.TrimPrefix(importPath, prefix)
				for component := range agency {
					if dependency == component || strings.HasPrefix(dependency, component+"/") {
						t.Errorf("%s imports Agency component %q", path, dependency)
						break
					}
				}
			}
		})
	}
}

func assertMnemondPackageGraph(t *testing.T, root string) {
	t.Helper()
	want := map[string][]string{
		"internal/memory/embed":       {},
		"internal/memory/model":       {},
		"internal/memory/importdraft": {"internal/memory/model"},
		"internal/memory/store":       {"internal/memory/embed", "internal/memory/model"},
		"internal/memory/search": {
			"internal/memory/embed", "internal/memory/model", "internal/memory/store",
		},
		"internal/memory/graph": {
			"internal/memory/embed", "internal/memory/model", "internal/memory/search",
			"internal/memory/store",
		},
		"internal/memory/service": {
			"internal/memory/embed", "internal/memory/graph", "internal/memory/model",
			"internal/memory/search", "internal/memory/store",
		},
		"internal/memory/setup/assets": {},
		"internal/memory/setup":        {"internal/memory/setup/assets"},
		"internal/agency":              {},
		"internal/agency/client":       {"internal/agency"},
		"internal/agency/attach":       {},
		"internal/agency/authority":    {"internal/agency"},
		"internal/agency/artifact":     {"internal/agency"},
		"internal/agency/peerlink":     {"internal/agency", "internal/agency/artifact"},
		"internal/daemon": {
			"internal/agency", "internal/agency/artifact", "internal/agency/authority",
			"internal/agency/peerlink",
		},
		"cmd/agency": {"internal/agency/attach", "internal/agency/client", "internal/daemon"},
		"cmd/memory": {
			"internal/memory/embed", "internal/memory/graph", "internal/memory/importdraft",
			"internal/memory/model", "internal/memory/search", "internal/memory/service",
			"internal/memory/setup", "internal/memory/setup/assets", "internal/memory/store",
		},
	}
	got := make(map[string]map[string]struct{}, len(want))
	for component := range want {
		got[component] = map[string]struct{}{}
		forEachPackageGoFile(t, root, component, func(path string, file *ast.File) {
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Errorf("%s: unquote import: %v", path, err)
					continue
				}
				if importPath == "github.com/libp2p/go-libp2p-core" ||
					strings.HasPrefix(importPath, "github.com/libp2p/go-libp2p-core/") {
					t.Errorf("%s imports retired libp2p Core path %q", path, importPath)
				}
				const prefix = modulePath + "/"
				if !strings.HasPrefix(importPath, prefix) {
					continue
				}
				dependency := strings.TrimPrefix(importPath, prefix)
				if dependency != component {
					got[component][dependency] = struct{}{}
				}
			}
		})
	}
	for component, expected := range want {
		actual := make([]string, 0, len(got[component]))
		for dependency := range got[component] {
			actual = append(actual, dependency)
		}
		slices.Sort(actual)
		slices.Sort(expected)
		if !slices.Equal(actual, expected) {
			t.Errorf("%s dependencies = %v, want %v", component, actual, expected)
		}
	}
}

func assertNoCaseKindsInProduction(t *testing.T, root string) {
	t.Helper()
	forEachProductionGoFile(t, root, func(path string, file *ast.File) {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			for _, forbidden := range []string{
				"review.", "contract-net.", "blackboard.",
				"memory.wiki.", "teamwork.", "channel.",
			} {
				if strings.Contains(strings.ToLower(value), forbidden) {
					t.Errorf("%s contains case-specific production literal %q", path, value)
				}
			}
			return true
		})
	})
}

func assertInteractiveAttachmentOnly(t *testing.T, root string) {
	t.Helper()
	var declarations, calls []string
	forEachProductionGoFile(t, root, func(path string, file *ast.File) {
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.FuncDecl:
				if strings.HasPrefix(value.Name.Name, "Issue") &&
					strings.HasSuffix(value.Name.Name, "Attachment") {
					declarations = append(declarations, filepath.ToSlash(path)+"::"+value.Name.Name)
				}
			case *ast.CallExpr:
				selector, ok := value.Fun.(*ast.SelectorExpr)
				if ok && strings.HasPrefix(selector.Sel.Name, "Issue") &&
					strings.HasSuffix(selector.Sel.Name, "Attachment") {
					calls = append(calls, filepath.ToSlash(path)+"::"+selector.Sel.Name)
				}
			}
			return true
		})
	})
	assertSingleArchitectureMatch(t, declarations, "/internal/agency/authority/",
		"IssueInteractiveAttachment", "attachment issuer declaration")
	assertSingleArchitectureMatch(t, calls, "/internal/daemon/",
		"IssueInteractiveAttachment", "attachment issuer call")
}

func assertCaseFixturesAreDataOnly(t *testing.T, root string) {
	t.Helper()
	casesRoot := filepath.Join(root, "testdata", "mnemond", "cases")
	entries, err := os.ReadDir(casesRoot)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		names = append(names, name)
		for _, file := range []string{"nodes.txt", "playbook.md", "oracle.sh"} {
			info, err := os.Stat(filepath.Join(casesRoot, name, file))
			if err != nil || info.Size() == 0 {
				t.Errorf("case fixture %s/%s is missing or empty", name, file)
			}
			if file == "oracle.sh" && err == nil && info.Mode()&0o111 == 0 {
				t.Errorf("case fixture %s/%s is not executable", name, file)
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("no R7 collaboration case fixtures")
	}

	for _, runnerName := range []string{"lib.sh", "run_cases.sh"} {
		runner, err := os.ReadFile(filepath.Join(root, "test", "mnemond", "scenarios", runnerName))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(runner))
		for _, forbidden := range append(slices.Clone(names), "examples/") {
			if strings.Contains(text, strings.ToLower(forbidden)) {
				t.Errorf("generic runner %s contains case-specific token %q", runnerName, forbidden)
			}
		}
	}

	examplesRoot := filepath.Join(root, "testdata", "mnemond", "examples")
	err = filepath.WalkDir(examplesRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode()&0o111 != 0 {
				t.Errorf("example is executable: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertNoFixturePathsInProduction(t *testing.T, root string) {
	t.Helper()
	forEachProductionGoFile(t, root, func(path string, file *ast.File) {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			for _, forbidden := range []string{
				"testdata/mnemond/examples", "testdata/mnemond/cases", "testdata/mnemond/domainops",
			} {
				if strings.Contains(filepath.ToSlash(value), forbidden) {
					t.Errorf("%s refers to fixture path %q", path, value)
				}
			}
			return true
		})
	})
}

func forEachComponentGoFile(t *testing.T, root, component string, visit func(string, *ast.File)) {
	t.Helper()
	walkGoFiles(t, filepath.Join(root, filepath.FromSlash(component)), visit)
}

// forEachPackageGoFile visits one exact Go package directory. Unlike a product
// component scan, it must not merge child packages into their parent when
// enforcing the promoted import graph.
func forEachPackageGoFile(t *testing.T, root, packagePath string, visit func(string, *ast.File)) {
	t.Helper()
	base := filepath.Join(root, filepath.FromSlash(packagePath))
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("scan package %s: %v", base, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(base, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		visit(path, file)
	}
}

func forEachProductionGoFile(t *testing.T, root string, visit func(string, *ast.File)) {
	t.Helper()
	for _, base := range []string{filepath.Join(root, "internal"), filepath.Join(root, "cmd")} {
		walkGoFiles(t, base, visit)
	}
}

func walkGoFiles(t *testing.T, base string, visit func(string, *ast.File)) {
	t.Helper()
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		visit(path, file)
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", base, err)
	}
}

func assertSingleArchitectureMatch(t *testing.T, matches []string, directory, want, label string) {
	t.Helper()
	if len(matches) != 1 || !strings.Contains(matches[0], directory) ||
		!strings.HasSuffix(matches[0], "::"+want) {
		t.Fatalf("%s = %v, want one %s in %s", label, matches, want, directory)
	}
}
