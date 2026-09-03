package mcp

import (
	"encoding/json"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
)

const (
	maxRememberBytes  = 8000
	maxTags           = 20
	maxTagChars       = 100
	maxEntities       = 50
	maxEntityChars    = 200
	maxMetadata       = 20
	maxMetadataKey    = 100
	maxMetadataValue  = 1000
	defaultImportance = 3
	defaultDepth      = 2
	defaultWeight     = 0.5
)

func recallInputSchema() *jsonschema.Schema {
	return mustInputSchema[recallInput](func(properties map[string]*jsonschema.Schema) {
		boundedString(properties["query"], 1, maxQueryChars)
		boundedInteger(properties["limit"], 1, maxToolResults, defaultLimit)
		properties["category"].Enum = stringEnum("preference", "decision", "fact", "insight", "context", "general")
		properties["source"].MaxLength = jsonschema.Ptr(maxSourceChars)
		properties["intent"].Enum = stringEnum("WHY", "WHEN", "ENTITY", "GENERAL")
	})
}

func searchInputSchema() *jsonschema.Schema {
	return mustInputSchema[searchInput](func(properties map[string]*jsonschema.Schema) {
		boundedString(properties["query"], 1, maxQueryChars)
		boundedInteger(properties["limit"], 1, maxToolResults, defaultLimit)
	})
}

func relatedInputSchema() *jsonschema.Schema {
	return mustInputSchema[relatedInput](func(properties map[string]*jsonschema.Schema) {
		boundedString(properties["id"], 1, maxIDChars)
		properties["edge_type"].Enum = stringEnum("temporal", "semantic", "causal", "entity")
		boundedInteger(properties["depth"], 1, maxRelatedDepth, defaultDepth)
		boundedInteger(properties["limit"], 1, maxToolResults, defaultRelatedLimit)
	})
}

func rememberInputSchema() *jsonschema.Schema {
	return mustInputSchema[rememberInput](func(properties map[string]*jsonschema.Schema) {
		boundedString(properties["content"], 1, maxRememberBytes)
		properties["category"].Enum = stringEnum("preference", "decision", "fact", "insight", "context", "general")
		properties["category"].Default = json.RawMessage(`"general"`)
		boundedInteger(properties["importance"], 1, 5, defaultImportance)
		boundedStringArray(properties["tags"], maxTags, maxTagChars)
		properties["source"].MaxLength = jsonschema.Ptr(maxSourceChars)
		properties["source"].Default = json.RawMessage(`"user"`)
		boundedStringArray(properties["entities"], maxEntities, maxEntityChars)
		properties["entity_mode"].Enum = stringEnum("merge", "provided", "auto")
		properties["entity_mode"].Default = json.RawMessage(`"merge"`)
	})
}

func linkInputSchema() *jsonschema.Schema {
	return mustInputSchema[linkInput](func(properties map[string]*jsonschema.Schema) {
		boundedString(properties["source_id"], 1, maxIDChars)
		boundedString(properties["target_id"], 1, maxIDChars)
		properties["edge_type"].Enum = stringEnum("temporal", "semantic", "causal", "entity")
		properties["edge_type"].Default = json.RawMessage(`"semantic"`)
		properties["weight"].Minimum = jsonschema.Ptr(0.0)
		properties["weight"].Maximum = jsonschema.Ptr(1.0)
		properties["weight"].Default = json.RawMessage(`0.5`)
		metadata := properties["metadata"]
		metadata.MaxProperties = jsonschema.Ptr(maxMetadata)
		metadata.PropertyNames = &jsonschema.Schema{Type: "string", MaxLength: jsonschema.Ptr(maxMetadataKey)}
		metadata.AdditionalProperties.MaxLength = jsonschema.Ptr(maxMetadataValue)
	})
}

func mustInputSchema[T any](configure func(map[string]*jsonschema.Schema)) *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	configure(schema.Properties)
	return schema
}

func boundedString(schema *jsonschema.Schema, minimum, maximum int) {
	schema.MinLength = jsonschema.Ptr(minimum)
	schema.MaxLength = jsonschema.Ptr(maximum)
}

func boundedInteger(schema *jsonschema.Schema, minimum, maximum, defaultValue int) {
	schema.Minimum = jsonschema.Ptr(float64(minimum))
	schema.Maximum = jsonschema.Ptr(float64(maximum))
	schema.Default = json.RawMessage(strconv.Itoa(defaultValue))
}

func boundedStringArray(schema *jsonschema.Schema, maxItems, maxItemChars int) {
	schema.MaxItems = jsonschema.Ptr(maxItems)
	schema.Items.MaxLength = jsonschema.Ptr(maxItemChars)
}

func stringEnum(values ...string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
