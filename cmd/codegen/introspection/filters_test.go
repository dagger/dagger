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

func TestKeepOnlyDep(t *testing.T) {
	schemaJSON, err := os.ReadFile(filepath.Join(testDataDir, "schema.json"))
	assert.NoError(t, err)

	var schema *Schema
	assert.NoError(t, json.Unmarshal(schemaJSON, &schema))

	result := schema.Include("dep")

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	assert.NoError(t, err)

	goldenPath := filepath.Join(testDataDir, "keep_dep_expected_schema.json")
	if *update {
		err = os.WriteFile(goldenPath, append(resultJSON, '\n'), 0o644)
		assert.NoError(t, err)
		return
	}

	expectedJSON, err := os.ReadFile(goldenPath)
	assert.NoError(t, err)

	assert.JSONEq(t, string(expectedJSON), string(resultJSON))
}

func TestKeepDepAndTest(t *testing.T) {
	schemaJSON, err := os.ReadFile(filepath.Join(testDataDir, "schema.json"))
	assert.NoError(t, err)

	var schema *Schema
	assert.NoError(t, json.Unmarshal(schemaJSON, &schema))

	result := schema.Include("dep", "test")

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	assert.NoError(t, err)

	goldenPath := filepath.Join(testDataDir, "keep_dep_and_test_expected_schema.json")
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

	// The test schema contains types owned by "dep" and "test" modules.
	assert.Equal(t, []string{"dep", "test"}, names)
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

func TestExcludeDepAndTest(t *testing.T) {
	schemaJSON, err := os.ReadFile(filepath.Join(testDataDir, "schema.json"))
	assert.NoError(t, err)

	var schema *Schema
	assert.NoError(t, json.Unmarshal(schemaJSON, &schema))

	result := schema.Exclude("dep", "test")

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	assert.NoError(t, err)

	goldenPath := filepath.Join(testDataDir, "keep_core_only_expected_schema.json")
	if *update {
		err = os.WriteFile(goldenPath, append(resultJSON, '\n'), 0o644)
		assert.NoError(t, err)
		return
	}

	expectedJSON, err := os.ReadFile(goldenPath)
	assert.NoError(t, err)

	assert.JSONEq(t, string(expectedJSON), string(resultJSON))
}
