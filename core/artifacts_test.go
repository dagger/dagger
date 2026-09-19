package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

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
