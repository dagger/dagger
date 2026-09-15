package workspace

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateConfigJSONNumbers(t *testing.T) {
	const input = "[modules.sdk]\nsource = './sdk'\n\n[sdks.test]\nmodule = 'sdk'\n"
	cfg, err := ParseConfig([]byte(input))
	require.NoError(t, err)
	var settings map[string]any
	decoder := json.NewDecoder(strings.NewReader(`{"integer":42,"large":9223372036854775807,"negative":-42,"float":1.5,"exponent":1e3,"array":[1,2.5],"nested":{"number":3}}`))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&settings))
	entry := cfg.SDKs["test"]
	entry.Scopes = map[string]SDKScope{"demo": {Settings: settings}}
	cfg.SDKs["test"] = entry
	out, err := UpdateConfigBytes([]byte(input), cfg)
	require.NoError(t, err)
	parsed, err := ParseConfig(out)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"integer": int64(42), "large": int64(9223372036854775807),
		"negative": int64(-42), "float": float64(1.5), "exponent": float64(1000),
		"array":  []any{int64(1), float64(2.5)},
		"nested": map[string]any{"number": int64(3)},
	}, parsed.SDKs["test"].Scopes["demo"].Settings)
	require.Contains(t, string(out), "source = './sdk'")
}
