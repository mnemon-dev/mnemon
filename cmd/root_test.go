package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

// aml-serve and aml-watch are run by systemd, so their standard error is the
// journal: a failure is printed once and bare, as update's is, with no usage
// block and no cobra "Error:" line (the other Memory commands keep both: see
// TestMemoryKeepsItsExistingCobraErrorOutput). aml-watch is meant to fail
// routinely, every minute while its webhook is down.
func TestAMLWatchFailureIsPrintedOnceThroughTheProductRoot(t *testing.T) {
	// Loopback servers only: a webhook that fails, and an admin listener that
	// is not ready, which is one alert to say to it.
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(hook.Close)
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"ready":false,"reasons":["breaker open: jev"]}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(admin.Close)
	const token = "s3cr3t-t0k3n-r00t"
	t.Setenv("AML_ALERT_WEBHOOK", hook.URL+"/hook/"+token)
	t.Setenv("AML_ALERT_KIND", "feishu")
	state := filepath.Join(t.TempDir(), "watch-state.json")
	// The command tree is process-global and a flag keeps its value from one
	// Execute to the next: give --help its default back, so that this test can
	// run again (-count) and the tests after it do not print help.
	t.Cleanup(func() {
		if command, _, err := productRoot().Find([]string{"memory-agent", "aml-watch"}); err == nil {
			command.Flags().Set("help", "false")
		}
	})

	// A delivery failure: the watcher's own error, once.
	var stdout, stderr bytes.Buffer
	exitCode := Execute(context.Background(),
		[]string{"memory-agent", "aml-watch", "--admin", admin.URL, "--public-url", "", "--state", state},
		strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 || stdout.Len() != 0 {
		t.Fatalf("failed delivery: exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if got := strings.Count(stderr.String(), "alert delivery failed"); got != 1 {
		t.Fatalf("the error is printed %d times, not once:\n%s", got, stderr.String())
	}
	for _, unwanted := range []string{"Usage:", "Flags:", "Error:", token, hook.Listener.Addr().String()} {
		if strings.Contains(stderr.String(), unwanted) {
			t.Fatalf("stderr shows %q:\n%s", unwanted, stderr.String())
		}
	}

	// A refused setting: the same, and still once.
	stdout.Reset()
	stderr.Reset()
	exitCode = Execute(context.Background(),
		[]string{"memory-agent", "aml-watch", "--admin", admin.URL, "--public-url", "", "--state", "relative.json"},
		strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 || stdout.Len() != 0 || strings.Count(stderr.String(), "--state must be an absolute path") != 1 ||
		strings.Contains(stderr.String(), "Usage:") || strings.Contains(stderr.String(), "Error:") {
		t.Fatalf("refused setting: exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}

	// A flag the command does not have: quiet too.
	stdout.Reset()
	stderr.Reset()
	exitCode = Execute(context.Background(), []string{"memory-agent", "aml-watch", "--no-such-flag"},
		strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 || strings.Count(stderr.String(), "unknown flag: --no-such-flag") != 1 || strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("unknown flag: exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}

	// Its help is still help: on standard output, exit 0.
	stdout.Reset()
	stderr.Reset()
	exitCode = Execute(context.Background(), []string{"memory-agent", "aml-watch", "--help"},
		strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 || !strings.Contains(stdout.String(), "--public-url") || stderr.Len() != 0 {
		t.Fatalf("help: exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
}

func TestAMLServeFailureIsPrintedOnceThroughTheProductRoot(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Execute(context.Background(),
		[]string{"memory-agent", "aml-serve", "--data-root", "relative", "--memory-config", "relative.json"},
		strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 || stdout.Len() != 0 || strings.Count(stderr.String(), "aml-serve requires an absolute --data-root") != 1 ||
		strings.Contains(stderr.String(), "Usage:") || strings.Contains(stderr.String(), "Error:") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
}
