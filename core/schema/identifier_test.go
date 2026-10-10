package schema

import (
	"context"
	"testing"

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
}
