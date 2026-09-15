package main

import (
	"encoding/json"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceSchemaUsesConfigFieldNames(t *testing.T) {
	t.Parallel()

	var targetDef *target
	for i := range targets {
		if targets[i].id == "dagger.toml" {
			targetDef = &targets[i]
			break
		}
	}
	require.NotNil(t, targetDef)

	schema := new(jsonschema.Reflector).Reflect(targetDef.value)
	data, err := json.Marshal(schema)
	require.NoError(t, err)

	out := string(data)
	require.Contains(t, out, `"modules"`)
	require.Contains(t, out, `"defaults_from_dotenv"`)
	require.Contains(t, out, `"legacy-default-path"`)
	require.NotContains(t, out, `"Modules"`)
	require.NotContains(t, out, `"DefaultsFromDotEnv"`)
	require.NotContains(t, out, `"LegacyDefaultPath"`)
}
