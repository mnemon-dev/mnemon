package architecture_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/mnemon-dev/mnemon"

func TestReleaseBoundary(t *testing.T) {
	root := repositoryRoot(t)
	t.Run("all retained Go packages belong to the root module", func(t *testing.T) {
		assertSingleModuleImportLaw(t, root)
	})
	t.Run("the release has one mnemon executable with formal command namespaces", func(t *testing.T) {
		assertFormalCommands(t, root)
	})
	t.Run("retired command and Harness topology is absent", func(t *testing.T) {
		assertRetiredHarnessAbsent(t, root)
	})
	t.Run("command help preserves Memory Agency and MCP separation", func(t *testing.T) {
		assertCommandHelpSeparation(t, root)
	})
}

func assertSingleModuleImportLaw(t *testing.T, root string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contents, []byte("module "+modulePath+"\n")) {
		t.Fatalf("root go.mod does not declare %s", modulePath)
	}
	assertOnlyRootGoModule(t, root)

	command := exec.Command("go", "list", "-f", "{{.ImportPath}}", "./...")
	command.Dir = root
	command.Env = withoutGoWork(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("list root packages: %v\n%s", err, output)
	}
	for _, packagePath := range strings.Fields(string(output)) {
		if packagePath != modulePath && !strings.HasPrefix(packagePath, modulePath+"/") {
			t.Errorf("root package has foreign import path %q", packagePath)
		}
		if packagePath == modulePath+"/harness" || strings.HasPrefix(packagePath, modulePath+"/harness/") {
			t.Errorf("retained package still belongs to the Harness module: %q", packagePath)
		}
	}

	for _, base := range []string{"cmd", "internal", "test", "testdata"} {
		assertImportsUseRootModule(t, filepath.Join(root, base))
	}
}

func assertOnlyRootGoModule(t *testing.T, root string) {
	t.Helper()
	var modules []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "dist") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && entry.Name() == "go.mod" {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			modules = append(modules, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan module manifests: %v", err)
	}
	if !slices.Equal(modules, []string{"go.mod"}) {
		t.Fatalf("Go module manifests = %v, want only root go.mod", modules)
	}
}

func assertImportsUseRootModule(t *testing.T, base string) {
	t.Helper()
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if importPath == modulePath+"/harness" || strings.HasPrefix(importPath, modulePath+"/harness/") {
				t.Errorf("%s imports retired Harness path %q", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan imports under %s: %v", base, err)
	}
}

func assertFormalCommands(t *testing.T, root string) {
	t.Helper()
	assertDirectoryNames(t, filepath.Join(root, "cmd"), []string{"agency", "mcp", "memory"})
	assertRootCommandDelegatesToCmd(t, root)
	for target, want := range map[string]string{
		".":            "main",
		"./cmd":        "cmd",
		"./cmd/agency": "agency",
		"./cmd/mcp":    "mcp",
		"./cmd/memory": "memory",
	} {
		command := exec.Command("go", "list", "-f", "{{.Name}}", target)
		command.Dir = root
		command.Env = withoutGoWork(os.Environ())
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("list %s: %v\n%s", target, err, output)
		}
		if got := strings.TrimSpace(string(output)); got != want {
			t.Errorf("%s package = %q, want %q", target, got, want)
		}
	}
}

func assertRootCommandDelegatesToCmd(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "main.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse root command: %v", err)
	}
	importsCmd := false
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err == nil && importPath == modulePath+"/cmd" {
			importsCmd = true
		}
	}
	if !importsCmd {
		t.Fatal("root main must import the product cmd package")
	}
	callsExecute := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Execute" {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if ok && identifier.Name == "cmd" {
			callsExecute = true
		}
		return true
	})
	if !callsExecute {
		t.Fatal("root main must delegate execution to cmd.Execute")
	}
}

func assertRetiredHarnessAbsent(t *testing.T, root string) {
	t.Helper()
	for _, path := range []string{
		"harness", "cmd/mnemon-harness", "cmd/mnemon", "cmd/mnemond",
		"internal/mnemoncli", "internal/cli",
	} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Errorf("retired path still exists: %s", path)
		}
	}
}

func assertCommandHelpSeparation(t *testing.T, root string) {
	t.Helper()
	mnemon := commandHelp(t, root)
	wantMnemon := []string{
		"agency", "completion", "embed", "forget", "gc", "help", "import", "link", "log",
		"mcp", "recall", "receipt", "related", "remember", "search", "setup", "show", "status",
		"store", "viz",
	}
	if got := cobraTopLevelCommands(mnemon); !slices.Equal(got, wantMnemon) {
		t.Errorf("mnemon top-level commands = %v, want %v", got, wantMnemon)
	}

	agency := commandHelp(t, root, "agency")
	if got, want := cobraTopLevelCommands(agency), []string{"peer", "serve", "setup"}; !slices.Equal(got, want) {
		t.Errorf("mnemon agency top-level commands = %v, want %v", got, want)
	}

	mcp := commandHelp(t, root, "mcp")
	if got, want := cobraTopLevelCommands(mcp), []string{"serve"}; !slices.Equal(got, want) {
		t.Errorf("mnemon mcp top-level commands = %v, want %v", got, want)
	}
}

func cobraTopLevelCommands(help []byte) []string {
	lines := strings.Split(string(help), "\n")
	inCommands := false
	var commands []string
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case "Available Commands:":
			inCommands = true
			continue
		case "Flags:":
			inCommands = false
		}
		if !inCommands || !strings.HasPrefix(line, "  ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 0 {
			commands = append(commands, fields[0])
		}
	}
	return commands
}

func commandHelp(t *testing.T, root string, args ...string) []byte {
	t.Helper()
	commandArgs := append([]string{"run", "."}, args...)
	commandArgs = append(commandArgs, "--help")
	command := exec.Command("go", commandArgs...)
	command.Dir = root
	command.Env = withoutGoWork(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("mnemon %s help: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}

func withoutGoWork(environment []string) []string {
	result := slices.DeleteFunc(slices.Clone(environment),
		func(value string) bool { return strings.HasPrefix(value, "GOWORK=") })
	return append(result, "GOWORK=off")
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	for dir := filepath.Dir(source); ; dir = filepath.Dir(dir) {
		contents, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && bytes.Contains(contents, []byte("module "+modulePath+"\n")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repository root not found from %s", source)
		}
	}
}

func assertDirectoryNames(t *testing.T, path string, want []string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var got []string
	for _, entry := range entries {
		if entry.IsDir() {
			got = append(got, entry.Name())
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("directories under %s = %v, want %v", path, got, want)
	}
}
