package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestToolStateOrigin(t *testing.T) {
	dag := newCoreDagqlServerForTest(t, &Query{})
	installModuleObjectTestModuleClass(dag)
	makeObject := func(src *ModuleSource, name, typeName string) *ModuleObject {
		source := newTypeDefDetachedResult(t, dag, "reload-source", src)
		mod := newTypeDefDetachedResult(t, dag, "reload-module", &Module{
			NameField: name, OriginalName: "staff", Source: dagql.NonNull(source),
		})
		return &ModuleObject{Module: mod, TypeDef: &ObjectTypeDef{OriginalName: typeName}}
	}
	gitSource := func(repo, revision, subpath string) *ModuleSource {
		return &ModuleSource{
			Kind: ModuleSourceKindGit, SourceRootSubpath: subpath,
			Git: &GitModuleSource{CloneRef: repo, Commit: revision, Version: revision},
		}
	}
	original := makeObject(gitSource("https://github.com/vito/agents", "old", "staff"), "staff", "Staff")
	t.Run("new revision is same origin", func(t *testing.T) {
		next := makeObject(gitSource("https://github.com/vito/agents", "new", "staff"), "staff", "Staff")
		require.NoError(t, sameToolStateOrigin(t.Context(), original, next))
	})
	for _, tc := range []struct {
		name, repo, subpath, alias, object string
	}{
		{"different repository", "https://github.com/other/agents", "staff", "staff", "Staff"},
		{"different subdirectory", "https://github.com/vito/agents", "other", "staff", "Staff"},
		{"different installation", "https://github.com/vito/agents", "staff", "other", "Staff"},
		{"different object", "https://github.com/vito/agents", "staff", "staff", "Other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := makeObject(gitSource(tc.repo, "new", tc.subpath), tc.alias, tc.object)
			require.Error(t, sameToolStateOrigin(t.Context(), original, next))
		})
	}
	t.Run("local roots are distinct", func(t *testing.T) {
		old := makeObject(&ModuleSource{Kind: ModuleSourceKindLocal, SourceRootSubpath: "staff", Local: &LocalModuleSource{ContextDirectoryPath: "/one"}}, "staff", "Staff")
		next := makeObject(&ModuleSource{Kind: ModuleSourceKindLocal, SourceRootSubpath: "staff", Local: &LocalModuleSource{ContextDirectoryPath: "/two"}}, "staff", "Staff")
		require.ErrorContains(t, sameToolStateOrigin(t.Context(), old, next), "module source changed")
	})
	t.Run("local served and overlay sources share an origin", func(t *testing.T) {
		dag.InstallObject(dagql.NewClass[*Workspace](dag))
		ws := newTypeDefDetachedResult(t, dag, "reload-workspace", &Workspace{Address: "file:///one", selectedEnv: "dev"})
		oldSource := &ModuleSource{Kind: ModuleSourceKindLocal, SourceRootSubpath: "staff", Local: &LocalModuleSource{ContextDirectoryPath: "/one"}}
		newSource := oldSource.Clone()
		newSource.Workspace = ws
		require.NoError(t, sameToolStateOrigin(t.Context(), makeObject(oldSource, "staff", "Staff"), makeObject(newSource, "staff", "Staff")))
	})
	t.Run("unknown origin is refused", func(t *testing.T) {
		next := makeObject(&ModuleSource{Kind: ModuleSourceKindDir}, "staff", "Staff")
		require.ErrorContains(t, sameToolStateOrigin(t.Context(), original, next), "no stable source identity")
	})
}
