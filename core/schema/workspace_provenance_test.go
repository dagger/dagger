package schema

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceProvenanceSchema(t *testing.T) {
	env := &persistedSchemaTestEnv{dbPath: filepath.Join(t.TempDir(), "cache.db")}
	ctx, _, srv := env.open(t)
	dagql.Fields[*core.Query]{
		dagql.Func("__testProvenance", func(context.Context, *core.Query, struct{}) (*core.WorkspaceProvenance, error) {
			return &core.WorkspaceProvenance{Selectors: []*core.WorkspaceSelector{{Path: "/docs", Include: []string{}, Exclude: []string{}, Kind: "WORKSPACE_READ"}}}, nil
		}),
	}.Install(srv)
	for _, tc := range []struct {
		path string
		want bool
	}{{"docs/intro.md", true}, {"src/main.go", false}} {
		var got bool
		err := srv.Select(ctx, srv.Root(), &got,
			dagql.Selector{Field: "__testProvenance"},
			dagql.Selector{Field: "affectedByPath", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String(tc.path)}}},
		)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	schema := srv.Schema()
	for name, fields := range map[string][]string{
		"WorkspaceProvenance": {"selectors", "affectedByPath", "affectedByDiff"},
		"WorkspaceSelector":   {"path", "include", "exclude", "conservative", "kind"},
		"Artifact":            {"provenance", "affectedByPath", "affectedByDiff"},
		"Artifacts":           {"filterAffectedByPath", "filterAffectedByDiff"},
	} {
		def := schema.Types[name]
		require.NotNil(t, def, name)
		for _, field := range fields {
			require.NotNil(t, def.Fields.ForName(field), name+"."+field)
		}
	}
	for _, name := range []string{"WorkspaceProvenance", "Artifact", "Artifacts"} {
		field := "affectedByDiff"
		if name == "Artifacts" {
			field = "filterAffectedByDiff"
		}
		arg := schema.Types[name].Fields.ForName(field).Arguments.ForName("changes")
		require.NotNil(t, arg, name+"."+field+"(changes:)")
		require.Equal(t, "ID!", arg.Type.String())
	}
}
