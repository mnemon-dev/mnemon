package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type toolCallResult struct {
	IsError           bool           `json:"isError"`
	StructuredContent map[string]any `json:"structuredContent"`
}

func TestMnemonMCPStdioProcess(t *testing.T) {
	repository := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "mnemon")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mnemon: %v\n%s", err, output)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dataDir := t.TempDir()
	command := exec.CommandContext(ctx, binary, "--data-dir", dataDir, "--store", "mcp-e2e", "mcp", "serve")
	command.Env = append(os.Environ(), "MNEMON_EMBED_ENDPOINT=http://127.0.0.1:1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(stdin)
	decoder := json.NewDecoder(bufio.NewReader(stdout))

	writeRPC(t, encoder, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "process-e2e", "version": "1"},
		},
	})
	initialize := readRPC(t, decoder, 1)
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	decodeResult(t, initialize, &initialized)
	if initialized.ProtocolVersion != "2025-06-18" || initialized.ServerInfo.Name != "mnemon" {
		t.Fatalf("initialize result = %#v", initialized)
	}
	writeRPC(t, encoder, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{},
	})

	writeRPC(t, encoder, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{},
	})
	listed := readRPC(t, decoder, 2)
	var tools struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	decodeResult(t, listed, &tools)
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if want := []string{"link", "recall", "related", "remember", "search", "status"}; !slices.Equal(names, want) {
		t.Fatalf("tool names = %v, want %v", names, want)
	}

	remembered := callTool(t, encoder, decoder, 3, "remember", map[string]any{
		"content": "process boundary durable memory", "category": "fact", "no_diff": true,
	})
	id, _ := remembered.StructuredContent["id"].(string)
	if remembered.IsError || id == "" {
		t.Fatalf("remember result = %#v", remembered)
	}
	searched := callTool(t, encoder, decoder, 4, "search", map[string]any{"query": "process durable"})
	results, _ := searched.StructuredContent["results"].([]any)
	if searched.IsError || len(results) != 1 {
		t.Fatalf("search result = %#v", searched)
	}
	status := callTool(t, encoder, decoder, 5, "status", map[string]any{})
	if status.IsError || status.StructuredContent["total_insights"] != float64(1) {
		t.Fatalf("status result = %#v", status)
	}

	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	stderrBytes, readErr := io.ReadAll(stderr)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait for MCP server: %v\nstderr: %s", err, stderrBytes)
	}
	if len(stderrBytes) != 0 {
		t.Fatalf("MCP server wrote unexpected diagnostics: %s", stderrBytes)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "data", "mcp-e2e", "mnemon.db")); err != nil {
		t.Fatalf("global data/store flags did not select the expected database: %v", err)
	}
}

func callTool(t *testing.T, encoder *json.Encoder, decoder *json.Decoder,
	id int, name string, arguments map[string]any) toolCallResult {
	t.Helper()
	writeRPC(t, encoder, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": arguments},
	})
	response := readRPC(t, decoder, id)
	var result toolCallResult
	decodeResult(t, response, &result)
	return result
}

func writeRPC(t *testing.T, encoder *json.Encoder, message map[string]any) {
	t.Helper()
	if err := encoder.Encode(message); err != nil {
		t.Fatalf("write JSON-RPC message: %v", err)
	}
}

func readRPC(t *testing.T, decoder *json.Decoder, id int) rpcResponse {
	t.Helper()
	for {
		var response rpcResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatalf("read JSON-RPC response %d: %v", id, err)
		}
		if response.ID != id {
			continue
		}
		if len(response.Error) != 0 && string(response.Error) != "null" {
			t.Fatalf("JSON-RPC response %d failed: %s", id, response.Error)
		}
		return response
	}
}

func decodeResult(t *testing.T, response rpcResponse, output any) {
	t.Helper()
	if err := json.Unmarshal(response.Result, output); err != nil {
		t.Fatalf("decode JSON-RPC response %d: %v\n%s", response.ID, err, response.Result)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test filename")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(filename), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root %q has no go.mod: %v", root, err)
	}
	return root
}
