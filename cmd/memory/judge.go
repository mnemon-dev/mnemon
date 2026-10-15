package memory

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/graph"
	"github.com/mnemon-dev/mnemon/internal/memory/judge"
	"github.com/mnemon-dev/mnemon/internal/memory/search"
)

// judgeContentChars caps how much of one memory is sent to the judge, keeping
// a full request well under the 64 KiB body limit of a local server.
const judgeContentChars = 1200

// relationOptions are the choices offered when classifying how a new memory
// relates to an existing semantic candidate.
var relationOptions = map[string]string{
	"duplicate":   "the new memory says the same thing as the existing one",
	"supersedes":  "the new memory updates or replaces the existing one",
	"contradicts": "the new memory conflicts with the existing one without clearly replacing it",
	"related":     "same subject, different facts",
	"unrelated":   "different subjects",
}

// judgment is the judge's classification of one semantic candidate.
type judgment struct {
	Relation    string  `json:"relation"`
	Probability float64 `json:"probability"`
}

// judgedCandidate is a semantic candidate with an optional judgment. A
// candidate the judge did not classify serializes exactly as before.
type judgedCandidate struct {
	graph.SemanticCandidate
	Judgment *judgment `json:"judgment,omitempty"`
}

// warnJudge prints a one-line advisory; the judge never fails a command.
func warnJudge(format string, args ...any) {
	msg := strings.Join(strings.Fields(fmt.Sprintf(format, args...)), " ")
	fmt.Fprintf(os.Stderr, "warning: %s\n", msg)
}

// clipRunes truncates s to at most n runes.
func clipRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// judgeRecall scores every result against the query and drops those below
// --judge-min. It fails open: when the judge is missing or errors, resp is
// left untouched.
func judgeRecall(query string, resp *search.RecallResponse) {
	jc := judge.NewClient()
	if jc == nil {
		warnJudge("--judge ignored: MNEMON_JUDGE_MODEL is not set")
		return
	}
	if len(resp.Results) == 0 {
		return
	}
	memories := make(map[string]string, len(resp.Results))
	questions := make([]judge.Question, len(resp.Results))
	for i, r := range resp.Results {
		name := fmt.Sprintf("m%d", i)
		memories[name] = clipRunes(r.Insight.Content, judgeContentChars)
		questions[i] = judge.Question{
			Name: name,
			Instructions: fmt.Sprintf(
				"Is memory `%s` about the same subject as `query`, so it could help answer it?", name),
		}
	}
	state := map[string]any{"query": query, "memories": memories}
	scores, err := jc.Noul(context.Background(), state, questions)
	if err != nil {
		warnJudge("judge unavailable, returning unjudged results: %v", err)
		return
	}
	kept := make([]search.RecallResult, 0, len(resp.Results))
	for i, r := range resp.Results {
		relevance := roundScore(scores[questions[i].Name])
		if relevance < recJudgeMin {
			continue
		}
		r.Relevance = &relevance
		kept = append(kept, r)
	}
	resp.Results = kept
}

// judgeCandidates classifies each semantic candidate, auto-linked ones
// included: a near-identical candidate is the likeliest duplicate or
// superseded memory.
// It returns false, leaving the caller's output unchanged, when there is
// nothing to judge or the judge is missing or errors.
func judgeCandidates(content string, candidates []graph.SemanticCandidate) ([]judgedCandidate, bool) {
	jc := judge.NewClient()
	if jc == nil {
		warnJudge("--judge ignored: MNEMON_JUDGE_MODEL is not set")
		return nil, false
	}
	existing := make(map[string]string)
	var questions []judge.ChoiceQuestion
	for i, c := range candidates {
		name := fmt.Sprintf("c%d", i)
		existing[name] = clipRunes(c.Content, judgeContentChars)
		questions = append(questions, judge.ChoiceQuestion{
			Name: name,
			Instructions: fmt.Sprintf(
				"How does `new_memory` relate to existing memory `%s`?", name),
			Options: relationOptions,
		})
	}
	if len(questions) == 0 {
		return nil, false
	}
	state := map[string]any{
		"new_memory":        clipRunes(content, judgeContentChars),
		"existing_memories": existing,
	}
	answers, err := jc.Choice(context.Background(), state, questions)
	if err != nil {
		warnJudge("judge unavailable, leaving candidates unjudged: %v", err)
		return nil, false
	}
	out := make([]judgedCandidate, len(candidates))
	for i, c := range candidates {
		out[i] = judgedCandidate{SemanticCandidate: c}
		if a, ok := answers[fmt.Sprintf("c%d", i)]; ok {
			out[i].Judgment = &judgment{Relation: a.Choice, Probability: roundScore(a.Probability)}
		}
	}
	return out, true
}
