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

// ModuleSource.naming is served on every v1 view, like Query.naming: SDK
// modules that call it declare pre-cut versions (v1.0.0-beta.20).
func TestModuleSourceNamingViews(t *testing.T) {
	for _, tc := range []struct {
		view call.View
		want bool
	}{
		{"", true},
		{call.View(engine.IdentifierCasingVersion), true},
		{call.View(engine.APIViewVersion("v1.0.0-beta.20")), true},
		{call.View(engine.APIViewVersion("v1.0.0-0")), true},
		{"v0.21.0", false},
	} {
		t.Run(string(tc.view), func(t *testing.T) {
			ctx, dag := newNestingTestServer(t, tc.view)
			h := dagql.NewDefaultHandler(dag)
			gql := client.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				h.ServeHTTP(w, r.WithContext(ctx))
			}))
			var res struct {
				ModuleSource struct {
					Fields []struct{ Name string }
				}
				CurrentModule struct {
					Fields []struct{ Name string }
				}
				Query struct {
					Fields []struct{ Name string }
				}
				ModuleNaming *struct {
					Fields []struct{ Name string }
				}
			}
			query := `{
				moduleSource: __type(name: "ModuleSource") { fields { name } }
				currentModule: __type(name: "CurrentModule") { fields { name } }
				query: __type(name: "Query") { fields { name } }
			}`
			if tc.want {
				query = `{
					moduleSource: __type(name: "ModuleSource") { fields { name } }
					currentModule: __type(name: "CurrentModule") { fields { name } }
					query: __type(name: "Query") { fields { name } }
					moduleNaming: __type(name: "ModuleNaming") { fields { name } }
				}`
			}
			require.NoError(t, gql.Post(query, &res))
			var fields, currentFields, queryFields []string
			for _, f := range res.ModuleSource.Fields {
				fields = append(fields, f.Name)
			}
			for _, f := range res.CurrentModule.Fields {
				currentFields = append(currentFields, f.Name)
			}
			for _, f := range res.Query.Fields {
				queryFields = append(queryFields, f.Name)
			}
			if !tc.want {
				require.NotContains(t, fields, "naming")
				require.NotContains(t, currentFields, "naming")
				require.NotContains(t, queryFields, "naming")
				return
			}
			require.Contains(t, fields, "naming")
			require.Contains(t, currentFields, "naming")
			require.Contains(t, queryFields, "naming")
			for _, removed := range []string{"identifier", "formatIdentifiers", "namingDictionary"} {
				require.NotContains(t, queryFields, removed)
			}
			require.NotNil(t, res.ModuleNaming)
			var namingFields []string
			for _, f := range res.ModuleNaming.Fields {
				namingFields = append(namingFields, f.Name)
			}
			require.ElementsMatch(t, []string{"id", "version", "name", "originalName", "typeNames", "fieldNames", "enumMemberNames", "mainObject"}, namingFields)
		})
	}
}
