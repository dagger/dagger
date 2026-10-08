package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/engine/engineutil"
	gitsession "github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestReconstructGitRemoteMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		pack engineutil.GitCheckoutPack
		want []GitRemote
	}{
		{
			name: "all remotes without origin",
			pack: engineutil.GitCheckoutPack{HasRemoteMetadata: true, UpstreamRemote: "trunk", Remotes: []*gitsession.CheckoutRemote{
				{Name: "fork", Url: "https://example.com/fork"},
				{Name: "trunk", Url: "https://example.com/trunk", PushUrl: "ssh://git@example.com/trunk"},
			}},
			want: []GitRemote{{Name: "fork", URL: "https://example.com/fork"}, {Name: "trunk", URL: "https://example.com/trunk", PushURL: "ssh://git@example.com/trunk"}},
		},
		{name: "captured empty list", pack: engineutil.GitCheckoutPack{HasRemoteMetadata: true}},
		{name: "older client", want: []GitRemote{{Name: "origin", URL: "https://example.com/origin"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, reconstructGitDir(t.Context(), dir, &tc.pack, "https://example.com/origin"))
			git := gitutil.NewGitCLI(gitutil.WithDir(dir))
			remotes, err := readGitConfigRemotes(t.Context(), git)
			require.NoError(t, err)
			if len(tc.want) == 0 {
				require.Empty(t, remotes)
			} else {
				require.Equal(t, tc.want, remotes)
			}
			if tc.pack.HasRemoteMetadata {
				out, err := git.Run(t.Context(), "config", "--get", "dagger.remotes")
				require.NoError(t, err)
				var captured []GitRemote
				require.NoError(t, json.Unmarshal(out, &captured))
				require.Equal(t, tc.want, MergeGitRemotes(nil, captured))
			}
		})
	}
}
