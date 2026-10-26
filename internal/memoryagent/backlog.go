package memoryagent

import (
	"context"
	"encoding/json"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// Backlog is what a scope's timelines (or layered) consolidation has yet to
// fold: the pending records of its state and the records written after its
// watermark, and their characters as a job reads them (UTF-16 units of
// header and text, the units that bound a job's window: readNewRecords). A
// record after the watermark that a job reads past without keeping it
// counts as a record of no characters.
type Backlog struct {
	Records    int
	Characters int64
}

// Backlog measures the scope's backlog without running a job: it makes no
// model call, takes no job lock and writes nothing. It reads the stored
// state, then the records after its watermark, at the store's current
// revision, so a job running meanwhile may already have moved past part of
// what it counts. Without a timelines state there is none.
func (a *Agent) Backlog(ctx context.Context, scope records.Scope) (Backlog, error) {
	settings, reading := a.config.Consolidation, a.roleReading()
	if !settings.Timelines() || a.overlay == nil {
		return Backlog{}, nil
	}
	state, _, err := a.overlay.loadTimelines(ctx, scope)
	if err != nil {
		return Backlog{}, err
	}
	backlog := Backlog{Records: len(state.Pending)}
	for _, record := range state.Pending {
		backlog.Characters += int64(jsLen(record.Header) + jsLen(record.Text))
	}
	revision, heads, err := a.store.Heads(ctx, "", scope)
	if err != nil {
		return Backlog{}, err
	}
	fresh := []string{}
	for _, head := range heads {
		if head.Version > state.Watermark {
			fresh = append(fresh, head.ID)
		}
	}
	backlog.Records += len(fresh)
	for start := 0; start < len(fresh); start += 500 {
		snapshot, err := a.store.Get(ctx, records.GetRequest{Revision: revision, IDs: fresh[start:min(start+500, len(fresh))]})
		if err != nil {
			return Backlog{}, err
		}
		for _, raw := range snapshot.Records {
			var value recordValue
			if json.Unmarshal([]byte(raw), &value) != nil {
				continue
			}
			if header, body, ok := readable(reading, value.Title, value.Content, value.Data, nil); ok {
				backlog.Characters += int64(jsLen(header) + jsLen(body))
			}
		}
	}
	return backlog, nil
}

// JobWindow is the most characters of pending records one consolidation job
// reads (MaxBatches × BatchCharacters, the bound of readNewRecords): 160,000
// with the defaults; 0 without a timelines state.
func (a *Agent) JobWindow() int64 {
	settings := a.config.Consolidation
	if !settings.Timelines() {
		return 0
	}
	return int64(settings.MaxBatches) * int64(settings.BatchCharacters)
}
