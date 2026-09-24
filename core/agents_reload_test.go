package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestToolStateIdentity(t *testing.T) {
	dag := newCoreDagqlServerForTest(t, &Query{})
	installModuleObjectTestModuleClass(dag)
	makeObject := func(src *ModuleSource, name, typeName string) *ModuleObject {
		var source dagql.Nullable[dagql.ObjectResult[*ModuleSource]]
		if src != nil {
			source = dagql.NonNull(newTypeDefDetachedResult(t, dag, "reload-source", src))
		}
		mod := newTypeDefDetachedResult(t, dag, "reload-module", &Module{
			NameField: name, OriginalName: "staff", Source: source,
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
	for _, tc := range []struct {
		name string
		src  *ModuleSource
	}{
		{"new revision", gitSource("https://github.com/vito/agents", "new", "staff")},
		{"fork", gitSource("https://github.com/other/agents", "new", "staff")},
		{"different subdirectory", gitSource("https://github.com/vito/agents", "new", "other")},
		{"local clone", &ModuleSource{Kind: ModuleSourceKindLocal, SourceRootSubpath: "staff", Local: &LocalModuleSource{ContextDirectoryPath: "/clone"}}},
		{"cached implementation without stable source metadata", &ModuleSource{Kind: ModuleSourceKindDir}},
		{"absent source metadata", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := makeObject(tc.src, "staff", "Staff")
			require.NoError(t, sameToolStateIdentity(original, next))
		})
	}
	for _, tc := range []struct {
		name, alias, module, object string
	}{
		{"different installation", "other", "staff", "Staff"},
		{"different intrinsic module", "staff", "other", "Staff"},
		{"different object", "staff", "staff", "Other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := makeObject(nil, tc.alias, tc.object)
			next.Module.Self().OriginalName = tc.module
			require.ErrorContains(t, sameToolStateIdentity(original, next), "module or object identity changed")
		})
	}
	t.Run("local roots can move", func(t *testing.T) {
		old := makeObject(&ModuleSource{Kind: ModuleSourceKindLocal, SourceRootSubpath: "staff", Local: &LocalModuleSource{ContextDirectoryPath: "/one"}}, "staff", "Staff")
		next := makeObject(&ModuleSource{Kind: ModuleSourceKindLocal, SourceRootSubpath: "staff", Local: &LocalModuleSource{ContextDirectoryPath: "/two"}}, "staff", "Staff")
		require.NoError(t, sameToolStateIdentity(old, next))
	})
}

func TestExpertiseConversion(t *testing.T) {
	srv := newCoreDagqlServerForTest(t, &Query{})
	installModuleObjectTestModuleClass(srv)
	mod := newTypeDefDetachedResult(t, srv, "agent-module", &Module{NameField: "agent"})
	artifact := &Artifact{Path: []string{"agent", "configure"}, TypeName: "Expertise", Directives: []string{"agent"},
		Node: &ModTreeNode{Name: "configure", OriginalModule: mod}}
	agent, err := NewExpertise(artifact)
	require.NoError(t, err)
	require.Equal(t, "agent", agent.OriginalModule().Name())
	// References keep their own metadata wrappers, independent of the selection.
	agent.Artifact.Node.Name = "changed"
	require.Equal(t, "configure", artifact.Node.Name)
	for _, invalid := range []*Artifact{
		{Path: []string{"unmarked"}, TypeName: "LLM", Node: artifact.Node},
		{Path: []string{"wrong-type"}, TypeName: "Container", Directives: []string{"agent"}, Node: artifact.Node},
	} {
		_, err := NewExpertise(invalid)
		require.ErrorContains(t, err, "not a source of expertise")
	}
}
