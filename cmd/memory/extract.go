package memory

import (
	"context"
	"fmt"
	"os"

	"github.com/mnemon-dev/mnemon/internal/memory/extract"
	"github.com/mnemon-dev/mnemon/internal/memory/graph"
)

// entityExtractor asks the opt-in extraction model (MNEMON_EXTRACT_MODEL) for
// a memory's entities before the write transaction. It fails open: after the
// first failure it warns once and stops asking for the rest of the command, so
// a batch import does not wait out the timeout on every insight.
type entityExtractor struct {
	client *extract.Client
}

func newEntityExtractor() *entityExtractor {
	client, err := extract.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: entity extraction disabled: %v\n", err)
	}
	return &entityExtractor{client: client}
}

// entities returns the model's entities for content, or nil when no model ran
// because extraction is not configured, the provided entities make it
// unnecessary, or it failed.
func (x *entityExtractor) entities(content string, provided []string, mode graph.EntityMode) []string {
	if x.client == nil || !graph.ReliesOnExtraction(mode, provided) {
		return nil
	}
	names, err := x.client.Entities(context.Background(), content)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: entity extraction unavailable, using heuristic extraction: %v\n", err)
		x.client = nil
		return nil
	}
	return names
}
