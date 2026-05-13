package search

import (
	"sort"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/embed"
	"github.com/mnemon-dev/mnemon/internal/model"
)

// DiffSuggestion classifies how a new fact relates to existing content.
type DiffSuggestion string

const (
	DiffAdd       DiffSuggestion = "ADD"
	DiffDuplicate DiffSuggestion = "DUPLICATE"
	DiffConflict  DiffSuggestion = "CONFLICT"
	DiffUpdate    DiffSuggestion = "UPDATE"
)

// DiffMatch represents one existing insight compared against the new content.
type DiffMatch struct {
	ID               string         `json:"id"`
	Content          string         `json:"content"`
	TokenSimilarity  float64        `json:"token_similarity"`
	CosineSimilarity float64        `json:"cosine_similarity,omitempty"`
	Similarity       float64        `json:"similarity"`
	Suggestion       DiffSuggestion `json:"suggestion"`
}

// DiffResult is the output of a diff check.
type DiffResult struct {
	Suggestion DiffSuggestion `json:"suggestion"`
	Matches    []DiffMatch    `json:"matches"`
}

// DiffOptions controls diff behavior.
type DiffOptions struct {
	Limit         int            // max candidates to compare (default 5)
	NewEmbedding  []float64      // pre-computed embedding for new content (optional)
	ExistingEmbed []EmbeddedItem // pre-loaded embeddings (optional, avoids DB call)
}

// EmbeddedItem pairs an insight ID with its deserialized embedding vector.
type EmbeddedItem struct {
	ID        string
	Embedding []float64
}

// Diff compares new content against existing insights and returns a suggestion.
// It uses token overlap as the baseline, enhanced with cosine similarity when
// embeddings are available.
func Diff(insights []*model.Insight, newContent string, opts DiffOptions) DiffResult {
	if opts.Limit <= 0 {
		opts.Limit = 5
	}

	// Step 1: keyword search to find top candidates
	candidates := KeywordSearch(insights, newContent, opts.Limit)

	// Build a map of pre-loaded embeddings for fast lookup
	embedMap := make(map[string][]float64, len(opts.ExistingEmbed))
	for _, ei := range opts.ExistingEmbed {
		embedMap[ei.ID] = ei.Embedding
	}

	// Step 2: score each candidate
	matches := make([]DiffMatch, 0, len(candidates))
	for _, c := range candidates {
		tokenSim := JaccardSimilarity(newContent, c.Insight.Content)

		var cosineSim float64
		if opts.NewEmbedding != nil {
			if existVec, ok := embedMap[c.Insight.ID]; ok && existVec != nil {
				cosineSim = embed.CosineSimilarity(opts.NewEmbedding, existVec)
			}
		}

		// Combined similarity: cosine only contributes when above 0.85.
		// Below that, same-domain content (e.g. two butterfly survey locations)
		// clusters around 0.70–0.84 and produces false UPDATE matches.
		similarity := tokenSim
		if cosineSim >= 0.85 && cosineSim > similarity {
			similarity = cosineSim
		}

		suggestion := classifySuggestion(similarity, newContent, c.Insight.Content)
		matches = append(matches, DiffMatch{
			ID:               c.Insight.ID,
			Content:          c.Insight.Content,
			TokenSimilarity:  tokenSim,
			CosineSimilarity: cosineSim,
			Similarity:       similarity,
			Suggestion:       suggestion,
		})
	}

	// Step 3: also check embedding-only matches if we have embeddings
	// (keyword search might miss semantically similar content with different words)
	if opts.NewEmbedding != nil && len(opts.ExistingEmbed) > 0 {
		// Find top cosine matches not already in keyword results
		seen := make(map[string]bool, len(matches))
		for _, m := range matches {
			seen[m.ID] = true
		}

		type cosinePair struct {
			id  string
			sim float64
		}
		var topCosine []cosinePair
		for _, ei := range opts.ExistingEmbed {
			if seen[ei.ID] {
				continue
			}
			cs := embed.CosineSimilarity(opts.NewEmbedding, ei.Embedding)
			if cs >= 0.7 { // only consider meaningfully similar items
				topCosine = append(topCosine, cosinePair{ei.ID, cs})
			}
		}

		// Sort by cosine descending, take up to opts.Limit
		sort.Slice(topCosine, func(i, j int) bool {
			return topCosine[i].sim > topCosine[j].sim
		})
		if len(topCosine) > opts.Limit {
			topCosine = topCosine[:opts.Limit]
		}

		// Look up full insight for cosine-only matches
		insightMap := make(map[string]*model.Insight, len(insights))
		for _, ins := range insights {
			insightMap[ins.ID] = ins
		}

		for _, cp := range topCosine {
			ins, ok := insightMap[cp.id]
			if !ok {
				continue
			}
			tokenSim := JaccardSimilarity(newContent, ins.Content)
			similarity := tokenSim
			if cp.sim >= 0.85 && cp.sim > similarity {
				similarity = cp.sim
			}
			suggestion := classifySuggestion(similarity, newContent, ins.Content)

			// Only add if this match is meaningful (not ADD)
			if suggestion != DiffAdd {
				matches = append(matches, DiffMatch{
					ID:               ins.ID,
					Content:          ins.Content,
					TokenSimilarity:  tokenSim,
					CosineSimilarity: cp.sim,
					Similarity:       similarity,
					Suggestion:       suggestion,
				})
			}
		}
	}

	// Overall suggestion: take the strongest match
	overall := DiffAdd
	if len(matches) > 0 {
		overall = matches[0].Suggestion
		// Check if any match is DUPLICATE (strongest signal)
		for _, m := range matches {
			if m.Suggestion == DiffDuplicate {
				overall = DiffDuplicate
				break
			}
		}
	}

	return DiffResult{
		Suggestion: overall,
		Matches:    matches,
	}
}

// negationWords detects potential contradictions.
var negationWords = []string{
	"not", "no longer", "don't", "doesn't", "never", "switched from",
	"instead of", "rather than", "replaced", "deprecated",
	"不", "没有", "不再", "放弃", "替换", "取消",
}

func classifySuggestion(similarity float64, newText, existingText string) DiffSuggestion {
	if similarity < 0.5 {
		return DiffAdd
	}

	// Check for negation/conflict signals (even at high similarity)
	newLower := strings.ToLower(newText)
	existLower := strings.ToLower(existingText)
	for _, neg := range negationWords {
		if strings.Contains(newLower, neg) || strings.Contains(existLower, neg) {
			return DiffConflict
		}
	}

	if similarity > 0.9 {
		return DiffDuplicate
	}
	return DiffUpdate
}
