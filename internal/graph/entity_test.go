package graph

import (
	"testing"
)

func TestExtractEntities_CamelCase(t *testing.T) {
	entities := ExtractEntities("The HttpServer handles requests from MyClient")
	has := toSet(entities)
	if !has["HttpServer"] {
		t.Error("want CamelCase entity 'HttpServer'")
	}
	if !has["MyClient"] {
		t.Error("want CamelCase entity 'MyClient'")
	}
}

func TestExtractEntities_Acronyms(t *testing.T) {
	entities := ExtractEntities("Use the API with HTTP and SQL databases")
	has := toSet(entities)
	if !has["API"] {
		t.Error("want acronym 'API'")
	}
	if !has["HTTP"] {
		t.Error("want acronym 'HTTP'")
	}
	if !has["SQL"] {
		t.Error("want acronym 'SQL'")
	}
}

func TestExtractEntities_AcronymStopwords(t *testing.T) {
	entities := ExtractEntities("IT IS IN THE BOX")
	has := toSet(entities)
	// "IT", "IS", "IN" are stopwords
	if has["IT"] || has["IS"] || has["IN"] || has["THE"] {
		t.Errorf("acronym stopwords should be filtered, got %v", entities)
	}
}

func TestExtractEntities_URLs(t *testing.T) {
	entities := ExtractEntities("Visit https://github.com/mnemon-dev/mnemon for details")
	has := toSet(entities)
	if !has["https://github.com/mnemon-dev/mnemon"] {
		t.Errorf("want URL entity, got %v", entities)
	}
}

func TestExtractEntities_AtMentions(t *testing.T) {
	entities := ExtractEntities("Created by @johndoe and @alice")
	has := toSet(entities)
	if !has["johndoe"] {
		t.Error("want @mention 'johndoe'")
	}
	if !has["alice"] {
		t.Error("want @mention 'alice'")
	}
}

func TestExtractEntities_ChineseBookTitles(t *testing.T) {
	entities := ExtractEntities("参考《知识图谱》和「内存管理」")
	has := toSet(entities)
	if !has["知识图谱"] {
		t.Error("want 《》 entity '知识图谱'")
	}
	if !has["内存管理"] {
		t.Error("want 「」 entity '内存管理'")
	}
}

func TestExtractEntities_TechDictionary(t *testing.T) {
	entities := ExtractEntities("Built with Go and SQLite, deployed on Docker")
	has := toSet(entities)
	if !has["Go"] {
		t.Error("want tech dict 'Go'")
	}
	if !has["SQLite"] {
		t.Error("want tech dict 'SQLite'")
	}
	if !has["Docker"] {
		t.Error("want tech dict 'Docker'")
	}
}

func TestExtractEntities_FilePaths(t *testing.T) {
	entities := ExtractEntities("Edit the file cmd/root.go to add the flag")
	has := toSet(entities)
	if !has["cmd/root.go"] {
		t.Errorf("want file path 'cmd/root.go', got %v", entities)
	}
}

func TestExtractEntities_Empty(t *testing.T) {
	entities := ExtractEntities("")
	if len(entities) != 0 {
		t.Errorf("empty input: want 0 entities, got %v", entities)
	}
}

func TestExtractEntities_NoDuplicates(t *testing.T) {
	// "API" appears via regex AND could be in dictionary
	entities := ExtractEntities("API calls to API endpoints")
	count := 0
	for _, e := range entities {
		if e == "API" {
			count++
		}
	}
	if count > 1 {
		t.Errorf("want no duplicate 'API', got %d occurrences", count)
	}
}

func TestMergeEntities_Basic(t *testing.T) {
	provided := []string{"Go", "Docker"}
	extracted := []string{"SQLite", "Go"} // "Go" is duplicate
	merged := mergeEntities(provided, extracted)

	if len(merged) != 3 {
		t.Fatalf("want 3 merged entities, got %d: %v", len(merged), merged)
	}
	// Provided first
	if merged[0] != "Go" || merged[1] != "Docker" || merged[2] != "SQLite" {
		t.Errorf("wrong order: want [Go Docker SQLite], got %v", merged)
	}
}

func TestMergeEntities_BothEmpty(t *testing.T) {
	merged := mergeEntities(nil, nil)
	if merged == nil {
		t.Error("want non-nil empty slice, got nil")
	}
	if len(merged) != 0 {
		t.Errorf("want 0 entities, got %d", len(merged))
	}
}

func TestMergeEntities_EmptyStringsFiltered(t *testing.T) {
	merged := mergeEntities([]string{"", "Go"}, []string{"", ""})
	if len(merged) != 1 || merged[0] != "Go" {
		t.Errorf("want [Go], got %v", merged)
	}
}

func TestResolveEntities_MergeMode(t *testing.T) {
	entities := ResolveEntities("We deploy HttpServer on Docker", []string{"deployment-pipeline"}, EntityModeMerge)
	has := toSet(entities)
	if !has["deployment-pipeline"] || !has["HttpServer"] || !has["Docker"] {
		t.Errorf("merge mode should include provided and extracted entities, got %v", entities)
	}
}

func TestResolveEntities_ProvidedMode(t *testing.T) {
	entities := ResolveEntities("We deploy HttpServer on Docker", []string{"deployment-pipeline"}, EntityModeProvided)
	has := toSet(entities)
	if !has["deployment-pipeline"] {
		t.Errorf("provided mode should keep provided entity, got %v", entities)
	}
	if has["HttpServer"] || has["Docker"] {
		t.Errorf("provided mode should not include extracted entities, got %v", entities)
	}
}

func TestResolveEntities_AutoMode(t *testing.T) {
	entities := ResolveEntities("We deploy HttpServer on Docker", []string{"deployment-pipeline"}, EntityModeAuto)
	has := toSet(entities)
	if has["deployment-pipeline"] {
		t.Errorf("auto mode should ignore provided entities, got %v", entities)
	}
	if !has["HttpServer"] || !has["Docker"] {
		t.Errorf("auto mode should include extracted entities, got %v", entities)
	}
}

func TestSplitWords_PreservesCasing(t *testing.T) {
	words := splitWords("Hello World GoLang")
	expected := []string{"Hello", "World", "GoLang"}
	if len(words) != len(expected) {
		t.Fatalf("want %d words, got %d: %v", len(expected), len(words), words)
	}
	for i, want := range expected {
		if words[i] != want {
			t.Errorf("word[%d]: want %q, got %q", i, want, words[i])
		}
	}
}

func TestSplitWords_Punctuation(t *testing.T) {
	words := splitWords("hello, world! foo-bar")
	has := toSet(words)
	if !has["hello"] || !has["world"] || !has["foo"] || !has["bar"] {
		t.Errorf("want all words split by punctuation, got %v", words)
	}
}

// --- Fourth-path (index-aware) extraction tests ---

func TestExtractEntitiesIndexed_NilIndexMatchesExtractEntities(t *testing.T) {
	text := "Built with Go and SQLite, deployed on Docker"
	a := ExtractEntities(text)
	b := ExtractEntitiesIndexed(text, nil)
	if !equalAsSets(a, b) {
		t.Errorf("nil index should match ExtractEntities: base=%v indexed=%v", a, b)
	}
}

func TestExtractEntitiesIndexed_EmptyIndexMatchesExtractEntities(t *testing.T) {
	text := "Built with Go and SQLite, deployed on Docker"
	a := ExtractEntities(text)
	b := ExtractEntitiesIndexed(text, map[string]bool{})
	if !equalAsSets(a, b) {
		t.Errorf("empty index should match ExtractEntities: base=%v indexed=%v", a, b)
	}
}

func TestExtractEntitiesIndexed_SingleSegmentCamelCase_AdmittedWhenKnown(t *testing.T) {
	// Default paths skip single-segment CamelCase to avoid noise. With the
	// name in the index, the fourth path admits it.
	known := map[string]bool{"Athena": true, "Hestia": true}
	entities := ExtractEntitiesIndexed("Athena and Hestia are project codenames", known)
	has := toSet(entities)
	if !has["Athena"] {
		t.Errorf("want known single-segment CamelCase 'Athena', got %v", entities)
	}
	if !has["Hestia"] {
		t.Errorf("want known single-segment CamelCase 'Hestia', got %v", entities)
	}
}

func TestExtractEntitiesIndexed_SingleSegmentCamelCase_RejectedWhenUnknown(t *testing.T) {
	// Negative case: ensure the filter is doing work. A capitalized token
	// not in the index must NOT be admitted.
	known := map[string]bool{"Athena": true}
	entities := ExtractEntitiesIndexed("Banana is a tasty fruit and Athena is our agent", known)
	has := toSet(entities)
	if has["Banana"] {
		t.Errorf("'Banana' is not in index — should not be admitted, got %v", entities)
	}
	if !has["Athena"] {
		t.Errorf("'Athena' is in index — should be admitted, got %v", entities)
	}
}

func TestExtractEntitiesIndexed_LowercaseKnownWordAdmitted(t *testing.T) {
	// Fourth-path B: tokenized scan picks up lowercase index entries that
	// the regex-based paths cannot match.
	known := map[string]bool{"openclaw": true}
	entities := ExtractEntitiesIndexed("see openclaw docs for the harness layer", known)
	has := toSet(entities)
	if !has["openclaw"] {
		t.Errorf("want lowercase known 'openclaw', got %v", entities)
	}
}

func TestExtractEntitiesIndexed_PreservesDefaultPaths(t *testing.T) {
	// The fourth path is purely additive — existing CamelCase, acronym, and
	// dictionary matches must still come through.
	known := map[string]bool{"Athena": true}
	entities := ExtractEntitiesIndexed("Athena uses HttpServer and Go to call API", known)
	has := toSet(entities)
	if !has["Athena"] {
		t.Errorf("want fourth-path 'Athena', got %v", entities)
	}
	if !has["HttpServer"] {
		t.Errorf("want CamelCase 'HttpServer', got %v", entities)
	}
	if !has["Go"] {
		t.Errorf("want techDictionary 'Go', got %v", entities)
	}
	if !has["API"] {
		t.Errorf("want acronym 'API', got %v", entities)
	}
}

func TestExtractEntitiesIndexed_NoDuplicates(t *testing.T) {
	known := map[string]bool{"Athena": true}
	entities := ExtractEntitiesIndexed("Athena Athena Athena", known)
	count := 0
	for _, e := range entities {
		if e == "Athena" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want 1 'Athena', got %d (entities=%v)", count, entities)
	}
}

func TestExtractEntitiesIndexed_StopwordsStillFilter(t *testing.T) {
	// Even if a stopword were perversely listed in the index, the stopword
	// filter must still reject it — defense-in-depth.
	known := map[string]bool{"THE": true, "IS": true}
	entities := ExtractEntitiesIndexed("IT IS IN THE BOX", known)
	has := toSet(entities)
	if has["THE"] {
		t.Errorf("'THE' is a stopword — must not be admitted, got %v", entities)
	}
	if has["IS"] {
		t.Errorf("'IS' is a stopword — must not be admitted, got %v", entities)
	}
}

func TestResolveEntitiesIndexed_MergeMode(t *testing.T) {
	known := map[string]bool{"Athena": true}
	entities := ResolveEntitiesIndexed("Athena deploys HttpServer", []string{"deployment-pipeline"}, EntityModeMerge, known)
	has := toSet(entities)
	if !has["deployment-pipeline"] {
		t.Errorf("merge mode should keep provided entity, got %v", entities)
	}
	if !has["Athena"] {
		t.Errorf("merge mode should include fourth-path 'Athena', got %v", entities)
	}
	if !has["HttpServer"] {
		t.Errorf("merge mode should include CamelCase 'HttpServer', got %v", entities)
	}
}

func TestResolveEntitiesIndexed_ProvidedModeIgnoresIndex(t *testing.T) {
	known := map[string]bool{"Athena": true}
	entities := ResolveEntitiesIndexed("Athena deploys HttpServer", []string{"deployment-pipeline"}, EntityModeProvided, known)
	has := toSet(entities)
	if !has["deployment-pipeline"] {
		t.Errorf("provided mode should keep provided entity, got %v", entities)
	}
	if has["Athena"] || has["HttpServer"] {
		t.Errorf("provided mode should ignore extraction (and the index), got %v", entities)
	}
}

func TestResolveEntitiesIndexed_AutoMode(t *testing.T) {
	known := map[string]bool{"Athena": true}
	entities := ResolveEntitiesIndexed("Athena deploys HttpServer", []string{"deployment-pipeline"}, EntityModeAuto, known)
	has := toSet(entities)
	if has["deployment-pipeline"] {
		t.Errorf("auto mode should ignore provided entities, got %v", entities)
	}
	if !has["Athena"] || !has["HttpServer"] {
		t.Errorf("auto mode should include extracted entities, got %v", entities)
	}
}

func TestExtractEntitiesIndexed_FirstMentionNotInIndex_NotCaptured(t *testing.T) {
	// Documents the intended boundary of the fourth path: it is filter-only,
	// not a self-seeding extractor. A single-segment CamelCase entity that
	// has never been stored is NOT admitted by auto-mode extraction. This
	// means the fourth path cannot bootstrap new vocabulary on its own — a
	// first appearance must come via the existing regex+dictionary paths or
	// via pre-provided entities (typical for LLM-extracted or user-supplied
	// entity lists). Establishes that there is no write-side deadlock:
	// auto-mode of a never-seen entity simply returns the baseline result
	// (which excludes single-segment CamelCase), it does not block or error.
	emptyIndex := map[string]bool{}
	entities := ExtractEntitiesIndexed("Athena and Hestia are codenames", emptyIndex)
	has := toSet(entities)
	if has["Athena"] || has["Hestia"] {
		t.Errorf("never-seen single-segment CamelCase must NOT be admitted by an empty index; got %v", entities)
	}
}

func TestExtractEntitiesIndexed_SeedingViaProvidedEntities(t *testing.T) {
	// Verifies the full seed-then-propagate cycle the design depends on:
	// (1) a first-mention insight carries the entity via pre-provided
	//     entities through ResolveEntitiesIndexed in merge mode (the engine
	//     default), and the entity reaches the final list even with an
	//     empty index — the fourth path does not interfere with seeding.
	// (2) once the entity is "in" the index (modeled here by adding it to
	//     the map between calls — the engine writes it to the insight, the
	//     next LoadKnownEntities will see it), a subsequent auto-mode
	//     extraction picks it up via the fourth path.
	// Together: the fourth path consumes the index, the existing provided/
	// regex paths feed it. There is no chicken-and-egg deadlock.
	const newVocab = "Athena"

	// Step 1: first-mention insight, provided entity, empty index.
	firstResolved := ResolveEntitiesIndexed("seeding the new vocabulary", []string{newVocab}, EntityModeMerge, map[string]bool{})
	hasFirst := toSet(firstResolved)
	if !hasFirst[newVocab] {
		t.Fatalf("seed step: provided entity %q should reach the final list even with empty index; got %v", newVocab, firstResolved)
	}

	// Step 2: subsequent auto-mode extraction with the entity now in index.
	index := map[string]bool{newVocab: true}
	secondResolved := ResolveEntitiesIndexed("a follow-up mentioning Athena later", nil, EntityModeAuto, index)
	hasSecond := toSet(secondResolved)
	if !hasSecond[newVocab] {
		t.Errorf("propagate step: auto-mode should admit %q via the fourth path once in the index; got %v", newVocab, secondResolved)
	}
}

func TestResolveEntitiesIndexed_NilIndexEqualsResolveEntities(t *testing.T) {
	content := "Built with Go and SQLite"
	provided := []string{"deployment-pipeline"}
	for _, mode := range []EntityMode{EntityModeMerge, EntityModeProvided, EntityModeAuto} {
		a := ResolveEntities(content, provided, mode)
		b := ResolveEntitiesIndexed(content, provided, mode, nil)
		if !equalAsSets(a, b) {
			t.Errorf("mode=%s: nil-indexed should equal ResolveEntities; base=%v indexed=%v", mode, a, b)
		}
	}
}

func equalAsSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	am := toSet(a)
	for _, v := range b {
		if !am[v] {
			return false
		}
	}
	return true
}

func toSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}
