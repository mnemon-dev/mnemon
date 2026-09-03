package mcp

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	memoryservice "github.com/mnemon-dev/mnemon/internal/memory/service"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestMemory(t *testing.T) *memoryservice.Service {
	t.Helper()
	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://127.0.0.1:1")
	return memoryservice.New(memoryservice.Config{
		DataDir: t.TempDir(), StoreName: "mcp-test", Warnings: io.Discard,
	})
}

func connectTestClient(t *testing.T, server *Server) *sdkmcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.sdk.Connect(ctx, serverTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect server: %v", err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "mnemon-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		cancel()
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
		cancel()
	})
	return clientSession
}

func callStructured[T any](t *testing.T, session *sdkmcp.ClientSession,
	name string, arguments map[string]any) (T, *sdkmcp.CallToolResult) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("call %s returned tool error: %#v", name, result.Content)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal %s structured result: %v", name, err)
	}
	var output T
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatalf("decode %s structured result: %v\n%s", name, err, encoded)
	}
	return output, result
}

func TestServerAdvertisesSixBoundedTools(t *testing.T) {
	session := connectTestClient(t, New("test-version", newTestMemory(t)))
	initialized := session.InitializeResult()
	if initialized == nil || initialized.Capabilities == nil || initialized.Capabilities.Tools == nil {
		t.Fatalf("initialize capabilities = %#v, want static tools capability", initialized)
	}
	if initialized.Capabilities.Tools.ListChanged {
		t.Fatal("server advertised listChanged for its static tool set")
	}
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(result.Tools))
	byName := make(map[string]*sdkmcp.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
		byName[tool.Name] = tool
	}
	slices.Sort(names)
	want := []string{"link", "recall", "related", "remember", "search", "status"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
	type annotationExpectation struct {
		readOnly    bool
		destructive bool
		idempotent  bool
		openWorld   bool
	}
	wantAnnotations := map[string]annotationExpectation{
		"recall":   {readOnly: false, destructive: false, idempotent: false, openWorld: true},
		"search":   {readOnly: false, destructive: false, idempotent: false, openWorld: false},
		"related":  {readOnly: true, idempotent: true},
		"status":   {readOnly: true, idempotent: true},
		"remember": {destructive: true, openWorld: true},
		"link":     {destructive: true},
	}
	for name, want := range wantAnnotations {
		annotations := byName[name].Annotations
		if annotations == nil || annotations.DestructiveHint == nil || annotations.OpenWorldHint == nil {
			t.Errorf("%s annotations = %#v", name, annotations)
			continue
		}
		if annotations.ReadOnlyHint != want.readOnly ||
			*annotations.DestructiveHint != want.destructive ||
			annotations.IdempotentHint != want.idempotent ||
			*annotations.OpenWorldHint != want.openWorld {
			t.Errorf("%s annotations = %#v, want %#v", name, annotations, want)
		}
	}
	recallSchema, ok := byName["recall"].InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("recall schema type = %T", byName["recall"].InputSchema)
	}
	required, _ := recallSchema["required"].([]any)
	if !slices.Contains(required, any("query")) {
		t.Fatalf("recall required fields = %#v", required)
	}
	recallProperties, ok := recallSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("recall properties = %#v", recallSchema["properties"])
	}
	assertSchemaNumber(t, recallProperties, "query", "maxLength", maxQueryChars)
	assertSchemaNumber(t, recallProperties, "limit", "minimum", 1)
	assertSchemaNumber(t, recallProperties, "limit", "maximum", maxToolResults)

	rememberSchema, ok := byName["remember"].InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("remember schema type = %T", byName["remember"].InputSchema)
	}
	rememberProperties, ok := rememberSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("remember properties = %#v", rememberSchema["properties"])
	}
	assertSchemaNumber(t, rememberProperties, "content", "maxLength", maxRememberBytes)
	assertSchemaNumber(t, rememberProperties, "importance", "maximum", 5)
}

func TestServerMemoryWorkflowAndDefaultTruncation(t *testing.T) {
	session := connectTestClient(t, New("test-version", newTestMemory(t)))
	longContent := "durable marker " + strings.Repeat("记忆content ", 90)
	first, _ := callStructured[memoryservice.RememberResult](t, session, "remember", map[string]any{
		"content": longContent, "category": "fact", "no_diff": true,
	})
	second, _ := callStructured[memoryservice.RememberResult](t, session, "remember", map[string]any{
		"content": "A release review depends on the durable marker", "category": "decision", "no_diff": true,
	})
	if first.ID == "" || second.ID == "" || first.ID == second.ID {
		t.Fatalf("remember IDs = %q, %q", first.ID, second.ID)
	}

	link, _ := callStructured[memoryservice.LinkResult](t, session, "link", map[string]any{
		"source_id": first.ID, "target_id": second.ID, "edge_type": "causal", "weight": 0.8,
	})
	if link.Metadata["created_by"] != "mcp" {
		t.Fatalf("link metadata = %#v", link.Metadata)
	}
	related, _ := callStructured[insightListResult](t, session, "related", map[string]any{
		"id": first.ID, "edge_type": "causal", "depth": 1,
	})
	if len(related.Results) != 1 || related.Results[0].ID != second.ID {
		t.Fatalf("related = %#v", related)
	}

	recall, _ := callStructured[insightListResult](t, session, "recall", map[string]any{
		"query": "durable marker", "basic": true,
	})
	firstRecall := resultByID(t, recall.Results, first.ID)
	if !firstRecall.Truncated || utf8.RuneCountInString(firstRecall.Content) > defaultContentChars || recall.TruncationHint == "" {
		t.Fatalf("default recall projection = %#v, hint = %q", firstRecall, recall.TruncationHint)
	}
	fullRecall, _ := callStructured[insightListResult](t, session, "recall", map[string]any{
		"query": "durable marker", "basic": true, "full": true,
	})
	fullFirst := resultByID(t, fullRecall.Results, first.ID)
	if fullFirst.Content != longContent || fullFirst.Truncated || fullRecall.TruncationHint != "" {
		t.Fatalf("full recall projection = %#v, hint = %q", fullFirst, fullRecall.TruncationHint)
	}
	search, _ := callStructured[insightListResult](t, session, "search", map[string]any{
		"query": "release durable",
	})
	if len(search.Results) != 2 {
		t.Fatalf("search results = %#v", search.Results)
	}
	status, _ := callStructured[memoryservice.StatusResult](t, session, "status", map[string]any{})
	if status.TotalInsights != 2 || status.EdgeCount < 2 {
		t.Fatalf("status = %#v", status)
	}
}

func TestServerReturnsActionableToolErrors(t *testing.T) {
	session := connectTestClient(t, New("test-version", newTestMemory(t)))
	zero := 0
	_, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "recall", Arguments: map[string]any{"query": "anything", "limit": zero},
	})
	if err == nil || !strings.Contains(err.Error(), "minimum") {
		t.Fatalf("invalid limit error = %v", err)
	}

	_, err = session.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "remember", Arguments: map[string]any{
			"content": "bounded input", "source": strings.Repeat("s", maxSourceChars+1),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "maxLength") {
		t.Fatalf("oversized source error = %v", err)
	}
}

func TestServeRejectsInvalidStreams(t *testing.T) {
	server := New("test-version", newTestMemory(t))
	if err := server.Serve(nil, strings.NewReader(""), io.Discard); err == nil {
		t.Fatal("Serve accepted a nil context")
	}
	if err := server.Serve(context.Background(), nil, io.Discard); err == nil {
		t.Fatal("Serve accepted nil stdin")
	}
	if err := server.Serve(context.Background(), strings.NewReader(""), nil); err == nil {
		t.Fatal("Serve accepted nil stdout")
	}
}

func TestStdioInitializeEchoesSupportedClientProtocolVersion(t *testing.T) {
	for _, version := range []string{"2024-11-05", "2025-03-26", "2025-06-18"} {
		t.Run(version, func(t *testing.T) {
			server := New("test-version", newTestMemory(t))
			serverInput, clientOutput := io.Pipe()
			clientInput, serverOutput := io.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- server.Serve(ctx, serverInput, serverOutput)
			}()
			waited := false
			defer func() {
				cancel()
				_ = clientOutput.Close()
				_ = clientInput.Close()
				_ = serverOutput.Close()
				if !waited {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("stdio server cleanup did not stop")
					}
				}
			}()

			request := map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "initialize",
				"params": map[string]any{
					"protocolVersion": version,
					"capabilities":    map[string]any{},
					"clientInfo":      map[string]any{"name": "raw-test", "version": "1"},
				},
			}
			if err := json.NewEncoder(clientOutput).Encode(request); err != nil {
				t.Fatal(err)
			}
			var response struct {
				Result struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"result"`
			}
			if err := json.NewDecoder(clientInput).Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.Result.ProtocolVersion != version {
				t.Fatalf("protocol version = %q, want client version %q", response.Result.ProtocolVersion, version)
			}
			_ = clientOutput.Close()
			select {
			case err := <-done:
				waited = true
				if err != nil {
					t.Fatalf("serve after client close: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("stdio server did not stop after client closed stdin")
			}
		})
	}
}

func resultByID(t *testing.T, results []insightResult, id string) insightResult {
	t.Helper()
	for _, result := range results {
		if result.ID == id {
			return result
		}
	}
	t.Fatalf("result %s not found in %#v", id, results)
	return insightResult{}
}

func assertSchemaNumber(t *testing.T, properties map[string]any, property, keyword string, want int) {
	t.Helper()
	schema, ok := properties[property].(map[string]any)
	if !ok {
		t.Fatalf("schema property %q = %#v", property, properties[property])
	}
	if got, ok := schema[keyword].(float64); !ok || got != float64(want) {
		t.Fatalf("schema property %q %s = %#v, want %d", property, keyword, schema[keyword], want)
	}
}
