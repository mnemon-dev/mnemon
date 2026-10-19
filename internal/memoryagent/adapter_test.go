package memoryagent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// Helpers an HTTP adapter (internal/aml) builds on: the packing of a
// conversation, agents over shared models, the View bound, hash-verified
// hydration and warming the timelines layer's embeddings.

func TestPackConversationIsWhatWriteStores(t *testing.T) {
	s, _ := agentStore(t)
	ctx := context.Background()
	at := time.Date(2023, 5, 8, 13, 56, 0, 0, time.UTC)
	req := ConversationRequest{ID: "aml-1", SessionID: "s1", Messages: []ConversationMessage{
		{Role: "user", Content: foxText(10), At: &at},
		{Role: "assistant", Content: foxText(10)},
	}}
	parent, chunks, err := PackConversation(req)
	if err != nil || len(chunks) < 2 || parent != strings.Join(chunks, "\n\n") || !strings.HasPrefix(parent, "Conversation s1 on 2023/05/08 (Mon) 13:56:\n") {
		t.Fatal(len(chunks), err)
	}
	result, err := WriteConversation(ctx, s, req)
	if err != nil {
		t.Fatal(err)
	}
	if stored := fetchValue(t, s, result.Revision, "aml-1"); stored.Content != parent {
		t.Fatal("PackConversation differs from the stored parent")
	}
	for i, ref := range result.Records {
		if fetchValue(t, s, result.Revision, ref.ID).Content != chunks[i] {
			t.Fatal("chunk", i)
		}
	}
	// No timestamp: the heading has no date. The id and source do not count.
	parent, _, err = PackConversation(ConversationRequest{SessionID: "s2", Messages: []ConversationMessage{msg("user", "hello")}})
	if err != nil || parent != "Conversation s2:\nuser: hello" {
		t.Fatalf("%q %v", parent, err)
	}
	// It validates like WriteConversation.
	for _, bad := range []ConversationRequest{{SessionID: "s"}, {Messages: []ConversationMessage{msg("user", "x")}}, {SessionID: "s", Messages: []ConversationMessage{msg("", "x")}}} {
		_, _, packErr := PackConversation(bad)
		_, writeErr := WriteConversation(ctx, s, bad)
		if packErr == nil || writeErr == nil || packErr.Error() != writeErr.Error() {
			t.Fatal(packErr, writeErr)
		}
	}
}

func TestOpenWithModelsSharesModels(t *testing.T) {
	dir := t.TempDir()
	s, err := records.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	models := Models{Planner: completeFunc(nil), Judge: decisionFunc(fixtureJudge)}
	config := Config{Version: 1, Strategy: "simple", Consolidation: &ConsolidationConfig{Mode: "timelines"}}
	a, err := OpenWithModels(s, dir, config, false, models)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memory-index.db")); err != nil {
		t.Fatal("overlay not opened", err)
	}
	if a.MaxItems() != 16 {
		t.Fatal(a.MaxItems())
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	config.View = &ViewConfig{MaxItems: 100}
	if a, err = OpenWithModels(s, dir, config, false, models); err != nil || a.MaxItems() != 100 {
		t.Fatal(err)
	}
	a.Close()
	// memory-agent/v1 has no View bound; a missing model is New's error.
	if a, err = OpenWithModels(s, dir, Config{Version: 1, Policy: &ReadPolicy{}}, false, Models{}); err != nil || a.MaxItems() != 0 {
		t.Fatal(err)
	}
	a.Close()
	if _, err = OpenWithModels(s, dir, Config{Version: 1, Strategy: "simple"}, false, Models{}); err == nil {
		t.Fatal("simple strategy without models")
	}

	// OpenModels builds the configured providers and their budget.
	built, budget, err := OpenModels(Config{Version: 1, Planner: &ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1"}})
	if err != nil || built.Planner == nil || built.Judge != nil || built.Consolidator != nil || budget != nil {
		t.Fatal(built, budget, err)
	}
	built, budget, err = OpenModels(Config{Version: 1, BudgetFile: filepath.Join(t.TempDir(), "budget.db")})
	if err != nil || budget == nil || built.Planner != nil {
		t.Fatal(err)
	}
	budget.Close()
	if _, _, err = OpenModels(Config{Version: 1, Judge: &ModelConfig{Kind: "nope", Model: "m"}}); err == nil {
		t.Fatal("unknown provider kind")
	}
}

func TestHydrateKeepsOrderAndProvesChunks(t *testing.T) {
	s, _ := agentStore(t)
	ctx := context.Background()
	first, err := WriteConversation(ctx, s, ConversationRequest{ID: "c1", SessionID: "s1", Messages: []ConversationMessage{msg("user", "alpha one")}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := WriteConversation(ctx, s, ConversationRequest{ID: "c2", SessionID: "s2", Messages: []ConversationMessage{msg("user", "beta two")}})
	if err != nil {
		t.Fatal(err)
	}
	a := simpleAgent(t, s, completeFunc(nil))
	one, two := first.Records[0].ID, second.Records[0].ID
	// Requested order, unknown ids and documents skipped, parents proved.
	items, err := a.Hydrate(ctx, "", records.Scope{}, []string{two, "missing", "c1", one})
	if err != nil || !slices.Equal(recordIDs(items), []string{two, one}) {
		t.Fatal(recordIDs(items), err)
	}
	if items[0].Text != "Conversation s2:\nuser: beta two" || items[0].Parent == nil || *items[0].Parent != second.Document || items[1].Reference != first.Records[0] {
		t.Fatal(items)
	}
	// An earlier revision does not know the second conversation.
	if items, err = a.Hydrate(ctx, first.Revision, records.Scope{}, []string{two, one}); err != nil || !slices.Equal(recordIDs(items), []string{one}) {
		t.Fatal(recordIDs(items), err)
	}
	if _, err = a.Hydrate(ctx, "", records.Scope{}, make([]string, 201)); err == nil {
		t.Fatal("more than 200 ids")
	}
}

func TestWarmLayerEmbedsLayerTexts(t *testing.T) {
	ctx := context.Background()
	server := &embeddingServer{}
	url := server.start(t, func(string) []float64 { return []float64{1, 0} })
	embedding := &records.EmbeddingConfig{URL: url, Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	threadTexts, valueTexts := layerTexts(layerMemory())
	texts := []string{}
	for _, text := range threadTexts {
		texts = append(texts, text)
	}
	for _, text := range valueTexts {
		texts = append(texts, text)
	}
	inputs := func() []string {
		server.mu.Lock()
		defer server.mu.Unlock()
		return slices.Clone(server.inputs)
	}

	s, overlay := layerStore(t)
	a := layerAgent(t, s, overlay, Config{Embedding: embedding}, completeFunc(nil), layerJudge())
	if err := a.WarmLayer(ctx, records.Scope{}); err != nil {
		t.Fatal(err)
	}
	got := inputs()
	if len(got) != len(texts) {
		t.Fatal(got)
	}
	for _, text := range texts {
		if !slices.Contains(got, "search_document: "+jsSlice(text, 0, layerEmbedText)) {
			t.Fatal("not embedded:", text)
		}
	}
	// The read finds them stored: nothing is left to embed.
	vectors, err := s.DerivedVectors(ctx, texts, *embedding, false)
	if err != nil || slices.ContainsFunc(vectors, func(v []float32) bool { return v == nil }) {
		t.Fatal(vectors, err)
	}
	if err := a.WarmLayer(ctx, records.Scope{}); err != nil || len(inputs()) != len(texts) {
		t.Fatal(err, len(inputs()))
	}

	// A failing embedder is the caller's error to log.
	server.mu.Lock()
	server.fail = true
	server.mu.Unlock()
	s, overlay = layerStore(t)
	if err := layerAgent(t, s, overlay, Config{Embedding: embedding}, completeFunc(nil), layerJudge()).WarmLayer(ctx, records.Scope{}); err == nil {
		t.Fatal("embedding failure")
	}

	// Without an embedder, a layer, or a state there is nothing to do.
	before := len(inputs())
	s, overlay = layerStore(t)
	if err := layerAgent(t, s, overlay, Config{}, completeFunc(nil), layerJudge()).WarmLayer(ctx, records.Scope{}); err != nil {
		t.Fatal(err)
	}
	off := Config{Embedding: embedding, Policy: &ReadPolicy{}}
	if err := layerAgent(t, s, overlay, off, completeFunc(nil), layerJudge()).WarmLayer(ctx, records.Scope{}); err != nil {
		t.Fatal(err)
	}
	empty, _ := agentStore(t)
	if err := layerAgent(t, empty, nil, Config{Embedding: embedding}, completeFunc(nil), layerJudge()).WarmLayer(ctx, records.Scope{}); err != nil {
		t.Fatal(err)
	}
	if len(inputs()) != before {
		t.Fatal("embedded without a layer to warm")
	}
}
