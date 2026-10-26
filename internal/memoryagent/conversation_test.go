package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mnemon-dev/mnemon/internal/records"
)

func msg(role, content string) ConversationMessage {
	return ConversationMessage{Role: role, Content: content}
}

func TestJSLenAndSlice(t *testing.T) {
	// "é" is one UTF-16 unit, "世" is one, "😀" is a surrogate pair (two).
	text := "aé世😀b"
	if got := jsLen(text); got != 6 {
		t.Fatal("jsLen counts UTF-16 units, not bytes or runes", got)
	}
	cases := []struct {
		start, end int
		want       string
	}{
		{0, 3, "aé世"},
		{0, 4, "aé世"}, // the end cut falls inside the pair and moves left
		{0, 5, "aé世😀"},
		{3, 5, "😀"},
		{4, 5, "😀"}, // the start cut falls inside the pair and moves left
		{5, 6, "b"},
		{2, 2, ""},
		{4, 4, ""},
		{-3, 2, "aé"},
		{3, 99, "😀b"},
		{5, 1, ""},
	}
	for _, c := range cases {
		if got := jsSlice(text, c.start, c.end); got != c.want || !utf8.ValidString(got) {
			t.Fatalf("jsSlice(%d,%d) = %q, want %q", c.start, c.end, got, c.want)
		}
	}
	if jsLen("") != 0 || jsSlice("", 0, 4) != "" {
		t.Fatal("empty string")
	}
}

func TestDialogueLinesWrap(t *testing.T) {
	// "user: " is 6 units; every word is 9 letters plus a space, so spaces sit at
	// indexes 15, 25, ... 535, 545. lastIndexOf(' ', 540) is 535.
	words := strings.TrimSpace(strings.Repeat("abcdefghi ", 130)) // 1299 chars
	lines := DialogueLines([]ConversationMessage{msg("user", words)}, 540)
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "user: ") {
		t.Fatal("unexpected lines", lines)
	}
	if jsLen(lines[0]) != 535 {
		t.Fatal("first line must be cut at the last space at index <= 540", jsLen(lines[0]))
	}
	for i, line := range lines {
		if jsLen(line) > 540 || strings.HasPrefix(line, " ") || strings.HasSuffix(line, " ") {
			t.Fatalf("line %d is too long or not trimmed: %q", i, line)
		}
		if i > 0 && (strings.HasPrefix(line, "user:") || !strings.HasPrefix(line, "abcdefghi")) {
			t.Fatalf("continuation line %d carries a speaker or starts mid-word: %q", i, line)
		}
	}
	if "user: "+words != strings.Join(lines, " ") {
		t.Fatal("cutting at spaces must not lose or reorder text")
	}

	// 1,300 characters with no space at all: hard cuts at exactly 540.
	flat := DialogueLines([]ConversationMessage{msg("user", strings.Repeat("x", 1300))}, 540)
	if len(flat) != 3 || jsLen(flat[0]) != 540 || jsLen(flat[1]) != 540 || jsLen(flat[2]) != 1300+6-540-540 || !strings.HasPrefix(flat[0], "user: x") || flat[1][0] != 'x' {
		t.Fatal("unbroken text must be cut at the limit", len(flat))
	}

	// The last space is at index 306 (> 270): cut there. A space at 106 (<= 270): cut at 540.
	late := DialogueLines([]ConversationMessage{msg("user", strings.Repeat("a", 300)+" "+strings.Repeat("b", 1000))}, 540)
	if jsLen(late[0]) != 306 || late[1][0] != 'b' {
		t.Fatal("space beyond half the limit must be used", jsLen(late[0]), late[1][:3])
	}
	early := DialogueLines([]ConversationMessage{msg("user", strings.Repeat("a", 100)+" "+strings.Repeat("b", 1000))}, 540)
	if jsLen(early[0]) != 540 || !strings.HasSuffix(early[0], "b") {
		t.Fatal("space within the first half must be ignored", jsLen(early[0]))
	}
	// A space exactly at the limit index is a valid cut and is skipped by trimStart.
	edge := DialogueLines([]ConversationMessage{msg("user", strings.Repeat("a", 533)+" "+strings.Repeat("b", 100))}, 540)
	if len(edge) != 2 || jsLen(edge[0]) != 539 || edge[1] != strings.Repeat("b", 100) {
		t.Fatal("space inside the window", edge)
	}
	at := DialogueLines([]ConversationMessage{msg("user", strings.Repeat("a", 534)+" "+strings.Repeat("b", 100))}, 540)
	if len(at) != 2 || jsLen(at[0]) != 540 || at[1] != strings.Repeat("b", 100) {
		t.Fatal("space exactly at the limit", at)
	}
}

func TestDialogueLinesCollapsesWhitespaceAndKeepsMessages(t *testing.T) {
	lines := DialogueLines([]ConversationMessage{
		msg("user", "  hello \n\t  world\u00a0again \u3000 "),
		msg("assistant", "short"),
		msg("user", ""),
	}, 540)
	want := []string{"user: hello world again", "assistant: short", "user: "}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines %q, want %q", lines, want)
	}
}

func TestDialogueLinesNeverSplitSurrogatePairs(t *testing.T) {
	// 700 emoji = 1,400 units with no spaces: cut points land on pair boundaries.
	lines := DialogueLines([]ConversationMessage{msg("user", strings.Repeat("😀", 700))}, 540)
	joined := ""
	for _, line := range lines {
		if jsLen(line) > 540 || !utf8.ValidString(line) || strings.ContainsRune(line, utf8.RuneError) {
			t.Fatal("line broke a surrogate pair or exceeded the limit", jsLen(line))
		}
		joined += line
	}
	if joined != "user: "+strings.Repeat("😀", 700) {
		t.Fatal("emoji text changed while wrapping")
	}
	// Tiny limits still make progress.
	if got := DialogueLines([]ConversationMessage{msg("u", "😀😀😀")}, 1); len(got) < 3 {
		t.Fatal("tiny limit must terminate and keep text", got)
	}
}

func TestNoteChunksHeading(t *testing.T) {
	heading := "Conversation s1 on 2023/05/08 (Mon) 13:56"
	var lines, want []string
	for i := 0; i < 40; i++ {
		line := strings.Repeat(string(rune('a'+i%26)), 49) + string(rune('0'+i%10)) // 50 units
		lines = append(lines, line, "", "  \t ")
		want = append(want, line)
	}
	chunks := NoteChunks(heading, lines, 600)
	if len(chunks) < 3 {
		t.Fatal("expected several chunks", len(chunks))
	}
	var got []string
	for _, chunk := range chunks {
		if !strings.HasPrefix(chunk, heading+":\n") || jsLen(chunk) > 600 {
			t.Fatalf("chunk must start with the heading and fit in 600 units: %d %q", jsLen(chunk), chunk[:60])
		}
		body := strings.TrimPrefix(chunk, heading+":\n")
		if body == "" {
			t.Fatal("empty chunk")
		}
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) == "" {
				t.Fatal("empty line was kept")
			}
			got = append(got, line)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatal("lines were split, dropped or reordered")
	}
	// 10 lines of 50 fit with the heading (509 + 41 <= 600), the 11th does not.
	if strings.Count(chunks[0], "\n") != 10 {
		t.Fatal("chunk boundary", strings.Count(chunks[0], "\n"))
	}
	if got := NoteChunks(heading, []string{"", "  "}, 600); len(got) != 0 {
		t.Fatal("no lines, no chunks", got)
	}
}

func TestNoteChunksAuthorBoundary(t *testing.T) {
	// The author's rule compares join(chunk+line) + heading to the limit and
	// ignores the ":\n" separator, so a chunk may be limit+2 long. Port, not fix.
	a, b := strings.Repeat("a", 299), strings.Repeat("b", 299)
	chunks := NoteChunks("H", []string{a, b, "c"}, 600)
	if len(chunks) != 2 || chunks[0] != "H:\n"+a+"\n"+b || jsLen(chunks[0]) != 602 || chunks[1] != "H:\nc" {
		t.Fatal("author boundary not reproduced", len(chunks))
	}
	// One unit more and the second line starts a new chunk.
	chunks = NoteChunks("H", []string{a, b + "b", "c"}, 600)
	if len(chunks) != 2 || chunks[0] != "H:\n"+a || chunks[1] != "H:\n"+b+"b\nc" {
		t.Fatal("chunk boundary off by one", chunks)
	}
	// A single line longer than the limit still forms a chunk of its own.
	chunks = NoteChunks("H", []string{strings.Repeat("z", 700)}, 600)
	if len(chunks) != 1 {
		t.Fatal("overlong line", len(chunks))
	}
	// Lengths are UTF-16 units: 150 emoji are 150 runes but 300 units, so two
	// such lines and the heading (602) split, which a rune count (302) would not.
	e := strings.Repeat("😀", 150)
	if got := NoteChunks("H", []string{e, e}, 600); len(got) != 2 {
		t.Fatal("emoji must count as two units", len(got))
	}
	e = strings.Repeat("😀", 149)
	if got := NoteChunks("H", []string{e, e}, 600); len(got) != 1 {
		t.Fatal("two 298-unit lines and the heading (598) fit", len(got))
	}
	if got := NoteChunks("H", []string{e, e + "xxx"}, 600); len(got) != 2 {
		t.Fatal("two lines and the heading (601) do not fit", len(got))
	}
}

func fetchValue(t *testing.T, s *records.Store, revision, id string) recordValue {
	t.Helper()
	snapshot, err := s.Get(context.Background(), records.GetRequest{Revision: revision, IDs: []string{id}})
	if err != nil || len(snapshot.Records) != 1 {
		t.Fatal("record missing", id, err)
	}
	var value recordValue
	if err := json.Unmarshal([]byte(snapshot.Records[0]), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func foxText(n int) string {
	return strings.TrimSpace(strings.Repeat("The quick brown fox jumps over the lazy dog. ", n))
}

func TestWriteConversationRecords(t *testing.T) {
	s, _ := agentStore(t)
	ctx := context.Background()
	first := time.Date(2023, 5, 8, 13, 56, 30, 123456789, time.UTC)
	second := first.Add(5 * time.Minute)
	req := ConversationRequest{
		ID:        "conv-1",
		SessionID: "s1",
		Source:    "aml",
		Messages: []ConversationMessage{
			{Role: "user", Content: foxText(8), At: &first},
			{Role: "assistant", Content: foxText(8), At: &second},
		},
	}
	result, err := WriteConversation(ctx, s, req)
	if err != nil || len(result.Records) < 2 || result.Revision == "" {
		t.Fatal(result, err)
	}
	heading := "Conversation s1 on 2023/05/08 (Mon) 13:56:\n"
	parent := fetchValue(t, s, result.Revision, "conv-1")
	if parent.Kind != "memory-document" || parent.State != "archived" || reference("conv-1", mustRaw(t, s, result.Revision, "conv-1")) != result.Document {
		t.Fatal("parent", parent.Kind, parent.State)
	}
	if parent.Scope != "global" || parent.SessionID != nil || parent.WorkspaceID != nil {
		t.Fatal("default scope must be global without session or workspace")
	}
	chunks := make([]string, 0, len(result.Records))
	ids := make([]string, 0, len(result.Records))
	for i, ref := range result.Records {
		child := fetchValue(t, s, result.Revision, ref.ID)
		if child.Kind != "memory-record" || child.State != "active" || child.ID != fmt.Sprintf("conv-1-%05d", i) {
			t.Fatal("chunk record", child.ID, child.Kind, child.State)
		}
		if !strings.HasPrefix(child.Content, heading) || jsLen(child.Content) > 600 {
			t.Fatalf("chunk %d is not a packed chunk: %q", i, child.Content)
		}
		if child.Data["eventAt"] != "2023-05-08T13:56:30.123456789Z" || child.Data["session"] != "s1" || child.Data["source"] != "aml" || child.Data["documentID"] != "conv-1" {
			t.Fatal("chunk data", child.Data)
		}
		start, end := int(child.Data["start"].(float64)), int(child.Data["end"].(float64))
		if parent.Content[start:end] != child.Content {
			t.Fatal("offsets do not locate the chunk in the parent", start, end)
		}
		chunks = append(chunks, child.Content)
		ids = append(ids, ref.ID)
	}
	if parent.Content != strings.Join(chunks, "\n\n") {
		t.Fatal("parent must be the packed chunks joined by a blank line")
	}
	if parent.Data["eventAt"] != "2023-05-08T13:56:30.123456789Z" || parent.Data["source"] != "aml" || parent.Date == nil || *parent.Date != "2023-05-08" {
		t.Fatal("parent data", parent.Data, parent.Date)
	}
	// The chunks are exactly the author's packing of the author's lines.
	wantChunks := NoteChunks("Conversation s1 on 2023/05/08 (Mon) 13:56", DialogueLines(req.Messages, 540), 600)
	if strings.Join(wantChunks, "\x00") != strings.Join(chunks, "\x00") {
		t.Fatal("chunks differ from NoteChunks(DialogueLines)")
	}
	// Evidence verifies each chunk against its parent.
	items, err := evidence(ctx, s, result.Revision, records.Scope{}, ids)
	if err != nil || len(items) != len(ids) {
		t.Fatal(items, err)
	}
	for i, item := range items {
		if item.Reference != result.Records[i] || item.Parent == nil || *item.Parent != result.Document || item.Text != chunks[i] || item.Date != "2023-05-08T13:56:30.123456789Z" {
			t.Fatal("evidence", item)
		}
	}
	// The text is searchable.
	query := mustQuery(t, s, result.Revision, "lazy dog")
	if len(query) == 0 {
		t.Fatal("conversation chunks are not searchable")
	}
}

func TestWriteConversationNormalizesTimeToUTC(t *testing.T) {
	s, _ := agentStore(t)
	zone := time.FixedZone("UTC+8", 8*3600)
	at := time.Date(2023, 5, 9, 2, 30, 0, 0, zone) // 2023-05-08 18:30 UTC
	result, err := WriteConversation(context.Background(), s, ConversationRequest{
		ID: "tz", SessionID: "s1", Messages: []ConversationMessage{{Role: "user", Content: "hi", At: &at}}})
	if err != nil {
		t.Fatal(err)
	}
	child := fetchValue(t, s, result.Revision, result.Records[0].ID)
	if child.Data["eventAt"] != "2023-05-08T18:30:00Z" || !strings.HasPrefix(child.Content, "Conversation s1 on 2023/05/08 (Mon) 18:30:\n") {
		t.Fatal("time must be UTC", child.Data["eventAt"], child.Content)
	}
}

func TestWriteConversationUsesFirstTimestampedMessage(t *testing.T) {
	s, _ := agentStore(t)
	later := time.Date(2024, 2, 29, 23, 59, 0, 0, time.UTC)
	other := later.Add(time.Hour)
	result, err := WriteConversation(context.Background(), s, ConversationRequest{
		ID: "ts", SessionID: "s2", Messages: []ConversationMessage{
			{Role: "user", Content: "no stamp"}, {Role: "assistant", Content: "stamped", At: &later}, {Role: "user", Content: "later", At: &other}}})
	if err != nil {
		t.Fatal(err)
	}
	child := fetchValue(t, s, result.Revision, result.Records[0].ID)
	if child.Data["eventAt"] != "2024-02-29T23:59:00Z" || !strings.HasPrefix(child.Content, "Conversation s2 on 2024/02/29 (Thu) 23:59:\nuser: no stamp\n") {
		t.Fatal("first message with a timestamp decides", child.Data["eventAt"], child.Content)
	}
}

func TestWriteConversationNoTimestamps(t *testing.T) {
	s, _ := agentStore(t)
	before := time.Now().UTC().Add(-time.Second)
	result, err := WriteConversation(context.Background(), s, ConversationRequest{
		ID: "plain", SessionID: "s1", Messages: []ConversationMessage{msg("user", "hello there"), msg("assistant", "hi")}})
	if err != nil || len(result.Records) != 1 {
		t.Fatal(result, err)
	}
	child := fetchValue(t, s, result.Revision, result.Records[0].ID)
	if child.Content != "Conversation s1:\nuser: hello there\nassistant: hi" {
		t.Fatal("heading without timestamps", child.Content)
	}
	written, err := time.Parse(time.RFC3339Nano, child.Data["eventAt"].(string))
	if err != nil || written.Before(before) || written.After(time.Now().UTC().Add(time.Second)) {
		t.Fatal("eventAt must be the write time", child.Data["eventAt"], err)
	}
	parent := fetchValue(t, s, result.Revision, "plain")
	if parent.Data["eventAt"] != child.Data["eventAt"] || parent.Data["source"] != "" {
		t.Fatal("parent and chunk share the write time", parent.Data)
	}
	if parent.Content != child.Content {
		t.Fatal("a single chunk is the whole parent")
	}
}

func TestWriteConversationDuplicateID(t *testing.T) {
	s, _ := agentStore(t)
	ctx := context.Background()
	req := ConversationRequest{ID: "dup", SessionID: "s1", Messages: []ConversationMessage{msg("user", "first")}}
	_, err := WriteConversation(ctx, s, req)
	if err != nil {
		t.Fatal(err)
	}
	req.Messages = []ConversationMessage{msg("user", "replacement")}
	if _, err = WriteConversation(ctx, s, req); !errors.Is(err, ErrDocumentExists) {
		t.Fatal("duplicate id must return ErrDocumentExists", err)
	}
	if got := fetchValue(t, s, "", "dup-00000"); !strings.Contains(got.Content, "first") || strings.Contains(got.Content, "replacement") {
		t.Fatal("duplicate write changed the stored document", got.Content)
	}
	// Write reports the same condition with the same sentinel and message.
	_, err = Write(ctx, s, WriteRequest{ID: "dup", Text: "replacement"})
	if !errors.Is(err, ErrDocumentExists) || err.Error() != "document id already exists; append a new document for a correction" {
		t.Fatal("Write must return ErrDocumentExists with its original message", err)
	}
	// A conversation cannot overwrite a document written by Write either.
	if _, err = Write(ctx, s, WriteRequest{ID: "plain-doc", Text: "text"}); err != nil {
		t.Fatal(err)
	}
	if _, err = WriteConversation(ctx, s, ConversationRequest{ID: "plain-doc", SessionID: "s1", Messages: []ConversationMessage{msg("user", "x")}}); !errors.Is(err, ErrDocumentExists) {
		t.Fatal(err)
	}
	// Existing chunk ids block an otherwise new document.
	seedNote(t, s, "taken-00000", "unrelated old record")
	if _, err = WriteConversation(ctx, s, ConversationRequest{ID: "taken", SessionID: "s1", Messages: []ConversationMessage{msg("user", "x")}}); !errors.Is(err, ErrDocumentExists) {
		t.Fatal("existing chunk overwritten", err)
	}
}

func TestPackLongAndCJK(t *testing.T) {
	s, _ := agentStore(t)
	ctx := context.Background()
	cjk := string([]rune(strings.Repeat("记忆代理写入测试，", 250))[:2000])
	if len([]rune(cjk)) != 2000 {
		t.Fatal("fixture", len([]rune(cjk)))
	}
	result, err := WriteConversation(ctx, s, ConversationRequest{
		ID: "cjk", SessionID: "会话-1", Messages: []ConversationMessage{msg("user", cjk)}})
	if err != nil {
		t.Fatal(err)
	}
	ids, joined := []string{}, ""
	for _, ref := range result.Records {
		child := fetchValue(t, s, result.Revision, ref.ID)
		if jsLen(child.Content) > 600 || !utf8.ValidString(child.Content) || !strings.HasPrefix(child.Content, "Conversation 会话-1:\n") {
			t.Fatal("CJK chunk", jsLen(child.Content))
		}
		ids = append(ids, ref.ID)
		joined += strings.TrimPrefix(child.Content, "Conversation 会话-1:\n")
	}
	// 2,000 CJK units + "user: " cut at 540: lines of 540, 540, 540, 386 -> one chunk each.
	if len(ids) != 4 || strings.TrimPrefix(joined, "user: ") != cjk {
		t.Fatal("CJK text must survive packing", len(ids))
	}
	if items, err := evidence(ctx, s, result.Revision, records.Scope{}, ids); err != nil || len(items) != len(ids) {
		t.Fatal("chunks must verify against the parent", err)
	}
	// A long ASCII message with emoji (two units each) also stays within the limit.
	emoji := strings.Repeat("hello 😀 world ", 300)
	result, err = WriteConversation(ctx, s, ConversationRequest{
		ID: "emoji", SessionID: "s1", Messages: []ConversationMessage{msg("user", emoji), msg("assistant", emoji)}})
	if err != nil {
		t.Fatal(err)
	}
	ids = ids[:0]
	for _, ref := range result.Records {
		child := fetchValue(t, s, result.Revision, ref.ID)
		if jsLen(child.Content) > 600 || !utf8.ValidString(child.Content) || strings.ContainsRune(child.Content, utf8.RuneError) {
			t.Fatal("emoji chunk", jsLen(child.Content))
		}
		ids = append(ids, ref.ID)
	}
	if items, err := evidence(ctx, s, result.Revision, records.Scope{}, ids); err != nil || len(items) != len(ids) {
		t.Fatal("emoji chunks must verify against the parent", err)
	}
}

func TestWriteConversationScopes(t *testing.T) {
	s, _ := agentStore(t)
	ctx := context.Background()
	abs := "/work/space"
	one := []ConversationMessage{msg("user", "scoped")}
	// session scope: the conversation session id is also the record session.
	result, err := WriteConversation(ctx, s, ConversationRequest{ID: "sess", SessionID: "s9", Scope: "session", WorkspaceID: &abs, Messages: one})
	if err != nil {
		t.Fatal(err)
	}
	child := fetchValue(t, s, result.Revision, result.Records[0].ID)
	if child.Scope != "session" || child.SessionID == nil || *child.SessionID != "s9" || child.WorkspaceID == nil || *child.WorkspaceID != abs || child.Data["session"] != "s9" {
		t.Fatal("session scope", child.Scope, child.SessionID, child.WorkspaceID)
	}
	if _, err = evidence(ctx, s, result.Revision, records.Scope{}, []string{child.ID}); err != nil {
		t.Fatal("scoped chunk must verify against its parent", err)
	}
	// project scope needs an absolute workspace and carries no record session.
	result, err = WriteConversation(ctx, s, ConversationRequest{ID: "proj", SessionID: "s9", Scope: "project", WorkspaceID: &abs, Messages: one})
	if err != nil {
		t.Fatal(err)
	}
	child = fetchValue(t, s, result.Revision, result.Records[0].ID)
	if child.Scope != "project" || child.SessionID != nil || *child.WorkspaceID != abs || child.Data["session"] != "s9" {
		t.Fatal("project scope", child.Scope, child.SessionID)
	}
	rel := "relative"
	bad := []ConversationRequest{
		{ID: "b1", SessionID: "s", Scope: "project", Messages: one},
		{ID: "b2", SessionID: "s", Scope: "project", WorkspaceID: &rel, Messages: one},
		{ID: "b3", SessionID: "s", Scope: "global", WorkspaceID: &abs, Messages: one},
		{ID: "b4", SessionID: "s", Scope: "weekly", Messages: one},
		{ID: "b5", SessionID: "s", Scope: "session", WorkspaceID: &rel, Messages: one},
	}
	for _, req := range bad {
		if _, err := WriteConversation(ctx, s, req); err == nil {
			t.Fatal("invalid scope accepted", req.ID)
		}
	}
}

func TestWriteConversationValidation(t *testing.T) {
	s, _ := agentStore(t)
	ctx := context.Background()
	one := []ConversationMessage{msg("user", "ok")}
	long := strings.Repeat("a", 2049)
	bad := map[string]ConversationRequest{
		"no messages":       {ID: "v1", SessionID: "s"},
		"no session":        {ID: "v2", Messages: one},
		"long session":      {ID: "v3", SessionID: strings.Repeat("s", 257), Messages: one},
		"bad session utf8":  {ID: "v4", SessionID: "s\xff", Messages: one},
		"bad id":            {ID: "has space", SessionID: "s", Messages: one},
		"long source":       {ID: "v5", SessionID: "s", Source: long, Messages: one},
		"blank role":        {ID: "v6", SessionID: "s", Messages: []ConversationMessage{msg(" ", "x")}},
		"bad content utf8":  {ID: "v7", SessionID: "s", Messages: []ConversationMessage{msg("user", "x\xff")}},
		"oversize":          {ID: "v8", SessionID: "s", Messages: []ConversationMessage{msg("user", strings.Repeat("x", 100001))}},
		"newline in role":   {ID: "v9", SessionID: "s", Messages: []ConversationMessage{msg("us\ner", "x")}},
		"newline in sessID": {ID: "v10", SessionID: "s\n1", Messages: one},
	}
	for name, req := range bad {
		if _, err := WriteConversation(ctx, s, req); err == nil {
			t.Fatal("accepted:", name)
		} else if errors.Is(err, ErrDocumentExists) {
			t.Fatal("validation error must not look like a duplicate:", name)
		}
	}
	// Nothing was written by the rejected requests, and the id is generated when omitted.
	result, err := WriteConversation(ctx, s, ConversationRequest{SessionID: "s", Messages: one})
	if err != nil || result.Document.ID == "" || !documentID.MatchString(result.Document.ID) {
		t.Fatal("generated id", result, err)
	}
}

func mustRaw(t *testing.T, s *records.Store, revision, id string) string {
	t.Helper()
	snapshot, err := s.Get(context.Background(), records.GetRequest{Revision: revision, IDs: []string{id}})
	if err != nil || len(snapshot.Records) != 1 {
		t.Fatal("record missing", id, err)
	}
	return snapshot.Records[0]
}

func mustQuery(t *testing.T, s *records.Store, revision, text string) []string {
	t.Helper()
	input := map[string]json.RawMessage{"query": json.RawMessage(`"` + text + `"`)}
	result, err := s.Query(context.Background(), records.QueryRequest{Revision: revision, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	return result.IDs
}
