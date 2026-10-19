package memoryagent

import "slices"

type QueryTrace struct {
	Query  string         `json:"query"`
	IDs    []string       `json:"ids"`
	Routes map[string]int `json:"routes,omitempty"`
}

type ContextTrace struct {
	ID       string  `json:"id"`
	Score    float64 `json:"score"`
	Selected bool    `json:"selected"`
}

type CandidateTrace struct {
	ID         string         `json:"id"`
	Screen     float64        `json:"screen"`
	Needed     float64        `json:"needed"`
	Superseded float64        `json:"superseded"`
	Coverage   []float64      `json:"coverage,omitempty"`
	Judged     bool           `json:"judged"`
	Selected   bool           `json:"selected"`
	Drop       string         `json:"drop,omitempty"`
	Context    []ContextTrace `json:"context,omitempty"`
}

// Traces contain bounded identities and decisions, never extra original prose.
// They are requested explicitly and cannot change the algorithm or budgets.
type ReadTrace struct {
	Queries    []QueryTrace     `json:"queries"`
	Candidates []CandidateTrace `json:"candidates"`
	Edges      []RelationEdge   `json:"edges,omitempty"`
}

func (r *readRun) readTrace(values []Evidence) *ReadTrace {
	t := &ReadTrace{Queries: []QueryTrace{}, Candidates: []CandidateTrace{}}
	ids := []string{}
	for _, value := range values {
		ids = append(ids, value.ID)
	}
	for _, page := range r.queries {
		t.Queries = append(t.Queries, QueryTrace{Query: page.query, IDs: append([]string(nil), page.ids...), Routes: page.routes})
	}
	for _, id := range r.order {
		c := r.items[id]
		trace := CandidateTrace{ID: id, Screen: c.screen, Needed: c.needed, Superseded: c.superseded, Coverage: c.coverage, Judged: c.judged, Selected: slices.Contains(ids, id)}
		if !trace.Selected {
			switch {
			case !c.judged:
				trace.Drop = "detail-budget"
			case c.superseded >= 0.5 && c.needed < 0.5:
				trace.Drop = "superseded"
			default:
				trace.Drop = "view-budget"
			}
		}
		if r.related != nil {
			for _, value := range r.related.context[id] {
				trace.Context = append(trace.Context, ContextTrace{ID: value.ID, Score: c.contextScores[value.ID], Selected: slices.Contains(ids, value.ID)})
			}
		}
		t.Candidates = append(t.Candidates, trace)
	}
	if r.related != nil {
		t.Edges = append([]RelationEdge(nil), r.related.edges...)
	}
	return t
}
