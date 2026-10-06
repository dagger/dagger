package core

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDefaultGitRemote(t *testing.T) {
	for _, tc := range []struct {
		name           string
		remotes        []GitRemote
		upstream, want string
	}{
		{name: "none"},
		{name: "sole", remotes: []GitRemote{{Name: "upstream"}}, want: "upstream"},
		{name: "origin before upstream", remotes: []GitRemote{{Name: "fork"}, {Name: "origin"}}, upstream: "fork", want: "origin"},
		{name: "upstream", remotes: []GitRemote{{Name: "fork"}, {Name: "trunk"}}, upstream: "trunk", want: "trunk"},
		{name: "ambiguous", remotes: []GitRemote{{Name: "fork"}, {Name: "trunk"}}},
		{name: "local upstream", remotes: []GitRemote{{Name: "fork"}, {Name: "trunk"}}, upstream: "."},
		{name: "missing upstream", remotes: []GitRemote{{Name: "fork"}, {Name: "trunk"}}, upstream: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := SelectDefaultGitRemote(tc.remotes, tc.upstream)
			if tc.want == "" {
				require.Nil(t, remote)
			} else {
				require.NotNil(t, remote)
				require.Equal(t, tc.want, remote.Name)
			}
		})
	}
}
