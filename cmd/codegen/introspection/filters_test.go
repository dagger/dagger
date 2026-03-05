package introspection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

const testDataDir = "./testdata"

func TestKeepOnlyDep(t *testing.T) {
	schemaJSON, err := os.ReadFile(filepath.Join(testDataDir, "schema.json"))
	assert.NoError(t, err)

	var schema *Schema
	assert.NoError(t, json.Unmarshal(schemaJSON, &schema))

	result := schema.Include("dep")

	resultJSON, err := json.Marshal(result)
	assert.NoError(t, err)

	expectedJSON, err := os.ReadFile(filepath.Join(testDataDir, "keep_dep_expected_schema.json"))
	assert.NoError(t, err)

	assert.JSONEq(t, string(expectedJSON), string(resultJSON))
}

func TestKeepDepAndTest(t *testing.T) {
	schemaJSON, err := os.ReadFile(filepath.Join(testDataDir, "schema.json"))
	assert.NoError(t, err)

	var schema *Schema
	assert.NoError(t, json.Unmarshal(schemaJSON, &schema))

	result := schema.Include("dep", "test")

	resultJSON, err := json.Marshal(result)
	assert.NoError(t, err)

	expectedJSON, err := os.ReadFile(filepath.Join(testDataDir, "keep_dep_and_test_expected_schema.json"))
	assert.NoError(t, err)

	assert.JSONEq(t, string(expectedJSON), string(resultJSON))
}

func TestExcludeDepAndTest(t *testing.T) {
	schemaJSON, err := os.ReadFile(filepath.Join(testDataDir, "schema.json"))
	assert.NoError(t, err)

	var schema *Schema
	assert.NoError(t, json.Unmarshal(schemaJSON, &schema))

	result := schema.Exclude("dep", "test")

	resultJSON, err := json.Marshal(result)
	assert.NoError(t, err)

	expectedJSON, err := os.ReadFile(filepath.Join(testDataDir, "keep_core_only_expected_schema.json"))
	assert.NoError(t, err)

	assert.JSONEq(t, string(expectedJSON), string(resultJSON))
}
