package memoryagent

import "context"

type Usage struct {
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	USD          float64 `json:"usd,omitempty"`
	CNY          float64 `json:"cny,omitempty"`
}

type Question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

type Completer interface {
	Complete(context.Context, string, string) (string, Usage, error)
}

// CompleterWithMax is a Completer whose calls set their own output limit, as
// the reference system's recall completer takes one per call. The simple
// strategy uses it when the planner has it.
type CompleterWithMax interface {
	CompleteWith(ctx context.Context, system, prompt string, maxTokens int) (string, Usage, error)
}

type JSONCompleter interface {
	CompleteJSON(context.Context, string, string) (string, Usage, error)
}

type Decider interface {
	Decide(context.Context, any, map[string]Question) (map[string]float64, Usage, error)
}

type Models struct {
	Planner Completer
	Judge   Decider
	// Consolidator folds timelines consolidation batches; nil uses Planner.
	Consolidator Completer
}

func (u *Usage) add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.USD += other.USD
	u.CNY += other.CNY
}
