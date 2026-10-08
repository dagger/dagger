package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// Exercise the real dependency resolver, intercepting only the final source
// load. An explicit parent URL already supplies the repository boundary; a
// local dependency must retain it rather than ask for import-path discovery.
func TestGitLocalDependencyPreservesSelector(t *testing.T) {
	type sourceArgs struct {
		RefString     string
		RefPin        string
		DisableFindUp bool
	}
	for _, tc := range []struct {
		name     string
		selector gitref.SelectorType
		version  string
		want     string
	}{
		{"explicit Git URL", gitref.GitRefSelector, "main", "https://git.example.org/team/sub/project.git#main:ci/dep"},
		{"literal version-shaped Git ref", gitref.GitRefSelector, "v1.2", "https://git.example.org/team/sub/project.git#v1.2:ci/dep"},
		{"legacy module version", gitref.ModuleVersionSelector, "main", "https://git.example.org/team/sub/project.git/ci/dep@main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := &Query{}
			ctx := ContextWithQuery(t.Context(), root)
			ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "dependency-test", SessionID: "dependency-test"})
			cache, err := dagql.NewCache(ctx, "", nil, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
			ctx = dagql.ContextWithCache(ctx, cache)
			dag := newCoreDagqlServerForTest(t, root)
			dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*ModuleSource]{Typed: &ModuleSource{}}))
			loaded := false
			var loadedArgs sourceArgs
			dagql.Fields[*Query]{
				dagql.Func("moduleSource", func(_ context.Context, _ *Query, args sourceArgs) (*ModuleSource, error) {
					loaded = true
					loadedArgs = args
					return &ModuleSource{ModuleName: "dep"}, nil
				}),
			}.Install(dag)
			parent := &ModuleSource{
				Kind:              ModuleSourceKindGit,
				ModuleName:        "parent",
				SourceRootSubpath: "ci",
				Git: &GitModuleSource{
					CloneRef: "https://git.example.org/team/sub/project.git",
					Version:  tc.version,
					Commit:   "resolved-commit",
					Selector: tc.selector,
				},
			}
			_, err = ResolveDepToSource(ctx, nil, dag, parent, "./dep", "", "")
			require.NoError(t, err)
			require.True(t, loaded)
			require.Equal(t, tc.want, loadedArgs.RefString)
			require.Equal(t, "resolved-commit", loadedArgs.RefPin)
			require.True(t, loadedArgs.DisableFindUp)
		})
	}
}
