package architecture_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReleaseRepositoryHygiene(t *testing.T) {
	root := repositoryRoot(t)
	var violations []string
	for _, trackedPath := range gitTrackedFiles(t, root) {
		if reason := forbiddenTrackedPathReason(trackedPath); reason != "" {
			violations = append(violations, trackedPath+": "+reason)
			continue
		}
		if !strings.EqualFold(path.Ext(trackedPath), ".json") {
			continue
		}
		raw, err := gitTrackedBlob(root, trackedPath)
		if err != nil {
			violations = append(violations, trackedPath+": read tracked JSON: "+err.Error())
			continue
		}
		if reason := trackedJSONViolation(trackedPath, raw); reason != "" {
			violations = append(violations, trackedPath+": "+reason)
		}
	}
	if len(violations) != 0 {
		slices.Sort(violations)
		t.Fatalf("tracked repository hygiene violations:\n%s",
			strings.Join(violations, "\n"))
	}

	for _, probe := range []string{
		".testdata/repository-hygiene-probe",
		".mnemon-dev/repository-hygiene-probe",
	} {
		assertIgnoredByRootGitignore(t, root, probe)
	}
}

func TestRepositoryHygieneRulesRejectGeneratedFiles(t *testing.T) {
	pathTests := []struct {
		path, want string
	}{
		{".testdata/mnemond/runs/example/report.json", ".testdata"},
		{".mnemon-dev/tmp/example/summary.json", ".mnemon-dev"},
		{"release/evidence/example/report.json", "run evidence"},
		{"release/logs/mnemond.log", "run log"},
		{"release/transcript/turn.json", "transcript"},
		{"release/credentials/codex-auth.json", "credential"},
		{"workspace/.codex/config.toml", "local Host configuration"},
		{"release/codex-auth.json", "credential"},
	}
	for _, test := range pathTests {
		if got := forbiddenTrackedPathReason(test.path); !strings.Contains(got, test.want) {
			t.Errorf("forbiddenTrackedPathReason(%q) = %q, want %q",
				test.path, got, test.want)
		}
	}

	jsonTests := []struct {
		path, raw, want string
	}{
		{"scratch/result.json", `{}`, "durable JSON category"},
		{"testdata/mnemond/cases/example/tmp.json", `{}`, "temporary JSON name"},
		{"internal/memory/setup/assets/fixtures/report-copy.json",
			`{"schema_version":1,"run_id":"run","status":"passed","git_sha":"abc",` +
				`"scenario":"example","commands":[],"assertions":[]}`,
			"run report"},
		{"internal/memory/setup/assets/fixtures/manifest-copy.json",
			`{"schema_version":1,"run_id":"run","files":[]}`,
			"run evidence manifest"},
		{"internal/memory/setup/assets/fixtures/command.json",
			`{"sequence":1,"node":"A","kind":"setup","started_unix_ms":1,` +
				`"finished_unix_ms":2,"exit_code":0,"evidence":[]}`,
			"run transcript"},
	}
	for _, test := range jsonTests {
		if got := trackedJSONViolation(test.path, []byte(test.raw)); !strings.Contains(got, test.want) {
			t.Errorf("trackedJSONViolation(%q) = %q, want %q",
				test.path, got, test.want)
		}
	}
}

func TestRepositoryHygieneRulesAcceptDurableJSONCategories(t *testing.T) {
	for _, trackedPath := range []string{
		"package.json",
		"npm/cli/package.json",
		"npm/cli/targets.json",
		"internal/memory/setup/assets/openclaw/plugin/openclaw.plugin.json",
		"internal/memory/setup/assets/openclaw/plugin/package.json",
	} {
		if reason := forbiddenTrackedPathReason(trackedPath); reason != "" {
			t.Errorf("%s: %s", trackedPath, reason)
		}
		if reason := trackedJSONViolation(trackedPath, []byte(`{}`)); reason != "" {
			t.Errorf("%s: %s", trackedPath, reason)
		}
	}
}

func gitTrackedFiles(t *testing.T, root string) []string {
	t.Helper()
	command := exec.Command("git", "-C", root, "ls-files", "-z", "--")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	entries := bytes.Split(output, []byte{0})
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if len(entry) != 0 {
			files = append(files, string(entry))
		}
	}
	return files
}

func gitTrackedBlob(root, trackedPath string) ([]byte, error) {
	command := exec.Command("git", "-C", root, "cat-file", "blob", ":"+trackedPath)
	return command.Output()
}

func assertIgnoredByRootGitignore(t *testing.T, root, probe string) {
	t.Helper()
	command := exec.Command("git", "-C", root, "check-ignore", "-v",
		"--no-index", "--", probe)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("%s is not ignored by the root .gitignore: %v", probe, err)
	}
	metadata, _, ok := strings.Cut(strings.TrimSpace(string(output)), "\t")
	if !ok {
		t.Fatalf("git check-ignore returned malformed output for %s: %q", probe, output)
	}
	source, _, ok := strings.Cut(metadata, ":")
	if !ok || filepath.ToSlash(source) != ".gitignore" {
		t.Fatalf("%s is ignored by %q, want the root .gitignore", probe, source)
	}
}

func forbiddenTrackedPathReason(trackedPath string) string {
	if reason := forbiddenTrackedDirectoryReason(trackedPath); reason != "" {
		return reason
	}
	return forbiddenTrackedFilenameReason(trackedPath)
}

func forbiddenTrackedDirectoryReason(trackedPath string) string {
	for _, component := range strings.Split(strings.ToLower(trackedPath), "/") {
		switch component {
		case ".testdata":
			return "generated .testdata must remain local"
		case ".mnemon-dev":
			return "ignored .mnemon-dev state must remain local"
		case "evidence", "run-evidence", "run_evidence", "runs":
			return "generated run evidence directory"
		case "log", "logs":
			return "generated run log directory"
		case "transcript", "transcripts":
			return "generated run transcript directory"
		case "credential", "credentials", "provider-credentials":
			return "provider credential directory"
		case ".agents", ".claude", ".codex", ".insight", ".kanna",
			".mnemon", ".openclaw", ".supervisor":
			return "local Host configuration directory"
		}
	}
	return ""
}

func forbiddenTrackedFilenameReason(trackedPath string) string {
	base := strings.ToLower(path.Base(trackedPath))
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env.local"):
		return "local credential environment file"
	case base == ".plan":
		return "local Host configuration file"
	case base == "auth.json" || base == "codex-auth.json" ||
		base == "credential.json" || base == "credentials.json" ||
		strings.HasSuffix(base, ".token"):
		return "provider credential file"
	case strings.HasSuffix(base, ".log") || strings.HasSuffix(base, ".stderr") ||
		strings.HasSuffix(base, ".stdout"):
		return "generated run log"
	case strings.HasSuffix(base, ".jsonl") || strings.HasSuffix(base, ".ndjson"):
		return "generated run transcript stream"
	default:
		return ""
	}
}

func trackedJSONViolation(trackedPath string, raw []byte) string {
	if reason := temporaryJSONNameReason(trackedPath); reason != "" {
		return reason
	}
	if durableJSONCategory(trackedPath) == "" {
		return "path is outside a closed durable JSON category"
	}
	if shape := generatedJSONShape(raw); shape != "" {
		return "contains a " + shape + " shape"
	}
	return ""
}

func temporaryJSONNameReason(trackedPath string) string {
	base := strings.ToLower(path.Base(trackedPath))
	switch base {
	case "report.json", "run-report.json", "run_report.json",
		"suite-report.json", "summary.json":
		return "uses a generated run-report name"
	case "transcript.json":
		return "uses a generated transcript name"
	case "manifest.json":
		return "uses a generated manifest name"
	}
	stem := strings.TrimSuffix(base, ".json")
	if stem == "tmp" || stem == "temp" || stem == "temporary" || stem == "scratch" ||
		strings.HasPrefix(stem, "tmp-") || strings.HasPrefix(stem, "temp-") ||
		strings.HasPrefix(stem, "temporary-") || strings.HasPrefix(stem, "scratch-") {
		return "uses a temporary JSON name"
	}
	for _, suffix := range []string{"-tmp", "_tmp", ".tmp", "-temp", "_temp", ".temp"} {
		if strings.HasSuffix(stem, suffix) {
			return "uses a temporary JSON name"
		}
	}
	return ""
}

func durableJSONCategory(trackedPath string) string {
	switch {
	case trackedPath == "package.json":
		return "DSH package manifest"
	case trackedPath == "npm/cli/package.json" || trackedPath == "npm/cli/targets.json":
		return "npm CLI manifest"
	case strings.HasPrefix(trackedPath, "internal/memory/setup/assets/"):
		return "managed asset"
	default:
		return ""
	}
}

func generatedJSONShape(raw []byte) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return ""
	}
	switch {
	case hasJSONKeys(object, "schema_version", "run_id", "files"):
		return "run evidence manifest"
	case hasJSONKeys(object, "schema_version", "run_id", "status", "git_sha",
		"scenario", "commands", "assertions"):
		return "run report"
	case hasJSONKeys(object, "schema_version", "run_id", "status", "git_sha",
		"bundle_kind", "cases", "generated_at"):
		return "suite run report"
	case hasJSONKeys(object, "sequence", "node", "kind", "started_unix_ms",
		"finished_unix_ms", "exit_code", "evidence"):
		return "run transcript"
	case hasJSONKeys(object, "run_id"):
		return "run-bound JSON"
	default:
		return ""
	}
}

func hasJSONKeys(object map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}
