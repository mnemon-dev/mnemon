package memoryagent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// Topic weights (consolidation.topicWeights and view.weightedRecall,
// docs/superpowers/specs/2026-10-26-neutral-memory-and-single-pass-search-design.md).
//
// Writing: once a batch is linked, JEV is asked, of every topic (thread) the
// batch's events joined and every record of the batch, whether the record is
// about the topic. A yes (≥ 0.5) is an edge topic → record, weight 1, and so
// is every batch record a new event of the topic cites. Only the batch's
// topics and records are asked, so a batch costs the same questions whatever
// the memory holds; a topic merged across batches gathers the edges of each.
//
// Reading: each search's words also find topics (by their events, the
// consolidated items' index); a record a found topic holds earns, for that
// search, 1/(60 + the topic's rank) of its best such topic, and a search's
// own pages keep their reciprocal-rank sum. The mix (mixWeighted) keeps every
// search's share of its own page first, as mixCues does, and ranks the rest
// by the sum of both.

const topicChunksSchema = `CREATE TABLE IF NOT EXISTS topic_chunks(scope TEXT NOT NULL,thread TEXT NOT NULL,record TEXT NOT NULL,PRIMARY KEY(scope,thread,record))`

func weightQuestion(n, c string) string {
	return "Is the candidate whose `id` is \"" + c + "\" about the topic whose `id` is \"" + n + "\"?"
}

const (
	weightYes = "It reports, asks, decides or discusses something that belongs to this topic."
	weightNo  = "It only shares a word, name or date with the topic, or it is about something else."
)

// batchWeights returns the edges thread → records of a linked batch: the
// batch records the events from index from on cite, and the batch records JEV
// calls about each thread those events joined. A failed JEV call leaves the
// edges found so far.
func (a *Agent) batchWeights(ctx context.Context, m timelineMemory, from int, batch []pendingRecord, state *timelineState, result *ConsolidateResult) map[string][]string {
	edges := map[string][]string{}
	add := func(thread, record string) {
		if !slices.Contains(edges[thread], record) {
			edges[thread] = append(edges[thread], record)
		}
	}
	inBatch := map[string]bool{}
	for _, r := range batch {
		inBatch[r.ID] = true
	}
	titles := map[string]string{}
	for _, t := range m.Threads {
		titles[t.ID] = t.Title
	}
	threads, events := []string{}, map[string][]string{}
	for _, e := range m.Events[min(from, len(m.Events)):] {
		if !slices.Contains(threads, e.Thread) {
			threads = append(threads, e.Thread)
		}
		events[e.Thread] = append(events[e.Thread], e.Date+" "+e.Text)
		for _, id := range e.Records {
			if inBatch[id] {
				add(e.Thread, id)
			}
		}
	}
	if len(threads) == 0 || a.models.Judge == nil {
		return edges
	}
	news, cands, pairs := []linkItem{}, []linkItem{}, []*linkPair{}
	for _, t := range threads {
		news = append(news, linkItem{ID: t, Name: titles[t], Text: authorExcerpt(strings.Join(events[t], " / "), simpleListing)})
	}
	for i, r := range batch {
		cands = append(cands, linkItem{ID: fmt.Sprintf("r%d", i+1), Text: authorExcerpt(r.Text, simpleListing)})
	}
	for i := range news {
		for j := range cands {
			pairs = append(pairs, &linkPair{kind: linkWeight, item: i, candidate: j})
		}
	}
	calls := 0
	if err := a.newLinker().judge(ctx, news, cands, pairs, state, result, &calls); err != nil {
		result.WeightErrors++
	}
	result.LinkCalls += calls
	for _, p := range pairs {
		if p.answered && p.same >= 0.5 {
			add(threads[p.item], batch[p.candidate].ID)
		}
	}
	return edges
}

// saveTopicChunks stores the edges thread → records, each once.
func (o *Overlay) saveTopicChunks(ctx context.Context, scope records.Scope, edges map[string][]string) error {
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	insert, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO topic_chunks(scope,thread,record) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	key := scopeKey(scope)
	for thread, ids := range edges {
		for _, id := range ids {
			if _, err := insert.ExecContext(ctx, key, thread, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// topicChunks is the records each of threads holds.
func (o *Overlay) topicChunks(ctx context.Context, scope records.Scope, threads []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(threads) == 0 {
		return out, nil
	}
	args := []any{scopeKey(scope)}
	for _, t := range threads {
		args = append(args, t)
	}
	rows, err := o.db.QueryContext(ctx, `SELECT thread,record FROM topic_chunks WHERE scope=? AND thread IN (?`+strings.Repeat(",?", len(threads)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var thread, record string
		if err := rows.Scan(&thread, &record); err != nil {
			return nil, err
		}
		out[thread] = append(out[thread], record)
	}
	return out, rows.Err()
}

// topicScores is, for each record a topic the cues' words find holds, the
// sum over the cues of 1/(rrfK + rank) of its best such topic, a topic's
// rank being its best event's among the cue's first page of events.
func (r *simpleRun) topicScores(ctx context.Context, cues []cue) (map[string]float64, error) {
	o := r.agent.overlay
	if o == nil {
		return nil, nil
	}
	if err := o.syncLayerIndex(ctx, r.req.Scope); err != nil {
		return nil, err
	}
	extra := map[string]float64{}
	for _, c := range cues {
		found, err := o.searchLayer(ctx, r.req.Scope, c.Text, simplePageSize)
		if err != nil {
			return nil, err
		}
		rank, order := map[string]int{}, []string{}
		for _, entry := range found {
			if entry.Kind != "event" {
				continue
			}
			if _, ok := rank[entry.Event.Thread]; !ok {
				rank[entry.Event.Thread] = len(order)
				order = append(order, entry.Event.Thread)
			}
		}
		held, err := o.topicChunks(ctx, r.req.Scope, order)
		if err != nil {
			return nil, err
		}
		best := map[string]float64{}
		for thread, ids := range held {
			score := 1 / float64(rrfK+rank[thread])
			for _, id := range ids {
				best[id] = max(best[id], score)
			}
		}
		for id, score := range best {
			extra[id] += score
		}
	}
	return extra, nil
}

// mixWeighted is mixCues with each id's topic score (topicScores) added to
// its reciprocal-rank sum: every list keeps its share of its own best finds
// first, and the rest, the topics' records among them, go by the sum.
func mixWeighted(lists [][]string, extra map[string]float64, limit int) []string {
	score, keys := map[string]float64{}, []string{}
	for _, list := range lists {
		for rank, id := range list {
			if _, ok := score[id]; !ok {
				keys = append(keys, id)
			}
			score[id] += 1 / float64(rrfK+rank)
		}
	}
	extraIDs := make([]string, 0, len(extra))
	for id := range extra {
		extraIDs = append(extraIDs, id)
	}
	slices.Sort(extraIDs)
	for _, id := range extraIDs {
		if _, ok := score[id]; !ok {
			keys = append(keys, id)
		}
		score[id] += extra[id]
	}
	share := max(1, limit/max(1, len(lists)))
	chosen, out := map[string]bool{}, []string{}
	add := func(id string) {
		if !chosen[id] && len(out) < limit {
			chosen[id] = true
			out = append(out, id)
		}
	}
	for _, list := range lists {
		fresh := slices.DeleteFunc(slices.Clone(list), func(id string) bool { return chosen[id] })
		for _, id := range fresh[:min(share, len(fresh))] {
			add(id)
		}
	}
	slices.SortStableFunc(keys, func(a, b string) int { return probabilityOrder(score[a], score[b]) })
	for _, id := range keys {
		add(id)
	}
	return out
}
