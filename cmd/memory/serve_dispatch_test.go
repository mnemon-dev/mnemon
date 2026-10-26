package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

func TestDispatchWriteConversationNeedsNoAgentAndHonoursReadOnly(t *testing.T) {
	oldReadOnly := readOnly
	t.Cleanup(func() { readOnly = oldReadOnly })
	raw, err := records.Open(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	request := []byte(`{"operation":"memory.writeConversation","id":"served","sessionId":"s1","source":"aml",` +
		`"messages":[{"role":"user","content":"I moved to Lisbon","at":"2023-05-08T13:56:00Z"},{"role":"assistant","content":"Noted."}]}`)

	readOnly = true
	if _, err := dispatchMemory(context.Background(), raw, nil, "memory.writeConversation", request); err == nil || !strings.Contains(err.Error(), "--readonly") {
		t.Fatal("read-only server accepted a conversation write", err)
	}
	snapshot, err := raw.Get(context.Background(), records.GetRequest{IDs: []string{"served"}})
	if err != nil || len(snapshot.Records) != 0 {
		t.Fatal("read-only write reached the store", err)
	}

	readOnly = false
	got, err := dispatchMemory(context.Background(), raw, nil, "memory.writeConversation", request)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := got.(memoryagent.WriteResult)
	if !ok || result.Document.ID != "served" || len(result.Records) != 1 || result.Revision == "" {
		t.Fatal("unexpected result", got)
	}
	snapshot, err = raw.Get(context.Background(), records.GetRequest{IDs: []string{result.Records[0].ID}})
	if err != nil || len(snapshot.Records) != 1 || !strings.Contains(snapshot.Records[0], "Conversation s1 on 2023/05/08 (Mon) 13:56:") {
		t.Fatal("conversation chunk was not stored with its dated heading", err)
	}

	// The same id is refused, and the sentinel survives the dispatcher.
	if _, err = dispatchMemory(context.Background(), raw, nil, "memory.writeConversation", request); !strings.Contains(err.Error(), memoryagent.ErrDocumentExists.Error()) {
		t.Fatal("duplicate", err)
	}
	if _, err = dispatchMemory(context.Background(), raw, nil, "memory.writeConversation", json.RawMessage(`{"messages":`)); err == nil {
		t.Fatal("malformed request accepted")
	}
	// Strategy operations still need an agent.
	if _, err = dispatchMemory(context.Background(), raw, nil, "memory.read", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "memory strategy requires") {
		t.Fatal("read without an agent", err)
	}
}
