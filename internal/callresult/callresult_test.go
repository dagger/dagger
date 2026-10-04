package callresult

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

func TestWriteSelected(t *testing.T) {
	for _, tc := range []struct {
		name     string
		format   string
		query    string
		response string
		want     string
	}{
		{
			name:     "json scalar",
			format:   FormatJSON,
			query:    `query{demo{slow}}`,
			response: `{"demo":{"slow":"done"}}`,
			want:     "\"done\"\n",
		},
		{
			name:     "json object scalar",
			format:   FormatJSON,
			query:    `query{demo{config}}`,
			response: `{"demo":{"config":{"a":1}}}`,
			want:     "{\n    \"a\": 1\n}\n",
		},
		{
			name:     "plain list",
			format:   FormatPlain,
			query:    `query{demo{list(msg:"x")}}`,
			response: `{"demo":{"list":["x","x-2"]}}`,
			want:     "x\nx-2\n",
		},
		{
			name:     "plain void",
			format:   FormatPlain,
			query:    `query{demo{nothing}}`,
			response: `{"demo":{"nothing":null}}`,
			want:     "",
		},
		{
			name:     "id through an alias and a list",
			format:   FormatID,
			query:    `query{demo{items{id:sync}}}`,
			response: `{"demo":{"items":[{"id":""},{"id":""}]}}`,
			want:     "- Query\n- Query\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data any
			require.NoError(t, json.Unmarshal([]byte(tc.response), &data))
			var out bytes.Buffer
			require.NoError(t, WriteSelected(&out, tc.format, tc.query, data))
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestIDOfSyncedObjects(t *testing.T) {
	id, err := call.NewEngineResultID(42, call.NewType(&ast.Type{NamedType: "Check", NonNull: true})).Encode()
	require.NoError(t, err)
	var expected bytes.Buffer
	require.NoError(t, EncodedID(&expected, id))
	for _, tc := range []struct {
		name     string
		response any
		want     string
	}{
		{"single", map[string]any{"id": map[string]any{"id": id}}, expected.String()},
		{"list", []any{map[string]any{"id": map[string]any{"id": id}}}, "- " + expected.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, ID(&out, tc.response))
			require.Equal(t, tc.want, out.String())
		})
	}
}
