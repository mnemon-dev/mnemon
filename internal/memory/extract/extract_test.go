package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type captured struct {
	auth string
	body wireRequest
}

func serve(t *testing.T, status int, reply string) (*Client, *[]captured) {
	t.Helper()
	var got []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req wireRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		got = append(got, captured{auth: r.Header.Get("Authorization"), body: req})
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MNEMON_EXTRACT_MODEL", "clairic")
	t.Setenv("MNEMON_EXTRACT_ENDPOINT", srv.URL+"/ai/run/@cf/cloudflare/clairic")
	t.Setenv("MNEMON_EXTRACT_API_KEY", "k-secret")
	t.Setenv("MNEMON_EXTRACT_TIMEOUT", "")
	c, err := NewClient()
	if err != nil || c == nil {
		t.Fatalf("NewClient = %v, %v", c, err)
	}
	return c, &got
}

func envelope(items any) string {
	data, _ := json.Marshal(map[string]any{
		"success": true,
		"errors":  []any{},
		"result":  map[string]any{"answers": map[string]any{"entities": map[string]any{"type": "list", "value": items}}},
	})
	return string(data)
}

func TestNewClientIsNilWithoutModel(t *testing.T) {
	t.Setenv("MNEMON_EXTRACT_MODEL", "")
	t.Setenv("MNEMON_EXTRACT_ENDPOINT", "http://127.0.0.1:1")
	if c, err := NewClient(); c != nil || err != nil {
		t.Fatalf("NewClient = %v, %v; want nil, nil", c, err)
	}
}

func TestNewClientRequiresEndpoint(t *testing.T) {
	t.Setenv("MNEMON_EXTRACT_MODEL", "clairic")
	t.Setenv("MNEMON_EXTRACT_ENDPOINT", "")
	if c, err := NewClient(); c != nil || !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("NewClient = %v, %v; want nil, ErrNoEndpoint", c, err)
	}
}

func TestEntitiesSendsTypedListRequest(t *testing.T) {
	// Given a Workers AI server that finds one name
	c, got := serve(t, http.StatusOK, envelope([]string{"Jane Rivera"}))
	content := "Jane Rivera joined the board"

	// When entities are requested
	names, err := c.Entities(context.Background(), content)

	// Then the name comes back, and the request carried the model, content, and a typed list field
	if err != nil || !reflect.DeepEqual(names, []string{"Jane Rivera"}) {
		t.Fatalf("Entities = %v, %v", names, err)
	}
	if len(*got) != 1 {
		t.Fatalf("requests = %d", len(*got))
	}
	req := (*got)[0]
	field := req.body.Fields[entitiesField]
	if req.auth != "Bearer k-secret" || req.body.Model != "clairic" || req.body.State != content ||
		field.Type != "list" || field.Items != "text" || field.Instructions == "" {
		t.Fatalf("request = %+v", req)
	}
}

func TestEntitiesAcceptsBareSystemOneResponse(t *testing.T) {
	c, _ := serve(t, http.StatusOK, `{"answers":{"entities":{"type":"list","value":["Acme Foundation"]}}}`)
	names, err := c.Entities(context.Background(), "funded by the Acme Foundation")
	if err != nil || !reflect.DeepEqual(names, []string{"Acme Foundation"}) {
		t.Fatalf("Entities = %v, %v", names, err)
	}
}

func TestEntitiesKeepsOnlyWholeTokensFromContent(t *testing.T) {
	// Given model output with edge noise, fragments, a duplicate, and an invented name
	content := "Added ~/w/demo for tool 5.1.0; the 「示範專案」 seal and a centre 愛; zh-TW cards; Jane Rivera joined."
	items := []string{
		"~/", "w/demo", "tool 5.1", "示範專案」", "TW", " Jane Rivera ",
		"Ghost Corp", "2026", "愛", "Jane Rivera", "~/w/demo",
	}
	c, _ := serve(t, http.StatusOK, envelope(items))

	// When entities are requested
	names, err := c.Entities(context.Background(), content)

	// Then only trimmed names that stand alone in the content survive, once each
	want := []string{"示範專案", "Jane Rivera", "~/w/demo"}
	if err != nil || !reflect.DeepEqual(names, want) {
		t.Fatalf("Entities = %v, %v; want %v", names, err, want)
	}
}

func TestEntitiesNullListIsEmptyNotNil(t *testing.T) {
	c, _ := serve(t, http.StatusOK, envelope(nil))
	names, err := c.Entities(context.Background(), "nothing named here")
	if err != nil || names == nil || len(names) != 0 {
		t.Fatalf("Entities = %#v, %v; want empty non-nil", names, err)
	}
}

func TestEntitiesCapsAtMaxEntities(t *testing.T) {
	items := make([]string, MaxEntities+10)
	for i := range items {
		items[i] = fmt.Sprintf("Item%02d", i)
	}
	c, _ := serve(t, http.StatusOK, envelope(items))
	names, err := c.Entities(context.Background(), strings.Join(items, " "))
	if err != nil || len(names) != MaxEntities {
		t.Fatalf("Entities returned %d names, %v; want %d", len(names), err, MaxEntities)
	}
}

func TestEntitiesErrorsNameTheFailureWithoutContentOrKey(t *testing.T) {
	const content = "secret plan for project Zebra"
	cases := []struct {
		name   string
		status int
		reply  string
		want   string
	}{
		{"http status", http.StatusUnauthorized,
			`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`, "status 401: Authentication error"},
		{"envelope failure", http.StatusOK, `{"success":false,"errors":[{"message":"model overloaded"}]}`, "reported failure: model overloaded"},
		{"wrong answer type", http.StatusOK, `{"answers":{"entities":{"type":"text","value":"Zebra"}}}`, `type "text"`},
		{"missing answer", http.StatusOK, `{"result":{"answers":{}}}`, `no "entities" answer`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := serve(t, tc.status, tc.reply)
			names, err := c.Entities(context.Background(), content)
			if err == nil || names != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Entities = %v, %v; want error containing %q", names, err, tc.want)
			}
			if strings.Contains(err.Error(), "k-secret") || strings.Contains(err.Error(), "secret plan") {
				t.Fatalf("error leaks the key or content: %v", err)
			}
		})
	}
}
