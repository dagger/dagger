package introspection

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

var update = flag.Bool("update", false, "update golden files")

const testDataDir = "./testdata"

func TestKeepOnlySub1(t *testing.T) {
	schemaJSON, err := os.ReadFile(filepath.Join(testDataDir, "schema.json"))
	assert.NoError(t, err)

	var schema *Schema
	assert.NoError(t, json.Unmarshal(schemaJSON, &schema))

	result := schema.Include("sub1")

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	assert.NoError(t, err)

	goldenPath := filepath.Join(testDataDir, "keep_sub1_expected_schema.json")
	if *update {
		err = os.WriteFile(goldenPath, append(resultJSON, '\n'), 0o644)
		assert.NoError(t, err)
		return
	}

	expectedJSON, err := os.ReadFile(goldenPath)
	assert.NoError(t, err)

	assert.JSONEq(t, string(expectedJSON), string(resultJSON))
}

func TestKeepSub1AndSub2(t *testing.T) {
	schemaJSON, err := os.ReadFile(filepath.Join(testDataDir, "schema.json"))
	assert.NoError(t, err)

	var schema *Schema
	assert.NoError(t, json.Unmarshal(schemaJSON, &schema))

	result := schema.Include("sub1", "sub2")

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	assert.NoError(t, err)

	goldenPath := filepath.Join(testDataDir, "keep_sub1_and_sub2_expected_schema.json")
	if *update {
		err = os.WriteFile(goldenPath, append(resultJSON, '\n'), 0o644)
		assert.NoError(t, err)
		return
	}

	expectedJSON, err := os.ReadFile(goldenPath)
	assert.NoError(t, err)

	assert.JSONEq(t, string(expectedJSON), string(resultJSON))
}

func TestDependencyNames(t *testing.T) {
	schemaJSON, err := os.ReadFile(filepath.Join(testDataDir, "schema.json"))
	assert.NoError(t, err)

	var schema *Schema
	assert.NoError(t, json.Unmarshal(schemaJSON, &schema))

	names := schema.DependencyNames()

	// The test schema is captured from go/namespacing which has sub1, sub2, and test modules.
	assert.Equal(t, []string{"sub1", "sub2", "test"}, names)
}

func TestDependencyNamesEmpty(t *testing.T) {
	// A schema with no sourceMap directives should return an empty slice.
	schema := &Schema{
		QueryType: struct {
			Name string `json:"name,omitempty"`
		}{Name: "Query"},
		Types: Types{
			{
				Kind:       TypeKindObject,
				Name:       "Query",
				Directives: Directives{},
			},
		},
	}

	names := schema.DependencyNames()
	assert.Empty(t, names)
}

func TestScrubField(t *testing.T) {
	schema := &Schema{
		Types: Types{
			{
				Kind: TypeKindObject,
				Name: "Query",
				Fields: []*Field{
					{Name: "keep"},
					{Name: "hide"},
				},
			},
			{
				Kind:   TypeKindObject,
				Name:   "Volume",
				Fields: []*Field{},
			},
		},
	}

	schema.ScrubField("Query", "hide")

	query := schema.Types.Get("Query")
	assert.NotNil(t, query)
	assert.Len(t, query.Fields, 1)
	assert.Equal(t, "keep", query.Fields[0].Name)
	assert.NotNil(t, schema.Types.Get("Volume"))

	schema.ScrubField("Query", "missing")
	assert.Len(t, query.Fields, 1)
	schema.ScrubField("Missing", "hide")
	assert.NotNil(t, schema.Types.Get("Query"))
}

func TestExcludeSub1AndSub2(t *testing.T) {
	schemaJSON, err := os.ReadFile(filepath.Join(testDataDir, "schema.json"))
	assert.NoError(t, err)

	var schema *Schema
	assert.NoError(t, json.Unmarshal(schemaJSON, &schema))

	result := schema.Exclude("sub1", "sub2")

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	assert.NoError(t, err)

	goldenPath := filepath.Join(testDataDir, "keep_core_and_test_expected_schema.json")
	if *update {
		err = os.WriteFile(goldenPath, append(resultJSON, '\n'), 0o644)
		assert.NoError(t, err)
		return
	}

	expectedJSON, err := os.ReadFile(goldenPath)
	assert.NoError(t, err)

	assert.JSONEq(t, string(expectedJSON), string(resultJSON))
}
