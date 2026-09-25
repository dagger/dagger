package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/stretchr/testify/require"
)

func TestCapturedHostHistoryScope(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "host-history-scope")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	md, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(t, err)
	url, err := gitutil.ParseURL("https://example.test/repo.git")
	require.NoError(t, err)
	anchor := strings.Repeat("a", 40)
	makeRef := func(key, sha string) dagql.ObjectResult[*GitRef] {
		remote := &RemoteGitRepository{URL: url, AuthUsername: key}
		repo := env.attach(t, ctx, cache, srv, key+"-repo", &GitRepository{Backend: remote, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
		repo, err = repo.WithContentDigest(ctx, hashutil.HashStrings("equal-repository-content"), call.ExtraDigestLabelRemoteCache)
		require.NoError(t, err)
		ref := &gitutil.Ref{SHA: sha, Name: sha}
		return env.attach(t, ctx, cache, srv, key+"-ref", &GitRef{Repo: repo, Ref: ref, Backend: &RemoteGitRef{repo: remote, Ref: ref}}).(dagql.ObjectResult[*GitRef])
	}
	parent, other := makeRef("alice", anchor), makeRef("bob", anchor)
	q := &Query{} // No server/host available: registration cannot perform IO.
	require.NoError(t, q.RegisterCapturedHostHistory(ctx, parent.Self().Repo, md.ClientID, "/approved", "state", anchor, url.Remote()))
	donor, ok, err := q.capturedHostHistory(ctx, parent)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "/approved", donor.path)
	_, ok, err = q.capturedHostHistory(ctx, other)
	require.NoError(t, err)
	require.False(t, ok, "equal content is not the same remote recipe")
	foreign := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "foreign", SessionID: md.SessionID})
	_, ok, err = q.capturedHostHistory(foreign, parent)
	require.NoError(t, err)
	require.False(t, ok, "donor routes never cross owners")
	require.Error(t, q.RegisterCapturedHostHistory(foreign, parent.Self().Repo, md.ClientID, "/approved", "state", anchor, url.Remote()))
	require.Error(t, q.RegisterCapturedHostHistory(ctx, parent.Self().Repo, md.ClientID, "/approved", "state", anchor, "https://other.test/repo.git"))
	_, ok, err = (&Query{}).capturedHostHistory(ctx, parent)
	require.NoError(t, err)
	require.False(t, ok, "new client lifecycle has no donor")
	pack, err := q.approvedHostCommitPack(ctx, parent, 1)
	require.NoError(t, err)
	require.Nil(t, pack, "ordinary promotion does not request host packs")
	original := parent.Self().Ref.SHA
	parent.Self().Ref.SHA = strings.Repeat("b", 40)
	parent.Self().Ref.Name = parent.Self().Ref.SHA
	_, ok, err = q.capturedHostHistory(ctx, parent)
	require.NoError(t, err)
	require.False(t, ok, "capability is for captured anchor only")
	parent.Self().Ref.SHA = original
	parent.Self().Ref.Name = "refs/heads/main"
	_, ok, err = q.capturedHostHistory(ctx, parent)
	require.NoError(t, err)
	require.False(t, ok, "only pinned remote sources may donate")
}

func TestImportHostCommitPack(t *testing.T) {
	for _, kind := range []string{"complete", "unrelated", "truncated", "missing", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			source, _, anchor := gitMirrorTestSource(t)
			prefix := filepath.Join(t.TempDir(), "pack")
			input := anchor + "\n"
			if kind == "unrelated" {
				gitMirrorTestRun(t, source, "checkout", "--orphan", "secret")
				gitMirrorTestRun(t, source, "-c", "user.name=Dagger", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "private")
				input += gitMirrorTestRun(t, source, "rev-parse", "HEAD") + "\n"
			}
			hash, err := runWorkspaceCommitGitInput(t.Context(), source, nil, strings.NewReader(input), "pack-objects", "--revs", prefix)
			require.NoError(t, err)
			pack := prefix + "-" + strings.TrimSpace(hash) + ".pack"
			if kind == "truncated" {
				require.NoError(t, os.WriteFile(pack, []byte("PACK"), 0600))
			}
			if kind == "missing" {
				anchor = strings.Repeat("f", 40)
			}
			ctx := t.Context()
			if kind == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			dest := t.TempDir()
			err = importHostCommitPack(ctx, dest, pack, anchor, []GitRemote{{Name: "origin", URL: "https://example.test/repo.git"}})
			if kind != "complete" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NoError(t, os.RemoveAll(source))
			require.NoError(t, os.Remove(pack))
			gitMirrorTestRun(t, dest, "fsck", "--full", "--strict")
			require.Equal(t, anchor, gitMirrorTestRun(t, dest, "rev-parse", "HEAD"))
			require.Empty(t, gitMirrorTestRun(t, dest, "for-each-ref"))
			require.NoFileExists(t, filepath.Join(dest, "objects", "info", "alternates"))
		})
	}
}
