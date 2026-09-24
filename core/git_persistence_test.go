package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestGitCheckoutBasePersistence(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "checkout-base")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	parentDir := env.directory(t, ctx, cache, srv, "parent-dir", "parent-snapshot")
	parentRepo := env.attach(t, ctx, cache, srv, "parent-repo", &GitRepository{Backend: &LocalGitRepository{Directory: parentDir}, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	parent := env.attach(t, ctx, cache, srv, "parent-ref", &GitRef{Repo: parentRepo, Ref: &gitutil.Ref{SHA: strings.Repeat("a", 40)}, Backend: &LocalGitRef{Ref: &gitutil.Ref{SHA: strings.Repeat("a", 40)}, repo: parentRepo.Self().Backend.(*LocalGitRepository)}}).(dagql.ObjectResult[*GitRef])
	childDir := env.directory(t, ctx, cache, srv, "child-dir", "child-snapshot")
	sha := strings.Repeat("b", 40)
	child := env.attach(t, ctx, cache, srv, "child-repo", &GitRepository{Backend: &LocalGitRepository{Directory: childDir, CheckoutBase: &GitCheckoutBase{Parent: parent, CommitSHA: sha}}, Remote: &gitutil.Remote{}})
	childID, parentID, dirID := persistedRowID(t, cache, child), persistedRowID(t, cache, parent), persistedRowID(t, cache, childDir)
	parentDirID := persistedRowID(t, cache, parentDir)
	refs := assertPersistedRefsMatchOwnership(t, ctx, cache, child)
	require.Equal(t, map[string]uint64{"objectJSON.local.directoryResultID": dirID, "objectJSON.local.checkoutBase.parentResultID": parentID}, refs)
	encoding := persistedEncoding(t, ctx, cache, child)
	frame, err := child.ResultCall()
	require.NoError(t, err)
	for range 2 {
		ctx, cache, srv = env.restart(t, ctx, cache)
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
		loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, childID)
		require.NoError(t, err)
		restored := loaded.Unwrap().(*GitRepository).Backend.(*LocalGitRepository)
		require.Equal(t, sha, restored.CheckoutBase.CommitSHA)
		require.Equal(t, parentID, persistedRowID(t, cache, restored.CheckoutBase.Parent))
		require.Equal(t, dirID, persistedRowID(t, cache, restored.Directory))
		retained := restored.CheckoutBase.Parent.Self().Repo.Self().Backend.(*LocalGitRepository).Directory
		require.Equal(t, parentDirID, persistedRowID(t, cache, retained), "parent snapshot remains retained through exact ref/repository dependencies")
		require.True(t, (&LocalGitRef{Ref: &gitutil.Ref{SHA: sha}, repo: restored}).incrementalCheckoutEligible())
		require.False(t, (&LocalGitRef{Ref: &gitutil.Ref{SHA: strings.Repeat("c", 40)}, repo: restored}).incrementalCheckoutEligible())
		require.Equal(t, encoding.Envelope, persistedEncoding(t, ctx, cache, loaded).Envelope)
	}
	var payload persistedGitRepositoryPayload
	require.NoError(t, json.Unmarshal(encoding.Envelope.ObjectJSON, &payload))
	for _, bad := range []*persistedGitCheckoutBase{
		{}, {ParentResultID: parentID}, {CommitSHA: sha}, {ParentResultID: parentID, CommitSHA: "abcd"}, {ParentResultID: parentID, CommitSHA: strings.Repeat("z", 40)}, {ParentResultID: dirID, CommitSHA: sha}, {ParentResultID: 999999, CommitSHA: sha},
	} {
		payload.Local.CheckoutBase = bad
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		_, err = (&GitRepository{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, childID, frame), data)
		require.Error(t, err)
	}
	// SHA256 provenance persists even though runtime materialization falls back.
	payload.Local.CheckoutBase = &persistedGitCheckoutBase{ParentResultID: parentID, CommitSHA: strings.Repeat("a", 64)}
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	_, err = (&GitRepository{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, childID, frame), data)
	require.NoError(t, err)
}

func TestRemoteGitCheckoutBasePersistence(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "remote-checkout-base")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	url, err := gitutil.ParseURL("https://example.com/repo.git")
	require.NoError(t, err)
	remote := &RemoteGitRepository{URL: url, AuthUsername: "authorized-reader", Platform: Platform{OS: "linux", Architecture: "amd64"}}
	repo := env.attach(t, ctx, cache, srv, "remote-repo", &GitRepository{Backend: remote, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	ref := &gitutil.Ref{SHA: strings.Repeat("a", 40)}
	parent := env.attach(t, ctx, cache, srv, "remote-ref", &GitRef{Repo: repo, Ref: ref, Backend: &RemoteGitRef{Ref: ref, repo: remote}}).(dagql.ObjectResult[*GitRef])
	seed := env.directory(t, ctx, cache, srv, "canonical-seed", "canonical-snapshot")
	dagql.Fields[*GitRef]{dagql.Func("tree", func(context.Context, *GitRef, struct{ DiscardGitDir bool }) (*Directory, error) {
		return seed.Self(), nil
	}).IsPersistable()}.Install(srv)
	var tree dagql.ObjectResult[*Directory]
	require.NoError(t, srv.Select(ctx, parent, &tree, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(true)}}}))
	other := env.attach(t, ctx, cache, srv, "different-remote-recipe", &GitRef{Repo: repo, Ref: ref, Backend: &RemoteGitRef{Ref: ref, repo: remote}}).(dagql.ObjectResult[*GitRef])
	var otherTree, retainedGitTree dagql.ObjectResult[*Directory]
	require.NoError(t, srv.Select(ctx, other, &otherTree, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(true)}}}))
	require.NoError(t, srv.Select(ctx, parent, &retainedGitTree, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(false)}}}))
	otherTreeID, retainedTreeID := persistedRowID(t, cache, otherTree), persistedRowID(t, cache, retainedGitTree)
	dir := env.directory(t, ctx, cache, srv, "owned-child", "owned-child-snapshot")
	sha := strings.Repeat("b", 40)
	child := env.attach(t, ctx, cache, srv, "child", &GitRepository{Backend: &LocalGitRepository{Directory: dir, CheckoutBase: &GitCheckoutBase{Parent: parent, Tree: tree, CommitSHA: sha}}, Remote: &gitutil.Remote{}})
	childID, parentID, treeID, dirID := persistedRowID(t, cache, child), persistedRowID(t, cache, parent), persistedRowID(t, cache, tree), persistedRowID(t, cache, dir)
	require.Equal(t, map[string]uint64{
		"objectJSON.local.directoryResultID":           dirID,
		"objectJSON.local.checkoutBase.parentResultID": parentID,
		"objectJSON.local.checkoutBase.treeResultID":   treeID,
	}, assertPersistedRefsMatchOwnership(t, ctx, cache, child))
	encoding := persistedEncoding(t, ctx, cache, child)
	frame, err := child.ResultCall()
	require.NoError(t, err)
	for range 2 {
		ctx, cache, srv = env.restart(t, ctx, cache)
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
		loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, childID)
		require.NoError(t, err)
		local := loaded.Unwrap().(*GitRepository).Backend.(*LocalGitRepository)
		require.Equal(t, treeID, persistedRowID(t, cache, local.CheckoutBase.Tree))
		require.Equal(t, parentID, persistedRowID(t, cache, local.CheckoutBase.Parent))
		require.Equal(t, "authorized-reader", local.CheckoutBase.Parent.Self().Repo.Self().Backend.(*RemoteGitRepository).AuthUsername)
		// There is deliberately no mirror/session backing to fetch from. The
		// exact canonical snapshot must survive solely through child ownership.
		snapshot, err := local.CheckoutBase.Tree.Self().Snapshot.GetOrEval(ctx, local.CheckoutBase.Tree.Result)
		require.NoError(t, err)
		require.Equal(t, "canonical-snapshot", snapshot.SnapshotID())
		require.True(t, (&LocalGitRef{Ref: &gitutil.Ref{SHA: sha}, repo: local}).incrementalCheckoutEligible())
		require.Equal(t, encoding.Envelope, persistedEncoding(t, ctx, cache, loaded).Envelope)
	}
	for _, badTree := range []uint64{parentID, dirID, otherTreeID, retainedTreeID, 999999} {
		var payload persistedGitRepositoryPayload
		require.NoError(t, json.Unmarshal(encoding.Envelope.ObjectJSON, &payload))
		payload.Local.CheckoutBase.TreeResultID = badTree
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		_, err = (&GitRepository{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, childID, frame), data)
		require.Error(t, err)
	}
}

func TestGitRepositoryRemotesPersistence(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
	dir := volumeTestCachedObjectResult(t, ctx, cache, srv, "push-routing", "git-directory", &Directory{})
	for _, remotes := range [][]GitRemote{
		nil,
		{
			{Name: "origin", URL: "https://fetch.test/repo", PushURL: "ssh://git@example.test/repo"},
			{Name: "upstream", URL: "https://upstream.test/repo"},
		},
	} {
		repo := &GitRepository{
			URL:     dagql.NonNull(dagql.String("https://fetch.test/repo")),
			Remotes: remotes,
			Backend: &LocalGitRepository{Directory: dir},
		}
		encoded, err := repo.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, 0, nil))
		require.NoError(t, err)
		decoded, err := (&GitRepository{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, 0, nil), encoded.JSON)
		require.NoError(t, err)
		restored := decoded.(*GitRepository)
		require.Equal(t, repo.URL, restored.URL)
		require.Equal(t, repo.Remotes, restored.Remotes)
		require.IsType(t, &LocalGitRepository{}, restored.Backend)
	}
}
