package core

import (
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestArtifactQuestionMarkPatternURIRoundTrip(t *testing.T) {
	all := &Artifacts{Entries: []*Artifact{
		{Path: []string{"scana"}, TypeName: "Check"},
		{Path: []string{"scanb"}, TypeName: "Check"},
		{Path: []string{"scanlong"}, TypeName: "Check"},
	}}
	selected, err := all.FilterPattern("scan?")
	require.NoError(t, err)
	require.Len(t, selected.Entries, 2)
	address, err := dagaddress.Parse(selected.URI())
	require.NoError(t, err)
	replayed, err := all.FilterURI(address)
	require.NoError(t, err)
	require.Equal(t, selected.Entries, replayed.Entries)
}

func TestArtifactAbsoluteURI(t *testing.T) {
	const commit = "1111111111111111111111111111111111111111"
	ref, err := dagql.NewResultForCall(&GitRef{Ref: &gitutil.Ref{SHA: commit}}, &dagql.ResultCall{})
	require.NoError(t, err)
	for _, remote := range []string{
		"https://github.com/acme/repo",
		"ssh://git@github.com/acme/repo",
		"git@github.com:acme/repo",
		"github.com/acme/repo",
	} {
		for _, subdir := range []string{"", "subdir"} {
			t.Run(remote+"/"+subdir, func(t *testing.T) {
				ws, err := dagql.NewResultForCall(&Workspace{
					Address: GitRefString(remote, subdir, "main"),
					source:  NewWorkspaceSourceGitRef(ref, false),
				}, &dagql.ResultCall{})
				require.NoError(t, err)
				artifact := &Artifact{
					Workspace: dagql.ObjectResult[*Workspace]{Result: ws},
					Path:      []string{"base"},
				}
				uri, err := artifact.URI(ArtifactURIOpts{Absolute: true})
				require.NoError(t, err)
				address := "github.com/acme/repo"
				if subdir != "" {
					address += "/" + subdir
				}
				require.Equal(t, "dag://"+address+"@"+commit+":base", uri)
			})
		}
	}
}

func TestArtifactWithoutWorkspace(t *testing.T) {
	bound := &Artifact{ModuleName: "roster", Path: []string{"roster", "members"}, TypeName: "Directory"}
	// No absolute address: it names a workspace.
	_, err := bound.URI(ArtifactURIOpts{Absolute: true})
	require.ErrorContains(t, err, "has no absolute address")
	uri, err := bound.URI(ArtifactURIOpts{DimensionKeys: true, TypeAssertion: true})
	require.NoError(t, err)
	require.Equal(t, "dag+directory://?directory=roster/members", uri)
	// Evaluated in the caller's context, which is left as is.
	ctx := t.Context()
	evalCtx, err := bound.WorkspaceContext(ctx)
	require.NoError(t, err)
	require.Equal(t, ctx, evalCtx)
	require.Nil(t, bound.BoundRoot())

	// An absolute address never selects an artifact without a workspace.
	ws, err := dagql.NewResultForCall(&Workspace{Address: "file:///ws"}, &dagql.ResultCall{})
	require.NoError(t, err)
	inWorkspace := &Artifact{ModuleName: "roster", Path: []string{"roster", "members"}, TypeName: "Directory", Workspace: dagql.ObjectResult[*Workspace]{Result: ws}}
	mixed := &Artifacts{Entries: []*Artifact{bound, inWorkspace}}
	absolute, err := dagaddress.Parse("dag://github.com/acme/repo@1111111111111111111111111111111111111111:roster/members")
	require.NoError(t, err)
	selected, err := mixed.FilterURI(absolute)
	require.NoError(t, err)
	require.Len(t, selected.Entries, 1)
	require.Same(t, ws.Self(), selected.Entries[0].Workspace.Self())
	relative, err := dagaddress.Parse("dag://roster/members")
	require.NoError(t, err)
	selected, err = mixed.FilterURI(relative)
	require.NoError(t, err)
	require.Len(t, selected.Entries, 2)
}

func TestArtifactParentDirectivesAreImmediate(t *testing.T) {
	parent := &ModTreeNode{Directives: []string{"generate"}}
	child := &ModTreeNode{Parent: parent}
	grandchild := &ModTreeNode{Parent: child}
	artifacts := &Artifacts{Entries: []*Artifact{
		{Path: []string{"child"}, Node: child},
		{Path: []string{"child", "grandchild"}, Node: grandchild},
		{Path: []string{"load"}},
	}}
	included := artifacts.FilterParentDirectives([]string{"generate"}, false)
	require.Len(t, included.Entries, 1)
	require.Equal(t, []string{"child"}, included.Entries[0].Path)
	excluded := artifacts.FilterParentDirectives([]string{"generate"}, true)
	require.Len(t, excluded.Entries, 2)
	require.Equal(t, []string{"child", "grandchild"}, excluded.Entries[0].Path)
	require.Equal(t, []string{"load"}, excluded.Entries[1].Path)
}
