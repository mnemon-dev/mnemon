package records

import (
	"context"
	"encoding/json"
	"testing"
)

// Sequence gives each record's write position: the order records were
// first written, kept across later versions.
func TestSequence(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	first, err := s.Put(ctx, PutRequest{Upsert: []Upsert{indexedRecord("z", "z", nil), indexedRecord("a", "a", nil)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Put(ctx, PutRequest{Upsert: []Upsert{indexedRecord("m", "m", nil)}}); err != nil {
		t.Fatal(err)
	}
	// A new version of z keeps z's place.
	changed := indexedRecord("z", "z changed", nil)
	var value map[string]any
	json.Unmarshal([]byte(changed.RecordJSON), &value)
	value["version"] = 2
	raw, _ := json.Marshal(value)
	changed.RecordJSON = string(raw)
	if _, err = s.Put(ctx, PutRequest{Upsert: []Upsert{changed}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Sequence(ctx, "", []string{"m", "a", "z", "missing"})
	if err != nil || len(got) != 3 || !(got["z"] < got["a"] && got["a"] < got["m"]) {
		t.Fatal(got, err)
	}
	if got, err = s.Sequence(ctx, first, []string{"m", "a"}); err != nil || len(got) != 1 || got["a"] == 0 {
		t.Fatal(got, err)
	}
	if _, err = s.Sequence(ctx, "nonsense", []string{"a"}); err == nil {
		t.Fatal("unknown revision accepted")
	}
}
