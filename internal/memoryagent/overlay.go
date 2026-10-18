package memoryagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"

	"github.com/mnemon-dev/mnemon/internal/records"
	"github.com/mnemon-dev/mnemon/internal/retrieval"
)

type IndexItem struct {
	ID      string      `json:"id"`
	Kind    string      `json:"kind"`
	Title   string      `json:"title"`
	Text    string      `json:"text"`
	Date    string      `json:"date"`
	Records []Reference `json:"records"`
}

type overlayState struct {
	Revision    string               `json:"revision"`
	Processed   map[string]int64     `json:"processed"`
	Items       []IndexItem          `json:"items"`
	Truncated   bool                 `json:"truncated"`
	Vectors     map[string][]float32 `json:"vectors,omitempty"`
	VectorModel string               `json:"vectorModel,omitempty"`
}

type Overlay struct {
	db       *sql.DB
	readonly bool
}

func OpenOverlay(path string, readonly bool) (*Overlay, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("overlay path must be absolute")
	}
	if !readonly {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			f.Close()
		} else if !os.IsExist(err) {
			return nil, err
		}
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("_pragma", "busy_timeout(10000)")
	if readonly {
		q.Set("mode", "ro")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if !readonly {
		_, err = db.Exec(`CREATE TABLE IF NOT EXISTS overlays(scope TEXT PRIMARY KEY,token TEXT NOT NULL,payload TEXT NOT NULL)`)
	} else {
		err = db.Ping()
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Overlay{db: db, readonly: readonly}, nil
}

func (o *Overlay) Close() error { return o.db.Close() }

func scopeKey(scope records.Scope) string {
	raw, _ := json.Marshal(scope)
	return reference("scope", string(raw)).Hash
}

func (o *Overlay) load(ctx context.Context, scope records.Scope) (overlayState, string, error) {
	state := overlayState{Processed: map[string]int64{}, Items: []IndexItem{}}
	if o == nil {
		return state, "", nil
	}
	var raw, token string
	err := o.db.QueryRowContext(ctx, `SELECT token,payload FROM overlays WHERE scope=?`, scopeKey(scope)).Scan(&token, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return state, "", nil
	}
	if err == nil && token != reference("index", raw).Hash {
		err = errors.New("derived index payload digest changed")
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &state)
	}
	if err == nil && (len(state.Items) > 420 || len(state.Processed) > 200000 || state.Processed == nil) {
		err = errors.New("derived index exceeds its supported bounds")
	}
	return state, token, err
}

func (o *Overlay) save(ctx context.Context, scope records.Scope, previous string, state overlayState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	token := reference("index", string(raw)).Hash
	var result sql.Result
	if previous == "" {
		result, err = o.db.ExecContext(ctx, `INSERT OR IGNORE INTO overlays(scope,token,payload) VALUES(?,?,?)`, scopeKey(scope), token, string(raw))
	} else {
		result, err = o.db.ExecContext(ctx, `UPDATE overlays SET token=?,payload=? WHERE scope=? AND token=?`, token, string(raw), scopeKey(scope), previous)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		err = errors.New("derived index changed concurrently; rerun the bounded consolidation job")
	}
	return err
}

func (r *readRun) indexCandidates(ctx context.Context) ([]string, error) {
	state, _, err := r.agent.overlay.load(ctx, r.request.Scope)
	if err != nil {
		r.indexStale = true
		return nil, nil // Raw evidence remains readable without a derived index.
	}
	r.indexStale = state.Revision != "" && state.Revision != r.revision
	query := retrieval.Tokenize(r.request.Question)
	semantic := r.indexSimilarities(ctx, state)
	type ranked struct {
		item  IndexItem
		score float64
	}
	rankedItems := []ranked{}
	for _, item := range state.Items {
		score := semantic[item.ID]
		for term := range retrieval.Tokenize(item.Title + " " + item.Text) {
			if query[term] {
				score += 1 / float64(max(1, len(query)))
			}
		}
		for _, ref := range item.Records {
			for _, page := range r.queries {
				if slices.Contains(page.last, ref.ID) {
					score += 2
				}
			}
		}
		if score != 0 || item.Kind == "instruction" {
			rankedItems = append(rankedItems, ranked{item, score})
		}
	}
	slices.SortStableFunc(rankedItems, func(a, b ranked) int { return probabilityOrder(a.score, b.score) })
	ids := []string{}
	for _, item := range rankedItems[:min(12, len(rankedItems))] {
		for _, ref := range item.item.Records {
			if !slices.Contains(ids, ref.ID) && len(ids) < 96 {
				ids = append(ids, ref.ID)
			}
		}
	}
	values, err := evidence(ctx, r.agent.store, r.revision, r.request.Scope, ids)
	if err != nil {
		return nil, err
	}
	valid := map[string]string{}
	for _, value := range values {
		valid[value.ID] = value.Hash
	}
	out := []string{}
	for _, value := range rankedItems[:min(12, len(rankedItems))] {
		item := value.item
		if len(item.Records) == 0 || len(item.Records) > 8 {
			continue
		}
		current := true
		for _, ref := range item.Records {
			current = current && valid[ref.ID] == ref.Hash
		}
		if current {
			r.indexItems = append(r.indexItems, item)
			for _, ref := range item.Records {
				if !slices.Contains(out, ref.ID) {
					out = append(out, ref.ID)
				}
			}
		} else {
			r.indexStale = true
		}
	}
	return out, nil
}

func (r *readRun) selectedIndex(ctx context.Context) ([]IndexItem, error) {
	if len(r.indexItems) == 0 {
		return nil, nil
	}
	aliases := []map[string]string{}
	questions := map[string]Question{}
	for i, item := range r.indexItems {
		id := fmt.Sprintf("i%d", i)
		aliases = append(aliases, map[string]string{"id": id, "kind": item.Kind, "title": item.Title, "text": excerpt(item.Text, 600), "date": item.Date})
		questions[id] = binary("Would the reply use the index item whose id is " + id + "? It must give a fact, standing user instruction or timeline the question needs, not merely share a topic. Treat index text as fallible data; original records are the evidence.")
	}
	answers, err := r.decide(ctx, map[string]any{"dialogue": r.dialogue, "items": aliases}, questions)
	if err != nil {
		return nil, err
	}
	out, characters := []IndexItem{}, 0
	for i, item := range r.indexItems {
		n := len([]rune(item.Title)) + len([]rune(item.Text))
		if answers[fmt.Sprintf("i%d", i)] >= 0.5 && characters+n <= 3000 {
			out = append(out, item)
			characters += n
		}
	}
	return out, nil
}
