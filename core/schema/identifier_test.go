package schema

import (
	"context"
	"net/http"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// __formatIdentifiers is there for module runtimes in every view, takes the
// naming dictionary's version as an argument, and formats exactly like
// formatIdentifiers, while staying out of the schema JSON.
func TestFormatIdentifiersForVersion(t *testing.T) {
	names := dagql.ArrayInput[dagql.String]{"withGPU", "prerequisiteSHAs", "httpClient"}
	format := func(ctx context.Context, dag *dagql.Server, field string, args ...dagql.NamedInput) ([]string, error) {
		var result dagql.Array[dagql.String]
		err := dag.Select(ctx, dag.Root(), &result, dagql.Selector{
			Field: field,
			Args:  append([]dagql.NamedInput{{Name: "names", Value: names}}, args...),
		})
		out := make([]string, len(result))
		for i, s := range result {
			out[i] = s.String()
		}
		return out, err
	}
	str := func(name, value string) dagql.NamedInput {
		return dagql.NamedInput{Name: name, Value: dagql.NewString(value)}
	}

	for _, version := range []string{"v0.21.0", "v1.0.0"} {
		t.Run(version, func(t *testing.T) {
			ctx, dag := newNestingTestServer(t, call.View(engine.APIViewVersion(version)))

			data, err := getSchemaJSON(nil, nil, dag.View, dag)
			require.NoError(t, err)
			query := decodeSchemaResponse(t, data).Schema.Query()
			require.NotNil(t, query)
			require.Nil(t, schemaField(query, "__formatIdentifiers"))

			got, err := format(ctx, dag, "__formatIdentifiers", str("casing", "SNAKE"), str("version", "v1.0.0"))
			require.NoError(t, err)
			require.Equal(t, []string{"with_gpu", "prerequisite_shas", "http_client"}, got)

			got, err = format(ctx, dag, "__formatIdentifiers", str("casing", "PASCAL"), str("version", "v1.0.0"))
			require.NoError(t, err)
			require.Equal(t, []string{"WithGPU", "PrerequisiteSHAs", "HTTPClient"}, got)

			got, err = format(ctx, dag, "__formatIdentifiers",
				str("casing", "CAMEL"), str("acronyms", "CAPITALIZED"), str("version", "v1.0.0-beta.15"))
			require.NoError(t, err)
			require.Equal(t, []string{"withGpu", "prerequisiteShas", "httpClient"}, got)

			_, err = format(ctx, dag, "__formatIdentifiers", str("casing", "pascal"), str("version", "v1.0.0"))
			require.ErrorContains(t, err, `unknown casing "pascal"`)

			_, err = format(ctx, dag, "__formatIdentifiers",
				str("casing", "PASCAL"), str("acronyms", "LOWER"), str("version", "v1.0.0"))
			require.ErrorContains(t, err, `unknown acronym style "LOWER"`)
		})
	}

	t.Run("same as formatIdentifiers", func(t *testing.T) {
		ctx, dag := newNestingTestServer(t, "v1.0.0")
		for _, tc := range []struct {
			casing   core.Casing
			acronyms core.AcronymStyle
		}{
			{core.CasingPascal, core.AcronymStyleUppercase},
			{core.CasingCamel, core.AcronymStyleCapitalized},
			{core.CasingSnake, core.AcronymStyleUppercase},
			{core.CasingScreamingSnake, core.AcronymStyleUppercase},
		} {
			want, err := format(ctx, dag, "formatIdentifiers",
				dagql.NamedInput{Name: "casing", Value: tc.casing},
				dagql.NamedInput{Name: "acronyms", Value: tc.acronyms},
			)
			require.NoError(t, err)
			require.Len(t, want, len(names))
			got, err := format(ctx, dag, "__formatIdentifiers",
				str("casing", string(tc.casing)), str("acronyms", string(tc.acronyms)), str("version", "v1.0.0"))
			require.NoError(t, err)
			require.Equal(t, want, got, string(tc.casing)+":"+string(tc.acronyms))
		}
	})

	t.Run("over GraphQL", func(t *testing.T) {
		// A runtime declaring an old engineVersion calls it with variables
		// of builtin types only, in a view without the Casing enum.
		ctx, dag := newNestingTestServer(t, "v0.21.0")
		h := dagql.NewDefaultHandler(dag)
		gql := client.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r.WithContext(ctx))
		}))
		var res struct {
			FormatIdentifiers []string `json:"__formatIdentifiers"`
		}
		require.NoError(t, gql.Post(`query FormatIdentifiers($names: [String!]!, $casing: String!, $acronyms: String, $version: String!) {
			__formatIdentifiers(names: $names, casing: $casing, acronyms: $acronyms, version: $version)
		}`, &res,
			client.Var("names", []string{"withGPU", "callID"}),
			client.Var("casing", "CAMEL"),
			client.Var("acronyms", "CAPITALIZED"),
			client.Var("version", "v1.0.0"),
		))
		require.Equal(t, []string{"withGpu", "callId"}, res.FormatIdentifiers)
	})
}

// formatIdentifiers takes an optional engine version whose naming dictionary
// to use, as codegen passes the __schemaVersion of the schema it generates;
// without one, it uses the caller's.
func TestFormatIdentifiersVersion(t *testing.T) {
	ctx, dag := newNestingTestServer(t, "v1.0.0")
	h := dagql.NewDefaultHandler(dag)
	gql := client.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	const query = `query FormatIdentifiers($names: [String!]!, $casing: Casing!, $acronyms: AcronymStyle, $version: String) {
		formatIdentifiers(names: $names, casing: $casing, acronyms: $acronyms, version: $version)
	}`
	for _, version := range []any{nil, "", "v0.21.0", "v1.0.0", "v1.0.0-beta.15"} {
		var res struct {
			FormatIdentifiers []string `json:"formatIdentifiers"`
		}
		require.NoError(t, gql.Post(query, &res,
			client.Var("names", []string{"withGPU", "callID"}),
			client.Var("casing", "CAMEL"),
			client.Var("acronyms", "CAPITALIZED"),
			client.Var("version", version),
		), version)
		require.Equal(t, []string{"withGpu", "callId"}, res.FormatIdentifiers, version)
	}
}
