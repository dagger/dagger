package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectScopeBindings(t *testing.T) {
	modules, selected := selectScopeBindings([]scopeBinding{
		{typeName: "StaffPullTools", moduleName: "staff"},
		{typeName: "Committer", moduleName: "committer", main: true},
		{typeName: "Staff", moduleName: "staff", main: true},
		{typeName: "GitToolsView", moduleName: "git-tools"},
		{typeName: "GitToolsAdmin", moduleName: "git-tools"},
	})
	require.Equal(t, []string{"staff", "committer", "git-tools"}, modules)
	typeNames := func(module string) []string {
		var names []string
		for _, b := range selected[module] {
			names = append(names, b.typeName)
		}
		return names
	}
	// A bound main object is the module's whole address space.
	require.Equal(t, []string{"Staff"}, typeNames("staff"))
	require.Equal(t, []string{"Committer"}, typeNames("committer"))
	// Without it, every bound object of the module contributes its tree.
	require.Equal(t, []string{"GitToolsView", "GitToolsAdmin"}, typeNames("git-tools"))
}

func TestMergeScopeArtifacts(t *testing.T) {
	bound := []*Artifact{
		{ModuleName: "roster", Path: []string{"roster", "members"}, TypeName: "Directory"},
		{ModuleName: "roster", Path: []string{"roster", "members", "dir"}, TypeName: "Directory"},
		{ModuleName: "git-tools", Path: []string{"git-tools", "head"}, TypeName: "GitRef"},
	}
	workspace := []*Artifact{
		// The workspace's own roster is shadowed by the bound one, including
		// its load failure and its entrypoint shorthand.
		{ModuleName: "roster", Path: []string{"roster", "members"}, TypeName: "Directory"},
		{ModuleName: "roster", Path: []string{"roster", "load"}, TypeName: "Check", LoadFailure: &ModuleLoadFailure{Name: "roster"}},
		{ModuleName: "Roster", Path: []string{"lint"}, TypeName: "Check"},
		{ModuleName: "app", Path: []string{"app", "build"}, TypeName: "Container"},
	}
	shadowed := map[string]bool{"roster": true, "git-tools": true}
	uris := func(selection *Artifacts) []string {
		var uris []string
		for _, entry := range selection.Entries {
			uri, err := entry.URI(ArtifactURIOpts{})
			require.NoError(t, err)
			uris = append(uris, uri)
		}
		return uris
	}

	all, err := mergeScopeArtifacts(bound, shadowed, workspace, nil)
	require.NoError(t, err)
	require.Nil(t, all.Selector.Paths)
	require.Equal(t, []string{"dag://app/build", "dag://git-tools/head", "dag://roster/members", "dag://roster/members/dir"}, uris(all))
	require.Same(t, bound[0], all.Entries[2], "the bound roster wins")

	narrowed, err := mergeScopeArtifacts(bound, shadowed, workspace[3:], []string{"roster:members"})
	require.NoError(t, err)
	require.Equal(t, []string{"roster/members/**"}, narrowed.Selector.Paths)
	// The workspace part arrives already narrowed by Workspace.artifacts;
	// include narrows the bound part the same way.
	require.Equal(t, []string{"dag://app/build", "dag://roster/members", "dag://roster/members/dir"}, uris(narrowed))
}
