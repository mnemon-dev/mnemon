package aml

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// searchAnswer is a /v1/search answer, its result and stages left raw.
type searchAnswer struct {
	Store    string            `json:"store"`
	Mode     string            `json:"mode"`
	Result   json.RawMessage   `json:"result"`
	Degraded []string          `json:"degraded"`
	Trace    []json.RawMessage `json:"trace"`
	Usage    usageAnswer       `json:"usage"`
	Error    string            `json:"error"`
}

type usageAnswer struct {
	Calls  int `json:"calls"`
	Tokens struct {
		Input  int `json:"input"`
		Output int `json:"output"`
	} `json:"tokens"`
	Cost map[string]float64 `json:"cost"`
}

// stageHeadAnswer is the fields every stage has.
type stageHeadAnswer struct {
	Name  string  `json:"name"`
	MS    float64 `json:"ms"`
	Calls int     `json:"calls"`
	usageAnswer
}

type searchJSON struct {
	Search int       `json:"search"`
	Text   string    `json:"text"`
	Kind   string    `json:"kind"`
	Source string    `json:"source"`
	Ranked int       `json:"ranked"`
	IDs    []string  `json:"ids"`
	Scores []float64 `json:"scores"`
}

type blockJSON struct {
	Position int      `json:"position"`
	ID       string   `json:"id"`
	Layer    string   `json:"layer"`
	Items    []string `json:"items"`
	Records  []string `json:"records"`
	Excerpt  string   `json:"excerpt"`
}

// stage decodes the trace's stage named name into out; it fails the test
// when there is none.
func stage(t *testing.T, answer searchAnswer, name string, out any) {
	t.Helper()
	for _, raw := range answer.Trace {
		var head stageHeadAnswer
		if err := json.Unmarshal(raw, &head); err != nil {
			t.Fatal(err)
		}
		if head.Name == name {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatal(err, string(raw))
			}
			return
		}
	}
	t.Fatalf("no stage %s in %d stages", name, len(answer.Trace))
}

func stageNames(t *testing.T, answer searchAnswer) []string {
	t.Helper()
	names := []string{}
	for _, raw := range answer.Trace {
		var head stageHeadAnswer
		json.Unmarshal(raw, &head)
		names = append(names, head.Name)
	}
	return names
}

// search asks the inspect service for a search of body and decodes its
// answer, which must be 200.
func (ih *inspectHarness) search(t *testing.T, body map[string]any) searchAnswer {
	t.Helper()
	var answer searchAnswer
	ih.ok(t, "POST", "/v1/search", body, &answer)
	return answer
}

// rawData is the bytes of /search's data array.
func rawData(t *testing.T, h *harness, req SearchRequest) json.RawMessage {
	t.Helper()
	response := h.call("POST", "/search", req)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

// talk stores n Adds of different content for user, one session each.
func talk(t *testing.T, h *harness, user string, n int) {
	t.Helper()
	topics := []string{"the LGBTQ support group", "a painting of a sunrise", "adopting a dog named Biscuit", "a camping trip to the lake", "the support group's picnic", "a book about parenting"}
	for i := range n {
		h.mustAdd(t, AddRequest{RequestID: fmt.Sprintf("r%d", i), UserID: user, SessionID: fmt.Sprintf("s%d", i), Messages: []Message{
			{Role: "user", Content: fmt.Sprintf("Caroline here: yesterday I talked about %s, and it was great.", topics[i%len(topics)]), Timestamp: at(i * 60)},
			{Role: "assistant", Content: "That sounds lovely. Tell me more about it.", Timestamp: at(i*60 + 1)},
		}})
	}
}

// Lexical mode is the raw ranking aml-serve answers a degraded Search with:
// its result is byte-identical to that Search's data, whether aml-serve holds
// the store open (read live) or not (read quiescent).
func TestInspectLexicalEqualsRawRanking(t *testing.T) {
	h := layeredHarness(t)
	talk(t, h, "u1", 6)
	ih := newInspector(t, h, nil)
	h.judge.fail.Store(true) // every memory read fails: /search answers the raw ranking
	cases := []SearchRequest{
		{Query: "Caroline support group", UserID: "u1", TopK: 100},
		{Query: "Caroline support group", UserID: "u1", TopK: 2},
		{Query: "dog named Biscuit", UserID: "u1", TopK: 5},
		{Query: "When did Caroline go camping?", UserID: "u1", TopK: 3, Options: []string{"May", "June"}},
		{Query: "nothing matches this", UserID: "u1", TopK: 10},
	}
	want := []json.RawMessage{}
	for _, c := range cases {
		degraded := h.server.DegradedSearches()
		want = append(want, rawData(t, h, c))
		if h.server.DegradedSearches() != degraded+1 {
			t.Fatal("the Search was not degraded")
		}
	}
	check := func(state string) {
		for i, c := range cases {
			answer := ih.search(t, map[string]any{"user_id": c.UserID, "query": c.Query, "options": c.Options, "top_k": c.TopK})
			if !bytes.Equal(answer.Result, want[i]) {
				t.Fatalf("%s, %q top_k %d:\ninspect %s\nsearch  %s", state, c.Query, c.TopK, answer.Result, want[i])
			}
			if answer.Mode != "lexical" || answer.Store != userKey("u1")[:8] || answer.Usage.Calls != 0 || len(answer.Degraded) != 0 {
				t.Fatalf("%+v", answer)
			}
		}
	}
	if items := h.search(t, cases[0]); len(items) < 4 {
		t.Fatal("the raw ranking is too short to compare", items)
	}
	check("live")
	h.server.Close() // no writer holds the store now
	if _, err := os.Stat(filepath.Join(h.root, "users", userKey("u1"), "records.db-wal")); !os.IsNotExist(err) {
		t.Fatal("the store is not quiescent", err)
	}
	check("quiescent")
}

// A lexical trace: what is pending, the raw ranking's candidates with their
// words scores, and the result item by item.
func TestInspectLexicalTrace(t *testing.T) {
	h := layeredHarness(t)
	talk(t, h, "u1", 5)
	ih := newInspector(t, h, nil)
	answer := ih.search(t, map[string]any{"key": userKey("u1"), "query": "Caroline support group", "top_k": 3})
	if names := stageNames(t, answer); !slices.Equal(names, []string{"pending", "candidates", "compose"}) {
		t.Fatal(names)
	}
	var pending struct {
		Count     int      `json:"count"`
		IDs       []string `json:"ids"`
		Watermark int64    `json:"watermark"`
		Folded    *bool    `json:"folded"`
	}
	stage(t, answer, "pending", &pending)
	var memory memoryAnswer
	ih.ok(t, "GET", "/v1/stores/"+userKey("u1")+"/memory", nil, &memory)
	if pending.Count != memory.Pending.Count || !slices.Equal(pending.IDs, memory.Pending.IDs) || len(pending.IDs) != pending.Count ||
		pending.Watermark != *memory.Watermark || pending.Watermark == 0 || pending.Folded == nil || *pending.Folded {
		t.Fatalf("%+v %+v", pending, memory)
	}
	var candidates struct {
		stageHeadAnswer
		Searches []searchJSON      `json:"searches"`
		Pool     []string          `json:"pool"`
		Layer    []json.RawMessage `json:"layer"`
	}
	stage(t, answer, "candidates", &candidates)
	if len(candidates.Searches) != 1 || candidates.Pool != nil || candidates.Layer != nil || candidates.Calls != 0 {
		t.Fatalf("%+v", candidates)
	}
	s := candidates.Searches[0]
	if s.Search != 0 || s.Text != "Caroline support group" || s.Kind != "query" || s.Source != "record" || len(s.IDs) != 3 || s.Ranked < 3 || len(s.Scores) != 3 {
		t.Fatalf("%+v", s)
	}
	for i, score := range s.Scores {
		if score <= 0 || i > 0 && score > s.Scores[i-1] {
			t.Fatalf("scores %v do not follow the ranking", s.Scores)
		}
	}
	var compose struct {
		Blocks    []blockJSON `json:"blocks"`
		Truncated bool        `json:"truncated"`
		Order     string      `json:"order"`
	}
	stage(t, answer, "compose", &compose)
	var result []SearchItem
	json.Unmarshal(answer.Result, &result)
	if len(compose.Blocks) != len(result) || compose.Order != "time" {
		t.Fatalf("%+v", compose)
	}
	for i, block := range compose.Blocks {
		if block.Position != i || block.ID != result[i].ID || block.Layer != "records" || !slices.Equal(block.Items, []string{block.ID}) ||
			!slices.Equal(block.Records, []string{block.ID}) || !slices.Contains(s.IDs, block.ID) || !strings.HasPrefix(result[i].Content, strings.TrimSuffix(block.Excerpt, "…")) {
			t.Fatalf("%d: %+v", i, block)
		}
	}
	// trace false: no trace.
	var quiet map[string]json.RawMessage
	ih.ok(t, "POST", "/v1/search", map[string]any{"key": userKey("u1"), "query": "Caroline", "trace": false}, &quiet)
	if string(quiet["trace"]) != "null" || quiet["result"] == nil {
		t.Fatal(quiet)
	}
}

func TestInspectSearchRefusals(t *testing.T) {
	h := layeredHarness(t)
	h.mustAdd(t, addRequest("r1", "u1"))
	ih := newInspector(t, h, nil)
	key := userKey("u1")
	for _, c := range []struct {
		body  string
		field string
	}{
		{`{"query":"x"}`, "key"},
		{`{"key":"` + key + `","user_id":"u1","query":"x"}`, "key"},
		{`{"key":"NOT-A-KEY","query":"x"}`, "key"},
		{`{"key":7,"query":"x"}`, "key"},
		{`{"user_id":" ","query":"x"}`, "user_id"},
		{`{"key":"` + key + `"}`, "query"},
		{`{"key":"` + key + `","query":"  "}`, "query"},
		{`{"key":"` + key + `","query":"` + strings.Repeat("q", 12001) + `"}`, "query"},
		{`{"key":"` + key + `","query":"x","options":"May"}`, "options"},
		{`{"key":"` + key + `","query":"x","options":[{}]}`, "options"},
		{`{"key":"` + key + `","query":"x","top_k":0}`, "top_k"},
		{`{"key":"` + key + `","query":"x","top_k":101}`, "top_k"},
		{`{"key":"` + key + `","query":"x","top_k":2.5}`, "top_k"},
		{`{"key":"` + key + `","query":"x","mode":"deep"}`, "mode"},
		{`{"key":"` + key + `","query":"x","trace":"yes"}`, "trace"},
	} {
		ih.refused(t, "POST", "/v1/search", c.body, http.StatusBadRequest, "invalid", c.field)
	}
	ih.refused(t, "POST", "/v1/search", map[string]any{"user_id": "nobody", "query": "x"}, http.StatusNotFound, "store_not_found", "")
	// Full mode is refused while no cap is set, before anything is read.
	plain := newInspector(t, h, func(config *InspectConfig) { config.DebugCaps = nil })
	plain.refused(t, "POST", "/v1/search", map[string]any{"key": key, "query": "x", "mode": "full"}, http.StatusConflict, "full_disabled", "")
	if h.planner.seen() != nil {
		t.Fatal("a refused search called the planner")
	}
}

// fileStates is every file under root: its size, time and contents.
func fileStates(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		out[strings.TrimPrefix(path, root)] = fmt.Sprintf("%d %s %s", info.Size(), info.ModTime().Format(time.RFC3339Nano), hex.EncodeToString(sum[:8]))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// lockDown takes every write permission away from root's tree, as systemd's
// ReadOnlyPaths= does, until the test ends.
func lockDown(t *testing.T, root string) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("POSIX permissions, which root ignores")
	}
	var dirs []string
	filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil {
			if entry.IsDir() {
				dirs = append(dirs, path)
			} else {
				os.Chmod(path, 0o444)
			}
		}
		return nil
	})
	for _, dir := range slices.Backward(dirs) {
		os.Chmod(dir, 0o555)
	}
	t.Cleanup(func() {
		for _, dir := range dirs {
			os.Chmod(dir, 0o755)
		}
	})
}

// fakeInstance is a data root of two users, consolidated on Add in timelines
// mode with one record of each left pending, that no aml-serve holds; and an
// inspect service over it whose full searches call the fake provider. The
// instance's own budget file exists, as on a host.
type fakeInstance struct {
	h              *harness
	ih             *inspectHarness
	instanceBudget string
	debugBudget    string
}

func newFakeInstance(t *testing.T, edit func(*InspectConfig)) *fakeInstance {
	t.Helper()
	base := startFakeProvider(t)
	h := layeredHarness(t)
	talk(t, h, "u1", 5)
	talk(t, h, "u2", 3)
	h.server.Close()
	f := &fakeInstance{h: h, instanceBudget: filepath.Join(t.TempDir(), "model-budget.db"), debugBudget: filepath.Join(t.TempDir(), "debug", "debug-budget.db")}
	budget, err := memoryagent.OpenBudgetWithCaps(f.instanceBudget, map[string]float64{"deepseek": 600, "jev": 60})
	if err != nil {
		t.Fatal(err)
	}
	budget.Close()
	config := h.server.config.MemoryConfig
	fakeProviderModels(t, &config, base)
	config.BudgetFile = f.instanceBudget
	f.ih = newInspector(t, h, func(c *InspectConfig) {
		c.MemoryConfig = config
		c.DebugBudgetFile = f.debugBudget
		realModels(c)
		if edit != nil {
			edit(c)
		}
	})
	return f
}

// A full search runs the read path against the fake provider and reports
// its stages in order, each with its time, calls, tokens and cost; it bills
// the debug budget and leaves the instance's budget file as it was.
func TestInspectFullTrace(t *testing.T) {
	f := newFakeInstance(t, nil)
	instance := fileStates(t, filepath.Dir(f.instanceBudget))
	ih := f.ih
	answer := ih.search(t, map[string]any{"user_id": "u1", "query": "When did Caroline go to the support group?", "top_k": 20, "mode": "full"})
	if names := stageNames(t, answer); !slices.Equal(names, []string{"pending", "cues", "candidates", "judge", "need", "compose"}) {
		t.Fatal(names)
	}
	var result []SearchItem
	if err := json.Unmarshal(answer.Result, &result); err != nil || len(result) == 0 || answer.Mode != "full" {
		t.Fatal(string(answer.Result), err)
	}
	shown := map[string]bool{}
	for i, item := range result {
		shown[item.ID] = true
		if item.Score != 1/float64(i+1) {
			t.Fatal(i, item)
		}
	}
	// Every stage's head; their calls and costs add up to the usage.
	calls, cost := 0, map[string]float64{}
	for _, raw := range answer.Trace {
		var head stageHeadAnswer
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &head)
		json.Unmarshal(raw, &fields)
		for _, field := range []string{"name", "ms", "calls", "tokens", "cost"} {
			if fields[field] == nil {
				t.Fatalf("stage %s lacks %s", head.Name, field)
			}
		}
		if head.MS < 0 || len(head.Cost) != 2 {
			t.Fatalf("%+v", head)
		}
		calls += head.Calls
		for kind, amount := range head.Cost {
			cost[kind] += amount
		}
	}
	if answer.Usage.Calls != calls || answer.Usage.Calls == 0 || answer.Usage.Tokens.Input == 0 || answer.Usage.Cost["deepseek"] <= 0 || answer.Usage.Cost["jev"] <= 0 ||
		!almost(cost["deepseek"], answer.Usage.Cost["deepseek"]) || !almost(cost["jev"], answer.Usage.Cost["jev"]) {
		t.Fatalf("%+v %v %v", answer.Usage, calls, cost)
	}

	var cues struct {
		stageHeadAnswer
		Cues []struct {
			Text string `json:"text"`
			Kind string `json:"kind"`
		} `json:"cues"`
		Needs []struct {
			Text string `json:"text"`
			All  bool   `json:"all"`
		} `json:"needs"`
		Needless *bool `json:"needless"`
	}
	stage(t, answer, "cues", &cues)
	if len(cues.Cues) == 0 || cues.Cues[0].Kind != "message" || len(cues.Needs) == 0 || cues.Needless == nil || cues.Calls != 2 || cues.Cost["deepseek"] <= 0 || cues.Cost["jev"] != 0 {
		t.Fatalf("%+v", cues)
	}
	var candidates struct {
		stageHeadAnswer
		Searches []searchJSON `json:"searches"`
		Pool     []string     `json:"pool"`
		Layer    []struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Source string `json:"source"`
			Lit    *int   `json:"lit"`
			Near   *int   `json:"near"`
		} `json:"layer"`
	}
	stage(t, answer, "candidates", &candidates)
	if len(candidates.Searches) < len(cues.Cues) || len(candidates.Pool) == 0 || candidates.Layer == nil || candidates.Calls != 0 {
		t.Fatalf("%+v", candidates)
	}
	for i, s := range candidates.Searches {
		if s.Search != i || s.Text != cues.Cues[i].Text || s.Kind != cues.Cues[i].Kind || s.Source != "record" || len(s.Scores) != len(s.IDs) || len(s.IDs) > 20 {
			t.Fatalf("%+v", s)
		}
		for j, score := range s.Scores {
			if score <= 0 || j > 0 && score > s.Scores[j-1] {
				t.Fatalf("search %q: scores %v", s.Text, s.Scores)
			}
		}
	}
	for _, item := range candidates.Layer {
		if item.Source != "layer" || item.Kind != "topic" && item.Kind != "value" || item.Lit == nil {
			t.Fatalf("%+v", item)
		}
	}
	var judge struct {
		stageHeadAnswer
		Records []struct {
			ID       string   `json:"id"`
			Source   string   `json:"source"`
			Excerpt  string   `json:"excerpt"`
			Screen   *float64 `json:"screen"`
			Needed   float64  `json:"needed"`
			Judged   bool     `json:"judged"`
			Decision string   `json:"decision"`
			Shown    bool     `json:"shown"`
		} `json:"records"`
		Layer  []json.RawMessage `json:"layer"`
		Linked []json.RawMessage `json:"linked"`
	}
	stage(t, answer, "judge", &judge)
	if len(judge.Records) == 0 || judge.Calls == 0 || judge.Cost["jev"] <= 0 || judge.Layer == nil || judge.Linked == nil {
		t.Fatalf("%+v", judge)
	}
	for _, record := range judge.Records {
		if record.Source != "record" || !slices.Contains([]string{"needed", "kept", "dropped", "superseded"}, record.Decision) || record.Shown != shown[record.ID] ||
			len([]rune(record.Excerpt)) > 200 || record.Decision == "kept" && !record.Shown || record.Screen == nil {
			t.Fatalf("%+v", record)
		}
	}
	var need struct {
		Rounds []json.RawMessage `json:"rounds"`
		States []string          `json:"states"`
		Stop   string            `json:"stop"`
	}
	stage(t, answer, "need", &need)
	if need.Rounds == nil || len(need.States) != len(cues.Needs) || !slices.Contains([]string{"covered", "given up", "rounds"}, need.Stop) {
		t.Fatalf("%+v", need)
	}
	var compose struct {
		Blocks []blockJSON `json:"blocks"`
		Order  string      `json:"order"`
	}
	stage(t, answer, "compose", &compose)
	if len(compose.Blocks) != len(result) || compose.Order != "time" || len(candidates.Layer) == 0 {
		t.Fatalf("%+v", compose)
	}
	if !slices.ContainsFunc(compose.Blocks, func(b blockJSON) bool { return b.Layer != "records" && len(b.Items) > 0 }) {
		t.Fatal("the consolidated layer shows no block", compose.Blocks)
	}
	for i, block := range compose.Blocks {
		if block.Position != i || block.ID != result[i].ID || !slices.Contains([]string{"instructions", "topics", "timelines", "facts", "insights", "records"}, block.Layer) {
			t.Fatalf("%+v", block)
		}
		if block.Layer == "records" && (!slices.Equal(block.Items, []string{block.ID}) || strings.HasPrefix(block.ID, "index:")) {
			t.Fatalf("%+v", block)
		}
		for _, record := range block.Records {
			if !shown[record] {
				t.Fatalf("block %d links %s, which the result does not show", i, record)
			}
		}
	}

	// The debug budget paid; the process counts the search.
	var info infoAnswer
	ih.ok(t, "GET", "/v1/info", nil, &info)
	if info.Debug.Spent["deepseek"] <= 0 || info.Debug.Spent["jev"] <= 0 || info.Debug.Remaining["deepseek"] >= 5 || info.Recent.FullSearches != 1 ||
		!almost(info.Recent.AvgCost["deepseek"], answer.Usage.Cost["deepseek"]) || !almost(info.Recent.AvgCost["jev"], answer.Usage.Cost["jev"]) {
		t.Fatalf("%+v", info)
	}
	if after := fileStates(t, filepath.Dir(f.instanceBudget)); !sameMap(instance, after) {
		t.Fatalf("the instance's budget file changed:\n%v\n%v", instance, after)
	}

	// Full mode is refused without caps, and the service is refused when its
	// debug budget file is the instance's.
	capless := newInspector(t, f.h, func(c *InspectConfig) {
		c.MemoryConfig = f.ih.inspector.config.MemoryConfig
		c.DebugCaps = nil
		realModels(c)
	})
	capless.ok(t, "GET", "/v1/info", nil, &info)
	if info.Modes["full"] {
		t.Fatal("full mode without caps")
	}
	capless.refused(t, "POST", "/v1/search", map[string]any{"user_id": "u1", "query": "Caroline", "mode": "full"}, http.StatusConflict, "full_disabled", "")
	config := f.ih.inspector.config
	config.DebugBudgetFile, config.Logger = f.instanceBudget, nil
	if inspector, err := NewInspector(config); err == nil || !strings.Contains(err.Error(), "must differ from the configuration's budgetFile") {
		if inspector != nil {
			inspector.Close()
		}
		t.Fatal("an inspect service spending the instance's budget:", err)
	}
	if after := fileStates(t, filepath.Dir(f.instanceBudget)); !sameMap(instance, after) {
		t.Fatal("a refused service touched the instance's budget file")
	}
}

func almost(a, b float64) bool { return a-b < 1e-12 && b-a < 1e-12 }

// A full search the debug budget cannot pay for answers 402, with what it
// did; one whose read fails otherwise answers 502 with the raw ranking.
func TestInspectFullFailures(t *testing.T) {
	poor := newFakeInstance(t, func(c *InspectConfig) { c.DebugCaps = map[string]float64{"deepseek": 0.1, "jev": 1} })
	response := poor.ih.refused(t, "POST", "/v1/search", map[string]any{"user_id": "u1", "query": "Caroline", "mode": "full"}, http.StatusPaymentRequired, "debug_budget_exhausted", "")
	var spent searchAnswer
	json.Unmarshal(response.Body.Bytes(), &spent)
	if spent.Result != nil || spent.Usage.Cost == nil {
		t.Fatal(response.Body.String())
	}

	f := newFakeInstance(t, nil)
	// JEV fails every call: the read fails at its screen.
	config := f.ih.inspector.config.MemoryConfig
	config.Judge = &memoryagent.ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "INSPECT_TEST_JEV", TestEndpoint: "http://127.0.0.1:1/v1/systemone", MaxAttempts: 1}
	broken := newInspector(t, f.h, func(c *InspectConfig) {
		c.MemoryConfig = config
		c.DebugBudgetFile = filepath.Join(t.TempDir(), "debug-budget.db")
		realModels(c)
	})
	response = broken.refused(t, "POST", "/v1/search", map[string]any{"user_id": "u1", "query": "Caroline support group", "top_k": 4, "mode": "full"}, http.StatusBadGateway, "provider_failed", "")
	var failed searchAnswer
	json.Unmarshal(response.Body.Bytes(), &failed)
	lexical := broken.search(t, map[string]any{"user_id": "u1", "query": "Caroline support group", "top_k": 4})
	if !bytes.Equal(failed.Result, lexical.Result) || len(failed.Trace) == 0 || failed.Usage.Calls == 0 {
		t.Fatal(response.Body.String())
	}
	if names := stageNames(t, failed); names[0] != "pending" || !slices.Contains(names, "cues") {
		t.Fatal(names)
	}
}

// Every route, lexical and full searches included, over a data root the
// service may not write: everything answers, and no file of the data root
// or of the instance's budget changes, none is created, nothing
// checkpoints.
func TestInspectNeverWrites(t *testing.T) {
	f := newFakeInstance(t, nil)
	lockDown(t, f.h.root)
	lockDown(t, filepath.Dir(f.instanceBudget))
	before, budgetBefore := fileStates(t, f.h.root), fileStates(t, filepath.Dir(f.instanceBudget))
	ih := f.ih
	ih.ok(t, "GET", "/v1/info", nil, nil)
	ih.ok(t, "GET", "/v1/stores", nil, nil)
	for _, user := range []string{"u1", "u2"} {
		key := userKey(user)
		ih.ok(t, "POST", "/v1/resolve", map[string]any{"user_id": user}, nil)
		ih.ok(t, "GET", "/v1/stores/"+key+"/records", nil, nil)
		for _, section := range []string{"threads", "events", "facts", "directives"} {
			ih.ok(t, "GET", "/v1/stores/"+key+"/memory?section="+section, nil, nil)
		}
		ih.search(t, map[string]any{"key": key, "query": "Caroline support group"})
		ih.search(t, map[string]any{"key": key, "query": "When did Caroline adopt the dog?", "mode": "full"})
	}
	after := fileStates(t, f.h.root)
	for path := range after {
		if strings.HasSuffix(path, "-wal") || strings.HasSuffix(path, "-shm") || strings.HasSuffix(path, "-journal") {
			t.Errorf("the service made %s", path)
		}
	}
	if !sameMap(before, after) {
		t.Fatalf("the data root changed:\nbefore %v\nafter  %v", before, after)
	}
	if !sameMap(budgetBefore, fileStates(t, filepath.Dir(f.instanceBudget))) {
		t.Fatal("the instance's budget changed")
	}
}

// Full searches skip the flush a Search makes: the pending records stay
// pending, the trace names them, and the consolidation state is as it was.
func TestInspectNoFlush(t *testing.T) {
	h := layeredHarness(t)
	for _, id := range []string{"r1", "r2", "r3"} {
		h.mustAdd(t, addRequest(id, "u1")) // r1 and r2 fold; r3 is the tail
	}
	ih := newInspector(t, h, nil)
	key := userKey("u1")
	index := filepath.Join(h.root, "users", key, "memory-index.db")
	stateBefore, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	folds := h.consolidator.calls.Load()
	var records recordsAnswer
	ih.ok(t, "GET", "/v1/stores/"+key+"/records", nil, &records)
	tail := records.Records[len(records.Records)-1].ID

	answer := ih.search(t, map[string]any{"key": key, "query": question, "mode": "full"})
	var pending struct {
		Count     int      `json:"count"`
		IDs       []string `json:"ids"`
		Watermark int64    `json:"watermark"`
		Folded    bool     `json:"folded"`
	}
	stage(t, answer, "pending", &pending)
	if pending.Count != 1 || !slices.Equal(pending.IDs, []string{tail}) || pending.Folded || pending.Watermark == 0 {
		t.Fatalf("%+v", pending)
	}
	var memory memoryAnswer
	ih.ok(t, "GET", "/v1/stores/"+key+"/memory", nil, &memory)
	if memory.Pending.Count != 1 || !slices.Equal(memory.Pending.IDs, []string{tail}) || *memory.Watermark != pending.Watermark {
		t.Fatalf("%+v", memory)
	}
	stateAfter, err := os.ReadFile(index)
	if err != nil || !bytes.Equal(stateBefore, stateAfter) || h.consolidator.calls.Load() != folds {
		t.Fatal("the full search folded", h.consolidator.calls.Load(), folds, err)
	}
	// aml-serve's Search folds the tail first: there was something to fold.
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 10})
	if h.consolidator.calls.Load() != folds+1 {
		t.Fatal("the Search did not fold the tail")
	}
}

// openStores counts the stores the service holds open now.
func (ih *inspectHarness) openStores() int64 { return ih.inspector.open.Load() }

// A live aml-serve keeps adding to the store the service reads: every read
// answers, holds its store only while it reads, and no Add waits for one —
// not even while a full search is stopped in the middle of its read.
func TestInspectReadsWhileServeWrites(t *testing.T) {
	h := layeredHarness(t)
	talk(t, h, "u1", 3)
	ih := newInspector(t, h, nil)
	key := userKey("u1")
	ctx, stop := context.WithCancel(context.Background())
	var adds atomic.Int64
	var work sync.WaitGroup
	work.Add(1)
	go func() {
		defer work.Done()
		for i := 0; ctx.Err() == nil; i++ {
			if response := h.call("POST", "/add", AddRequest{RequestID: fmt.Sprintf("live-%d", i), UserID: "u1", SessionID: fmt.Sprintf("live-%d", i), Messages: []Message{
				{Role: "user", Content: fmt.Sprintf("Caroline here again, note %d about the support group.", i)},
			}}); response.Code != http.StatusOK {
				t.Errorf("Add %d: %d %s", i, response.Code, response.Body.String())
				return
			}
			adds.Add(1)
		}
	}()
	defer func() {
		stop()
		work.Wait()
	}()
	for round := 0; round < 15; round++ {
		ih.ok(t, "GET", "/v1/stores", nil, nil)
		ih.ok(t, "GET", "/v1/stores/"+key+"/records?limit=200", nil, nil)
		ih.ok(t, "GET", "/v1/stores/"+key+"/memory?section=events", nil, nil)
		ih.search(t, map[string]any{"key": key, "query": "Caroline support group"})
		if round%5 == 0 {
			ih.search(t, map[string]any{"key": key, "query": question, "mode": "full"})
		}
		if n := ih.openStores(); n != 0 {
			t.Fatalf("the service holds %d stores between requests", n)
		}
	}
	if adds.Load() == 0 {
		t.Fatal("no Add went through")
	}

	// A full search waits for its judge in the middle of its read: it holds
	// its store open, and the Adds go on all the same.
	h.judge.block.Store(true)
	leave, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ih.callContext(t, leave, "POST", "/v1/search", map[string]any{"key": key, "query": question, "mode": "full"})
	}()
	eventually(t, func() bool { return h.judge.blocked.Load() > 0 && ih.openStores() == 1 })
	from := adds.Load()
	eventually(t, func() bool { return adds.Load() >= from+5 })
	cancel()
	<-done
	h.judge.block.Store(false)
	if n := ih.openStores(); n != 0 {
		t.Fatal("the search left its store open", n)
	}
}
