package memoryagent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEdgeKindsConfig(t *testing.T) {
	s, _ := agentStore(t)
	models := Models{Planner: &limitPlanner{}, Judge: decisionFunc(fixtureJudge)}
	for _, mode := range []string{"timelines", "layered"} {
		kinds := []string{"entity", "semantic", "temporal", "causal"}
		a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: mode, NativeEdges: true, EdgeKinds: kinds}}, models, nil)
		if err != nil || !reflect.DeepEqual(a.config.Consolidation.EdgeKinds, kinds) {
			t.Fatal(mode, err)
		}
	}
	for _, bad := range []struct {
		config ConsolidationConfig
		error  string
	}{
		{ConsolidationConfig{Mode: "layered", NativeEdges: true, EdgeKinds: []string{"similar"}}, `unknown edge kind "similar"`},
		{ConsolidationConfig{Mode: "layered", NativeEdges: true, EdgeKinds: []string{"Semantic"}}, `unknown edge kind "Semantic"`},
		{ConsolidationConfig{Mode: "layered", NativeEdges: true, EdgeKinds: []string{"semantic", "causal", "semantic"}}, `edge kind "semantic" is listed twice`},
		{ConsolidationConfig{Mode: "layered", EdgeKinds: []string{"semantic"}}, "edgeKinds requires nativeEdges"},
		{ConsolidationConfig{Mode: "timelines", EdgeKinds: []string{"entity"}}, "edgeKinds requires nativeEdges"},
		{ConsolidationConfig{EdgeKinds: []string{"entity"}}, `require mode "timelines" or "layered"`},
	} {
		if _, err := New(s, Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &bad.config}, models, nil); err == nil || !strings.Contains(err.Error(), bad.error) {
			t.Fatalf("%+v: %v", bad.config, err)
		}
	}
	// The edges are JEV's decisions: timelines mode needs a decision model
	// for them too.
	timelines := Config{Version: 1, Policy: &ReadPolicy{}, Consolidation: &ConsolidationConfig{Mode: "timelines", NativeEdges: true, EdgeKinds: []string{"temporal"}}}
	if _, err := New(s, timelines, Models{Planner: &limitPlanner{}}, nil); err == nil || !strings.Contains(err.Error(), "edgeKinds requires a decision model") {
		t.Fatal(err)
	}
	timelines.Consolidation.EdgeKinds = nil
	if _, err := New(s, timelines, Models{Planner: &limitPlanner{}}, nil); err != nil {
		t.Fatal(err)
	}
	// Empty, the field keeps the stage-2 bytes (and policy).
	raw, _ := json.Marshal(ConsolidationConfig{Mode: "layered", NativeEdges: true, EdgeKinds: []string{}})
	if string(raw) != `{"mode":"layered","nativeEdges":true}` {
		t.Fatal(string(raw))
	}
	raw, _ = json.Marshal(ConsolidationConfig{Mode: "layered", NativeEdges: true, EdgeKinds: []string{"semantic", "causal"}})
	if string(raw) != `{"mode":"layered","nativeEdges":true,"edgeKinds":["semantic","causal"]}` {
		t.Fatal(string(raw))
	}
	var config Config
	if err := json.Unmarshal([]byte(`{"version":1,"policy":{},"consolidation":{"mode":"layered","nativeEdges":true,"edgeKinds":[]}}`), &config); err != nil {
		t.Fatal(err)
	}
	a, err := New(s, config, models, nil)
	if err != nil || a.config.Consolidation.EdgeKinds != nil {
		t.Fatal(err, a.config.Consolidation.EdgeKinds)
	}
}
