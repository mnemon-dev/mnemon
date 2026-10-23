package records

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// The revision digest is sha256("[" + join(",", record JSON of the current records in seq order) + "]"). Put keeps
// the SHA-256 state of that input without its closing "]" in meta, under digest/state and digest/state-revision, and
// a Put that only appends records resumes it. These tests check every digest against a from-scratch recomputation,
// check the saved state against an independently computed one, and count the Puts' full reloads of the records.

// currentRaws is the record JSON of the current records in seq order, read the way a from-scratch recomputation
// reads them.
func currentRaws(t *testing.T, s *Store) []string {
	t.Helper()
	ctx := context.Background()
	rev, _, err := revision(ctx, s.db, "")
	if err != nil {
		t.Fatal(err)
	}
	all, err := load(ctx, s.db, rev)
	if err != nil {
		t.Fatal(err)
	}
	raws := make([]string, len(all))
	for i, r := range all {
		raws[i] = r.Raw
	}
	return raws
}

// fullDigest is the digest of the records recomputed from scratch, as Put computed it before it kept a digest state.
func fullDigest(raws []string) string {
	return textHash("[" + strings.Join(raws, ",") + "]")
}

// stateOf is the saved form of the SHA-256 state after hashing prefix.
func stateOf(t *testing.T, prefix string) string {
	t.Helper()
	h := sha256.New()
	h.Write([]byte(prefix))
	state, err := h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(state)
}

func metaRow(t *testing.T, s *Store, key string) (string, bool) {
	t.Helper()
	var value string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return value, true
}

func setMetaRow(t *testing.T, s *Store, key, value string) {
	t.Helper()
	if _, err := s.db.Exec("INSERT INTO meta VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
		t.Fatal(err)
	}
}

func deleteMetaRows(t *testing.T, s *Store, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, err := s.db.Exec("DELETE FROM meta WHERE key=?", key); err != nil {
			t.Fatal(err)
		}
	}
}

// countFullLoads counts the full reloads of the current records that Put makes from now until the test ends.
func countFullLoads(t *testing.T) *atomic.Int64 {
	t.Helper()
	var calls atomic.Int64
	original := loadCurrent
	loadCurrent = func(ctx context.Context, db reader, rev int64) ([]record, error) {
		calls.Add(1)
		return original(ctx, db, rev)
	}
	t.Cleanup(func() { loadCurrent = original })
	return &calls
}

func mustPut(t *testing.T, s *Store, req PutRequest) string {
	t.Helper()
	hash, err := s.Put(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// putExpecting puts req and fails unless the Put reloaded the current records wantLoads times, returned the digest a
// from-scratch recomputation gives, made that digest readable as the new revision, and left the digest state of the
// new revision in meta: its revision label, and the SHA-256 state of the current records' prefix. It returns the digest.
func putExpecting(t *testing.T, s *Store, loads *atomic.Int64, req PutRequest, wantLoads int64) string {
	t.Helper()
	before := loads.Load()
	hash := mustPut(t, s, req)
	if got := loads.Load() - before; got != wantLoads {
		t.Fatalf("Put reloaded the current records %d times, want %d", got, wantLoads)
	}
	raws := currentRaws(t, s)
	if want := fullDigest(raws); hash != want {
		t.Fatalf("Put returned digest %s, a recomputation gives %s", hash, want)
	}
	if stored, _ := metaRow(t, s, "digest"); stored != hash {
		t.Fatalf("meta digest is %s, Put returned %s", stored, hash)
	}
	snapshot, err := s.Get(context.Background(), GetRequest{Revision: hash})
	if err != nil || snapshot.Revision != hash || !reflect.DeepEqual(snapshot.Records, raws) {
		t.Fatalf("the returned digest does not read back the current records: %v", err)
	}
	number, _ := metaRow(t, s, "revision")
	if label, ok := metaRow(t, s, "digest/state-revision"); !ok || label != number {
		t.Fatalf("digest/state-revision is %q (saved %v), want the revision number %q", label, ok, number)
	}
	want := stateOf(t, "["+strings.Join(raws, ","))
	if saved, ok := metaRow(t, s, "digest/state"); !ok || saved != want {
		t.Fatalf("digest/state is %q (saved %v), want the state of the current records' prefix %q", saved, ok, want)
	}
	return hash
}

// varyingContent changes length, so the saved state ends at every offset of SHA-256's 64-byte block, and carries
// multi-byte characters.
func varyingContent(rng *rand.Rand, step int) string {
	return fmt.Sprintf("step %d %s", step, strings.Repeat("é日", rng.IntN(40)))
}

func TestDigestMatchesFullRecompute(t *testing.T) {
	s := testStore(t)
	loads := countFullLoads(t)
	rng := rand.New(rand.NewPCG(1, 2))
	var live, gone []string // ids with and without a current record
	fresh, updates, deletes, revived := 0, 0, 0, 0
	appended := func(step, n int) []Upsert {
		out := make([]Upsert, n)
		for i := range out {
			var id string
			if len(gone) > 0 && rng.IntN(4) == 0 { // a deleted id comes back as a new record, last in seq order
				k := rng.IntN(len(gone))
				id = gone[k]
				gone = slices.Delete(gone, k, k+1)
				revived++
			} else {
				id = fmt.Sprintf("r%04d", fresh)
				fresh++
			}
			live = append(live, id)
			out[i] = sample(id, varyingContent(rng, step))
		}
		return out
	}
	var hash string
	for step := 0; step < 400; step++ {
		var req PutRequest
		wantLoads := int64(0) // an append resumes the saved state
		switch roll := rng.IntN(100); {
		case roll < 70 || len(live) == 0:
			req.Upsert = appended(step, 1+rng.IntN(5))
		case roll < 85:
			req.Upsert = []Upsert{sample(live[rng.IntN(len(live))], varyingContent(rng, step))}
			if rng.IntN(3) == 0 { // an update that also appends
				req.Upsert = append(req.Upsert, appended(step, 1)...)
			}
			wantLoads = 1
			updates++
		default:
			k := rng.IntN(len(live))
			req.Delete = []string{live[k]}
			gone = append(gone, live[k])
			live = slices.Delete(live, k, k+1)
			if rng.IntN(3) == 0 { // a delete that also appends
				req.Upsert = appended(step, 1+rng.IntN(2))
			}
			wantLoads = 1
			deletes++
		}
		if step == 0 {
			wantLoads = 1 // the first Put finds no digest state
		}
		hash = putExpecting(t, s, loads, req, wantLoads)
	}
	if updates == 0 || deletes == 0 || revived == 0 {
		t.Fatalf("the sequence did not exercise every operation: %d updates, %d deletes, %d revived ids", updates, deletes, revived)
	}
	snapshot, err := s.Get(context.Background(), GetRequest{})
	if err != nil || snapshot.Revision != hash || len(snapshot.Records) != len(live) {
		t.Fatalf("after the sequence: revision %s (last digest %s), %d records (want %d): %v", snapshot.Revision, hash, len(snapshot.Records), len(live), err)
	}
}

func TestAppendDoesNotLoadAllRecords(t *testing.T) {
	s := testStore(t)
	loads := countFullLoads(t)
	putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("seed", "first")}}, 1) // no state yet: recomputed, then saved
	for i := 0; i < 10; i++ {
		putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample(fmt.Sprintf("a%d", i), "appended")}}, 0)
	}
	putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("a3", "updated")}}, 1)
	putExpecting(t, s, loads, PutRequest{Delete: []string{"a4"}}, 1)
	putExpecting(t, s, loads, PutRequest{Delete: []string{"never-written"}}, 1) // any delete recomputes
	putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("a4", "back")}}, 0)
	putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("b", "appended")}}, 0)
}

func TestDigestResumesAfterOldWriter(t *testing.T) {
	for _, tc := range []struct {
		name  string
		leave func(t *testing.T, s *Store)
	}{
		{"store no newer build has written", func(t *testing.T, s *Store) {
			deleteMetaRows(t, s, "digest/state", "digest/state-revision") // an older build never wrote them
		}},
		{"state of an earlier revision", func(t *testing.T, s *Store) {
			state, _ := metaRow(t, s, "digest/state")
			label, _ := metaRow(t, s, "digest/state-revision")
			mustPut(t, s, PutRequest{Upsert: []Upsert{sample("older-build", "wrote this revision")}})
			setMetaRow(t, s, "digest/state", state) // an older build leaves the keys as it found them
			setMetaRow(t, s, "digest/state-revision", label)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			loads := countFullLoads(t)
			for _, id := range []string{"a", "b", "c"} {
				mustPut(t, s, PutRequest{Upsert: []Upsert{sample(id, "before")}})
			}
			tc.leave(t, s)
			putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("d", "after")}}, 1)
			putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("e", "after")}}, 0)
		})
	}
}

func TestDigestSelfCheckRejectsBadState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state func(t *testing.T, raws []string) string
	}{
		{"state of a different prefix", func(t *testing.T, _ []string) string {
			return stateOf(t, `[{"id":"elsewhere"},{"id":"else"}`)
		}},
		{"state of the records before the last one", func(t *testing.T, raws []string) string {
			return stateOf(t, "["+strings.Join(raws[:len(raws)-1], ","))
		}},
		{"state that is not base64", func(*testing.T, []string) string { return "!!not base64!!" }},
		{"bytes that are not a SHA-256 state", func(*testing.T, []string) string {
			return base64.StdEncoding.EncodeToString([]byte("short"))
		}},
		{"empty state", func(*testing.T, []string) string { return "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			loads := countFullLoads(t)
			for _, id := range []string{"a", "b", "c"} {
				mustPut(t, s, PutRequest{Upsert: []Upsert{sample(id, "before")}})
			}
			setMetaRow(t, s, "digest/state", tc.state(t, currentRaws(t, s))) // its revision label still says "current"
			putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("d", "after")}}, 1)
			putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("e", "after")}}, 0)
		})
	}
}

func TestDigestIgnoresStateOfAnotherRevision(t *testing.T) {
	for _, tc := range []struct {
		name  string
		label func(current int) string
	}{
		{"earlier revision", func(current int) string { return strconv.Itoa(current - 1) }},
		{"later revision", func(current int) string { return strconv.Itoa(current + 1) }},
		{"not a number", func(int) string { return "x" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			loads := countFullLoads(t)
			for _, id := range []string{"a", "b", "c"} {
				mustPut(t, s, PutRequest{Upsert: []Upsert{sample(id, "before")}})
			}
			number, _ := metaRow(t, s, "revision")
			current, err := strconv.Atoi(number)
			if err != nil {
				t.Fatal(err)
			}
			// The state itself is right for the current records; only its revision label differs.
			setMetaRow(t, s, "digest/state-revision", tc.label(current))
			putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("d", "after")}}, 1)
			putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("e", "after")}}, 0)
		})
	}
}

func TestDigestAppendToEmptiedStore(t *testing.T) {
	s := testStore(t)
	loads := countFullLoads(t)
	b, c := sample("b", "first after empty"), sample("c", "second")
	mustPut(t, s, PutRequest{Upsert: []Upsert{sample("a", "to be deleted")}})
	if got := mustPut(t, s, PutRequest{Delete: []string{"a"}}); got != emptyRevision {
		t.Fatalf("a store without records has digest %s, want %s", got, emptyRevision)
	}
	if got := putExpecting(t, s, loads, PutRequest{}, 0); got != emptyRevision {
		t.Fatalf("an empty Put on an empty store: digest %s, want %s", got, emptyRevision)
	}
	// The first record of an empty store takes no comma.
	if got := putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{b}}, 0); got != textHash("["+b.RecordJSON+"]") {
		t.Fatalf("first record after empty: digest %s", got)
	}
	if got := putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{c}}, 0); got != textHash("["+b.RecordJSON+","+c.RecordJSON+"]") {
		t.Fatalf("second record: digest %s", got)
	}
	// A Put that changes nothing is an append of no records: the digest stays, the revision moves on.
	if got := putExpecting(t, s, loads, PutRequest{}, 0); got != textHash("["+b.RecordJSON+","+c.RecordJSON+"]") {
		t.Fatalf("empty Put: digest %s", got)
	}
	putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("d", "after the empty Put")}}, 0)
}

// The digest covers each record's JSON exactly as TS sent it. A Put that resumes the digest hashes the bytes it was
// given, so they must be the bytes a later recomputation reads back: whitespace around the JSON, invalid UTF-8 inside
// a string and unusual characters included.
func TestDigestHashesRecordJSONVerbatim(t *testing.T) {
	s := testStore(t)
	loads := countFullLoads(t)
	seed := sample("seed", "first")
	mustPut(t, s, PutRequest{Upsert: []Upsert{seed}})
	sent := []string{seed.RecordJSON}
	for i, content := range []string{"\xff\xfe broken \xc3\x28", `tab\there \u0000 \ud800`, "\u2028 line separator", "e\u0301 and \ufffd"} {
		u := sample(fmt.Sprintf("odd%d", i), content)
		u.RecordJSON = " \n\t" + u.RecordJSON + "\r\n "
		sent = append(sent, u.RecordJSON)
		if got := putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{u}}, 0); got != fullDigest(sent) {
			t.Fatalf("record %d: digest %s is not the hash of the JSON as sent", i, got)
		}
	}
}

func TestDigestFastPathMatchesTSGolden(t *testing.T) {
	var fixture struct {
		Revision string
		Upsert   []Upsert
	}
	data, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	loads := countFullLoads(t)
	var hash string
	for _, part := range [][]Upsert{fixture.Upsert[:2], fixture.Upsert[2:3], fixture.Upsert[3:]} {
		hash = mustPut(t, s, PutRequest{Upsert: part})
	}
	if hash != fixture.Revision {
		t.Fatalf("appending the records in three Puts gives digest %s, TS computed %s", hash, fixture.Revision)
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("three appends reloaded the current records %d times, want 1 (the first Put, without a state)", got)
	}
}

func TestDigestStateSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, PutRequest{Upsert: []Upsert{sample("a", "one"), sample("b", "two")}})
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(dir, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	loads := countFullLoads(t)
	putExpecting(t, s, loads, PutRequest{Upsert: []Upsert{sample("c", "three")}}, 0)
}
