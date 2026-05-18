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

func toSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}
