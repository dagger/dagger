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

func TestRemoteLookupHEAD(t *testing.T) {
	const sha = "1111111111111111111111111111111111111111"
	for _, tc := range []struct {
		name    string
		symrefs map[string]string
		want    string
	}{
		{name: "detached"},
		{name: "branch", symrefs: map[string]string{"HEAD": "refs/heads/feature"}, want: "refs/heads/feature"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := &Remote{
				Refs:    []*Ref{{Name: "HEAD", SHA: sha}, {Name: "refs/heads/feature", SHA: sha}},
				Symrefs: tc.symrefs,
			}
			ref, err := remote.Lookup("HEAD")
			require.NoError(t, err)
			require.Equal(t, tc.want, ref.Name)
			require.Equal(t, sha, ref.SHA)
			require.Equal(t, "HEAD", remote.Refs[0].Name, "lookup must not change the stored ref")

			// A real branch remains named even if HEAD is detached at it.
			branch, err := remote.Lookup("feature")
			require.NoError(t, err)
			require.Equal(t, "refs/heads/feature", branch.Name)
		})
	}
}
