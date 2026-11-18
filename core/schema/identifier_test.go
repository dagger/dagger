package schema

import (
	"net/http"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// Query.naming takes an optional engine version, normalized as a module's is;
// without one, it answers for the caller's. Its dictionary is the version's,
// whichever side of the identifier casing cut it's on.
func TestNamingVersion(t *testing.T) {
	ctx, dag := newNestingTestServer(t, "v1.0.0")
	h := dagql.NewDefaultHandler(dag)
	gql := client.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	const query = `query Naming($names: [String!]!, $casing: Casing!, $acronyms: AcronymStyle, $version: String) {
		naming(version: $version) {
			version
			formatIdentifiers(names: $names, casing: $casing, acronyms: $acronyms)
		}
	}`
	engineView := engine.ModuleAPIViewVersion(engine.Version)
	for _, tc := range []struct {
		version any
		want    string
	}{
		{nil, engine.ModuleAPIViewVersion("v1.0.0")},
		{"", engine.ModuleAPIViewVersion("v1.0.0")},
		{"v0.21.0", engine.ModuleAPIViewVersion("v0.21.0")},
		{"v1.0.0-beta.15", engine.ModuleAPIViewVersion("v1.0.0-beta.15")},
		{engine.IdentifierCasingVersion, engine.ModuleAPIViewVersion(engine.IdentifierCasingVersion)},
		{"v1.0.0", engine.ModuleAPIViewVersion("v1.0.0")},
		{"v1.999.0", engineView},
	} {
		var res struct {
			Naming struct {
				Version           string
				FormatIdentifiers []string `json:"formatIdentifiers"`
			}
		}
		require.NoError(t, gql.Post(query, &res,
			client.Var("names", []string{"withGPU", "callID"}),
			client.Var("casing", "CAMEL"),
			client.Var("acronyms", "CAPITALIZED"),
			client.Var("version", tc.version),
		), tc.version)
		require.Equal(t, tc.want, res.Naming.Version, tc.version)
		require.Equal(t, []string{"withGpu", "callId"}, res.Naming.FormatIdentifiers, tc.version)
	}
}

// The v1 views before v1.0.0-beta.26 serve v1.0.0-beta.25's identifier API,
// with its dictionary, whatever the version asked for.
func TestBeta25IdentifierAPI(t *testing.T) {
	ctx, dag := newNestingTestServer(t, call.View(engine.APIViewVersion("v1.0.0-beta.25")))
	h := dagql.NewDefaultHandler(dag)
	gql := client.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	type term struct{ Spelling string }
	var res struct {
		Identifier        struct{ Words []struct{ Text string } }
		FormatIdentifiers []string
		NamingDictionary  []term
	}
	require.NoError(t, gql.Post(`{
		identifier(name: "K8STools") { words { text } }
		formatIdentifiers(names: ["withGPU", "K8STools", "K3SCluster"], casing: PASCAL, version: "v1.0.0")
		namingDictionary { spelling }
	}`, &res))
	require.Equal(t, []struct{ Text string }{{"K8S"}, {"tools"}}, res.Identifier.Words)
	require.Equal(t, []string{"WithGPU", "K8STools", "K3SCluster"}, res.FormatIdentifiers)
	require.Contains(t, res.NamingDictionary, term{"GraphQL"})
	require.NotContains(t, res.NamingDictionary, term{"K8s"})
	require.NotContains(t, res.NamingDictionary, term{"K3s"})
}
