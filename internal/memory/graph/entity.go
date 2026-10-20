package graph

import (
	"regexp"
	"strings"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

// Maximum number of existing nodes to link per entity (avoid hot-entity explosion).
const maxEntityLinks = 5

// Maximum total entity edges to create per insight (bounds cost of many-entity insights).
const maxTotalEntityEdges = 50

// EntityMode controls how final insight entities are derived.
type EntityMode string

const (
	// EntityModeMerge keeps pre-provided entities and supplements them with auto extraction.
	EntityModeMerge EntityMode = "merge"
	// EntityModeProvided uses only pre-provided entities.
	EntityModeProvided EntityMode = "provided"
	// EntityModeAuto uses only auto-extracted entities.
	EntityModeAuto EntityMode = "auto"
)

// ValidEntityMode reports whether mode is a supported entity handling mode.
func ValidEntityMode(mode EntityMode) bool {
	switch mode {
	case EntityModeMerge, EntityModeProvided, EntityModeAuto:
		return true
	default:
		return false
	}
}

var (
	// acronymPattern matches ALLCAPS acronyms 2-6 chars (e.g., API, HTTP, SQL).
	// It is the one pattern that guesses whether a token names something, so
	// model-extracted entities replace it (see ResolveEntitiesWithModel).
	acronymPattern = regexp.MustCompile(`\b([A-Z]{2,6})\b`)
	// filePathPattern matches file paths and dotted names (e.g., ./cmd/root.go,
	// /etc/config.yml, example.org). Its matches must also pass looksLikePath.
	filePathPattern = regexp.MustCompile(`(?:^|[\s"'(])([.\w/-]+\.\w{1,10})(?:[\s"'),.]|$)`)
)

var entityPatterns = []*regexp.Regexp{
	// CamelCase identifiers (e.g., MyClass, HttpServer)
	regexp.MustCompile(`\b([A-Z][a-z]+(?:[A-Z][a-z]+)+)\b`),
	acronymPattern,
	filePathPattern,
	// URLs
	regexp.MustCompile(`https?://[^\s"'<>)]+`),
	// @mentions
	regexp.MustCompile(`@([a-zA-Z_]\w+)`),
	// Chinese book title marks / quotes
	regexp.MustCompile(`[《「]([^》」]+)[》」]`),
}

// wideEntityPatterns are looser regexes whose matches are NOT admitted on
// their own — they must be filtered against a caller-supplied known-entity
// set. They form the "fourth path" of ExtractEntitiesIndexed: a wide cast
// over capitalized tokens (single-segment CamelCase, name-like identifiers)
// that the default patterns deliberately skip to avoid noise. The index
// acts as the filter, so noise is dropped before it reaches the caller.
var wideEntityPatterns = []*regexp.Regexp{
	// Capitalized tokens of length >= 2 — covers single-segment CamelCase
	// (Athena, Hestia, FastAPI) without admitting bare initial-caps words.
	regexp.MustCompile(`\b([A-Z][a-zA-Z0-9]+)\b`),
}

// techDictionary contains common technology terms that regex patterns miss
// because they look like ordinary words (lowercase, single word, etc.).
var techDictionary = map[string]bool{
	// Languages
	"Go": true, "Rust": true, "Python": true, "Java": true, "Kotlin": true,
	"Swift": true, "Ruby": true, "Elixir": true, "Zig": true, "Lua": true,
	"Dart": true, "Scala": true, "Perl": true, "Haskell": true, "OCaml": true,
	"Julia": true, "Clojure": true,
	// JS ecosystem
	"JavaScript": true, "TypeScript": true, "React": true, "Vue": true,
	"Angular": true, "Svelte": true, "Next": true, "Nuxt": true,
	"Node": true, "Deno": true, "Bun": true, "Vite": true, "Webpack": true,
	// Databases
	"SQLite": true, "PostgreSQL": true, "Postgres": true, "MySQL": true,
	"Redis": true, "MongoDB": true, "DynamoDB": true, "Cassandra": true,
	"Qdrant": true, "Milvus": true, "Chroma": true, "Pinecone": true,
	"Neo4j": true, "Weaviate": true, "Elasticsearch": true,
	// Infra & Cloud
	"Docker": true, "Kubernetes": true, "Terraform": true, "Ansible": true,
	"Nginx": true, "Caddy": true, "Kafka": true, "RabbitMQ": true,
	"AWS": true, "GCP": true, "Azure": true, "Vercel": true, "Netlify": true,
	"Cloudflare": true, "Supabase": true, "Firebase": true,
	// AI/ML
	"Ollama": true, "OpenAI": true, "Claude": true, "Anthropic": true,
	"PyTorch": true, "TensorFlow": true, "LangChain": true, "LlamaIndex": true,
	"FAISS": true, "Hugging": true,
	// Tools & Frameworks
	"Git": true, "GitHub": true, "GitLab": true, "Cobra": true,
	"FastAPI": true, "Flask": true, "Django": true, "Rails": true,
	"Spring": true, "Express": true, "Gin": true, "Echo": true, "Fiber": true,
	"Pytest": true, "Jest": true, "Vitest": true,
	// Protocols & Formats
	"gRPC": true, "GraphQL": true, "WebSocket": true, "OAuth": true,
	"JWT": true, "YAML": true, "TOML": true, "Protobuf": true,
	// Mnemon-specific
	"MAGMA": true, "MCP": true, "RLM": true,
}

// acronymStopwords filters out all-caps tokens that aren't tech entities:
// short common English words, and ordinary words written in capitals for
// emphasis or as labels (NEVER, ONLY, NOTE, FIXED). In a real store the latter
// were among the most frequent "entities" and linked unrelated memories.
// Words that double as technical names (HEAD, POST, PATH, HOME, REST, DELETE,
// LINE) are deliberately absent.
var acronymStopwords = map[string]bool{
	"IN": true, "ON": true, "AT": true, "TO": true, "BY": true,
	"OR": true, "AN": true, "IF": true, "IS": true, "IT": true,
	"OF": true, "AS": true, "DO": true, "NO": true, "SO": true,
	"UP": true, "WE": true, "HE": true, "MY": true, "BE": true,
	"GO":  true, // "Go" (capitalized) is in techDictionary, but "GO" all-caps is ambiguous
	"THE": true, "AND": true, "FOR": true, "ARE": true, "BUT": true,
	"NOT": true, "YOU": true, "ALL": true, "CAN": true, "HER": true,
	"WAS": true, "ONE": true, "OUR": true, "OUT": true, "HAS": true,
	"HAD": true, "HOW": true, "MAN": true, "NEW": true, "NOW": true,
	"OLD": true, "SEE": true, "WAY": true, "MAY": true, "SAY": true,
	"SHE": true, "TWO": true, "USE": true, "BOY": true, "DID": true,
	"GET": true, "HIM": true, "HIS": true, "LET": true, "PUT": true,
	"TOP": true, "TOO": true, "ANY": true,
	// Ordinary words written in capitals for emphasis or as labels.
	"OK": true, "YES": true, "TRUE": true, "FALSE": true,
	"ONLY": true, "BOTH": true, "NEVER": true, "ALWAYS": true, "MUST": true,
	"STILL": true, "SAME": true, "ALSO": true, "EVERY": true, "OTHER": true,
	"OWN": true, "REAL": true, "WORTH": true, "HARD": true, "FULL": true,
	"EMPTY": true, "ZERO": true, "KEEP": true, "SKIP": true, "LEFT": true,
	"WITH": true, "FROM": true, "THAT": true, "THIS": true, "THAN": true,
	"WHAT": true, "WHY": true, "DOES": true, "CANNOT": true, "NEEDS": true,
	"BEFORE": true, "AFTER": true, "FIRST": true, "SECOND": true, "THIRD": true,
	"FINAL": true, "THREE": true, "FOUR": true, "FIVE": true, "HIGH": true,
	"PASS": true, "FAIL": true, "FAILED": true, "FIX": true, "FIXED": true,
	"DONE": true, "WRONG": true, "STALE": true, "ARMED": true, "OPEN": true,
	"CLOSED": true, "FOUND": true, "SENT": true, "SAFE": true, "CLEAN": true,
	"VERIFY": true, "CHECK": true, "REVIEW": true, "DRAFT": true, "PROOF": true,
	"NOTE": true, "NOTES": true, "LESSON": true, "RULE": true, "FACT": true,
	"FACTS": true, "DEFECT": true, "CAUSE": true, "GOTCHA": true, "TRAP": true,
	"BUG": true, "ISSUE": true, "ERROR": true,
}

// ExtractEntities extracts named entities from text using regex patterns
// and a technology dictionary for common terms regex would miss.
func ExtractEntities(text string) []string {
	return extractEntities(text, true)
}

// extractEntities runs the regex and dictionary paths. guessAcronyms selects
// acronymPattern; callers holding model-extracted entities leave it out.
func extractEntities(text string, guessAcronyms bool) []string {
	seen := make(map[string]bool)
	var entities []string

	// 1. Regex-based extraction
	for _, pat := range entityPatterns {
		if pat == acronymPattern && !guessAcronyms {
			continue
		}
		matches := pat.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			entity := m[len(m)-1]
			if entity == "" || seen[entity] {
				continue
			}
			// Filter acronym stopwords
			if acronymStopwords[entity] {
				continue
			}
			if pat == filePathPattern && !looksLikePath(entity) {
				continue
			}
			seen[entity] = true
			entities = append(entities, entity)
		}
	}

	// 2. Dictionary-based extraction: scan for known tech terms
	words := splitWords(text)
	for _, word := range words {
		if techDictionary[word] && !seen[word] {
			seen[word] = true
			entities = append(entities, word)
		}
	}

	return entities
}

// looksLikePath reports whether a file-path match could name a file or
// domain. The path pattern also matches numbers, versions, and addresses with
// no letter (0.52, 127.0.0.1) and dotted single letters (e.g, i.e, J.Y, p.4);
// in a real store those were thousands of entities that named nothing.
func looksLikePath(s string) bool {
	if !strings.ContainsFunc(s, func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }) {
		return false
	}
	for _, segment := range strings.Split(s, ".") {
		if len(segment) > 1 {
			return true
		}
	}
	return false
}

// ResolveEntities returns the final entity list for a content/provided pair.
func ResolveEntities(content string, provided []string, mode EntityMode) []string {
	return ResolveEntitiesIndexed(content, provided, mode, nil)
}

// ExtractEntitiesIndexed extends ExtractEntities with a fourth extraction
// path: a wide-cast regex over capitalized tokens plus a tokenized scan over
// the rest of the text, whose candidates are admitted only when they appear
// in knownEntities. Store callers exclude hot entities from this filter using
// active-insight document frequency. This call does not mutate the index, but
// write callers persist its output, feeding later writes; the frequency gate
// bounds that feedback. Strict regex and dictionary paths bypass the filter.
//
// The fourth path exists because the default regex+dictionary paths
// short-circuit to an empty entity set on text whose only entities are
// single-segment CamelCase names (e.g., personal-project vocabulary) that
// are not in techDictionary. When such names already appear on prior
// insights — i.e., live in the entity index — the indexed extractor admits
// them on subsequent text without any dictionary edit.
//
// When knownEntities is nil or empty, behavior is identical to
// ExtractEntities. Callers without DB access can pass nil to get the
// pre-existing semantics.
func ExtractEntitiesIndexed(text string, knownEntities map[string]bool) []string {
	return extractEntitiesIndexed(text, knownEntities, true)
}

// extractEntitiesIndexed is ExtractEntitiesIndexed with the acronym guess
// optional (see extractEntities).
func extractEntitiesIndexed(text string, knownEntities map[string]bool, guessAcronyms bool) []string {
	entities := extractEntities(text, guessAcronyms)
	if len(knownEntities) == 0 {
		return entities
	}
	seen := make(map[string]bool, len(entities)+8)
	for _, e := range entities {
		seen[e] = true
	}
	// Fourth-path A: wide-cast capitalized tokens, admitted only when known.
	for _, pat := range wideEntityPatterns {
		for _, m := range pat.FindAllStringSubmatch(text, -1) {
			cand := m[len(m)-1]
			if cand == "" || seen[cand] || acronymStopwords[cand] {
				continue
			}
			if knownEntities[cand] {
				seen[cand] = true
				entities = append(entities, cand)
			}
		}
	}
	// Fourth-path B: every tokenized word, admitted only when known. This
	// catches non-CamelCase user vocabulary stored in the index (e.g.,
	// lowercase project names or short identifiers).
	for _, word := range splitWords(text) {
		if word == "" || seen[word] || acronymStopwords[word] {
			continue
		}
		if knownEntities[word] {
			seen[word] = true
			entities = append(entities, word)
		}
	}
	return entities
}

// ResolveEntitiesIndexed is the index-aware sibling of ResolveEntities.
// See ExtractEntitiesIndexed for the fourth-path semantics. A nil
// knownEntities map is identical to calling ResolveEntities.
func ResolveEntitiesIndexed(content string, provided []string, mode EntityMode, knownEntities map[string]bool) []string {
	switch mode {
	case EntityModeProvided:
		return mergeEntities(provided, nil)
	case EntityModeAuto:
		return mergeEntities(nil, ExtractEntitiesIndexed(content, knownEntities))
	default:
		return mergeEntities(provided, ExtractEntitiesIndexed(content, knownEntities))
	}
}

// ReliesOnExtraction reports whether resolving entities for mode and provided
// uses automatic extraction alone: auto mode, or merge mode with nothing
// provided. Only then is an extraction model worth asking, because entities a
// caller provides already name what the memory is about.
func ReliesOnExtraction(mode EntityMode, provided []string) bool {
	switch mode {
	case EntityModeAuto:
		return true
	case EntityModeMerge:
		return len(provided) == 0
	default:
		return false
	}
}

// ResolveEntitiesWithModel is ResolveEntitiesIndexed for content an extraction
// model has read. When resolution relies on extraction alone (see
// ReliesOnExtraction), the model's entities replace the acronym guess, while
// the exact paths (CamelCase, file paths, URLs, @mentions, title marks,
// dictionary) and the known-entity index still apply. A nil modelEntities
// means no model ran and is identical to ResolveEntitiesIndexed; an empty,
// non-nil one means the model found nothing.
func ResolveEntitiesWithModel(content string, provided []string, mode EntityMode, knownEntities map[string]bool, modelEntities []string) []string {
	if modelEntities == nil || !ReliesOnExtraction(mode, provided) {
		return ResolveEntitiesIndexed(content, provided, mode, knownEntities)
	}
	return mergeEntities(modelEntities, extractEntitiesIndexed(content, knownEntities, false))
}

// splitWords splits text into ASCII-alphanumeric words preserving original casing.
func splitWords(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9')
	})
}

// mergeEntities deduplicates and merges pre-provided entities (e.g. LLM-extracted)
// with regex-extracted entities. Pre-provided entities appear first.
func mergeEntities(provided, extracted []string) []string {
	seen := make(map[string]bool)
	var merged []string
	for _, e := range provided {
		if e != "" && !seen[e] {
			seen[e] = true
			merged = append(merged, e)
		}
	}
	for _, e := range extracted {
		if e != "" && !seen[e] {
			seen[e] = true
			merged = append(merged, e)
		}
	}
	if merged == nil {
		merged = []string{}
	}
	return merged
}

// CreateEntityEdges creates entity co-occurrence edges between the new insight
// and existing insights that share the same entities.
func CreateEntityEdges(db *store.DB, insight *model.Insight) int {
	if len(insight.Entities) == 0 {
		return 0
	}

	now := time.Now().UTC()
	count := 0

	for _, entity := range insight.Entities {
		if count >= maxTotalEntityEdges {
			break
		}
		ids, err := db.FindInsightsWithEntity(entity, insight.ID, maxEntityLinks)
		if err != nil || len(ids) == 0 {
			continue
		}

		for _, targetID := range ids {
			if count >= maxTotalEntityEdges {
				break
			}
			// new → old
			err = db.InsertEdge(&model.Edge{
				SourceID:  insight.ID,
				TargetID:  targetID,
				EdgeType:  model.EdgeEntity,
				Weight:    1.0,
				Metadata:  map[string]string{"entity": entity},
				CreatedAt: now,
			})
			if err == nil {
				count++
			}
			// old → new (reverse)
			err = db.InsertEdge(&model.Edge{
				SourceID:  targetID,
				TargetID:  insight.ID,
				EdgeType:  model.EdgeEntity,
				Weight:    1.0,
				Metadata:  map[string]string{"entity": entity},
				CreatedAt: now,
			})
			if err == nil {
				count++
			}
		}
	}
	return count
}
