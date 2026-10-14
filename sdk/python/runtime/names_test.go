package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const namesTestSchema = `{
  "__schemaVersion": "v1.0.0",
  "__schema": {
    "queryType": {"name": "Query"},
    "types": [
      {
        "name": "Query",
        "fields": [
          {"name": "gitRef", "args": [{"name": "url"}]},
          %s
        ]
      },
      {
        "name": "GitRef",
        "fields": [{"name": "prerequisiteSHAs", "args": []}]
      },
      {
        "name": "GitRefOpts",
        "inputFields": [{"name": "withGPU"}, {"name": "_"}]
      },
      {
        "name": "NetworkProtocol",
        "enumValues": [{"name": "TCP"}, {"name": "UDP"}]
      },
      {
        "name": "__Type",
        "fields": [{"name": "possibleTypes", "args": []}]
      }
    ]
  }
}`

const formatIdentifiersField = `{"name": "formatIdentifiers", "args": [{"name": "names"}, {"name": "casing"}, {"name": "acronyms"}]}`

func namesTestJSON(withFormatIdentifiers bool) []byte {
	field := `{"name": "id", "args": []}`
	if withFormatIdentifiers {
		field = formatIdentifiersField
	}
	return []byte(strings.Replace(namesTestSchema, "%s", field, 1))
}

func TestSchemaNames(t *testing.T) {
	var schema introspectionSchema
	require.NoError(t, json.Unmarshal(namesTestJSON(true), &schema))
	require.True(t, schema.hasFormatIdentifiers())
	require.Equal(t, "v1.0.0", schema.SchemaVersion)
	require.Equal(t, []string{
		"GitRef", "GitRefOpts", "NetworkProtocol", "Query", "TCP", "UDP",
		"acronyms", "casing", "formatIdentifiers", "gitRef", "names",
		"prerequisiteSHAs", "url", "withGPU",
	}, schema.names())

	schema = introspectionSchema{}
	require.NoError(t, json.Unmarshal(namesTestJSON(false), &schema))
	require.False(t, schema.hasFormatIdentifiers())
}

func TestBatchNames(t *testing.T) {
	require.Nil(t, batchNames(nil, 10))
	require.Equal(t, [][]string{{"abcd", "efg"}, {"hijklmnopq"}, {"r"}},
		batchNames([]string{"abcd", "efg", "hijklmnopq", "r"}, 7))
}

func TestSchemaNamesFile(t *testing.T) {
	ctx := context.Background()
	var calls int
	snake := func(_ context.Context, names []string, version string) ([]string, error) {
		calls++
		require.Equal(t, "v1.0.0", version)
		out := make([]string, len(names))
		for i, name := range names {
			out[i] = "snake_" + name
		}
		return out, nil
	}

	data, err := schemaNamesFile(ctx, namesTestJSON(true), snake)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	var file map[string]map[string]string
	require.NoError(t, json.Unmarshal(data, &file))
	require.Len(t, file, 1)
	require.Equal(t, "snake_prerequisiteSHAs", file["SNAKE:UPPERCASE"]["prerequisiteSHAs"])
	require.Equal(t, "snake_withGPU", file["SNAKE:UPPERCASE"]["withGPU"])
	require.NotContains(t, file["SNAKE:UPPERCASE"], "_")
	require.NotContains(t, file["SNAKE:UPPERCASE"], "possibleTypes")

	// A schema without Query.formatIdentifiers formats nothing: codegen
	// keeps its legacy conversion.
	calls = 0
	data, err = schemaNamesFile(ctx, namesTestJSON(false), snake)
	require.NoError(t, err)
	require.Nil(t, data)
	require.Zero(t, calls)

	// A short answer is an error, not a partial map.
	_, err = schemaNamesFile(ctx, namesTestJSON(true), func(context.Context, []string, string) ([]string, error) {
		return []string{"x"}, nil
	})
	require.ErrorContains(t, err, "got 1 back")
}
