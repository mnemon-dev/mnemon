package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/graph"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/search"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

func TestNativeServiceParityAndExternalWrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "native")
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	first := &model.Insight{ID: "first", Content: "Redis cache port", Category: model.CategoryFact, Importance: 3, Entities: []string{"Redis"}, Tags: []string{}, CreatedAt: now, UpdatedAt: now}
	if err = db.InsertInsight(first); err != nil {
		t.Fatal(err)
	}
	all, _ := db.GetAllActiveInsights()
	want, err := search.IntentAwareRecallFrom(db, all, "Redis", nil, graph.ExtractEntitiesIndexed("Redis", store.KnownEntities(all)), 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := New(db, nil)
	value, err := service.Dispatch(context.Background(), []byte(`{"operation":"native.recall","query":"Redis"}`))
	if err != nil || !reflect.DeepEqual(value, want) {
		t.Fatalf("native recall algorithm drift: %#v %#v %v", value, want, err)
	}
	// A separate legacy writer uses the same old database while serve stays open.
	writer, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	second := *first
	second.ID, second.Content = "second", "Docker service endpoint"
	if err = writer.InsertInsight(&second); err != nil {
		t.Fatal(err)
	}
	value, err = service.Dispatch(context.Background(), []byte(`{"operation":"native.search","query":"Docker"}`))
	if err != nil || len(value.([]search.ScoredInsight)) != 1 || value.([]search.ScoredInsight)[0].Insight.ID != "second" {
		t.Fatal("external native write invisible", value, err)
	}
	got, _ := db.GetInsightByID("first")
	if got.AccessCount != 1 {
		t.Fatal("native access semantics", got)
	}
	if _, err = service.Dispatch(context.Background(), []byte(`{"operation":"native.recall","query":"Redis","intent":"INVALID"}`)); err == nil {
		t.Fatal("invalid intent admitted")
	}
	value, err = service.Dispatch(context.Background(), []byte(`{"operation":"native.show","id":"second"}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	if len(encoded) == 0 {
		t.Fatal("missing native object")
	}
}
