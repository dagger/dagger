package core

import (
	"testing"

	"github.com/dagger/dagger/core/provenance"
	"github.com/stretchr/testify/require"
)

func TestSourceScopeAppliesToChangedPaths(t *testing.T) {
	inputs := newWorkspaceProvenance([]provenance.Selector{
		{Path: "/src/main.go", Kind: provenance.WorkspaceRead},
		{Path: "/README.md", Kind: provenance.WorkspaceRead},
	})
	scopes := []WorkspacePath{"/src"}
	unknown := newWorkspaceProvenance([]provenance.Selector{conservativeProvenance(provenance.WorkspaceRead)})
	for _, changed := range []*ChangesetPaths{{}, {Modified: []string{"README.md"}}} {
		match, err := unknown.AffectedByChangedPaths(changed, scopes, nil)
		require.NoError(t, err)
		require.False(t, match, "unknown inputs need a changed path inside the requested scope")
	}
	match, err := unknown.AffectedByChangedPaths(&ChangesetPaths{Modified: []string{"src/main.go"}}, scopes, nil)
	require.NoError(t, err)
	require.True(t, match)

	match, err = inputs.AffectedByChangedPaths(&ChangesetPaths{Modified: []string{"README.md"}}, scopes, nil)
	require.NoError(t, err)
	require.False(t, match, "an unchanged scoped input must not match a change outside the scope")

	match, err = inputs.AffectedByChangedPaths(&ChangesetPaths{Modified: []string{"README.md", "src/main.go"}}, scopes, nil)
	require.NoError(t, err)
	require.True(t, match)

	match, err = inputs.AffectedByChangedPaths(&ChangesetPaths{AllRemoved: []string{"src/", "src/main.go"}}, scopes, nil)
	require.NoError(t, err)
	require.True(t, match, "removed subtrees retain their input paths")
}
