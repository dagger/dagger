package callresult

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/querybuilder"
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

// fakeResponse answers a query builder's request with a fixed response.
type fakeResponse string

func (r fakeResponse) MakeRequest(_ context.Context, _ *graphql.Request, resp *graphql.Response) error {
	return json.Unmarshal([]byte(r), resp.Data)
}

// TestSelectedMatchesQueryBuilder requires that Selected takes the same value
// from a response as the CLI's query builder binds.
func TestSelectedMatchesQueryBuilder(t *testing.T) {
	for _, tc := range []struct {
		name     string
		q        func(*querybuilder.Selection) *querybuilder.Selection
		response string
	}{
		{
			name:     "scalar",
			q:        func(q *querybuilder.Selection) *querybuilder.Selection { return q.Select("demo").Select("slow") },
			response: `{"demo":{"slow":"done"}}`,
		},
		{
			name: "field of a list of objects",
			q: func(q *querybuilder.Selection) *querybuilder.Selection {
				return q.Select("containers").Select("stdout")
			},
			response: `{"containers":[{"stdout":"one"},{"stdout":"two"}]}`,
		},
		{
			name: "synced ids of a list of objects",
			q: func(q *querybuilder.Selection) *querybuilder.Selection {
				return q.Select("demo").Select("items").SelectWithAlias("id", "sync")
			},
			response: `{"demo":{"items":[{"id":"a"},{"id":"b"}]}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := tc.q(querybuilder.Query().Client(fakeResponse(tc.response)))
			var foreground any
			require.NoError(t, q.Bind(&foreground).Execute(t.Context()))

			query, err := q.Build(t.Context())
			require.NoError(t, err)
			var data any
			require.NoError(t, json.Unmarshal([]byte(tc.response), &data))
			selected, err := Selected(query, data)
			require.NoError(t, err)
			require.Equal(t, foreground, selected)
		})
	}
}
