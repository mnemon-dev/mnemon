package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/spf13/cobra"
)

var vizFormat string
var vizOutput string

var vizCmd = &cobra.Command{
	Use:   "viz",
	Short: "Export knowledge graph for visualization",
	Long:  "Export the knowledge graph as DOT (Graphviz) or HTML (vis.js interactive) format.",
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		insights, err := db.GetAllActiveInsights()
		if err != nil {
			return fmt.Errorf("get insights: %w", err)
		}
		edges, err := db.GetAllEdges()
		if err != nil {
			return fmt.Errorf("get edges: %w", err)
		}

		var out string
		switch vizFormat {
		case "dot":
			out = renderDOT(insights, edges)
		case "html":
			out = renderHTML(insights, edges)
		default:
			return fmt.Errorf("unsupported format: %s (use dot or html)", vizFormat)
		}

		if vizOutput == "" || vizOutput == "-" {
			fmt.Print(out)
			return nil
		}
		if err := os.WriteFile(vizOutput, []byte(out), 0644); err != nil {
			return fmt.Errorf("write file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "written to %s\n", vizOutput)
		return nil
	},
}

func init() {
	vizCmd.Flags().StringVar(&vizFormat, "format", "dot", "output format: dot or html")
	vizCmd.Flags().StringVarP(&vizOutput, "output", "o", "-", "output file (- for stdout)")
	rootCmd.AddCommand(vizCmd)
}

// nodeLabel returns a short display label for a node.
func nodeLabel(i *model.Insight) string {
	content := i.Content
	if utf8.RuneCountInString(content) > 60 {
		content = truncateRunes(content, 60) + "..."
	}
	return fmt.Sprintf("[%s] %s", i.Category, content)
}

// categoryColor returns a color for a category.
func categoryColor(c model.Category) string {
	switch c {
	case model.CategoryDecision:
		return "#e74c3c"
	case model.CategoryFact:
		return "#3498db"
	case model.CategoryInsight:
		return "#9b59b6"
	case model.CategoryPreference:
		return "#2ecc71"
	case model.CategoryContext:
		return "#f39c12"
	default:
		return "#95a5a6"
	}
}

// edgeColor returns a color for an edge type.
func edgeColor(t model.EdgeType) string {
	switch t {
	case model.EdgeTemporal:
		return "#aaaaaa"
	case model.EdgeSemantic:
		return "#3498db"
	case model.EdgeCausal:
		return "#e74c3c"
	case model.EdgeEntity:
		return "#2ecc71"
	default:
		return "#cccccc"
	}
}

func renderDOT(insights []*model.Insight, edges []*model.Edge) string {
	var b strings.Builder
	b.WriteString("digraph mnemon {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=\"filled,rounded\", fontsize=10, fontname=\"Helvetica\"];\n")
	b.WriteString("  edge [fontsize=8, fontname=\"Helvetica\"];\n\n")

	// Build set of active IDs for edge filtering
	active := make(map[string]bool, len(insights))
	for _, i := range insights {
		active[i.ID] = true
	}

	for _, i := range insights {
		label := strings.ReplaceAll(nodeLabel(i), `"`, `\"`)
		label = strings.ReplaceAll(label, "\n", " ")
		shortID := i.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		color := categoryColor(i.Category)
		b.WriteString(fmt.Sprintf("  %q [label=%q, fillcolor=%q, fontcolor=\"white\"];\n",
			i.ID, shortID+": "+label, color))
	}

	b.WriteString("\n")
	for _, e := range edges {
		if !active[e.SourceID] || !active[e.TargetID] {
			continue
		}
		color := edgeColor(e.EdgeType)
		subType := e.Metadata["sub_type"]
		edgeLabel := string(e.EdgeType)
		if subType != "" {
			edgeLabel = subType
		}
		b.WriteString(fmt.Sprintf("  %q -> %q [label=%q, color=%q, fontcolor=%q];\n",
			e.SourceID, e.TargetID, edgeLabel, color, color))
	}

	b.WriteString("}\n")
	return b.String()
}

// jsStr returns a JSON-encoded string literal safe for embedding in JavaScript.
// json.Marshal produces properly escaped output for \n, \t, ", \, etc.
func jsStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func renderHTML(insights []*model.Insight, edges []*model.Edge) string {
	// Build set of active IDs for edge filtering
	active := make(map[string]bool, len(insights))
	for _, i := range insights {
		active[i.ID] = true
	}

	var nodes strings.Builder
	for idx, i := range insights {
		if idx > 0 {
			nodes.WriteString(",\n")
		}
		shortID := i.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		label := nodeLabel(i)
		label = strings.ReplaceAll(label, "\n", " ")
		title := strings.ReplaceAll(i.Content, "\n", "\\n")
		color := categoryColor(i.Category)
		nodes.WriteString(fmt.Sprintf(
			`{id:%s,label:%s,title:%s,color:%s,font:{color:"white"}}`,
			jsStr(i.ID), jsStr(shortID+": "+label), jsStr(title), jsStr(color)))
	}

	var edgesJS strings.Builder
	first := true
	for _, e := range edges {
		if !active[e.SourceID] || !active[e.TargetID] {
			continue
		}
		if !first {
			edgesJS.WriteString(",\n")
		}
		first = false
		color := edgeColor(e.EdgeType)
		subType := e.Metadata["sub_type"]
		edgeLabel := string(e.EdgeType)
		if subType != "" {
			edgeLabel = subType
		}
		edgesJS.WriteString(fmt.Sprintf(
			`{from:%s,to:%s,label:%s,color:{color:%s},arrows:"to",font:{color:%s,size:10}}`,
			jsStr(e.SourceID), jsStr(e.TargetID), jsStr(edgeLabel), jsStr(color), jsStr(color)))
	}

	return fmt.Sprintf(htmlTemplate, nodes.String(), edgesJS.String())
}

const htmlTemplate = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Mnemon Knowledge Graph</title>
<script src="https://unpkg.com/vis-network/standalone/umd/vis-network.min.js"></script>
<style>
  body { margin: 0; padding: 0; background: #1a1a2e; font-family: sans-serif; }
  #graph { width: 100vw; height: 100vh; }
  #legend { position: fixed; top: 10px; right: 10px; background: rgba(0,0,0,0.7);
    color: white; padding: 12px; border-radius: 8px; font-size: 12px; }
  .leg-item { display: flex; align-items: center; margin: 4px 0; }
  .leg-dot { width: 12px; height: 12px; border-radius: 50%%; margin-right: 8px; }
  .leg-line { width: 20px; height: 3px; margin-right: 8px; }
</style>
</head>
<body>
<div id="graph"></div>
<div id="legend">
  <b>Nodes</b>
  <div class="leg-item"><div class="leg-dot" style="background:#e74c3c"></div>decision</div>
  <div class="leg-item"><div class="leg-dot" style="background:#3498db"></div>fact</div>
  <div class="leg-item"><div class="leg-dot" style="background:#9b59b6"></div>insight</div>
  <div class="leg-item"><div class="leg-dot" style="background:#2ecc71"></div>preference</div>
  <div class="leg-item"><div class="leg-dot" style="background:#f39c12"></div>context</div>
  <div class="leg-item"><div class="leg-dot" style="background:#95a5a6"></div>general</div>
  <br><b>Edges</b>
  <div class="leg-item"><div class="leg-line" style="background:#aaaaaa"></div>temporal</div>
  <div class="leg-item"><div class="leg-line" style="background:#3498db"></div>semantic</div>
  <div class="leg-item"><div class="leg-line" style="background:#e74c3c"></div>causal</div>
  <div class="leg-item"><div class="leg-line" style="background:#2ecc71"></div>entity</div>
</div>
<script>
var nodes = new vis.DataSet([%s]);
var edges = new vis.DataSet([%s]);
var container = document.getElementById("graph");
var data = { nodes: nodes, edges: edges };
var options = {
  physics: { solver: "forceAtlas2Based", forceAtlas2Based: { gravitationalConstant: -30 } },
  interaction: { hover: true, tooltipDelay: 100 },
  nodes: { shape: "box", margin: 8, borderWidth: 0, font: { size: 11 } },
  edges: { smooth: { type: "continuous" }, font: { size: 9 } }
};
new vis.Network(container, data, options);
</script>
</body>
</html>`
