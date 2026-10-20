package memoryagent

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// hashAnswer is a probability that depends on what it is about only.
func hashAnswer(parts ...string) float64 {
	h := fnv.New64a()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return float64(h.Sum64()%1000) / 1000
}

// layeredEndpoint is an OpenAI-compatible endpoint for the planner, the
// judge and the consolidator of layered reads and jobs over topic notes:
// L0 extracts the notes (layeredReply), L2 concludes about their items
// (insightReply), decisions are answered by what they ask about, and the
// planner plans the kayak's status.
func layeredEndpoint(t *testing.T) *completionServer {
	t.Helper()
	server := newCompletionServer(t, "")
	server.respond(func(body string) (int, string) {
		var request struct {
			Messages       []struct{ Content string }
			ResponseFormat any `json:"response_format"`
		}
		if json.Unmarshal([]byte(body), &request) != nil || len(request.Messages) != 2 {
			return http.StatusBadRequest, "{}"
		}
		system, prompt := request.Messages[0].Content, request.Messages[1].Content
		reply := "NEED: the kayak's status"
		switch {
		case request.ResponseFormat != nil:
			var decision struct {
				State     json.RawMessage
				Questions map[string]Question
			}
			json.Unmarshal([]byte(prompt), &decision)
			answers := map[string]float64{}
			for key, q := range decision.Questions {
				answers[key] = hashAnswer(key[:1], q.Instructions, string(decision.State))
			}
			raw, _ := json.Marshal(answers)
			reply = string(raw)
		case system == layeredSystem:
			reply = layeredReply(prompt)
		case system == insightSystem:
			reply = insightReply(prompt)
		case strings.HasPrefix(system, "You plan searches in the notes"):
			reply = "kayak status\nNOTE: the kayak is booked"
		}
		return http.StatusOK, completionBody(reply)
	})
	return server
}

// The evaluation cache replays model calls by their exact request bytes:
// with insights, consolidation jobs and reads send every request they send
// without them, byte for byte (each answered from the cache), and only the
// L2 calls, the L2 linking calls and the reads' insight call are new.
func TestInsightsAddOnlyTheirOwnModelRequests(t *testing.T) {
	server := layeredEndpoint(t)
	t.Setenv("UNIT_OPENAI_CREDENTIAL", "test-only-placeholder")
	model := &ModelConfig{Kind: "openai", Model: "test-model", BaseURL: server.URL + "/v1", KeyEnv: "UNIT_OPENAI_CREDENTIAL", PriceIn: 1, PriceOut: 2}
	budgetFile, cacheFile := filepath.Join(t.TempDir(), "budget.db"), filepath.Join(t.TempDir(), "model-cache.db")
	questions := []string{"What is the kayak's status?", "When were the taxes and the garden updated?"}
	// run consolidates a fresh store of the same notes as the AML adapter
	// does (Adds' jobs with hold, then the flush), then reads, each step a
	// process of its own over the shared cache.
	run := func(insights bool) ([]View, []ConsolidateResult) {
		s, dir := agentStore(t)
		seedTopics(t, s, 8)
		config := Config{Version: 1, Strategy: "simple", Planner: model, Judge: model, Consolidator: model, BudgetFile: budgetFile, ModelCache: cacheFile,
			View: &ViewConfig{MaxItems: 8}, Consolidation: &ConsolidationConfig{Mode: "layered", BatchCharacters: 300, Insights: insights}}
		open := func() *Agent {
			a, err := Open(s, dir, config, false)
			if err != nil {
				t.Fatal(err)
			}
			return a
		}
		results := []ConsolidateResult{}
		for _, hold := range []bool{true, false} {
			for range 20 {
				a := open()
				result, err := a.Consolidate(context.Background(), ConsolidateRequest{Hold: hold})
				a.Close()
				if err != nil {
					t.Fatal(err)
				}
				results = append(results, result)
				if hold && result.Folded == 0 || !hold && result.Remaining == 0 {
					break
				}
			}
		}
		views := []View{}
		for _, question := range questions {
			a := open()
			view, err := a.Read(context.Background(), ReadRequest{Question: question})
			a.Close()
			if err != nil {
				t.Fatal(err)
			}
			views = append(views, view)
		}
		return views, results
	}
	offViews, offResults := run(false)
	sent := len(server.received())
	if sent == 0 || offResults[0].Folded == 0 {
		t.Fatal(sent, offResults)
	}
	onViews, onResults := run(true)
	l2, linking, reading := 0, 0, 0
	for _, body := range server.received()[sent:] {
		var request struct{ Messages []struct{ Content string } }
		json.Unmarshal([]byte(body), &request)
		system, prompt := request.Messages[0].Content, request.Messages[1].Content
		switch {
		case system == insightSystem:
			l2++
		case strings.Contains(prompt, "Does the new insight whose `id` is"):
			linking++
		case strings.Contains(prompt, `"source":"insight"`):
			reading++
		default:
			t.Fatalf("a request that insights should not change was sent again:\n%s", prompt)
		}
	}
	insightBatches, layer := 0, 0
	for _, result := range onResults {
		insightBatches += result.InsightBatches
	}
	for _, view := range onViews {
		layer += view.Stats.LayerInsights
		if !strings.HasPrefix(view.Index[len(view.Index)-1].ID, "5-insights") {
			t.Fatal(blockIDs(view.Index))
		}
	}
	if l2 == 0 || l2 != insightBatches || linking == 0 || reading != len(questions) || layer == 0 {
		t.Fatal(l2, insightBatches, linking, reading, layer)
	}
	// The records the Views show are the same.
	for i := range offViews {
		if got, want := recordIDs(onViews[i].Records), recordIDs(offViews[i].Records); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatal(got, want)
		}
	}
}
