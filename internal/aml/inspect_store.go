package aml

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

// readStore is a user's store opened as a reader for one request, and its
// agent: no file of it is written, created or checkpointed.
type readStore struct {
	key   string
	store *records.Store
	agent *memoryagent.Agent
}

// openStore opens the store with key as a reader (records.OpenReader,
// memoryagent.OpenReader). A missing store is an error wrapping
// fs.ErrNotExist. Close it when the request's reads end, before answering.
func (i *Inspector) openStore(key string) (*readStore, error) {
	dir := i.storeDir(key)
	store, err := records.OpenReader(dir)
	if err != nil {
		return nil, err
	}
	agent, err := memoryagent.OpenReader(store, dir, i.config.MemoryConfig, i.models)
	if err != nil {
		store.Close()
		return nil, err
	}
	i.open.Add(1)
	return &readStore{key: key, store: store, agent: agent}, nil
}

// close closes the store after checking what it read stands: it is
// records.ErrChanged when a file read quiescent changed meanwhile, and what
// was read must not be answered.
func (i *Inspector) closeStore(s *readStore) error {
	err := errors.Join(s.store.Unchanged(), s.agent.Unchanged())
	s.agent.Close()
	s.store.Close()
	i.open.Add(-1)
	return err
}

// readFrom opens the store with key as a reader, runs read on it, and closes
// it, checking that what it read stands (closeStore); a panic in read is its
// error. A missing store is an error wrapping fs.ErrNotExist.
func (i *Inspector) readFrom(key string, read func(*readStore) error) (err error) {
	s, err := i.openStore(key)
	if err != nil {
		return err
	}
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("panic: %v\n%s", value, debug.Stack())
		}
		err = errors.Join(err, i.closeStore(s))
	}()
	return read(s)
}

// storeFailure answers a store that could not be read: 404 store_not_found
// for none, 503 store_busy when it changed under the read or SQLite was
// busy (retry), 503 busy when the request was canceled, 500 internal
// otherwise (logged by its prefix, without a path).
func (i *Inspector) storeFailure(w http.ResponseWriter, r *http.Request, key, operation string, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		inspectError(w, http.StatusNotFound, "store_not_found")
	case storeBusy(err):
		w.Header().Set("Retry-After", storeBusyRetry)
		inspectError(w, http.StatusServiceUnavailable, "store_busy")
	case r.Context().Err() != nil:
		w.Header().Set("Retry-After", busyRetry)
		inspectError(w, http.StatusServiceUnavailable, "busy")
	default:
		i.log.Error(fmt.Sprintf("%s failed (store %s): %v", operation, short(key), withoutPath(err)))
		inspectError(w, http.StatusInternalServerError, "internal")
	}
}

// storeBusy reports whether err says a store could not be read consistently
// now, but may be soon (records.Retryable).
func storeBusy(err error) bool { return records.Retryable(err) }

// storeKey is the {key} of a request's path: 32 lowercase hex digits; on
// failure it has answered 400 invalid (key).
func storeKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.PathValue("key")
	if !isUserKey(key) {
		invalid(w, "key")
		return "", false
	}
	noteStore(r, key)
	return key, true
}

// pageQuery reads a page's after (an integer ≥ 0, absent: -1) and limit
// (1..most, absent: fallback); on failure it has answered 400 invalid.
func pageQuery(w http.ResponseWriter, query url.Values, fallback, most int) (after int64, limit int, ok bool) {
	after, limit = -1, fallback
	if query.Has("after") {
		n, err := strconv.ParseInt(query.Get("after"), 10, 64)
		if err != nil || n < 0 {
			invalid(w, "after")
			return 0, 0, false
		}
		after = n
	}
	if query.Has("limit") {
		n, err := strconv.Atoi(query.Get("limit"))
		if err != nil || n < 1 || n > most {
			invalid(w, "limit")
			return 0, 0, false
		}
		limit = n
	}
	return after, limit, true
}

// inspectStoreRow is a store of GET /v1/stores.
type inspectStoreRow struct {
	Store         string              `json:"store"`
	Key           string              `json:"key"`
	Records       *int                `json:"records"`
	Bytes         int64               `json:"bytes"`
	Modified      string              `json:"modified"`
	Consolidation *storeConsolidation `json:"consolidation"`
	Error         string              `json:"error,omitempty"`
}

type storeConsolidation struct {
	Watermark int64 `json:"watermark"`
	Pending   int   `json:"pending"`
}

// serveStores answers GET /v1/stores?limit&after: the data root's stores in
// key order, as the overview lists them (readStores, storesAfter), each
// opened to count its records and read its consolidation's progress. A
// page's work is bounded: a row is one state read and one scan of its
// store's records (memoryagent.Agent.Progress), at most limit rows, and the
// page stops opening stores once its request has ended (the client left,
// or the service stops), answering 503 busy.
func (i *Inspector) serveStores(w http.ResponseWriter, r *http.Request) {
	limit, after, err := storesQuery(r.URL.Query(), defaultStoresPage)
	if err != nil {
		var field *fieldError
		errors.As(err, &field)
		invalid(w, field.field)
		return
	}
	stores, err := readStores(r.Context(), i.config.DataRoot)
	if err != nil {
		i.storeFailure(w, r, "", "the store scan", err)
		return
	}
	page, next := storesAfter(stores, after, limit)
	rows := make([]inspectStoreRow, 0, len(page))
	for _, stat := range page {
		if r.Context().Err() != nil {
			break
		}
		rows = append(rows, i.storeRow(r.Context(), stat))
	}
	if err := r.Context().Err(); err != nil {
		i.storeFailure(w, r, "", "the store page", err)
		return
	}
	inspectJSON(w, http.StatusOK, map[string]any{"total": len(stores), "stores": rows, "next": next})
}

// storeRow is a store's row: what the scan measured, and its records and
// consolidation's progress, read without decoding its consolidated memory,
// or why they could not be read.
func (i *Inspector) storeRow(ctx context.Context, stat storeStat) inspectStoreRow {
	row := inspectStoreRow{Store: short(stat.key), Key: stat.key, Bytes: stat.bytes, Modified: stat.modified.UTC().Format(time.RFC3339)}
	var p memoryagent.Progress
	err := i.readFrom(stat.key, func(s *readStore) (err error) {
		p, err = s.agent.Progress(ctx, records.Scope{})
		return err
	})
	switch {
	case err != nil && ctx.Err() != nil:
		row.Error = "store_busy" // the page ends unanswered: nothing to log
		return row
	case err != nil:
		row.Error = i.rowError(stat.key, err)
		return row
	}
	row.Records = &p.Records
	if p.Consolidates {
		row.Consolidation = &storeConsolidation{Watermark: p.Watermark, Pending: p.Pending}
	}
	return row
}

// rowError is the error of a store row: store_busy (retry) or
// store_unreadable, which is logged by the store's prefix.
func (i *Inspector) rowError(key string, err error) string {
	if storeBusy(err) {
		return "store_busy"
	}
	i.log.Warn(fmt.Sprintf("store unreadable (store %s): %v", short(key), withoutPath(err)))
	return "store_unreadable"
}

// inspectRecord is a record of GET /v1/stores/{key}/records.
type inspectRecord struct {
	ID        string `json:"id"`
	Seq       int64  `json:"seq"`
	CreatedAt string `json:"created_at"`
	Session   string `json:"session"`
	Content   string `json:"content"`
}

// serveRecords answers GET /v1/stores/{key}/records?after&limit: a page of
// the store's searchable records in write order (records.Store.Page).
func (i *Inspector) serveRecords(w http.ResponseWriter, r *http.Request) {
	key, ok := storeKey(w, r)
	if !ok {
		return
	}
	after, limit, ok := pageQuery(w, r.URL.Query(), recordsPageDefault, recordsPageMax)
	if !ok {
		return
	}
	var page []records.PageRecord
	var more bool
	err := i.readFrom(key, func(s *readStore) (err error) {
		page, more, err = s.store.Page(r.Context(), records.Scope{}, max(after, 0), limit)
		return err
	})
	if err != nil {
		i.storeFailure(w, r, key, "records", err)
		return
	}
	out := make([]inspectRecord, 0, len(page))
	for _, raw := range page {
		record, ok := recordOf(raw)
		if !ok {
			i.storeFailure(w, r, key, "records", errUndecodable)
			return
		}
		out = append(out, record)
	}
	var next *int64
	if more {
		next = &out[len(out)-1].Seq
	}
	inspectJSON(w, http.StatusOK, map[string]any{"records": out, "next": next})
}

// errUndecodable is the error of a stored record that does not decode.
var errUndecodable = errors.New("a stored record does not decode")

// recordOf is a record of a page as the API lists it.
func recordOf(raw records.PageRecord) (inspectRecord, bool) {
	record, ok := memoryagent.DecodeRecord(raw.JSON)
	return inspectRecord{ID: record.ID, Seq: raw.Seq, CreatedAt: record.Date, Session: record.Session, Content: record.Text}, ok
}

// lookupIDMax bounds the {id} of a lookup, in bytes.
const lookupIDMax = 256

// lookupID is the {id} of a lookup's path, decoded: 1 to lookupIDMax bytes,
// no control character; on failure it has answered 400 invalid (id). Like
// content, it is never logged.
func lookupID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if id == "" || len(id) > lookupIDMax || strings.ContainsFunc(id, unicode.IsControl) {
		invalid(w, "id")
		return "", false
	}
	return id, true
}

// serveRecord answers GET /v1/stores/{key}/records/{id}: the record with id
// as the store's records page lists it (records.Store.Record), or 404
// record_not_found when the page lists none with that id.
func (i *Inspector) serveRecord(w http.ResponseWriter, r *http.Request) {
	key, ok := storeKey(w, r)
	if !ok {
		return
	}
	id, ok := lookupID(w, r)
	if !ok {
		return
	}
	var raw records.PageRecord
	var found bool
	err := i.readFrom(key, func(s *readStore) (err error) {
		raw, found, err = s.store.Record(r.Context(), records.Scope{}, id)
		return err
	})
	if err != nil {
		i.storeFailure(w, r, key, "record", err)
		return
	}
	if !found {
		inspectError(w, http.StatusNotFound, "record_not_found")
		return
	}
	record, ok := recordOf(raw)
	if !ok {
		i.storeFailure(w, r, key, "record", errUndecodable)
		return
	}
	inspectJSON(w, http.StatusOK, map[string]any{"record": record})
}

// memorySections are the sections of GET /v1/stores/{key}/memory.
var memorySections = []string{"threads", "events", "facts", "directives"}

// inspectPending is the records not folded yet: their count and the first
// pendingIDsMax.
type inspectPending struct {
	Count int      `json:"count"`
	IDs   []string `json:"ids"`
}

// serveMemory answers GET /v1/stores/{key}/memory?section&after&limit: the
// counts of the store's consolidated memory, its consolidation's progress,
// and a page of one section.
func (i *Inspector) serveMemory(w http.ResponseWriter, r *http.Request) {
	key, ok := storeKey(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	section := "threads"
	if query.Has("section") {
		if section = query.Get("section"); !slices.Contains(memorySections, section) {
			invalid(w, "section")
			return
		}
	}
	after, limit, ok := pageQuery(w, query, memoryPageDefault, memoryPageMax)
	if !ok {
		return
	}
	var c memoryagent.Consolidation
	var consolidates bool
	err := i.readFrom(key, func(s *readStore) (err error) {
		c, consolidates, err = s.agent.Consolidated(r.Context(), records.Scope{}, pendingIDsMax)
		return err
	})
	if err != nil {
		i.storeFailure(w, r, key, "memory", err)
		return
	}
	items := memoryItems(c, section)
	start := int(after + 1)
	end := min(start+limit, len(items))
	start = min(start, end)
	answer := map[string]any{
		"counts":  map[string]int{"threads": len(c.Threads), "events": len(c.Events), "facts": len(c.Facts), "directives": len(c.Directives)},
		"section": section, "items": items[start:end], "watermark": nil, "pending": nil, "next": nil,
	}
	if consolidates {
		answer["watermark"], answer["pending"] = c.Watermark, inspectPending{Count: c.Pending, IDs: c.PendingIDs}
	}
	if end < len(items) {
		answer["next"] = end - 1
	}
	inspectJSON(w, http.StatusOK, answer)
}

// serveMemoryItem answers GET /v1/stores/{key}/memory/{section}/{id}: the
// item with id as the section's page lists it, or 404 item_not_found when
// the section has none.
func (i *Inspector) serveMemoryItem(w http.ResponseWriter, r *http.Request) {
	key, ok := storeKey(w, r)
	if !ok {
		return
	}
	section := r.PathValue("section")
	if !slices.Contains(memorySections, section) {
		invalid(w, "section")
		return
	}
	id, ok := lookupID(w, r)
	if !ok {
		return
	}
	var c memoryagent.Consolidation
	err := i.readFrom(key, func(s *readStore) (err error) {
		c, _, err = s.agent.Consolidated(r.Context(), records.Scope{}, 0)
		return err
	})
	if err != nil {
		i.storeFailure(w, r, key, "memory item", err)
		return
	}
	for _, item := range memoryItems(c, section) {
		if item.itemID() == id {
			inspectJSON(w, http.StatusOK, map[string]any{"section": section, "item": item})
			return
		}
	}
	inspectError(w, http.StatusNotFound, "item_not_found")
}

// The items of the memory sections, in the contract's field order.
type (
	memoryThread struct {
		Position int    `json:"position"`
		ID       string `json:"id"`
		Title    string `json:"title"`
		Events   int    `json:"events"`
		First    string `json:"first"`
		Last     string `json:"last"`
	}
	memoryEvent struct {
		Position int      `json:"position"`
		ID       string   `json:"id"`
		Thread   string   `json:"thread"`
		Date     string   `json:"date"`
		Text     string   `json:"text"`
		Records  []string `json:"records"`
		Entities []string `json:"entities,omitempty"`
	}
	memoryFact struct {
		Position int           `json:"position"`
		ID       string        `json:"id"`
		Key      string        `json:"key"`
		Values   []memoryValue `json:"values"`
	}
	memoryValue struct {
		Value      string   `json:"value"`
		Date       string   `json:"date"`
		Records    []string `json:"records"`
		Conflict   bool     `json:"conflict"`
		Supersedes *int     `json:"supersedes"`
	}
	memoryDirective struct {
		Position int      `json:"position"`
		ID       string   `json:"id"`
		Date     string   `json:"date"`
		Text     string   `json:"text"`
		Records  []string `json:"records"`
	}
)

// memoryItem is an item of a memory section, with its id.
type memoryItem interface{ itemID() string }

func (t memoryThread) itemID() string    { return t.ID }
func (e memoryEvent) itemID() string     { return e.ID }
func (f memoryFact) itemID() string      { return f.ID }
func (d memoryDirective) itemID() string { return d.ID }

// memoryItems is a section of the consolidated memory as the API lists it.
func memoryItems(c memoryagent.Consolidation, section string) []memoryItem {
	items := []memoryItem{}
	switch section {
	case "threads":
		for p, t := range c.Threads {
			items = append(items, memoryThread{Position: p, ID: t.ID, Title: t.Title, Events: t.Events, First: t.First, Last: t.Last})
		}
	case "events":
		for p, e := range c.Events {
			items = append(items, memoryEvent{Position: p, ID: e.ID, Thread: e.Thread, Date: e.Date, Text: e.Text, Records: e.Records, Entities: e.Entities})
		}
	case "facts":
		for p, f := range c.Facts {
			fact := memoryFact{Position: p, ID: f.Key, Key: f.Key, Values: []memoryValue{}}
			for _, v := range f.Values {
				value := memoryValue{Value: v.Value, Date: v.Date, Records: v.Records, Conflict: v.Conflict}
				if v.Supersedes >= 0 {
					value.Supersedes = &v.Supersedes
				}
				fact.Values = append(fact.Values, value)
			}
			items = append(items, fact)
		}
	case "directives":
		for p, d := range c.Directives {
			items = append(items, memoryDirective{Position: p, ID: d.ID, Date: d.Date, Text: d.Text, Records: d.Records})
		}
	}
	return items
}
