package memory

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/graph"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

const (
	zebraWest  = "the deploy target for project zebra is staging-west"
	zebraRunb  = "zebra deploy runbook: promote the build to staging-west first"
	zebraBread = "zebra bread recipe: deploy the oven at 220C for a crisp crust"
)

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return string(data)
}

// startJudgeServer fakes System One. noul questions score a memory 0.05 when
// its content mentions bread and 0.9 otherwise; choice questions answer
// "supersedes" at 0.87. The returned counter is the number of requests served.
func startJudgeServer(t *testing.T, status int) (*atomic.Int32, *[]map[string]any) {
	t.Helper()
	var hits atomic.Int32
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var req struct {
			State     map[string]any            `json:"state"`
			Questions map[string]map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
			return
		}
		bodies = append(bodies, map[string]any{"state": req.State, "questions": req.Questions})
		if status != http.StatusOK {
			http.Error(w, `{"error":"model unavailable"}`, status)
			return
		}
		memories, _ := req.State["memories"].(map[string]any)
		answers := map[string]any{}
		for name, q := range req.Questions {
			if q["type"] == "choice" {
				answers[name] = map[string]any{"type": "choice", "choice": "supersedes", "confidence": 0.87,
					"probabilities": map[string]float64{"supersedes": 0.87, "related": 0.13}}
				continue
			}
			score := 0.9
			if content, _ := memories[name].(string); strings.Contains(content, "bread") {
				score = 0.05
			}
			answers[name] = map[string]any{"type": "noul", "noul": score}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "test", "answers": answers})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MNEMON_JUDGE_MODEL", "test-model")
	t.Setenv("MNEMON_JUDGE_ENDPOINT", srv.URL)
	t.Setenv("MNEMON_JUDGE_API_KEY", "")
	t.Setenv("MNEMON_JUDGE_TIMEOUT", "")
	return &hits, &bodies
}

func configureJudgedRecall(t *testing.T) {
	t.Helper()
	oldDir, oldStore, oldReadOnly := dataDir, storeName, readOnly
	oldBasic, oldBrief, oldVerbose, oldLimit := recBasic, recBrief, recVerbose, recLimit
	oldCategory, oldSource, oldIntent, oldExcerpt := recCategory, recSource, recIntent, recExcerpt
	oldJudge, oldJudgeMin := recJudge, recJudgeMin
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDir, oldStore, oldReadOnly
		recBasic, recBrief, recVerbose, recLimit = oldBasic, oldBrief, oldVerbose, oldLimit
		recCategory, recSource, recIntent, recExcerpt = oldCategory, oldSource, oldIntent, oldExcerpt
		recJudge, recJudgeMin = oldJudge, oldJudgeMin
	})
	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("MNEMON_EMBED_PROTOCOL", "ollama")
	dataDir, storeName, readOnly = t.TempDir(), "judge-recall", true
	recBasic, recBrief, recVerbose, recLimit = false, false, false, 10
	recCategory, recSource, recIntent, recExcerpt = "", "", "GENERAL", 240
	recJudge, recJudgeMin = false, 0.5

	db, err := store.Open(store.StoreDir(dataDir, storeName))
	if err != nil {
		t.Fatal(err)
	}
	insertTestInsight(t, db, "west", zebraWest, "test", "2026-01-01T00:00:00Z")
	insertTestInsight(t, db, "runb", zebraRunb, "test", "2026-01-02T00:00:00Z")
	insertTestInsight(t, db, "bread", zebraBread, "test", "2026-01-03T00:00:00Z")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func runRecall(t *testing.T) (stdout, stderr string) {
	t.Helper()
	var runErr error
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			runErr = recallCmd.RunE(recallCmd, []string{"where does project zebra deploy"})
		})
	})
	if runErr != nil {
		t.Fatalf("recall: %v", runErr)
	}
	return stdout, stderr
}

type judgedRow struct {
	ID        string `json:"id"`
	Insight   struct{ ID string }
	Relevance *float64 `json:"relevance"`
}

func recallIDs(t *testing.T, out string, verbose bool) (map[string]*float64, int) {
	t.Helper()
	var resp struct {
		Results []judgedRow `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode recall output: %v\n%s", err, out)
	}
	ids := map[string]*float64{}
	for _, r := range resp.Results {
		id := r.ID
		if verbose {
			id = r.Insight.ID
		}
		ids[id] = r.Relevance
	}
	return ids, len(resp.Results)
}

func TestRecallJudgeAddsRelevanceAndDropsIrrelevant(t *testing.T) {
	for _, mode := range []string{"compact", "brief", "verbose"} {
		t.Run(mode, func(t *testing.T) {
			// Given a store where bread is retrievable but unrelated
			configureJudgedRecall(t)
			recBrief, recVerbose = mode == "brief", mode == "verbose"
			baseline, _ := runRecall(t)
			before, _ := recallIDs(t, baseline, mode == "verbose")
			if _, ok := before["bread"]; !ok || len(before) != 3 {
				t.Fatalf("unjudged recall should return all three memories: %s", baseline)
			}
			if strings.Contains(baseline, "relevance") {
				t.Fatalf("unjudged output must not mention relevance: %s", baseline)
			}

			// When recall runs with the judge
			hits, bodies := startJudgeServer(t, http.StatusOK)
			recJudge = true
			out, stderr := runRecall(t)

			// Then the bread memory is dropped, the rest carry relevance, and one request was sent
			after, _ := recallIDs(t, out, mode == "verbose")
			if _, ok := after["bread"]; ok || len(after) != 2 {
				t.Fatalf("judged recall = %v, want west and runb only\n%s", after, out)
			}
			for id, rel := range after {
				if rel == nil || *rel != 0.9 {
					t.Fatalf("relevance for %s = %v, want 0.9", id, rel)
				}
			}
			if hits.Load() != 1 || stderr != "" {
				t.Fatalf("requests = %d, stderr = %q", hits.Load(), stderr)
			}
			state := (*bodies)[0]["state"].(map[string]any)
			if state["query"] != "where does project zebra deploy" {
				t.Fatalf("state query = %v", state["query"])
			}
		})
	}
}

func TestRecallJudgeMinRaisesTheBar(t *testing.T) {
	// Given a judge that scores relevant memories 0.9
	configureJudgedRecall(t)
	startJudgeServer(t, http.StatusOK)
	recJudge, recJudgeMin = true, 0.95

	// When the minimum is above that score
	out, _ := runRecall(t)

	// Then nothing survives
	if _, n := recallIDs(t, out, false); n != 0 {
		t.Fatalf("results = %d, want 0\n%s", n, out)
	}
}

func TestRecallJudgeFailsOpenOnServerError(t *testing.T) {
	// Given the unjudged output and a judge server that errors
	configureJudgedRecall(t)
	baseline, _ := runRecall(t)
	startJudgeServer(t, http.StatusInternalServerError)
	recJudge = true

	// When recall runs with --judge
	out, stderr := runRecall(t)

	// Then the output is unchanged and one warning line is printed
	if out != baseline {
		t.Fatalf("fail-open output differs:\n%s\nvs\n%s", out, baseline)
	}
	if !strings.HasPrefix(stderr, "warning: judge unavailable") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("stderr = %q, want a single warning line", stderr)
	}
}

func TestRecallJudgeWithoutConfigurationWarns(t *testing.T) {
	// Given no judge model
	configureJudgedRecall(t)
	baseline, _ := runRecall(t)
	t.Setenv("MNEMON_JUDGE_MODEL", "")
	recJudge = true

	// When recall runs with --judge
	out, stderr := runRecall(t)

	// Then the output is unchanged and the warning names the variable
	if out != baseline {
		t.Fatalf("unconfigured output differs from baseline")
	}
	if !strings.Contains(stderr, "MNEMON_JUDGE_MODEL") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestRecallJudgeMinOutOfRangeIsRejected(t *testing.T) {
	// Given --judge with an impossible threshold
	configureJudgedRecall(t)
	recJudge, recJudgeMin = true, 1.5

	// When recall runs
	err := recallCmd.RunE(recallCmd, []string{"anything"})

	// Then it is rejected before any work
	if err == nil || !strings.Contains(err.Error(), "--judge-min") {
		t.Fatalf("err = %v", err)
	}
}

func configureJudgedRemember(t *testing.T) {
	t.Helper()
	configureRememberTest(t)
	oldJudge := remJudge
	t.Cleanup(func() { remJudge = oldJudge })
	t.Setenv("MNEMON_MAX_INSIGHTS", "1000")
	remJudge = true

	db, err := store.Open(store.StoreDir(dataDir, store.DefaultStoreName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insertTestInsight(t, db, "west", zebraWest, "test", "2026-01-01T00:00:00Z")
}

func runRemember(t *testing.T) (stdout, stderr string) {
	t.Helper()
	var runErr error
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			runErr = rememberCmd.RunE(rememberCmd, []string{"the deploy target for project zebra moved to staging-east"})
		})
	})
	if runErr != nil {
		t.Fatalf("remember: %v", runErr)
	}
	return stdout, stderr
}

type rememberOutput struct {
	Candidates []struct {
		ID         string `json:"id"`
		AutoLinked bool   `json:"auto_linked"`
		Judgment   *struct {
			Relation    string  `json:"relation"`
			Probability float64 `json:"probability"`
		} `json:"judgment"`
	} `json:"semantic_candidates"`
}

func TestRememberJudgeAnnotatesCandidatesWithoutCreatingEdges(t *testing.T) {
	// Given an existing memory and a judge that calls the update a supersession
	configureJudgedRemember(t)
	hits, bodies := startJudgeServer(t, http.StatusOK)

	// When the updated memory is remembered with --judge
	out, stderr := runRemember(t)

	// Then the candidate carries the judgment, and no supersedes edge was written
	var result rememberOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].ID != "west" {
		t.Fatalf("candidates = %+v\n%s", result.Candidates, out)
	}
	j := result.Candidates[0].Judgment
	if j == nil || j.Relation != "supersedes" || j.Probability != 0.87 {
		t.Fatalf("judgment = %+v", j)
	}
	if hits.Load() != 1 || stderr != "" {
		t.Fatalf("requests = %d, stderr = %q", hits.Load(), stderr)
	}
	q := (*bodies)[0]["questions"].(map[string]map[string]any)["c0"]
	if q["type"] != "choice" || len(q["criteria"].(map[string]any)) != 5 {
		t.Fatalf("question = %v", q)
	}

	db, err := store.Open(store.StoreDir(dataDir, store.DefaultStoreName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	edges, err := db.GetAllEdges()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if e.EdgeType == model.EdgeSupersedes {
			t.Fatalf("remember --judge created a supersedes edge: %+v", e)
		}
	}
}

func TestRememberJudgeLeavesOutputUnchangedOnFailure(t *testing.T) {
	// Given a judge server that errors
	configureJudgedRemember(t)
	startJudgeServer(t, http.StatusInternalServerError)

	// When remember runs with --judge
	out, stderr := runRemember(t)

	// Then candidates have no judgment and a single warning is printed
	var result rememberOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Judgment != nil || strings.Contains(out, `"judgment"`) {
		t.Fatalf("output was altered by a failed judge: %s", out)
	}
	if !strings.HasPrefix(stderr, "warning: judge unavailable") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestJudgeCandidatesIncludesAutoLinked(t *testing.T) {
	// Given a near-identical auto-linked candidate and a review-zone candidate
	hits, bodies := startJudgeServer(t, http.StatusOK)
	candidates := []graph.SemanticCandidate{
		{ID: "west", Content: zebraWest, Similarity: 0.91, AutoLinked: true},
		{ID: "runbook", Content: zebraRunb, Similarity: 0.62},
	}

	// When the new memory's candidates are judged
	judged, ok := judgeCandidates("the deploy target for project zebra moved to staging-east", candidates)

	// Then both carry a judgment: the auto-linked one is the likeliest supersession
	if !ok || len(judged) != 2 {
		t.Fatalf("judged = %+v, ok = %v", judged, ok)
	}
	for _, c := range judged {
		if c.Judgment == nil || c.Judgment.Relation != "supersedes" {
			t.Fatalf("candidate %s judgment = %+v", c.ID, c.Judgment)
		}
	}
	if hits.Load() != 1 || len((*bodies)[0]["questions"].(map[string]map[string]any)) != 2 {
		t.Fatalf("requests = %d, bodies = %v", hits.Load(), *bodies)
	}
}

func TestRememberWithoutJudgeNeverCallsIt(t *testing.T) {
	// Given a configured judge server but no --judge flag
	configureJudgedRemember(t)
	remJudge = false
	hits, _ := startJudgeServer(t, http.StatusOK)

	// When remember runs
	out, _ := runRemember(t)

	// Then the server is untouched and the output has no judgment
	if hits.Load() != 0 || strings.Contains(out, `"judgment"`) {
		t.Fatalf("requests = %d, output = %s", hits.Load(), out)
	}
}
