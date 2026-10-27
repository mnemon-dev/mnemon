package memoryagent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestReadSimpleNoEvidence: a read whose judge calls nothing needed reports
// no evidence and still fills its View; a read whose judge calls a record
// needed, or whose judgement fails, does not report no evidence.
func TestReadSimpleNoEvidence(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "answer", "codename ORBIT")
	seedNote(t, s, "other", "the weather was fine")
	planner := completeFunc(func(_ context.Context, system, _ string) (string, Usage, error) {
		if strings.HasPrefix(system, "You plan searches in the notes") {
			return "codename\nNOTE: the codename is ORBIT", Usage{}, nil
		}
		return "NEED: the codename", Usage{}, nil
	})
	// judge answers every question needed (or not), never superseded; with
	// err it fails the final judgement (the questions s<i>), which the read
	// survives degraded, and answers the screen.
	judge := func(needed float64, err error) decisionFunc {
		return func(_ context.Context, _ any, questions map[string]Question) (map[string]float64, Usage, error) {
			if _, final := questions["s0"]; final && err != nil {
				return nil, Usage{}, err
			}
			out := map[string]float64{}
			for key := range questions {
				out[key] = needed
				if strings.HasPrefix(key, "s") {
					out[key] = 0
				}
			}
			return out, Usage{}, nil
		}
	}
	read := func(decide decisionFunc) View {
		t.Helper()
		a, err := New(s, Config{Version: 1, Strategy: "simple"}, Models{Planner: planner, Judge: decide}, nil)
		if err != nil {
			t.Fatal(err)
		}
		view, err := a.Read(context.Background(), ReadRequest{Question: "What is the codename?"})
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	if view := read(judge(0, nil)); !view.NoEvidence || len(view.Records) == 0 {
		t.Fatalf("nothing needed: no evidence %v, %d records", view.NoEvidence, len(view.Records))
	}
	if view := read(judge(1, nil)); view.NoEvidence || len(view.Records) == 0 {
		t.Fatalf("a record needed: no evidence %v, %d records", view.NoEvidence, len(view.Records))
	}
	if view := read(judge(0, errors.New("JEV is down"))); view.NoEvidence || len(view.Stats.Degraded) == 0 {
		t.Fatalf("a failed judgement: no evidence %v, degraded %v", view.NoEvidence, view.Stats.Degraded)
	}
}
