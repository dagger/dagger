package gitutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteLookupPeelsTagWithoutMutation(t *testing.T) {
	tagSHA := "1111111111111111111111111111111111111111"
	commitSHA := "2222222222222222222222222222222222222222"
	remote := &Remote{Refs: []*Ref{
		{Name: "refs/tags/v1.0.0", SHA: tagSHA},
		{Name: "refs/tags/v1.0.0^{}", SHA: commitSHA},
	}}

	ref, err := remote.Lookup("v1.0.0")
	require.NoError(t, err)
	require.Equal(t, "refs/tags/v1.0.0", ref.Name)
	require.Equal(t, commitSHA, ref.SHA)
	require.Equal(t, "refs/tags/v1.0.0", remote.Refs[0].Name)
	require.Equal(t, "refs/tags/v1.0.0^{}", remote.Refs[1].Name)
}
