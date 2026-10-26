package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strconv"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// The read-only accessors of the AML inspect service (aml-inspect): an agent
// over a store read by a records reader (records.OpenReader), and the
// scope's consolidated memory and progress. None writes, folds, calls a model
// or takes a lock: each reads with its own statements, and a quiescent file
// changed meanwhile is reported by Unchanged.

// OpenReader opens the agent of a store that store reads as a records
// reader, with models the caller owns. Its derived index (memory-index.db in
// directory) is opened by the readers' rules (records.OpenReaderDB), and
// none is opened when the store has none: the scope then has no
// consolidated memory. Unlike OpenWithModels, an index that is there but
// does not open is an error, so that no read silently goes without its
// layer. Close the agent after the read, and check Unchanged when it ends.
func OpenReader(store *records.Store, directory string, config Config, models Models) (*Agent, error) {
	var overlay *Overlay
	var check func() error
	if config.Policy == nil || config.Policy.Consolidation {
		db, unchanged, err := records.OpenReaderDB(filepath.Join(directory, "memory-index.db"))
		switch {
		case err == nil:
			overlay, check = &Overlay{db: db, readonly: true}, unchanged
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
	}
	agent, err := New(store, config, models, overlay)
	if err != nil {
		if overlay != nil {
			overlay.Close()
		}
		return nil, err
	}
	agent.indexCheck = check
	return agent, nil
}

// Unchanged is records.ErrChanged when the agent's derived index was read
// quiescent (records.OpenReaderDB) and a writer has changed it since; nil
// otherwise. The store's own file is checked by its Unchanged.
func (a *Agent) Unchanged() error {
	if a.indexCheck == nil {
		return nil
	}
	return a.indexCheck()
}

// Consolidation is a scope's consolidated memory and the progress of its
// consolidation (timelines or layered), as one read of its state found
// them. Its items have the ids the Items of the View's layer blocks name
// (IndexItem.Items): a thread's own id, "e<i>" for the i-th event, a fact's
// key, "d<i>" for the i-th directive, i counting in memory order, which
// consolidation only appends to.
type Consolidation struct {
	// Watermark is the version (records.Head.Version) of the newest record
	// a job has read; 0 before the first job.
	Watermark int64
	// Pending counts the records not folded yet: those a job read and holds
	// back, then those written after the watermark (Backlog.Records);
	// PendingIDs names the first of them, in the order a job reads them.
	Pending    int
	PendingIDs []string
	Threads    []MemoryThread
	Events     []MemoryEvent
	Facts      []MemoryFact
	Directives []MemoryDirective
}

// MemoryThread is a topic thread: its events, and the dates of its first
// and last ("" with none).
type MemoryThread struct {
	ID, Title, First, Last string
	Events                 int
}

// MemoryEvent is an event on a thread, with the records it came from and
// the names it is about (layered extraction's; none in timelines mode).
type MemoryEvent struct {
	ID, Thread, Date, Text string
	Records, Entities      []string
}

// MemoryFact is a key's history of values, oldest first; the last is the
// current one.
type MemoryFact struct {
	Key    string
	Values []MemoryValue
}

// MemoryValue is one value of a key. Conflict is the consolidation's mark of
// a value that contradicts an earlier one without the user saying it
// changed; Supersedes is the index of the value it replaces, the one before
// it when the two differ, -1 for none.
type MemoryValue struct {
	Value, Date string
	Records     []string
	Conflict    bool
	Supersedes  int
}

// MemoryDirective is a standing instruction or preference the user stated.
type MemoryDirective struct {
	ID, Date, Text string
	Records        []string
}

// itemID is the id of the position-th consolidated item of a kind named by
// prefix: e (events) or d (directives) (Consolidation).
func itemID(prefix string, position int) string { return prefix + strconv.Itoa(position) }

// Consolidated reads the scope's consolidated memory and its consolidation's
// progress, with pendingIDs records named at most. It makes no model call,
// takes no job lock and writes nothing (as Backlog); a job running meanwhile
// may have moved past part of what it reports. ok is false when the
// configuration does not consolidate (mode timelines or layered).
func (a *Agent) Consolidated(ctx context.Context, scope records.Scope, pendingIDs int) (c Consolidation, ok bool, err error) {
	if !a.config.Consolidation.Timelines() {
		return Consolidation{}, false, nil
	}
	state, _, err := a.overlay.loadTimelines(ctx, scope)
	if err != nil {
		return Consolidation{}, false, err
	}
	c = Consolidation{Watermark: state.Watermark, PendingIDs: []string{}}
	for _, record := range state.Pending {
		if len(c.PendingIDs) < pendingIDs {
			c.PendingIDs = append(c.PendingIDs, record.ID)
		}
	}
	after, ids, err := a.store.Tail(ctx, scope, state.Watermark, max(0, pendingIDs-len(c.PendingIDs)))
	if err != nil {
		return Consolidation{}, false, err
	}
	c.Pending, c.PendingIDs = len(state.Pending)+after, append(c.PendingIDs, ids...)
	m := state.Memory
	_, events := eventThreads(m)
	for _, t := range m.Threads {
		thread := MemoryThread{ID: t.ID, Title: t.Title, Events: len(events[t.ID])}
		if n := len(events[t.ID]); n > 0 {
			thread.First, thread.Last = m.Events[events[t.ID][0]].Date, m.Events[events[t.ID][n-1]].Date
		}
		c.Threads = append(c.Threads, thread)
	}
	for i, e := range m.Events {
		c.Events = append(c.Events, MemoryEvent{ID: itemID("e", i), Thread: e.Thread, Date: e.Date, Text: e.Text, Records: copyIDs(e.Records), Entities: e.Entities})
	}
	for _, key := range m.FactOrder {
		fact := MemoryFact{Key: key}
		for i, v := range m.Facts[key] {
			value := MemoryValue{Value: v.Value, Date: v.Date, Records: copyIDs(v.Records), Conflict: v.Conflict, Supersedes: -1}
			if i > 0 && v.Value != m.Facts[key][i-1].Value {
				value.Supersedes = i - 1
			}
			fact.Values = append(fact.Values, value)
		}
		c.Facts = append(c.Facts, fact)
	}
	for i, d := range m.Directives {
		c.Directives = append(c.Directives, MemoryDirective{ID: itemID("d", i), Date: d.Date, Text: d.Text, Records: copyIDs(d.Records)})
	}
	return c, true, nil
}

// Progress is a scope's records and its consolidation's progress, as the
// inspect service's store rows show them (Agent.Progress).
type Progress struct {
	// Records counts the scope's searchable records, those
	// records.Store.Page lists.
	Records int
	// Consolidates says the configuration consolidates (timelines or
	// layered); Watermark and Pending are then Consolidation's, else 0.
	Consolidates bool
	Watermark    int64
	Pending      int
}

// Progress reads the scope's records and its consolidation's progress
// without decoding the consolidated memory: the state's watermark and the
// records it holds back come from its payload (its digest and format
// checked, as Consolidated checks them), and one statement counts the
// records and those written after the watermark. Like Consolidated, it makes
// no model call, takes no job lock and writes nothing.
func (a *Agent) Progress(ctx context.Context, scope records.Scope) (Progress, error) {
	var p Progress
	held := 0
	if a.config.Consolidation.Timelines() {
		var err error
		if p.Watermark, held, err = a.overlay.loadProgress(ctx, scope); err != nil {
			return Progress{}, err
		}
		p.Consolidates = true
	}
	heads, after, err := a.store.Count(ctx, scope, p.Watermark)
	if err != nil {
		return Progress{}, err
	}
	p.Records = heads
	if p.Consolidates {
		p.Pending = held + after
	}
	return p, nil
}

// StoredRecord is a searchable record as the inspect service lists it: its
// id, the date a read gives it (Evidence.Date, a Search item's created_at),
// the session it was added under (data.session, "" for none) and its text.
type StoredRecord struct{ ID, Date, Session, Text string }

// DecodeRecord reads a record's JSON (records.PageRecord.JSON) as reads do,
// by its exact property names; ok is false when it does not decode.
func DecodeRecord(raw string) (record StoredRecord, ok bool) {
	var value recordValue
	if json.Unmarshal([]byte(raw), &value) != nil {
		return StoredRecord{}, false
	}
	record = StoredRecord{ID: value.ID, Date: evidenceDate(value), Text: value.Content}
	record.Session, _ = value.Data["session"].(string)
	return record, true
}

// copyIDs is a copy of ids, empty rather than nil.
func copyIDs(ids []string) []string { return append([]string{}, ids...) }
