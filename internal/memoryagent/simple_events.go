package memoryagent

import (
	"context"
	"slices"
	"strings"
	"time"
)

// The events source of view.expand: the question's nearest consolidated
// events propose the records they cite. Unlike the seed source it needs no
// judged evidence, so it reaches records outside the sessions of what the
// cue searches found. The events are those of the read scope's timelines or
// layered state that cite records, each as "<thread title>: <date> <event
// text>"; they rank by meaning, the cosine of the message's query vector
// with each event text's document vector (texts cut as the layer's are),
// and by shared words (BM25 over the same texts) without an embedder or
// when embedding fails. The event vectors are stored like the layer's
// texts: WarmLayer embeds them after a consolidation, and a read embeds
// the ones still missing, a bounded number at a time.

// expandEventsEmbed bounds the event texts one read embeds (one batch of
// layer texts); a read that finds more missing ranks by shared words, and
// the next reads embed the rest.
var expandEventsEmbed = layerEmbedBatch

// expansionEvent is an event that cites records: the text that ranks it and
// the records it cites, in its order.
type expansionEvent struct {
	text    string
	records []string
}

// expansionEvents is the memory's events that cite records, in memory
// order; an event whose text and records repeat an earlier one's is left
// out.
func expansionEvents(m timelineMemory) []expansionEvent {
	titles := map[string]string{}
	for _, t := range m.Threads {
		titles[t.ID] = t.Title
	}
	out, seen := []expansionEvent{}, map[string]bool{}
	for _, e := range m.Events {
		if len(e.Records) == 0 {
			continue
		}
		text := strings.TrimSpace(e.Date + " " + e.Text)
		if title := titles[e.Thread]; title != "" {
			text = title + ": " + text
		}
		key := text + "\x00" + strings.Join(e.Records, "\x00")
		if !seen[key] {
			seen[key] = true
			out = append(out, expansionEvent{text, e.Records})
		}
	}
	return out
}

// expandsEvents reports whether the agent's view expansion lists events.
func (a *Agent) expandsEvents() bool {
	return a.config.View != nil && a.config.View.Expand != nil && slices.Contains(a.config.View.Expand.Kinds, expandEvents)
}

// eventCandidates is the events source: the records cited by the best top
// events for the message (its last message without a date line, as the
// message search reads it), in event rank order, then each event's record
// order; records known (pooled, or with rejudge judged) are left out, each
// record once. Without the timelines layer, a message, or a state with
// events it proposes nothing; a state that does not load degrades the read
// (expansion). Only a canceled read is an error.
func (r *simpleRun) eventCandidates(ctx context.Context, top int, known func(string) bool) ([]expansionCandidate, error) {
	query := jsSlice(lastMessage(r.dialogue), 0, 1000)
	if !r.agent.layerOn() || query == "" {
		return nil, nil
	}
	defer r.queryTime(time.Now())
	state, _, err := r.agent.overlay.loadTimelines(ctx, r.req.Scope)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		r.degrade("expansion")
		return nil, nil
	}
	events := expansionEvents(state.Memory)
	if len(events) == 0 {
		return nil, nil
	}
	texts := []string{}
	for _, e := range events {
		texts = append(texts, e.text)
	}
	order, err := r.rankEvents(ctx, query, texts, top)
	if err != nil {
		return nil, err
	}
	out, seen := []expansionCandidate{}, map[string]bool{}
	for _, i := range order {
		for _, id := range events[i].records {
			if !seen[id] && !known(id) {
				seen[id] = true
				out = append(out, expansionCandidate{id: id, kinds: []string{expandEvents}})
			}
		}
	}
	return out, nil
}

// rankEvents is the indexes of the best top texts for query: by meaning
// (byDot of the query vector with the texts' stored document vectors, ties
// in order), the missing vectors embedded and stored first, at most
// expandEventsEmbed of them; by shared words (bm25Top, positive scores
// only) without an embedder, or when embedding fails or leaves a vector
// missing, which degrades the read (expansion-lexical). Only a canceled
// read is an error.
func (r *simpleRun) rankEvents(ctx context.Context, query string, texts []string, top int) ([]int, error) {
	lexical := func(degraded bool) ([]int, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if degraded {
			r.degrade("expansion-lexical")
		}
		r.mu.Lock()
		r.stats.ExpandEventsLexical = true
		r.mu.Unlock()
		return bm25Top(texts, query, top), nil
	}
	config := r.agent.config.Embedding
	if config == nil {
		return lexical(false)
	}
	vector, err := r.agent.store.QueryVector(ctx, query, *config)
	if err != nil || len(vector) == 0 {
		return lexical(true)
	}
	vectors, err := r.agent.storedDocuments(ctx, texts)
	if err != nil {
		return lexical(true)
	}
	missing := []int{}
	for i, v := range vectors {
		if v == nil {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		fill, part := missing[:min(len(missing), expandEventsEmbed)], []string{}
		for _, i := range fill {
			part = append(part, texts[i])
		}
		filled, err := r.agent.layerDocuments(ctx, part)
		if err != nil {
			return lexical(true)
		}
		r.mu.Lock()
		r.stats.ExpandEventsEmbedded += len(fill)
		r.mu.Unlock()
		for k, i := range fill {
			vectors[i] = filled[k]
		}
	}
	// A vector still missing (past the bound) or of another size: words.
	if slices.ContainsFunc(vectors, func(v []float32) bool { return len(v) != len(vector) }) {
		return lexical(true)
	}
	return byDot(vector, vectors, top), nil
}
