package schema

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	codegenintrospection "github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/naming"
)

// schemaJSONNames lists every name the schema JSON's __identifiers must
// cover: type, field, argument, input field and enum value names, except
// introspection names.
func schemaJSONNames(schema *codegenintrospection.Schema) map[string]bool {
	names := map[string]bool{}
	add := func(name string) {
		if !strings.HasPrefix(name, "__") {
			names[name] = true
		}
	}
	for _, t := range schema.Types {
		if strings.HasPrefix(t.Name, "__") {
			continue
		}
		add(t.Name)
		for _, f := range t.Fields {
			add(f.Name)
			for _, arg := range f.Args {
				add(arg.Name)
			}
		}
		for _, f := range t.InputFields {
			add(f.Name)
		}
		for _, v := range t.EnumValues {
			add(v.Name)
		}
	}
	return names
}

func requireSchemaJSONIdentifiers(t *testing.T, data []byte, want bool) {
	t.Helper()
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	_, has := raw["__identifiers"]
	require.Equal(t, want, has, "__identifiers present")

	schema := decodeSchemaResponse(t, data).Schema
	if !want {
		require.Nil(t, schema.Identifiers)
		return
	}

	names := schemaJSONNames(schema)
	require.NotEmpty(t, names)
	for name := range names {
		if _, ok := schema.Identifiers[name]; !ok {
			// Only names with no letters or digits are left out.
			_, err := naming.Parse(name)
			require.Error(t, err, "missing words for %q", name)
		}
	}
	for name := range schema.Identifiers {
		require.True(t, names[name], "words for %q, which is not in the schema JSON", name)
	}

	for _, name := range []string{"Container", "withGPU", "prerequisiteSHAs", "GitRepository", "SHARED"} {
		id, err := naming.Parse(name)
		require.NoError(t, err)
		require.Equal(t, codegenintrospection.NewIdentifierWords(id), schema.Identifiers[name], name)
	}
	id, ok := schema.Identifier("experimentalWithAllGPUs")
	require.True(t, ok)
	require.Equal(t, "experimental_with_all_gpus", id.Format(naming.Snake, naming.Uppercase))
	require.Equal(t, "ExperimentalWithAllGpus", id.Format(naming.Pascal, naming.Capitalized))
}

// The schema JSON carries identifier words from the view the identifier API
// appears in, and not before.
func TestSchemaJSONIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{version: "v0.21.0"},
		{version: "v1.0.0-beta.15", want: true},
		{version: "v1.0.0-0", want: true},
		{version: "v1.0.0", want: true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			ctx, dag := newNestingTestServer(t, call.View(engine.APIViewVersion(tc.version)))
			data, err := getSchemaJSON(ctx, nil, nil, dag.View, dag)
			require.NoError(t, err)
			requireSchemaJSONIdentifiers(t, data, tc.want)
		})
	}

	t.Run("scrubbed names", func(t *testing.T) {
		ctx, dag := newNestingTestServer(t, "v1.0.0")
		data, err := getSchemaJSON(ctx, []string{"Host"}, []string{"Query.currentWorkspace"}, dag.View, dag)
		require.NoError(t, err)
		requireSchemaJSONIdentifiers(t, data, true)
		ids := decodeSchemaResponse(t, data).Schema.Identifiers
		require.NotContains(t, ids, "Host")
		require.NotContains(t, ids, "currentWorkspace")
	})
}

// __schemaJSONFile results are cached per view, so one cache serves JSON
// with and without identifier words to callers on either side of the gate.
func TestSchemaJSONFileIdentifiersPerView(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "schema-json-views", SessionID: "schema-json-views"})
	srv := &currentTypeDefsTestServer{platform: core.Platform{OS: "linux", Architecture: "amd64"}}
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)

	contents := func(view call.View) []byte {
		t.Helper()
		root := core.NewRoot(srv)
		dag, err := base.Fork(ctx, root, view)
		require.NoError(t, err)
		srv.dag = dag
		var file dagql.ObjectResult[*core.File]
		callCtx := dagql.ContextWithServer(core.ContextWithQuery(ctx, root), dag)
		require.NoError(t, dag.Select(callCtx, dag.Root(), &file, dagql.Selector{Field: "__schemaJSONFile"}))
		lazy, ok := file.Self().Lazy.(*core.FileBlobLazy)
		require.True(t, ok)
		return lazy.Contents
	}

	// Each side twice, interleaved, so a result cached for one view would
	// show up for the other.
	for range 2 {
		requireSchemaJSONIdentifiers(t, contents("v0.21.0"), false)
		requireSchemaJSONIdentifiers(t, contents("v1.0.0"), true)
	}
}

func TestSchemaMergeIdentifiers(t *testing.T) {
	ctx, dag := newNestingTestServer(t, "v1.0.0")
	data, err := getSchemaJSON(ctx, nil, nil, dag.View, dag)
	require.NoError(t, err)

	moduleTypes := `{"__schema": {"queryType": {"name": "Query"}, "types": [
		{"kind": "OBJECT", "name": "MyHTTPClient", "fields": [
			{"name": "getJSONValue", "type": {"kind": "SCALAR", "name": "String"},
			 "args": [{"name": "userIds", "type": {"kind": "SCALAR", "name": "String"}}]}
		]}
	]}, "__schemaVersion": "v1.0.0"}`

	var merged core.JSON
	require.NoError(t, dag.Select(ctx, dag.Root(), &merged,
		dagql.Selector{Field: "schema", Args: []dagql.NamedInput{{Name: "json", Value: core.JSON(data)}}},
		dagql.Selector{Field: "merge", Args: []dagql.NamedInput{
			{Name: "moduleTypes", Value: core.JSON(moduleTypes)},
			{Name: "moduleName", Value: dagql.NewString("myHttpClient")},
		}},
		dagql.Selector{Field: "contents"},
	))
	requireSchemaJSONIdentifiers(t, merged.Bytes(), true)
	schema := decodeSchemaResponse(t, merged.Bytes()).Schema
	for name, pascal := range map[string]string{
		"MyHTTPClient": "MyHTTPClient",
		"getJSONValue": "GetJSONValue",
		"userIds":      "UserIDs",
	} {
		id, ok := schema.Identifier(name)
		require.True(t, ok, name)
		require.Equal(t, pascal, id.Format(naming.Pascal, naming.Uppercase))
	}
}
