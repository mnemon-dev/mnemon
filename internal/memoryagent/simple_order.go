package memoryagent

import (
	"cmp"
	"context"
	"math"
	"slices"
	"time"
)

// The View's order (view.order): the reference system delivers its judged
// View to the answer model in time order, while a read selects, cuts and
// trims by rank. With "time" the records of a composed View (after the
// expansion and the timelines layer) are listed in time order; the index
// blocks keep their place and each record its score.

const viewOrderTime = "time"

// timeOrder lists records in time order. The key, total and deterministic:
// the record's date (Evidence.Date: the writer's data.eventAt, else the
// record's date, else its creation time), as an instant when it parses as
// RFC 3339 or as a day (YYYY-MM-DD, midnight UTC), earlier first; records
// whose date does not parse come after every dated one. Then the write
// position (records.Store.Sequence: the document's, then the chunk's place
// in it), earlier first; then the id. revision is the one the records were
// read at.
func (a *Agent) timeOrder(ctx context.Context, revision string, records []Evidence) ([]Evidence, error) {
	ids := []string{}
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	written, err := a.store.Sequence(ctx, revision, ids)
	if err != nil {
		return nil, err
	}
	position := func(id string) int64 {
		if seq, ok := written[id]; ok {
			return seq
		}
		return math.MaxInt64
	}
	out := slices.Clone(records)
	slices.SortFunc(out, func(x, y Evidence) int {
		tx, dx := recordTime(x.Date)
		ty, dy := recordTime(y.Date)
		switch {
		case dx != dy && dx:
			return -1
		case dx != dy:
			return 1
		case dx && !tx.Equal(ty):
			return tx.Compare(ty)
		}
		if order := cmp.Compare(position(x.ID), position(y.ID)); order != 0 {
			return order
		}
		return cmp.Compare(x.ID, y.ID)
	})
	return out, nil
}

// recordTime is a record date as an instant: RFC 3339 (fractional seconds
// allowed), or a day, at midnight UTC; ok is false when it is neither.
func recordTime(date string) (at time.Time, ok bool) {
	for _, layout := range []string{time.RFC3339Nano, time.DateOnly} {
		if at, err := time.Parse(layout, date); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

// orderView lists a composed View's records in time order when view.order is
// "time" (timeOrder), keeping their rank order in RankOrder, and returns
// the View as it is otherwise. When the records' write positions cannot be
// read the View stays in rank order with RankOrder unset, and the read is
// degraded (order); only a canceled read fails.
func (r *simpleRun) orderView(ctx context.Context, view View) (View, error) {
	if r.agent.config.View.Order != viewOrderTime {
		return view, nil
	}
	ranked := []string{}
	for _, record := range view.Records {
		ranked = append(ranked, record.ID)
	}
	ordered, err := r.agent.timeOrder(ctx, r.revision, view.Records)
	if err != nil {
		if ctx.Err() != nil {
			return View{}, ctx.Err()
		}
		r.degrade("order")
		view.Stats.Degraded = r.statsNow().Degraded
		return view, nil
	}
	view.Records, view.RankOrder = ordered, ranked
	return view, nil
}

// OrderRecords lists records as the View's order setting lists a read's
// records: in time order with view.order "time" (timeOrder), else as
// given. revision is the one they were read at. The AML adapter's raw
// fallback uses it, so a degraded Search delivers like a read.
func (a *Agent) OrderRecords(ctx context.Context, revision string, records []Evidence) ([]Evidence, error) {
	if a.config.View == nil || a.config.View.Order != viewOrderTime {
		return records, nil
	}
	return a.timeOrder(ctx, revision, records)
}
