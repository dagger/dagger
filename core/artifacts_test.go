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
