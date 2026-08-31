package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

func testService(t *testing.T, auditContent bool) (*Service, Config) {
	t.Helper()
	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://127.0.0.1:1")
	config := Config{
		DataDir: t.TempDir(), StoreName: "service-test",
		AuditContent: auditContent,
	}
	return New(config), config
}

func rememberTestInsight(t *testing.T, service *Service, content string) RememberResult {
	t.Helper()
	result, err := service.Remember(context.Background(), RememberRequest{
		Content: content, Category: "fact", Importance: 4,
		Tags: []string{"test"}, Entities: []string{"Mnemon"}, NoDiff: true,
	})
	if err != nil {
		t.Fatalf("remember %q: %v", content, err)
	}
	return result
}

func TestServiceMemoryWorkflow(t *testing.T) {
	service, _ := testService(t, true)
	first := rememberTestInsight(t, service, "Mnemon keeps durable release decisions")
	second := rememberTestInsight(t, service, "Release reviews depend on durable decisions")

	searchResults, err := service.Search(context.Background(), SearchRequest{Query: "durable decisions", Limit: 10})
	if err != nil || len(searchResults) != 2 {
		t.Fatalf("search results = %d, err = %v", len(searchResults), err)
	}
	recall, err := service.Recall(context.Background(), RecallRequest{Query: "release", Limit: 10})
	if err != nil || recall.SmartResults == nil || len(recall.SmartResults.Results) != 2 {
		t.Fatalf("smart recall = %#v, err = %v", recall.SmartResults, err)
	}
	basic, err := service.Recall(context.Background(), RecallRequest{Query: "Mnemon", Basic: true})
	if err != nil || len(basic.BasicResults) != 1 || basic.BasicResults[0].ID != first.ID {
		t.Fatalf("basic recall = %#v, err = %v", basic.BasicResults, err)
	}

	link, err := service.Link(context.Background(), LinkRequest{
		SourceID: first.ID, TargetID: second.ID, EdgeType: "causal",
		Weight: 0.8, Metadata: map[string]string{"reason": "test"}, CreatedBy: "service-test",
	})
	if err != nil || link.Metadata["created_by"] != "service-test" {
		t.Fatalf("link = %#v, err = %v", link, err)
	}
	related, err := service.Related(context.Background(), RelatedRequest{
		ID: first.ID, EdgeType: "causal", Depth: 1,
	})
	if err != nil || len(related) != 1 || related[0].ID != second.ID {
		t.Fatalf("related = %#v, err = %v", related, err)
	}
	status, err := service.Status(context.Background())
	if err != nil || status.TotalInsights != 2 || status.EdgeCount < 2 || status.DBSizeBytes == 0 {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

func TestRememberResultPreservesCLIWireShape(t *testing.T) {
	service, _ := testService(t, true)
	added, err := service.Remember(context.Background(), RememberRequest{
		Content: "wire shape marker", NoDiff: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	addedJSON, err := json.Marshal(added)
	if err != nil {
		t.Fatal(err)
	}
	var addedFields map[string]json.RawMessage
	if err := json.Unmarshal(addedJSON, &addedFields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"tags", "entities", "semantic_candidates", "causal_candidates", "auto_pruned_ids",
	} {
		if value, ok := addedFields[field]; !ok || string(value) != "[]" {
			t.Errorf("added %s = %s, present = %v; want []", field, value, ok)
		}
	}

	skipped, err := service.Remember(context.Background(), RememberRequest{Content: "wire shape marker"})
	if err != nil {
		t.Fatal(err)
	}
	skippedJSON, err := json.Marshal(skipped)
	if err != nil {
		t.Fatal(err)
	}
	var skippedFields map[string]json.RawMessage
	if err := json.Unmarshal(skippedJSON, &skippedFields); err != nil {
		t.Fatal(err)
	}
	if skipped.Action != "skipped" {
		t.Fatalf("duplicate action = %q", skipped.Action)
	}
	for _, field := range []string{
		"tags", "entities", "semantic_candidates", "causal_candidates", "auto_pruned_ids",
	} {
		if _, ok := skippedFields[field]; ok {
			t.Errorf("skipped result unexpectedly contains %q", field)
		}
	}
}

func TestServiceRedactsRemoteNaturalLanguageFromOplog(t *testing.T) {
	service, config := testService(t, false)
	secretContent := "private remote memory payload"
	rememberTestInsight(t, service, secretContent)
	if _, err := service.Search(context.Background(), SearchRequest{Query: "private remote"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Recall(context.Background(), RecallRequest{Query: "private", Basic: true}); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(store.StoreDir(config.DataDir, config.StoreName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entries, err := db.GetOplog(20)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Detail, "private") || strings.Contains(entry.Detail, secretContent) {
			t.Fatalf("oplog leaked remote content: %#v", entry)
		}
	}
}

func TestServiceReadOnlyRejectsWritesAndAllowsReads(t *testing.T) {
	service, config := testService(t, true)
	remembered := rememberTestInsight(t, service, "read-only snapshot content")
	readOnly := New(Config{
		DataDir: config.DataDir, StoreName: config.StoreName, ReadOnly: true,
	})
	results, err := readOnly.Recall(context.Background(), RecallRequest{Query: "snapshot", Basic: true})
	if err != nil || len(results.BasicResults) != 1 || results.BasicResults[0].ID != remembered.ID {
		t.Fatalf("read-only recall = %#v, err = %v", results.BasicResults, err)
	}
	if _, err := readOnly.Remember(context.Background(), RememberRequest{Content: "rejected"}); err == nil || !strings.Contains(err.Error(), "remember is unavailable with --readonly") {
		t.Fatalf("read-only remember error = %v", err)
	}
	if _, err := readOnly.Link(context.Background(), LinkRequest{
		SourceID: remembered.ID, TargetID: remembered.ID, Weight: 0.5,
	}); err == nil || !strings.Contains(err.Error(), "link is unavailable with --readonly") {
		t.Fatalf("read-only link error = %v", err)
	}
}

func TestServiceLinkRollsBackBothDirections(t *testing.T) {
	service, config := testService(t, true)
	first := rememberTestInsight(t, service, "first atomic link endpoint")
	second := rememberTestInsight(t, service, "second atomic link endpoint")
	if first.ID > second.ID {
		first, second = second, first
	}
	db, err := store.Open(store.StoreDir(config.DataDir, config.StoreName))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Conn().Exec(`
		CREATE TRIGGER reject_reverse_service_link
		BEFORE INSERT ON edges
		WHEN NEW.source_id > NEW.target_id AND NEW.edge_type = 'causal'
		BEGIN
			SELECT RAISE(ABORT, 'reverse rejected');
		END`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = service.Link(context.Background(), LinkRequest{
		SourceID: first.ID, TargetID: second.ID, EdgeType: "causal", Weight: 0.5,
	})
	if err == nil || !strings.Contains(err.Error(), "reverse rejected") {
		t.Fatalf("link error = %v", err)
	}
	db, err = store.Open(store.StoreDir(config.DataDir, config.StoreName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	edges, err := db.GetEdgesByNodeAndType(first.ID, model.EdgeCausal)
	if err != nil || len(edges) != 0 {
		t.Fatalf("causal edges after rollback = %#v, err = %v", edges, err)
	}
}

func TestServiceCanceledWaitDoesNotStartOperation(t *testing.T) {
	service, _ := testService(t, true)
	<-service.gate
	defer func() { service.gate <- struct{}{} }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Status(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("status error = %v, want context cancellation", err)
	}
}
