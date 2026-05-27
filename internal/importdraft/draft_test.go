package importdraft

import (
	"strings"
	"testing"
)

func TestValidateAcceptsMinimalDraft(t *testing.T) {
	draft := &MemoryDraft{
		SchemaVersion: CurrentSchemaVersion,
		Insights: []DraftInsight{
			{Content: "User prefers concise answers."},
		},
	}

	if err := draft.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsInvalidInsightFields(t *testing.T) {
	cases := []struct {
		name  string
		draft *MemoryDraft
		want  string
	}{
		{
			name: "missing content",
			draft: &MemoryDraft{
				SchemaVersion: CurrentSchemaVersion,
				Insights:      []DraftInsight{{Category: "fact"}},
			},
			want: "content is required",
		},
		{
			name: "invalid category",
			draft: &MemoryDraft{
				SchemaVersion: CurrentSchemaVersion,
				Insights:      []DraftInsight{{Content: "x", Category: "note"}},
			},
			want: "invalid category",
		},
		{
			name: "invalid importance",
			draft: &MemoryDraft{
				SchemaVersion: CurrentSchemaVersion,
				Insights:      []DraftInsight{{Content: "x", Importance: 6}},
			},
			want: "importance",
		},
		{
			name: "invalid created_at",
			draft: &MemoryDraft{
				SchemaVersion: CurrentSchemaVersion,
				Insights:      []DraftInsight{{Content: "x", CreatedAt: "2024-01-01"}},
			},
			want: "RFC 3339",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.draft.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestValidateRejectsInvalidEdges(t *testing.T) {
	draft := &MemoryDraft{
		SchemaVersion: CurrentSchemaVersion,
		Insights: []DraftInsight{
			{Content: "first"},
			{Content: "second"},
		},
		Edges: []DraftEdge{
			{SourceIndex: 0, TargetIndex: 2, EdgeType: "semantic"},
		},
	}

	err := draft.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil")
	}
	if !strings.Contains(err.Error(), "target_index") {
		t.Fatalf("Validate() error = %v, want target_index", err)
	}
}

func TestResolvedSourceFallsBackFromInsightToDraftToImport(t *testing.T) {
	draft := &MemoryDraft{
		Source: "chat-export",
		Insights: []DraftInsight{
			{Content: "one", Source: "manual"},
			{Content: "two"},
		},
	}

	if got := draft.ResolvedSource(0); got != "manual" {
		t.Fatalf("ResolvedSource(0) = %q, want manual", got)
	}
	if got := draft.ResolvedSource(1); got != "chat-export" {
		t.Fatalf("ResolvedSource(1) = %q, want chat-export", got)
	}

	draft.Source = ""
	if got := draft.ResolvedSource(1); got != "import" {
		t.Fatalf("ResolvedSource(1) = %q, want import", got)
	}
}
