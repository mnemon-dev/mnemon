package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRootComposesMemoryAndAgency(t *testing.T) {
	root := productRoot()
	for _, name := range []string{"remember", "recall", "setup", "agency", "update"} {
		child, _, err := root.Find([]string{name})
		if err != nil || child == root {
			t.Fatalf("root command %q is not registered", name)
		}
	}
	command, _, err := root.Find([]string{"agency", "peer", "prepare"})
	if err != nil || command.CommandPath() != "mnemon agency peer prepare" {
		t.Fatalf("Agency subtree is not composed into the product root: %v", err)
	}
}

func TestUnmanagedUpdateExplainsTheOneTimeNPMMigration(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Execute(context.Background(), []string{"update"}, strings.NewReader(""),
		&stdout, &stderr)
	if exitCode != 1 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "npm install --global @mnemon-dev/mnemon@latest") ||
		strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("unmanaged update: exit=%d stdout=%q stderr=%q",
			exitCode, stdout.String(), stderr.String())
	}
}

func TestExecuteRoutesAgencyWithoutChangingItsExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Execute(context.Background(), []string{"agency", "--version"},
		strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 || stdout.String() != "mnemon agency version dev\n" || stderr.Len() != 0 {
		t.Fatalf("agency version: exit=%d stdout=%q stderr=%q",
			exitCode, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = Execute(context.Background(), []string{"agency", "unknown"},
		strings.NewReader(""), &stdout, &stderr)
	if exitCode != 2 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "unknown command \"unknown\"") {
		t.Fatalf("agency rejection: exit=%d stdout=%q stderr=%q",
			exitCode, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = Execute(context.Background(), []string{"--data-dir", t.TempDir(), "agency", "unknown"},
		strings.NewReader(""), &stdout, &stderr)
	if exitCode != 2 || strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("Agency after a product flag: exit=%d stdout=%q stderr=%q",
			exitCode, stdout.String(), stderr.String())
	}
}

func TestMemoryKeepsItsExistingCobraErrorOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Execute(context.Background(), []string{"forget"},
		strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "Error:") ||
		!strings.Contains(stderr.String(), "Usage:") ||
		!strings.Contains(stderr.String(), "\n\naccepts 1 arg(s), received 0\n") {
		t.Fatalf("memory usage error: exit=%d stdout=%q stderr=%q",
			exitCode, stdout.String(), stderr.String())
	}
}

func TestMemoryHelpRemainsSuccessfulStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Execute(context.Background(), []string{"forget", "--help"},
		strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 || !strings.Contains(stdout.String(), "mnemon forget [id]") ||
		stderr.Len() != 0 {
		t.Fatalf("memory help: exit=%d stdout=%q stderr=%q",
			exitCode, stdout.String(), stderr.String())
	}
}

func TestUnknownRootCommandKeepsTheShortCobraDiagnostic(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Execute(context.Background(), []string{"unknown"},
		strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "Run 'mnemon --help' for usage.") ||
		strings.Contains(stderr.String(), "Usage:\n") {
		t.Fatalf("unknown command: exit=%d stdout=%q stderr=%q",
			exitCode, stdout.String(), stderr.String())
	}
}
