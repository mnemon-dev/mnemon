package records

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/retrieval"
)

func edge(id, kind string, weight float64) Link {
	return Link{ID: id, Version: 1, Type: kind, Weight: weight}
}

// similarFrom is the cosines of a hybrid base route giving these records
// these cosines; nil without any.
func similarFrom(cosines map[string]float64) *similarities {
	if cosines == nil {
		return nil
	}
	s := &similarities{at: map[string]int{}}
	for _, id := range slices.Sorted(maps.Keys(cosines)) {
		s.at[id] = len(s.cosines)
		s.cosines = append(s.cosines, scored{id, cosines[id]})
	}
	return s
}

// beamOnly runs the beam search alone and returns the records it reached
// that are not anchors, by raw path score (ties by id).
func beamOnly(t *testing.T, links map[string][]Link, intent retrieval.Intent, cosines map[string]float64, anchors ...scored) []string {
	t.Helper()
	best, err := (&derivedIndex{links: links}).traverse(context.Background(), anchors, intent, similarFrom(cosines))
	if err != nil {
		t.Fatal(err)
	}
	hits := []scored{}
	for id, score := range best {
		if !slices.ContainsFunc(anchors, func(a scored) bool { return a.id == id }) {
			hits = append(hits, scored{id, score})
		}
	}
	return rank(hits, len(hits))
}

// traverseOnly is beamOnly from anchors of equal score.
func traverseOnly(t *testing.T, links map[string][]Link, intent retrieval.Intent, cosines map[string]float64, anchors ...string) []string {
	t.Helper()
	seeds := []scored{}
	for _, id := range anchors {
		seeds = append(seeds, scored{id, 1.0 / 60})
	}
	return beamOnly(t, links, intent, cosines, seeds...)
}

func TestTraversalIntentWeights(t *testing.T) {
	// Equal link weights: the intent's edge weight decides. The ids sort the
	// other way.
	links := map[string][]Link{"a": {edge("x-semantic", "semantic", 0.5), edge("y-causal", "causal", 0.5), edge("z-temporal", "temporal", 0.5), edge("w-entity", "entity", 0.5)}}
	for _, tc := range []struct {
		intent retrieval.Intent
		want   []string
	}{
		{retrieval.IntentWhen, []string{"z-temporal", "y-causal", "w-entity", "x-semantic"}},
		{retrieval.IntentWhy, []string{"y-causal", "z-temporal", "w-entity", "x-semantic"}},
		{retrieval.IntentEntity, []string{"w-entity", "x-semantic", "y-causal", "z-temporal"}},
		{retrieval.IntentGeneral, []string{"w-entity", "x-semantic", "y-causal", "z-temporal"}},
	} {
		if got := traverseOnly(t, links, tc.intent, nil, "a"); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v", tc.intent, got)
		}
	}
	// The link weight scales the intent weight: a strong semantic link beats
	// a weak temporal one even for WHEN (0.10×1 > 0.65×0.1).
	links = map[string][]Link{"a": {edge("t", "temporal", 0.1), edge("s", "semantic", 1)}}
	if got := traverseOnly(t, links, retrieval.IntentWhen, nil, "a"); !slices.Equal(got, []string{"s", "t"}) {
		t.Error(got)
	}
}

func TestTraversalSemanticBreaksTies(t *testing.T) {
	links := map[string][]Link{"a": {edge("x", "semantic", 0.5), edge("y", "semantic", 0.5), edge("z", "semantic", 0.5)}}
	if got := traverseOnly(t, links, retrieval.IntentGeneral, nil, "a"); !slices.Equal(got, []string{"x", "y", "z"}) {
		t.Fatal(got)
	}
	// z has no cosine; a negative or NaN cosine adds nothing, as native.
	if got := traverseOnly(t, links, retrieval.IntentGeneral, map[string]float64{"x": -0.5, "y": 0.9, "w": math.NaN()}, "a"); !slices.Equal(got, []string{"y", "x", "z"}) {
		t.Fatal(got)
	}
	// Vectors are available, as native's test reads it, when the base route
	// gave some record a cosine, even a negative one; not with NaN alone.
	for _, tc := range []struct {
		s    *similarities
		want bool
	}{{nil, false}, {similarFrom(map[string]float64{}), false}, {similarFrom(map[string]float64{"a": math.NaN()}), false}, {similarFrom(map[string]float64{"a": math.NaN(), "b": -0.2}), true}} {
		if got := tc.s.available(); got != tc.want {
			t.Error(tc.s, got)
		}
	}
	// A record the cosines do not cover, or cover at another place, has none.
	s := similarFrom(map[string]float64{"a": 0.5})
	s.at["b"] = 0
	if s.of("a") != 0.5 || s.of("b") != 0 || s.of("c") != 0 {
		t.Fatal("cosine lookup")
	}
}

func TestTraversalStartsEachAnchorFromItsOwnScore(t *testing.T) {
	// A —causal→ B —causal→ n1 → … → n5, anchors A (2) and B (1): A
	// normalized 1, B 0.5. Native starts B's beam from 0.5 even after A's
	// path raised B to 1.7, so the order of the anchors does not matter.
	links := map[string][]Link{"A": {edge("B", "causal", 1)}, "B": {edge("n1", "causal", 1)}}
	for i := 1; i < 5; i++ {
		links[fmt.Sprintf("n%d", i)] = []Link{edge(fmt.Sprintf("n%d", i+1), "causal", 1)}
	}
	// WHY: 0.7 a step, five levels from each anchor.
	want := map[string]float64{"A": 1, "B": 1.7, "n1": 2.4, "n2": 3.1, "n3": 3.8, "n4": 4.5, "n5": 4.0}
	for _, anchors := range [][]scored{{{"A", 2}, {"B", 1}}, {{"B", 1}, {"A", 2}}} {
		best, err := (&derivedIndex{links: links}).traverse(context.Background(), anchors, retrieval.IntentWhy, nil)
		if err != nil || len(best) != len(want) {
			t.Fatal(best, err)
		}
		for id, score := range want {
			if math.Abs(best[id]-score) > 1e-9 {
				t.Errorf("anchors %v: %s = %v, want %v", anchors, id, best[id], score)
			}
		}
	}
}

func TestTraversalIgnoresSupersedesAndAnchors(t *testing.T) {
	links := map[string][]Link{
		"a": {edge("old", "supersedes", 1), edge("b", "causal", 1)},
		"b": {edge("c", "causal", 1)},
	}
	// b is an anchor too: it is not returned, but its neighbor is.
	if got := traverseOnly(t, links, retrieval.IntentWhy, nil, "a", "b"); !slices.Equal(got, []string{"c"}) {
		t.Fatal(got)
	}
	// Anchor scores are native's: normalized by the best anchor. A neighbor
	// of the better anchor ranks first; the ids sort the other way.
	links = map[string][]Link{"a": {edge("y", "causal", 1)}, "b": {edge("x", "causal", 1)}}
	if got := beamOnly(t, links, retrieval.IntentWhy, nil, scored{"a", 2.0 / 60}, scored{"b", 1.0 / 60}); !slices.Equal(got, []string{"y", "x"}) {
		t.Fatal(got)
	}
	// Path scores add up along the path, as native's do: a two-hop node
	// has a higher raw score than the one-hop node it was reached through
	// (the re-ranking decides the route; TestRecallReranks).
	links = map[string][]Link{"a": {edge("b", "causal", 1)}, "b": {edge("c", "causal", 1)}}
	if got := traverseOnly(t, links, retrieval.IntentWhy, nil, "a"); !slices.Equal(got, []string{"c", "b"}) {
		t.Fatal(got)
	}
	// No anchors, no route.
	if got := traverseOnly(t, links, retrieval.IntentWhy, nil); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestTraversalBudgets(t *testing.T) {
	// Depth: a causal chain a → n1 → n2 → … → n8.
	chain := map[string][]Link{"a": {edge("n1", "causal", 1)}}
	for i := 1; i < 8; i++ {
		chain[fmt.Sprintf("n%d", i)] = []Link{edge(fmt.Sprintf("n%d", i+1), "causal", 1)}
	}
	for intent, depth := range map[retrieval.Intent]int{retrieval.IntentWhy: 5, retrieval.IntentWhen: 5, retrieval.IntentEntity: 4, retrieval.IntentGeneral: 4} {
		if got := traverseOnly(t, chain, intent, nil, "a"); len(got) != depth || got[0] != fmt.Sprintf("n%d", depth) {
			t.Errorf("%s depth: %v", intent, got)
		}
	}
	// Width: thirty children of falling weight, each with one child. Only
	// the beam's best children are expanded.
	wide := map[string][]Link{}
	for i := 0; i < 30; i++ {
		child := fmt.Sprintf("c%02d", i)
		wide["a"] = append(wide["a"], edge(child, "causal", 1-float64(i)/100))
		wide[child] = []Link{edge(fmt.Sprintf("g%02d", i), "causal", 1)}
	}
	for intent, width := range map[retrieval.Intent]int{retrieval.IntentWhy: 15, retrieval.IntentWhen: 10, retrieval.IntentEntity: 10, retrieval.IntentGeneral: 10} {
		got := traverseOnly(t, wide, intent, nil, "a")
		grandchildren := slices.DeleteFunc(slices.Clone(got), func(id string) bool { return id[0] != 'g' })
		slices.Sort(grandchildren)
		if len(got) != 30+width || len(grandchildren) != width || grandchildren[width-1] != fmt.Sprintf("g%02d", width-1) {
			t.Errorf("%s width: %v", intent, got)
		}
	}
	// Visited: six hundred children; the anchor counts toward the budget.
	star := map[string][]Link{}
	for i := 0; i < 600; i++ {
		star["a"] = append(star["a"], edge(fmt.Sprintf("v%03d", i), "entity", 1))
	}
	for intent, visited := range map[retrieval.Intent]int{retrieval.IntentWhy: 500, retrieval.IntentWhen: 400, retrieval.IntentEntity: 400, retrieval.IntentGeneral: 500} {
		got := traverseOnly(t, star, intent, nil, "a")
		if len(got) != visited-1 || got[len(got)-1] != fmt.Sprintf("v%03d", visited-2) {
			t.Errorf("%s visited: %d", intent, len(got))
		}
	}
	// The candidate limit bounds the route: the re-ranked children (equal
	// signals, ids decide), the anchor (lowest graph score) last.
	got, reached, err := (&derivedIndex{links: star}).recall(context.Background(), []scored{{"a", 1}}, recallQuery{intent: retrieval.IntentGeneral, limit: 50})
	if err != nil || len(got) != 50 || got[0] != "v000" || got[49] != "v049" || reached != 50 {
		t.Error("limit", got, reached, err)
	}
	// An anchor without links: the route holds the anchor alone, and reached
	// nothing.
	got, reached, err = (&derivedIndex{}).recall(context.Background(), []scored{{"a", 1}}, recallQuery{intent: retrieval.IntentGeneral, limit: 50})
	if err != nil || !slices.Equal(got, []string{"a"}) || reached != 0 {
		t.Error("anchor only", got, reached, err)
	}
}

func TestRecallReranks(t *testing.T) {
	ctx := context.Background()
	// a —causal→ b —causal→ c. Raw path scores: c > b > a. The query's word
	// is in a and b.
	index := &derivedIndex{
		links: map[string][]Link{"a": {edge("b", "causal", 1)}, "b": {edge("c", "causal", 1)}},
		words: map[string][]string{"outage": {"a", "b"}},
	}
	if raw := traverseOnly(t, index.links, retrieval.IntentGeneral, nil, "a"); !slices.Equal(raw, []string{"c", "b"}) {
		t.Fatal(raw)
	}
	route := func(intent retrieval.Intent, cosines map[string]float64) []string {
		got, _, err := index.recall(ctx, []scored{{"a", 1.0 / 60}}, recallQuery{intent: intent, tokens: retrieval.Tokenize("outage"), similar: similarFrom(cosines), limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	// Without vectors native weighs keyword 0.45, entity 0.25, graph 0.30.
	// Graph scores, min-max normalized: a 0, b 0.5, c 1; keyword: a 1, b 1,
	// c 0. So b 0.60 beats a 0.45 and the deeper c 0.30.
	if got := route(retrieval.IntentGeneral, nil); !slices.Equal(got, []string{"b", "a", "c"}) {
		t.Fatal("GENERAL", got)
	}
	// Similarity from stored vectors counts too (keyword 0.30, entity 0.15,
	// similarity 0.35, graph 0.20): without the word, b's cosine still lifts
	// it over c.
	index.words = nil
	if got := route(retrieval.IntentGeneral, map[string]float64{"b": 0.9, "c": -0.4}); !slices.Equal(got, []string{"b", "c", "a"}) {
		t.Fatal("similarity", got)
	}
	// The weights do not depend on the intent; WHY's causal order then puts
	// causes first: re-ranked b, a, c becomes a, b, c.
	index.words = map[string][]string{"outage": {"a", "b"}}
	if got := route(retrieval.IntentWhy, nil); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatal("WHY", got)
	}
	// The query's entities, as the index resolves them, match the records'
	// entities: the named neighbor ranks first (the ids sort the other way).
	index = &derivedIndex{
		links: map[string][]Link{"a": {edge("x", "entity", 1), edge("y", "entity", 1)}},
		named: map[string][]string{"y": {"Athena"}},
	}
	got, _, err := index.recall(ctx, []scored{{"a", 1}}, recallQuery{intent: retrieval.IntentEntity, entities: []string{"athena"}, limit: 200})
	if err != nil || !slices.Equal(got, []string{"y", "x", "a"}) {
		t.Fatal("ENTITY", got, err)
	}
	// The entity signal stays within 1: names differing only in case count
	// once. y's entity 0.25 + graph 0.30 is below x's keyword 0.45 + 0.30.
	index.named["y"] = []string{"Athena", "ATHENA", "athena"}
	index.words = map[string][]string{"outage": {"x"}}
	got, _, err = index.recall(ctx, []scored{{"a", 1}}, recallQuery{intent: retrieval.IntentEntity, tokens: retrieval.Tokenize("outage"), entities: []string{"athena", "Athena"}, limit: 200})
	if err != nil || !slices.Equal(got, []string{"x", "y", "a"}) {
		t.Fatal("entity cap", got, err)
	}
}

func TestRecallRerankWeights(t *testing.T) {
	// One signal each; gr has the highest raw path score, so the only
	// nonzero graph score.
	candidates := map[string]recallSignals{
		"kw":  {keyword: 1, graph: 1},
		"ent": {entity: 1, graph: 1},
		"sim": {similarity: 1, graph: 1},
		"gr":  {graph: 2},
	}
	// Native's weights: with vectors 0.30/0.15/0.35/0.20, without them
	// 0.45/0.25/0/0.30 (keyword/entity/similarity/graph).
	if got := rerankRecall(candidates, true); !slices.Equal(got, []string{"sim", "kw", "gr", "ent"}) {
		t.Error("with vectors", got)
	}
	if got := rerankRecall(candidates, false); !slices.Equal(got, []string{"kw", "gr", "ent", "sim"}) {
		t.Error("without vectors", got)
	}
	// Entity against graph flips between the two: 0.25 > 0.30×0.8 without
	// vectors, 0.15 < 0.20×0.8 with them.
	candidates = map[string]recallSignals{"p": {entity: 1, graph: 0}, "q": {graph: 0.8}, "r": {graph: 1}}
	if without, with := rerankRecall(candidates, false), rerankRecall(candidates, true); !slices.Equal(without, []string{"r", "p", "q"}) || !slices.Equal(with, []string{"r", "q", "p"}) {
		t.Fatal(without, with)
	}
	// Equal raw scores: no graph signal at all.
	if got := rerankRecall(map[string]recallSignals{"b": {graph: 3}, "a": {graph: 3}}, true); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
}

func TestRecallCausalOrder(t *testing.T) {
	index := &derivedIndex{links: map[string][]Link{
		"y": {edge("x", "causal", 1), edge("z", "temporal", 1)},
		"p": {edge("q", "causal", 1)},
		"q": {edge("p", "causal", 1), edge("outside", "causal", 1)},
	}}
	// y causes x: y first, then the best ready record. Temporal links and
	// links leaving the results do not order.
	if got := index.causalOrder([]string{"x", "z", "y"}); !slices.Equal(got, []string{"z", "y", "x"}) {
		t.Fatal(got)
	}
	// A cycle keeps its records, after the ordered ones, in score order.
	if got := index.causalOrder([]string{"q", "p", "r"}); !slices.Equal(got, []string{"r", "q", "p"}) {
		t.Fatal(got)
	}
	if got := index.causalOrder([]string{"x"}); !slices.Equal(got, []string{"x"}) {
		t.Fatal(got)
	}
}

func TestTraversalRouteReachesTwoHops(t *testing.T) {
	ctx := context.Background()
	s := goldenIndexStore(t)
	// anchor —causal→ cause —causal→ root: relations stops at one hop.
	related, err := s.Query(ctx, indexRequest("Why outage Athena", &IndexPolicy{Version: 1, Relations: true}))
	if err != nil || !slices.Contains(related.IDs, "cause") || slices.Contains(related.IDs, "root") {
		t.Fatal(related, err)
	}
	got, err := s.Query(ctx, indexRequest("Why outage Athena", &IndexPolicy{Version: 1, Traversal: true}))
	if err != nil || !slices.Contains(got.IDs, "cause") || !slices.Contains(got.IDs, "root") || got.Index.Routes["traversal"] == 0 || got.Index.Routes["traversal+"] == 0 {
		t.Fatal(got.IDs, got.Index, err)
	}
	if got.Index.Policy == related.Index.Policy {
		t.Fatal("traversal policy shares an identity")
	}
	// The correction new → old is not traversed (the route holds the anchor
	// alone); the supersedes route follows it.
	got, err = s.Query(ctx, indexRequest("beta", &IndexPolicy{Version: 1, Traversal: true}))
	if v, ok := got.Index.Routes["traversal+"]; err != nil || !slices.Equal(got.IDs, []string{"new"}) || got.Index.Routes["traversal"] != 1 || !ok || v != 0 {
		t.Fatal(got.IDs, got.Index, err)
	}
	got, err = s.Query(ctx, indexRequest("beta", &IndexPolicy{Version: 1, Supersedes: true}))
	if err != nil || !slices.Contains(got.IDs, "old") {
		t.Fatal(got.IDs, err)
	}
	// Literal and explicit-id reads take no route.
	idRead := indexRequest("Why outage Athena", &IndexPolicy{Version: 1, Traversal: true})
	idRead.Input["ids"] = json.RawMessage(`["anchor"]`)
	for _, q := range []QueryRequest{indexRequest("", &IndexPolicy{Version: 1, Traversal: true}), idRead} {
		got, err = s.Query(ctx, q)
		if _, ok := got.Index.Routes["traversal"]; err != nil || ok {
			t.Fatal(got.Index, err)
		}
	}
	// An intent override reaches traversal: for WHEN the temporal link
	// leads, for WHY the causal one (and the causal order keeps the cause
	// after the anchor that links to it).
	order := func(intent string) []string {
		q := indexRequest("outage Athena", &IndexPolicy{Version: 1, Traversal: true})
		q.Input["intent"], _ = json.Marshal(intent)
		got, err := s.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(slices.Clone(got.IDs), func(id string) bool { return id != "followup" && id != "cause" })
	}
	if when, why := order("WHEN"), order("WHY"); !slices.Equal(when, []string{"followup", "cause"}) || !slices.Equal(why, []string{"cause", "followup"}) {
		t.Fatal(when, why)
	}
}

func TestRecencyAnchorsSeedTraversal(t *testing.T) {
	ctx := context.Background()
	s := recencyStore(t)
	// The newest record links to the oldest, which no route finds.
	_, err := s.Put(ctx, PutRequest{Upsert: []Upsert{
		indexedRecord("fresh", "plain fresh note", map[string]any{"eventAt": "2030-01-01", "links": []any{map[string]any{"id": "stale", "version": 1, "type": "causal", "weight": 1}}}),
		indexedRecord("stale", "plain stale note", map[string]any{"eventAt": "2000-01-01"}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Query(ctx, indexRequest("zzz", &IndexPolicy{Version: 1, Recency: true}))
	if err != nil || got.IDs[0] != "fresh" || slices.Contains(got.IDs, "stale") {
		t.Fatal(got.IDs, err)
	}
	got, err = s.Query(ctx, indexRequest("zzz", &IndexPolicy{Version: 1, Recency: true, Traversal: true}))
	// The route: the twenty anchors and stale, re-ranked.
	if err != nil || !slices.Contains(got.IDs, "stale") || got.Index.Routes["traversal"] != 21 || got.Index.Routes["traversal+"] != 1 {
		t.Fatal(got.IDs, got.Index, err)
	}
}

// The semantic term reuses the hybrid base route's cosines: no embedding
// and no vector read beyond the base route's.
func TestTraversalSemanticReusesBaseCosines(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int64
	letters := letterEmbeddings(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		letters.Config.Handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	cfg := &EmbeddingConfig{URL: server.URL, Model: "letters"}
	// The anchor has two semantic links of equal weight: "tag out" shares
	// the query's letters, "qqq xxx" does not. 210 fillers, anagrams of the
	// query, fill the anchors and the vector route's 200 places, so neither
	// neighbor is an anchor or found by the base route.
	upserts := []Upsert{
		indexedRecord("anchor", "outage", map[string]any{"links": []any{
			map[string]any{"id": "n-a", "version": 1, "type": "semantic", "weight": 0.5},
			map[string]any{"id": "n-b", "version": 1, "type": "semantic", "weight": 0.5}}}),
		indexedRecord("n-a", "qqq xxx", nil),
		indexedRecord("n-b", "tag out", nil),
	}
	for i := 0; i < 210; i++ {
		upserts = append(upserts, indexedRecord(fmt.Sprintf("f%03d", i), "eguota", nil))
	}
	s := testStore(t)
	if _, err := s.Put(ctx, PutRequest{Upsert: upserts}); err != nil {
		t.Fatal(err)
	}
	neighbors := func(ids []string) []string {
		return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return id != "n-a" && id != "n-b" })
	}
	for _, runtime := range []bool{false, true} {
		if runtime {
			stop, err := s.StartRuntime(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
		}
		hybrid := indexRequest("outage", &IndexPolicy{Version: 1, Lexical: true})
		hybrid.Input["match"], hybrid.Embedding = json.RawMessage(`"hybrid"`), cfg
		for range 2 { // the second one warm
			got, err := s.Query(ctx, hybrid)
			if err != nil || len(neighbors(got.IDs)) != 0 {
				t.Fatal("the base route found a neighbor", got.IDs, err)
			}
		}
		before := requests.Load()
		if _, err := s.Query(ctx, hybrid); err != nil {
			t.Fatal(err)
		}
		warm := requests.Load() - before
		hybrid.Index = &IndexPolicy{Version: 1, Lexical: true, Traversal: true}
		before = requests.Load()
		got, err := s.Query(ctx, hybrid)
		// The route: the twenty anchors and the two neighbors, re-ranked.
		if err != nil || got.Index.Routes["traversal"] != 22 || got.Index.Routes["traversal+"] != 2 {
			t.Fatal(got, err)
		}
		if requests.Load()-before != warm {
			t.Fatal("traversal embedded", warm, requests.Load()-before)
		}
		// The neighbor that shares the query's letters ranks first.
		if order := neighbors(got.IDs); !slices.Equal(order, []string{"n-b", "n-a"}) {
			t.Fatal("runtime", runtime, order)
		}
		// Without a query vector (words) the ids decide.
		words := indexRequest("outage", &IndexPolicy{Version: 1, Lexical: true, Traversal: true})
		words.Embedding = cfg
		got, err = s.Query(ctx, words)
		if order := neighbors(got.IDs); err != nil || !slices.Equal(order, []string{"n-a", "n-b"}) {
			t.Fatal("words", order, err)
		}
	}
}
