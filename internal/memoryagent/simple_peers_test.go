package memoryagent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// peerJudge answers like a JEV that reads the whole batch: budget review
// talk is needed; the old claim ("went fine") is no longer current only
// when its correction ("was redone") is another item of the same call. It
// records every call's candidate texts and question keys.
type peerJudge struct {
	calls     [][]string
	questions []map[string]Question
}

func (p *peerJudge) Decide(_ context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
	raw, _ := json.Marshal(state)
	var s struct{ Candidates []struct{ Text string } }
	json.Unmarshal(raw, &s)
	texts := []string{}
	for _, c := range s.Candidates {
		texts = append(texts, c.Text)
	}
	p.calls, p.questions = append(p.calls, texts), append(p.questions, questions)
	all := strings.Join(texts, "\n")
	needed := func(text string) float64 {
		if strings.Contains(text, "budget review") {
			return 0.9
		}
		return 0.1
	}
	out := map[string]float64{}
	for key := range questions {
		var i, k int
		switch {
		case func() bool { _, err := fmt.Sscanf(key, "c%dq%d", &i, &k); return err == nil }():
			out[key] = needed(texts[i])
		case func() bool { _, err := fmt.Sscanf(key, "s%d", &i); return err == nil }():
			out[key] = 0
			if strings.Contains(texts[i], "went fine") && strings.Contains(all, "was redone") {
				out[key] = 0.9
			}
		default:
			fmt.Sscanf(key, "n%d", &i)
			out[key] = needed(texts[i])
		}
	}
	return out, Usage{}, nil
}

// The stale-claim probe of the stage 4 review: a correction expanded both
// ways brought the corrected claim back, judged without its correction, and
// it entered as current. Judged with the seed as a peer, it is withdrawn.
func TestExpansionJudgesWithPeers(t *testing.T) {
	s := expandFixture(t)
	judge := &peerJudge{}
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: &ExpandConfig{Kinds: []string{"supersedes"}}}}, Models{Planner: replies("redone\nNOTE: it was redone", "NEED: the answer", "unrelated words"), Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.Read(context.Background(), ReadRequest{Question: "What was redone?"})
	if err != nil {
		t.Fatal(err)
	}
	// b2 (pooled, needed) corrects b0, which the expansion proposes.
	if slices.Contains(recordIDs(view.Records), "b0") || view.Stats.Expanded != 1 || view.Stats.ExpandShown != 0 {
		t.Fatal(recordIDs(view.Records), view.Stats)
	}
	last, questions := judge.calls[len(judge.calls)-1], judge.questions[len(judge.questions)-1]
	if !slices.Equal(last, []string{"Melanie: rocket budget review was redone", "Melanie: rocket budget review went fine"}) {
		t.Fatal(last)
	}
	// The peer is asked nothing: the candidate's questions alone, with
	// judgeAll's texts.
	_, want := batchRequest([]Message{user("What was redone?")}, []judgeCandidate{{Source: judgeSource, Text: last[0]}, {Source: judgeSource, Text: last[1]}})
	delete(want, "n0")
	delete(want, "s0")
	if !reflect.DeepEqual(questions, want) {
		t.Fatal(questions)
	}
}

// A candidate reached otherwise (adjacency, two records away) is judged
// with the peers too: the seed b2, needed, shows b0 is no longer current.
func TestExpansionPeersShowWhatIsThere(t *testing.T) {
	s := expandFixture(t)
	judge := &peerJudge{}
	expand := &ExpandConfig{Kinds: []string{"adjacent"}, AdjacentWindow: 2}
	a, err := New(s, Config{Version: 1, Strategy: "simple", View: &ViewConfig{Expand: expand}}, Models{Planner: replies("redone\nNOTE: it was redone", "NEED: the answer", "unrelated words"), Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.Read(context.Background(), ReadRequest{Question: "What was redone?"})
	if err != nil {
		t.Fatal(err)
	}
	last := judge.calls[len(judge.calls)-1]
	if slices.Contains(recordIDs(view.Records), "b0") || !slices.Contains(last, "Melanie: rocket budget review went fine") || last[0] != "Melanie: rocket budget review was redone" {
		t.Fatal(recordIDs(view.Records), last)
	}
}

func TestPeerBatches(t *testing.T) {
	item := func(id string, needed float64) *simpleItem { return scoredItem(id, needed, 0, id) }
	peers := []*simpleItem{item("p0", 0.9), item("p1", 0.8)}
	partner := item("seed-9", 0.6)
	candidates := []*simpleItem{}
	for i := 0; i < 80; i++ {
		candidates = append(candidates, item(fmt.Sprintf("c%02d", i), 0))
	}
	// c40 was reached over a correction from seed-9, not a peer.
	partners := map[*simpleItem][]*simpleItem{candidates[40]: {partner}}
	batches := peerBatches(candidates, peers, partners)
	sizes, total := []string{}, 0
	for _, b := range batches {
		sizes = append(sizes, fmt.Sprint(len(b.peers), "+", len(b.items)))
		total += len(b.items)
		if len(b.peers)+len(b.items) > judgeBatch || !slices.Equal(itemIDs(b.peers[:2]), []string{"p0", "p1"}) {
			t.Fatal(sizes)
		}
		if slices.Contains(b.items, candidates[40]) != slices.Contains(b.peers, partner) {
			t.Fatal("the partner of c40 travels with it alone", itemIDs(b.peers))
		}
	}
	if total != 80 || !slices.Equal(sizes, []string{"2+38", "3+37", "2+5"}) {
		t.Fatal(sizes, total)
	}
	// The peers: the seeds and the current needed judged items, each once,
	// best first, at most 8.
	scored := []*simpleItem{item("a", 0.9), item("b", 0.4), scoredItem("stale", 0.95, 0.8, ""), item("c", 0.7)}
	for i := 0; i < 8; i++ {
		scored = append(scored, item(fmt.Sprintf("n%d", i), 0.6))
	}
	got := expansionPeers(scored, []*simpleItem{scored[1], scored[2]})
	if !slices.Equal(itemIDs(got), []string{"stale", "a", "c", "n0", "n1", "n2", "n3", "n4"}) {
		t.Fatal(itemIDs(got))
	}
}
