package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/opencontainers/go-digest"
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
	otherID := persistedRowID(t, cache, other)
	otherTreeID, retainedTreeID := persistedRowID(t, cache, otherTree), persistedRowID(t, cache, retainedGitTree)
	dir := env.directory(t, ctx, cache, srv, "owned-child", "owned-child-snapshot")
	sha := strings.Repeat("b", 40)
	child := env.attach(t, ctx, cache, srv, "child", &GitRepository{Backend: &LocalGitRepository{Directory: dir, HistorySource: parent, CheckoutBase: &GitCheckoutBase{Parent: parent, Tree: tree, CommitSHA: sha}}, Remote: &gitutil.Remote{}})
	childID, parentID, treeID, dirID := persistedRowID(t, cache, child), persistedRowID(t, cache, parent), persistedRowID(t, cache, tree), persistedRowID(t, cache, dir)
	require.Equal(t, map[string]uint64{
		"objectJSON.local.directoryResultID":           dirID,
		"objectJSON.local.checkoutBase.parentResultID": parentID,
		"objectJSON.local.historySourceResultID":       parentID,
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
		require.Equal(t, parentID, persistedRowID(t, cache, local.HistorySource))
		require.NoError(t, local.validateHistorySource(ctx))
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
	for _, badSource := range []uint64{dirID, otherID, 999999} {
		var payload persistedGitRepositoryPayload
		require.NoError(t, json.Unmarshal(encoding.Envelope.ObjectJSON, &payload))
		payload.Local.HistorySourceResultID = badSource
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		_, err = (&GitRepository{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, childID, frame), data)
		require.Error(t, err)
	}
	for _, badTree := range []uint64{parentID, 999999} {
		var payload persistedGitRepositoryPayload
		require.NoError(t, json.Unmarshal(encoding.Envelope.ObjectJSON, &payload))
		payload.Local.CheckoutBase.TreeResultID = badTree
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		_, err = (&GitRepository{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, childID, frame), data)
		require.Error(t, err)
	}
	// A loadable tree that cannot prove this parent is only an optimization
	// lost: the repository decodes without it and checks out in full.
	for _, unproven := range []uint64{dirID, otherTreeID, retainedTreeID} {
		var payload persistedGitRepositoryPayload
		require.NoError(t, json.Unmarshal(encoding.Envelope.ObjectJSON, &payload))
		payload.Local.CheckoutBase.TreeResultID = unproven
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		decoded, err := (&GitRepository{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, childID, frame), data)
		require.NoError(t, err)
		local := decoded.(*GitRepository).Backend.(*LocalGitRepository)
		require.Nil(t, local.CheckoutBase.Tree.Self())
		require.Equal(t, parentID, persistedRowID(t, cache, local.CheckoutBase.Parent))
		require.False(t, (&LocalGitRef{Ref: &gitutil.Ref{SHA: sha}, repo: local}).incrementalCheckoutEligible())
	}
}

// The engine cache answers tree() on a content-equivalent GitRef with the first
// writer's result and frame. GitRef content digests cover URL/ref/auth only, so
// e.g. git(url).ref(sha) and git(url, keepGitDir: true).ref(sha) share a tree
// whose frame names the other recipe. withCommit on the second must still
// succeed, dropping only the unprovable pinned tree.
func TestGitCheckoutBaseContentEquivalentParentTree(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "equivalent-parent-tree")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	url, err := gitutil.ParseURL("https://example.com/repo.git")
	require.NoError(t, err)
	remote := &RemoteGitRepository{URL: url, Platform: Platform{OS: "linux", Architecture: "amd64"}}
	repo := env.attach(t, ctx, cache, srv, "remote-repo", &GitRepository{Backend: remote, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	ref := &gitutil.Ref{SHA: strings.Repeat("a", 40)}
	a := env.attach(t, ctx, cache, srv, "recipe-a", &GitRef{Repo: repo, Ref: ref, Backend: &RemoteGitRef{Ref: ref, repo: remote}}).(dagql.ObjectResult[*GitRef])
	b := env.attach(t, ctx, cache, srv, "recipe-b", &GitRef{Repo: repo, Ref: ref, Backend: &RemoteGitRef{Ref: ref, repo: remote}}).(dagql.ObjectResult[*GitRef])
	// Same content digest, as schema.ref assigns for equal URL/ref/auth.
	same := digest.FromString("gitRef-equivalent")
	a, err = a.WithContentDigest(ctx, same)
	require.NoError(t, err)
	b, err = b.WithContentDigest(ctx, same)
	require.NoError(t, err)
	seed := env.directory(t, ctx, cache, srv, "canonical-seed", "canonical-snapshot")
	dagql.Fields[*GitRef]{dagql.Func("tree", func(context.Context, *GitRef, struct{ DiscardGitDir bool }) (*Directory, error) {
		return seed.Self(), nil
	}).IsPersistable()}.Install(srv)
	sel := dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(true)}}}
	var treeA, treeB dagql.ObjectResult[*Directory]
	require.NoError(t, srv.Select(ctx, a, &treeA, sel))
	require.NoError(t, srv.Select(ctx, b, &treeB, sel))
	require.NoError(t, (&GitCheckoutBase{Parent: a, Tree: treeA, CommitSHA: ref.SHA}).validateTree(ctx))
	require.Error(t, (&GitCheckoutBase{Parent: b, Tree: treeB, CommitSHA: ref.SHA}).validateTree(ctx), "the cache returned recipe A's frame")

	// The value gitRefWithCommitRepository builds for recipe B.
	sha := strings.Repeat("b", 40)
	backend := &LocalGitRepository{Directory: seed, HistorySource: b, CheckoutBase: &GitCheckoutBase{Parent: b, Tree: treeB, CommitSHA: sha}}
	child := env.attach(t, ctx, cache, srv, "child", &GitRepository{Backend: backend, Remote: &gitutil.Remote{}})
	attached := child.Unwrap().(*GitRepository).Backend.(*LocalGitRepository)
	require.Nil(t, attached.CheckoutBase.Tree.Self())
	require.Equal(t, persistedRowID(t, cache, b), persistedRowID(t, cache, attached.CheckoutBase.Parent))
	require.False(t, (&LocalGitRef{Ref: &gitutil.Ref{SHA: sha}, repo: attached}).incrementalCheckoutEligible(), "remote parents without a proven tree check out in full")
	var payload persistedGitRepositoryPayload
	require.NoError(t, json.Unmarshal(persistedEncoding(t, ctx, cache, child).Envelope.ObjectJSON, &payload))
	require.Zero(t, payload.Local.CheckoutBase.TreeResultID)
}

// Owned storage retains the exact remote it was derived from, authentication
// included, so names it lacks can be resolved without asking the caller for
// credentials again. Only a remote repository may be retained.
func TestGitUpstreamPersistence(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "git-upstream")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	url, err := gitutil.ParseURL("ssh://git@example.com/private/repo.git")
	require.NoError(t, err)
	remote := &RemoteGitRepository{URL: url, AuthUsername: "authorized-reader", Platform: Platform{OS: "linux", Architecture: "amd64"}}
	upstream := env.attach(t, ctx, cache, srv, "remote-repo", &GitRepository{Backend: remote, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	dir := env.directory(t, ctx, cache, srv, "owned", "owned-snapshot")
	plain := env.attach(t, ctx, cache, srv, "plain-owned-repo", &GitRepository{Backend: &LocalGitRepository{Directory: dir}, Remote: &gitutil.Remote{}})
	child := env.attach(t, ctx, cache, srv, "owned-repo", &GitRepository{Backend: &LocalGitRepository{Directory: dir, Upstream: upstream}, Remote: &gitutil.Remote{}})
	childID, upstreamID, dirID, plainID := persistedRowID(t, cache, child), persistedRowID(t, cache, upstream), persistedRowID(t, cache, dir), persistedRowID(t, cache, plain)
	require.Equal(t, map[string]uint64{
		"objectJSON.local.directoryResultID": dirID,
		"objectJSON.local.upstreamResultID":  upstreamID,
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
		require.Equal(t, upstreamID, persistedRowID(t, cache, local.Upstream))
		require.Equal(t, "authorized-reader", local.Upstream.Self().Backend.(*RemoteGitRepository).AuthUsername)
		require.Equal(t, encoding.Envelope, persistedEncoding(t, ctx, cache, loaded).Envelope)
	}
	// A retained upstream must be a remote: owned storage never resolves
	// names through another owned repository or a directory.
	for _, badUpstream := range []uint64{plainID, dirID, 999999} {
		var payload persistedGitRepositoryPayload
		require.NoError(t, json.Unmarshal(encoding.Envelope.ObjectJSON, &payload))
		payload.Local.UpstreamResultID = badUpstream
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		_, err = (&GitRepository{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, childID, frame), data)
		require.Error(t, err)
	}
}

func TestGitUpstreamOf(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "git-upstream-of")
	ctx, cache, srv := env.open(t)
	url, err := gitutil.ParseURL("https://example.com/repo.git")
	require.NoError(t, err)
	remote := env.attach(t, ctx, cache, srv, "remote", &GitRepository{Backend: &RemoteGitRepository{URL: url}, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	dir := env.directory(t, ctx, cache, srv, "dir", "dir-snapshot")
	plain := env.attach(t, ctx, cache, srv, "plain", &GitRepository{Backend: &LocalGitRepository{Directory: dir}, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	derived := env.attach(t, ctx, cache, srv, "derived", &GitRepository{Backend: &LocalGitRepository{Directory: dir, Upstream: remote}, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])

	require.Equal(t, persistedRowID(t, cache, remote), persistedRowID(t, cache, GitUpstream(remote)), "a remote is its own upstream")
	require.Equal(t, persistedRowID(t, cache, remote), persistedRowID(t, cache, GitUpstream(derived)), "derived storage passes its upstream on")
	require.Nil(t, GitUpstream(plain).Self(), "storage with no remote origin has no upstream")
	require.Nil(t, GitUpstream(dagql.ObjectResult[*GitRepository]{}).Self())
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
		{{Name: "origin", URL: "https://fetch.test/repo", Implicit: true}},
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

func TestGitRemoteHandleAndUpstreamPersistence(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "git-remotes")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRemoteHandle]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	url, err := gitutil.ParseURL("https://example.com/source")
	require.NoError(t, err)
	upstream := env.attach(t, ctx, cache, srv, "upstream", &GitRepository{Backend: &RemoteGitRepository{URL: url, Platform: Platform{OS: "linux", Architecture: "amd64"}}, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	anchor := &gitutil.Ref{SHA: strings.Repeat("a", 40)}
	backend, err := upstream.Self().Backend.Get(ctx, anchor)
	require.NoError(t, err)
	history := env.attach(t, ctx, cache, srv, "history-source", &GitRef{Repo: upstream, Ref: anchor, Backend: backend}).(dagql.ObjectResult[*GitRef])
	dir := env.directory(t, ctx, cache, srv, "storage", "git-storage")
	selected := "trunk"
	source := env.attach(t, ctx, cache, srv, "source", &GitRepository{
		Backend:        &LocalGitRepository{Directory: dir, Upstream: upstream, HistorySource: history},
		Remotes:        []GitRemote{{Name: "trunk", URL: url.String()}, {Name: "fork", URL: "https://example.com/fork"}},
		UpstreamRemote: &selected,
		Remote:         &gitutil.Remote{},
	}).(dagql.ObjectResult[*GitRepository])
	remote := env.attach(t, ctx, cache, srv, "remote", &GitRemoteHandle{Name: "trunk", URL: url.String(), Source: source})
	remoteID, sourceID, upstreamID := persistedRowID(t, cache, remote), persistedRowID(t, cache, source), persistedRowID(t, cache, upstream)
	require.Equal(t, map[string]uint64{"objectJSON.sourceResultID": sourceID}, assertPersistedRefsMatchOwnership(t, ctx, cache, remote))
	historyID := persistedRowID(t, cache, history)
	require.Equal(t, map[string]uint64{
		"objectJSON.local.directoryResultID":     persistedRowID(t, cache, dir),
		"objectJSON.local.upstreamResultID":      upstreamID,
		"objectJSON.local.historySourceResultID": historyID,
	}, assertPersistedRefsMatchOwnership(t, ctx, cache, source))
	ctx, cache, srv = env.restart(t, ctx, cache)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRemoteHandle]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, remoteID)
	require.NoError(t, err)
	restored := loaded.Unwrap().(*GitRemoteHandle)
	require.Equal(t, "trunk", restored.Name.String())
	require.Equal(t, sourceID, persistedRowID(t, cache, restored.Source))
	require.Equal(t, upstreamID, persistedRowID(t, cache, GitUpstream(restored.Source)))
	local := restored.Source.Self().Backend.(*LocalGitRepository)
	require.Equal(t, historyID, persistedRowID(t, cache, local.HistorySource))
	require.NoError(t, local.validateHistorySource(ctx))
	require.Equal(t, &selected, restored.Source.Self().UpstreamRemote)
	remotes, selection, err := restored.Source.Self().ConfiguredRemotes(ctx)
	require.NoError(t, err)
	require.Equal(t, "trunk", selection)
	require.Equal(t, "trunk", SelectDefaultGitRemote(remotes, selection).Name)
}

// Workspace.__pullRepository keeps the receiver's HEAD, a ref of another
// (possibly owned shallow) storage, as the base of a descendant HEAD. The
// complete pulled storage has no history source of its own, only the
// receiver's Upstream.
func TestPulledGitCheckoutBasePersistence(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "pulled-checkout-base")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	url, err := gitutil.ParseURL("https://example.com/source")
	require.NoError(t, err)
	upstream := env.attach(t, ctx, cache, srv, "upstream", &GitRepository{Backend: &RemoteGitRepository{URL: url, Platform: Platform{OS: "linux", Architecture: "amd64"}}, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	anchor := &gitutil.Ref{SHA: strings.Repeat("a", 40)}
	anchorBackend, err := upstream.Self().Backend.Get(ctx, anchor)
	require.NoError(t, err)
	history := env.attach(t, ctx, cache, srv, "history-source", &GitRef{Repo: upstream, Ref: anchor, Backend: anchorBackend}).(dagql.ObjectResult[*GitRef])
	receiverDir := env.directory(t, ctx, cache, srv, "receiver-dir", "receiver-snapshot")
	receiverRepo := env.attach(t, ctx, cache, srv, "receiver-repo", &GitRepository{Backend: &LocalGitRepository{Directory: receiverDir, Upstream: upstream, HistorySource: history}, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	receiverSHA := &gitutil.Ref{SHA: strings.Repeat("b", 40)}
	receiver := env.attach(t, ctx, cache, srv, "receiver-head", &GitRef{Repo: receiverRepo, Ref: receiverSHA, Backend: &LocalGitRef{Ref: receiverSHA, repo: receiverRepo.Self().Backend.(*LocalGitRepository)}}).(dagql.ObjectResult[*GitRef])
	pulledDir := env.directory(t, ctx, cache, srv, "pulled-dir", "pulled-snapshot")
	sha := strings.Repeat("c", 40)
	pulled := env.attach(t, ctx, cache, srv, "pulled-repo", &GitRepository{Backend: &LocalGitRepository{Directory: pulledDir, Upstream: upstream, CheckoutBase: &GitCheckoutBase{Parent: receiver, CommitSHA: sha}}, Remote: &gitutil.Remote{}})
	pulledID, receiverID, upstreamID := persistedRowID(t, cache, pulled), persistedRowID(t, cache, receiver), persistedRowID(t, cache, upstream)
	require.Equal(t, map[string]uint64{
		"objectJSON.local.directoryResultID":           persistedRowID(t, cache, pulledDir),
		"objectJSON.local.upstreamResultID":            upstreamID,
		"objectJSON.local.checkoutBase.parentResultID": receiverID,
	}, assertPersistedRefsMatchOwnership(t, ctx, cache, pulled))
	for range 2 {
		ctx, cache, srv = env.restart(t, ctx, cache)
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
		loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, pulledID)
		require.NoError(t, err)
		restored := loaded.Unwrap().(*GitRepository).Backend.(*LocalGitRepository)
		require.Equal(t, sha, restored.CheckoutBase.CommitSHA)
		require.Equal(t, receiverID, persistedRowID(t, cache, restored.CheckoutBase.Parent))
		require.Equal(t, upstreamID, persistedRowID(t, cache, restored.Upstream))
		require.Nil(t, restored.HistorySource.Self())
		require.NoError(t, restored.validateHistorySource(ctx))
		require.True(t, (&LocalGitRef{Ref: &gitutil.Ref{SHA: sha}, repo: restored}).incrementalCheckoutEligible())
	}
}
