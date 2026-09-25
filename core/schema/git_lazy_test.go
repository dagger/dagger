package schema

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/stretchr/testify/require"
)

func TestNativeCommitBaseCacheScope(t *testing.T) {
	ctx, srv, cache, _ := resolverOutputFixture(t)
	s := &gitSchema{}
	calls := 0
	dagql.Fields[*core.GitRef]{dagql.NodeFuncWithDynamicInputs("__nativeCommitBase", func(ctx context.Context, _ dagql.ObjectResult[*core.GitRef], _ gitRefNativeCommitBaseArgs) (dagql.ObjectResult[*core.Directory], error) {
		calls++
		dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
		dir.SetPath("/")
		dir.SetSnapshot(nil)
		return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	}, s.gitRefNativeCommitBaseKey).IsPersistable()}.Install(srv)
	url, err := gitutil.ParseURL("https://example.test/repo.git")
	require.NoError(t, err)
	refs := make([]dagql.ObjectResult[*core.GitRef], 2)
	for i, username := range []string{"alice", "bob"} {
		remote := &core.RemoteGitRepository{URL: url, AuthUsername: username}
		repo := resolverAttach(t, ctx, srv, cache, username+"-repo", &core.GitRepository{Backend: remote, Remote: &gitutil.Remote{}})
		ref := &gitutil.Ref{SHA: strings.Repeat("a", 40)}
		backend, err := remote.Get(ctx, ref)
		require.NoError(t, err)
		refs[i] = resolverAttach(t, ctx, srv, cache, username+"-ref", &core.GitRef{Repo: repo, Ref: ref, Backend: backend})
		// Stronger than production auth digests: deliberately equate content.
		// The dynamic recipe input must still partition owned promotions.
		refs[i], err = refs[i].WithContentDigest(ctx, hashutil.HashStrings("same-content"), call.ExtraDigestLabelRemoteCache)
		require.NoError(t, err)
	}
	firstRecipe, err := refs[0].RecipeDigest(ctx)
	require.NoError(t, err)
	for _, ref := range []dagql.ObjectResult[*core.GitRef]{refs[0], refs[0], refs[1], refs[1]} {
		var output dagql.ObjectResult[*core.Directory]
		// A caller cannot force Bob into Alice's cache entry with this input.
		require.NoError(t, srv.Select(ctx, ref, &output, dagql.Selector{Field: "__nativeCommitBase", Args: []dagql.NamedInput{{Name: "parentRecipe", Value: dagql.String(firstRecipe.String())}}}))
		frame, err := output.ResultCall()
		require.NoError(t, err)
		want, err := ref.RecipeDigest(ctx)
		require.NoError(t, err)
		for _, arg := range frame.Args {
			if arg.Name == "parentRecipe" {
				require.Equal(t, want.String(), arg.Value.StringValue)
			}
		}
	}
	require.Equal(t, 2, calls, "exact recipes deduplicate, equal contents do not authorize reuse")
	calls = 0
	dagql.Fields[*core.GitRef]{dagql.NodeFuncWithDynamicInputs("__hydrateRepository", func(ctx context.Context, _ dagql.ObjectResult[*core.GitRef], _ gitRefHydrateRepositoryArgs) (dagql.ObjectResult[*core.Directory], error) {
		calls++
		dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
		dir.SetPath("/")
		dir.SetSnapshot(nil)
		return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	}, s.gitRefHydrateRepositoryKey).IsPersistable()}.Install(srv)
	for _, key := range []string{"first-storage", "second-storage"} {
		dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
		dir.SetPath("/")
		dir.SetSnapshot(nil)
		storage := resolverAttach(t, ctx, srv, cache, key, dir)
		storage, err = storage.WithContentDigest(ctx, hashutil.HashStrings("same-storage-content"), call.ExtraDigestLabelRemoteCache)
		require.NoError(t, err)
		id, err := storage.RecipeID(ctx)
		require.NoError(t, err)
		for range 2 {
			for _, ref := range refs {
				var result dagql.ObjectResult[*core.Directory]
				require.NoError(t, srv.Select(ctx, ref, &result, dagql.Selector{Field: "__hydrateRepository", Args: []dagql.NamedInput{{Name: "directory", Value: dagql.NewID[*core.Directory](id)}, {Name: "scope", Value: dagql.String("forged-scope")}}}))
			}
		}
	}
	require.Equal(t, 4, calls, "hydration is scoped to both exact source and owned storage recipes")
}

func TestGitResolvedFrames(t *testing.T) {
	sha := strings.Repeat("a", 40)
	override := strings.Repeat("b", 40)
	for _, test := range []struct {
		name, field, arg, wantField, wantName string
		fixed, detached                       bool
	}{
		{name: "head", field: "head", wantField: "__resolvedRef", wantName: "refs/heads/main"},
		{name: "detached", field: "head", wantField: "__resolvedRef", detached: true},
		{name: "branch", field: "branch", arg: "main", wantField: "__resolvedRef", wantName: "refs/heads/main"},
		{name: "tag", field: "tag", arg: "v1.0.0", wantField: "__resolvedRef", wantName: "refs/tags/v1.0.0"},
		{name: "ref", field: "ref", arg: "main", wantField: "__resolvedRef", wantName: "refs/heads/main"},
		{name: "legacy", field: "commit", arg: "main", wantField: "__resolvedRef", wantName: "refs/heads/main"},
		{name: "latest", field: "latest", wantField: "__resolvedRef", wantName: "refs/tags/v1.0.0"},
		{name: "override", field: "ref", arg: "main", wantField: "__resolvedRef", wantName: "refs/heads/main", fixed: true},
		{name: "fixed", field: "ref", arg: sha, wantField: "ref", wantName: sha},
		{name: "internal", field: "__resolvedRef", arg: "saved-fetch-name", wantField: "__resolvedRef", wantName: "saved-fetch-name", fixed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, srv, cache, server := resolverOutputFixture(t)
			srv.View = "v0.18.0"
			srv.InstallObject(dagql.NewClass[*core.GitRef](srv))
			s := &gitSchema{}
			dagql.Fields[*core.GitRepository]{
				dagql.NodeFunc("__resolvedRef", s.resolvedRef).View(AllVersion).IsPersistable(),
				dagql.NodeFunc("head", s.head).View(AllVersion), dagql.NodeFunc("ref", s.ref).View(AllVersion), dagql.NodeFunc("branch", s.branch).View(AllVersion), dagql.NodeFunc("tag", s.tag).View(AllVersion), dagql.NodeFunc("commit", s.commitRef).View(AllVersion), dagql.NodeFunc("latest", s.latest).View(AllVersion),
			}.Install(srv)
			dagql.Fields[*core.GitRef]{dagql.NodeFunc("tree", s.tree).View(AllVersion).IsPersistable()}.Install(srv)
			dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
			dir.SetPath("/")
			dir.SetSnapshot(nil)
			source := resolverAttach(t, ctx, srv, cache, "source", dir)
			remote := &gitutil.Remote{Refs: []*gitutil.Ref{{Name: "HEAD", SHA: sha}, {Name: "refs/heads/main", SHA: sha}, {Name: "refs/tags/v1.0.0", SHA: sha}}, Symrefs: map[string]string{"HEAD": "refs/heads/main"}}
			if test.detached {
				remote.Symrefs = nil
			}
			if test.name == "internal" {
				remote = nil
			} // An internal selection must not try to open the source.
			parent := resolverAttach(t, ctx, srv, cache, "repository", &core.GitRepository{Backend: &core.LocalGitRepository{Directory: source}, Remote: remote})
			selector := dagql.Selector{Field: test.field, View: srv.View}
			if test.arg != "" {
				name := "name"
				if test.field == "commit" {
					name = "id"
				}
				selector.Args = append(selector.Args, dagql.NamedInput{Name: name, Value: dagql.String(test.arg)})
			}
			if test.fixed {
				selector.Args = append(selector.Args, dagql.NamedInput{Name: "commit", Value: dagql.String(override)})
			}
			var result dagql.ObjectResult[*core.GitRef]
			rowsBefore := cache.Size()
			require.NoError(t, srv.Select(ctx, parent, &result, selector))
			t.Logf("Git %s selection: retained rows before=%d after=%d delta=%d", test.name, rowsBefore, cache.Size(), cache.Size()-rowsBefore)
			wantSHA := sha
			if test.fixed {
				wantSHA = override
			}
			require.Equal(t, wantSHA, result.Self().Ref.SHA)
			require.Equal(t, test.wantName, result.Self().Ref.Name)
			record, err := cache.CapturePersistedRecord(ctx, result)
			require.NoError(t, err)
			require.Equal(t, test.wantField, record.Call.Field)
			require.NotNil(t, record.Call.Receiver)
			require.Empty(t, record.Call.ImplicitInputs)
			if test.wantField == "__resolvedRef" {
				args := map[string]string{}
				for _, arg := range record.Call.Args {
					args[arg.Name] = arg.Value.StringValue
				}
				require.Equal(t, map[string]string{"name": test.wantName, "commit": wantSHA}, args)
			}
			var output dagql.ObjectResult[*core.Directory]
			rowsBefore = cache.Size()
			require.NoError(t, srv.Select(ctx, result, &output, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(true)}, {Name: "depth", Value: dagql.Int(3)}, {Name: "includeTags", Value: dagql.Boolean(true)}}}))
			t.Logf("Git %s tree: retained row delta=%d", test.name, cache.Size()-rowsBefore)
			lazy := output.Self().Lazy.(*core.DirectoryGitTreeLazy)
			require.False(t, lazy.IsEvaluated())
			require.Equal(t, wantSHA, lazy.Ref.Self().Ref.SHA)
			require.True(t, lazy.DiscardGitDir)
			require.True(t, lazy.IncludeTags)
			require.Equal(t, 3, lazy.Depth)
			tree, err := cache.CapturePersistedRecord(ctx, output)
			require.NoError(t, err)
			var payload struct {
				Form, LazyKind string
				LazyJSON       json.RawMessage
			}
			require.NoError(t, json.Unmarshal(tree.Envelope.ObjectJSON, &payload))
			require.Equal(t, "lazy", payload.Form)
			var inputs struct{ RefResultID uint64 }
			require.NoError(t, json.Unmarshal(payload.LazyJSON, &inputs))
			wantRef := record.ResultID
			if test.wantName != wantSHA {
				// Main pins a tree without .git to a SHA-named ref: tree selects
				// ref(name: <sha>) on the repository first, so the lazy tree's
				// input is that pinned ref, published right after the resolved one.
				wantRef++
			}
			require.Equal(t, wantRef, inputs.RefResultID)
			require.Empty(t, server.manager.outputs, "tree handle construction must not check out files")
		})
	}
}

func TestGitFixedCommitAndLockFrames(t *testing.T) {
	for _, mode := range []string{"commit", "ref", "locked branch"} {
		t.Run(mode, func(t *testing.T) {
			ctx, srv, cache, server := resolverOutputFixture(t)
			srv.View = workspaceLockingVersion
			srv.InstallObject(dagql.NewClass[*core.GitRef](srv))
			srv.InstallObject(dagql.NewClass[*core.GitCommit](srv))
			s := &gitSchema{}
			dagql.Fields[*core.GitRepository]{dagql.NodeFunc("__resolvedRef", s.resolvedRef).View(AllVersion).IsPersistable(), dagql.NodeFunc("branch", s.branch).View(AllVersion), dagql.NodeFunc("ref", s.ref).View(AllVersion), dagql.NodeFunc("commit", s.commit).View(AllVersion)}.Install(srv)
			u, err := gitutil.ParseURL("https://unreachable.invalid/repository.git")
			require.NoError(t, err)
			repo := &core.GitRepository{Backend: &core.RemoteGitRepository{URL: u}, URL: dagql.NonNull(dagql.String(u.Remote()))}
			parent := resolverAttach(t, ctx, srv, cache, "repository", repo)
			sha := strings.Repeat("c", 40)
			var result dagql.AnyResult
			if mode == "commit" {
				var commit dagql.ObjectResult[*core.GitCommit]
				require.NoError(t, srv.Select(ctx, parent, &commit, dagql.Selector{Field: "commit", View: srv.View, Args: []dagql.NamedInput{{Name: "id", Value: dagql.String(sha)}}}))
				require.Equal(t, sha, commit.Self().Ref.SHA)
				require.Empty(t, commit.Self().FetchRef.Name)
				result = commit
			} else {
				selector := dagql.Selector{Field: "ref", View: srv.View, Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(sha)}}}
				if mode == "locked branch" {
					lock := workspace.NewLock()
					inputs, err := gitLockInputs(repo, "refs/heads/main")
					require.NoError(t, err)
					require.NoError(t, lock.SetLookup(workspace.CoreLockNamespace, workspace.LockOperationGitSHA, inputs, sha))
					ctx = withWorkspaceLookupLockOverride(ctx, lock)
					selector.Field = "branch"
					selector.Args[0].Value = dagql.String("main")
				}
				var ref dagql.ObjectResult[*core.GitRef]
				require.NoError(t, srv.Select(ctx, parent, &ref, selector))
				require.Equal(t, sha, ref.Self().Ref.SHA)
				result = ref
			}
			record, err := cache.CapturePersistedRecord(ctx, result)
			require.NoError(t, err)
			wantField := mode
			if mode == "locked branch" {
				wantField = "__resolvedRef"
			}
			require.Equal(t, wantField, record.Call.Field)
			require.Nil(t, repo.Remote, "fixed and locked paths must not resolve the remote")
			require.Empty(t, server.manager.outputs)
			parentID, err := cache.PersistedResultID(parent)
			require.NoError(t, err)
			require.Equal(t, parentID, record.Call.Receiver.ResultID)
			require.Empty(t, record.Call.ImplicitInputs)
		})
	}
}

// Use the real ref/tree resolvers and cache, with only the checkout replaced.
// This isolates when equivalence is published from network authentication.
type authScopedTreeBackend struct {
	core.GitRefBackend
	calls    int
	err      error
	snapshot bkcache.ImmutableRef
}

func (b *authScopedTreeBackend) Tree(context.Context, *dagql.Server, bool, int, bool, []core.GitRemote) (*core.Directory, error) {
	b.calls++
	if b.err != nil {
		return nil, b.err
	}
	dir := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
	dir.SetPath("/")
	dir.SetSnapshot(b.snapshot)
	return dir, nil
}

func TestGitTreeContentIdentityAfterMaterialization(t *testing.T) {
	ctx, srv, cache, server := resolverOutputFixture(t)
	server.platform = core.Platform{OS: "linux", Architecture: "amd64"}
	srv.InstallObject(dagql.NewClass[*core.GitRef](srv))
	srv.InstallObject(dagql.NewClass[*core.Secret](srv))
	s := &gitSchema{}
	dagql.Fields[*core.GitRepository]{dagql.NodeFunc("ref", s.ref).View(AllVersion).IsPersistable()}.Install(srv)
	dagql.Fields[*core.GitRef]{dagql.NodeFunc("tree", s.tree).View(AllVersion).IsPersistable()}.Install(srv)
	u, err := gitutil.ParseURL("https://unreachable.invalid/repository.git")
	require.NoError(t, err)
	sha := strings.Repeat("a", 40)
	server.manager.inputs = map[string]*resolverOutputRef{}
	var trees []dagql.ObjectResult[*core.Directory]
	var backends []*authScopedTreeBackend
	for _, scope := range []string{"none", "token-a", "token-b"} {
		backend := &core.RemoteGitRepository{URL: u}
		if scope != "none" {
			backend.AuthToken = resolverAttach(t, ctx, srv, cache, scope, &core.Secret{Handle: dagql.SessionResourceHandle(scope)})
		}
		repo := resolverAttach(t, ctx, srv, cache, "repository-"+scope, &core.GitRepository{Backend: backend, URL: dagql.NonNull(dagql.String(u.Remote()))})
		var ref dagql.ObjectResult[*core.GitRef]
		require.NoError(t, srv.Select(ctx, repo, &ref, dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(sha)}}}))
		snapshot := &resolverOutputRef{root: t.TempDir(), id: scope}
		server.manager.inputs[scope] = snapshot
		checkout := &authScopedTreeBackend{snapshot: snapshot}
		if scope == "none" {
			checkout.err = errors.New("authentication required")
		}
		ref.Self().Backend = checkout
		var tree dagql.ObjectResult[*core.Directory]
		require.NoError(t, srv.Select(ctx, ref, &tree, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "depth", Value: dagql.Int(1)}}}))
		record, err := cache.CapturePersistedRecord(ctx, tree)
		require.NoError(t, err)
		require.Empty(t, record.Call.ContentDigest(), "pending tree must not publish content equivalence")
		var payload struct{ LazyJSON json.RawMessage }
		require.NoError(t, json.Unmarshal(record.Envelope.ObjectJSON, &payload))
		var recipe struct{ ContentDigest string }
		require.NoError(t, json.Unmarshal(payload.LazyJSON, &recipe))
		require.NotEmpty(t, recipe.ContentDigest, "deferred identity must survive persistence")
		for _, other := range trees {
			otherID, err := other.ID()
			require.NoError(t, err)
			id, err := tree.ID()
			require.NoError(t, err)
			require.NotEqual(t, otherID, id, "pending trees with different auth must retain different recipes")
		}
		trees = append(trees, tree)
		backends = append(backends, checkout)
		require.Zero(t, checkout.calls)
	}

	require.ErrorContains(t, cache.Evaluate(ctx, trees[0]), "authentication required")
	failed, err := cache.CapturePersistedRecord(ctx, trees[0])
	require.NoError(t, err)
	require.Empty(t, failed.Call.ContentDigest())

	var materializedDigest string
	for i, tree := range trees[1:] {
		if i == 1 {
			// Enter managed acquisition without installing a source, forcing
			// the saved recipe to run through a private operation receiver.
			require.NoError(t, cache.RunLazyTask(ctx, tree, "test:managed", dagql.LazyTaskSpec{Body: func(ctx context.Context) error {
				permit, outcome, err := cache.TryAcquire(ctx, tree, dagql.PersistedPartAddress{Part: "snapshot"}, dagql.PartTaskFromContext(ctx))
				require.NoError(t, err)
				require.Equal(t, dagql.GateGranted, outcome)
				permit.Release()
				return nil
			}}))
		}
		if i == 0 {
			// A completed checkout with failed ownership bookkeeping must
			// remain unshared. Retrying publishes without checking out again.
			before, err := tree.ContentPreferredDigest(ctx)
			require.NoError(t, err)
			server.manager.leaseFault = errors.New("injected owner failure")
			require.ErrorContains(t, cache.Evaluate(ctx, tree), "injected owner failure")
			_, err = cache.CapturePersistedRecord(ctx, tree)
			require.ErrorIs(t, err, dagql.ErrPersistStateNotReady)
			after, err := tree.ContentPreferredDigest(ctx)
			require.NoError(t, err)
			require.Equal(t, before, after, "unfinished bookkeeping must not publish content identity")
			require.Equal(t, 1, backends[i+1].calls)
			server.manager.leaseFault = nil
		}
		require.NoError(t, cache.Evaluate(ctx, tree))
		record, err := cache.CapturePersistedRecord(ctx, tree)
		require.NoError(t, err)
		dgst := record.Call.ContentDigest().String()
		require.NotEmpty(t, dgst)
		if materializedDigest == "" {
			materializedDigest = dgst
		} else {
			require.Equal(t, materializedDigest, dgst, "authorized materialized content is shared")
		}
		require.Equal(t, 1, backends[i+1].calls)
	}
	require.ErrorContains(t, cache.Evaluate(ctx, trees[0]), "authentication required", "successful content must not authorize the socket-less recipe")
}

func TestGitCommitTreeDefersContentIdentity(t *testing.T) {
	ctx, srv, cache, _ := resolverOutputFixture(t)
	srv.InstallObject(dagql.NewClass[*core.GitCommit](srv))
	s := &gitSchema{}
	dagql.Fields[*core.GitCommit]{dagql.NodeFunc("tree", s.commitTree).View(AllVersion).IsPersistable()}.Install(srv)
	u, err := gitutil.ParseURL("https://unreachable.invalid/repository.git")
	require.NoError(t, err)
	repo := resolverAttach(t, ctx, srv, cache, "repository", &core.GitRepository{Backend: &core.RemoteGitRepository{URL: u}})
	ref := &gitutil.Ref{SHA: strings.Repeat("b", 40)}
	backend, err := repo.Self().Backend.Get(ctx, ref)
	require.NoError(t, err)
	commit := resolverAttach(t, ctx, srv, cache, "commit", &core.GitCommit{Repo: repo, Ref: ref, Backend: backend})
	var tree dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, commit, &tree, dagql.Selector{Field: "tree"}))
	record, err := cache.CapturePersistedRecord(ctx, tree)
	require.NoError(t, err)
	require.Empty(t, record.Call.ContentDigest())
	var payload struct{ LazyJSON json.RawMessage }
	require.NoError(t, json.Unmarshal(record.Envelope.ObjectJSON, &payload))
	var recipe struct{ ContentDigest string }
	require.NoError(t, json.Unmarshal(payload.LazyJSON, &recipe))
	require.NotEmpty(t, recipe.ContentDigest)
}
