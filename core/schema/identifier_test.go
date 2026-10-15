package schema

import (
	"net/http"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

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
