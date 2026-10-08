package git

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckoutRemoteMetadata(t *testing.T) {
	repo, home := initRepo(t, "feature")
	commitFile(t, repo, home, "a", "one", "first")
	gitCmd(t, home, repo, "remote", "add", "fork", "https://example.com/fork")
	gitCmd(t, home, repo, "remote", "add", "trunk", "ssh://git@example.com/trunk")
	gitCmd(t, home, repo, "config", "remote.trunk.pushurl", "ssh://git@example.com/publish")
	gitCmd(t, home, repo, "config", "branch.feature.remote", "trunk")
	gitCmd(t, home, repo, "config", "branch.feature.merge", "refs/heads/main")
	state, err := collectCheckoutState(context.Background(), repo)
	require.NoError(t, err)
	require.Equal(t, "trunk", state.upstreamRemote)
	require.Len(t, state.remotes, 2)
	require.Equal(t, "fork", state.remotes[0].Name)
	require.Equal(t, "ssh://git@example.com/publish", state.remotes[1].PushUrl)
	packed := packCheckout(t, repo).metadata(t)
	require.True(t, packed.HasRemoteMetadata)
	require.Equal(t, state.remotes, packed.Remotes)
	require.Equal(t, "trunk", packed.UpstreamRemote)
	before := state.digest()
	gitCmd(t, home, repo, "remote", "set-url", "fork", "https://example.com/other")
	require.NotEqual(t, before, checkoutDigest(t, repo), "remote-only edits must change checkout identity")
	before = checkoutDigest(t, repo)
	gitCmd(t, home, repo, "config", "branch.feature.remote", "fork")
	require.NotEqual(t, before, checkoutDigest(t, repo), "upstream-only edits must change checkout identity")
	gitCmd(t, home, repo, "checkout", "--detach")
	state, err = collectCheckoutState(context.Background(), repo)
	require.NoError(t, err)
	require.Empty(t, state.upstreamRemote)
	gitCmd(t, home, repo, "remote", "set-url", "fork", "https://token@example.com/private")
	before = checkoutDigest(t, repo)
	gitCmd(t, home, repo, "remote", "set-url", "fork", "https://token@example.com/another")
	require.NotEqual(t, before, checkoutDigest(t, repo), "omitted URLs still invalidate captures")
	packed = packCheckout(t, repo).metadata(t)
	require.Empty(t, packed.Remotes[0].Url)
	require.Empty(t, packed.Remotes[0].PushUrl)
}

func TestCaptureGitRemoteSelectionMetadata(t *testing.T) {
	repo, home, remote := initCaptureRepo(t)
	gitCmd(t, home, repo, "remote", "rename", "origin", "trunk")
	gitCmd(t, home, repo, "remote", "add", "fork", remote)
	meta := captureGit(t, repo, &CaptureGitPolicy{}).metadata(t)
	require.Nil(t, meta.Error)
	require.True(t, meta.HasRemoteMetadata)
	require.Equal(t, "trunk", meta.UpstreamRemote)
	require.Equal(t, "trunk", meta.RemoteName)
	require.Equal(t, []*CheckoutRemote{{Name: "fork", Url: remote}, {Name: "trunk", Url: remote}}, meta.Remotes)
}

func TestCheckoutRemoteMetadataValidNames(t *testing.T) {
	for _, name := range []string{"team/trunk", "-fork"} {
		t.Run(name, func(t *testing.T) {
			repo, home := initRepo(t, "feature")
			commitFile(t, repo, home, "a", "one", "first")
			gitCmd(t, home, repo, "remote", "add", "--", name, "https://example.com/repo")
			remotes, _, _, err := checkoutRemoteMetadata(t.Context(), repo, "refs/heads/feature")
			require.NoError(t, err)
			require.Equal(t, []*CheckoutRemote{{Name: name, Url: "https://example.com/repo"}}, remotes)
		})
	}
}

func TestCheckoutRoutingURL(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"https://example.com/repo", "https://example.com/repo"},
		{"ssh://git@example.com/repo", "ssh://git@example.com/repo"},
		{"git@example.com:repo", "git@example.com:repo"},
		{"../repo", "../repo"},
		{"https://token@example.com/repo", ""},
		{"ssh://git:secret@example.com/repo", ""},
		{"https://example.com/repo?token=secret", ""},
	} {
		require.Equal(t, tc.want, checkoutRoutingURL(tc.raw))
	}
}
