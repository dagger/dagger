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
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/stretchr/testify/require"
)

type gitRevisionFixture struct {
	ctx    context.Context
	srv    *dagql.Server
	repo   *core.GitRepository
	parent dagql.ObjectResult[*core.GitRepository]
	walks  []gitRevisionWalk
}

type gitRevisionWalk struct {
	base *gitutil.Ref
	rev  gitutil.Revision
}

// newGitRevisionFixture serves GitRepository.ref for a remote repository whose
// commit walk is stubbed: walking any revision yields walked. A non-nil remote
// is seeded into the session's ls-remote cache, standing in for the network.
func newGitRevisionFixture(t *testing.T, remote *gitutil.Remote, walked string, walkErr error) *gitRevisionFixture {
	t.Helper()
	ctx, srv, cache, _ := resolverOutputFixture(t)
	srv.View = workspaceLockingVersion
	srv.InstallObject(dagql.NewClass[*core.GitRef](srv))
	f := &gitRevisionFixture{ctx: ctx, srv: srv}
	s := &gitSchema{walkRevision: func(_ context.Context, _ *core.GitRepository, base *gitutil.Ref, rev gitutil.Revision) (string, error) {
		f.walks = append(f.walks, gitRevisionWalk{base: base, rev: rev})
		return walked, walkErr
	}}
	dagql.Fields[*core.GitRepository]{
		dagql.NodeFunc("__resolvedRef", s.resolvedRef).View(AllVersion).IsPersistable(),
		dagql.NodeFunc("ref", s.revision).View(AllVersion),
	}.Install(srv)
	u, err := gitutil.ParseURL("https://unreachable.invalid/repository.git")
	require.NoError(t, err)
	if remote != nil {
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		require.NoError(t, err)
		_, err = cache.GetOrInitArbitrary(ctx, clientMetadata.SessionID,
			hashutil.HashStrings(clientMetadata.SessionID, u.Remote()).String(),
			func(context.Context) (any, error) {
				payload, err := json.Marshal(remote)
				return string(payload), err
			})
		require.NoError(t, err)
	}
	f.repo = &core.GitRepository{
		Backend: &core.RemoteGitRepository{URL: u},
		URL:     dagql.NonNull(dagql.String(u.Remote())),
	}
	f.parent = resolverAttach(t, ctx, srv, cache, "repository", f.repo)
	return f
}

func (f *gitRevisionFixture) ref(ctx context.Context, name string, extra ...dagql.NamedInput) (dagql.ObjectResult[*core.GitRef], error) {
	var result dagql.ObjectResult[*core.GitRef]
	err := f.srv.Select(ctx, f.parent, &result, dagql.Selector{
		Field: "ref",
		View:  f.srv.View,
		Args:  append([]dagql.NamedInput{{Name: "name", Value: dagql.String(name)}}, extra...),
	})
	return result, err
}

func gitLockNames(t *testing.T, lock *workspace.Lock) []string {
	t.Helper()
	var names []string
	for _, entry := range lock.Entries() {
		require.Equal(t, workspace.LockOperationGitSHA, entry.Operation)
		require.Len(t, entry.Inputs, 2)
		names = append(names, entry.Inputs[1].(string))
	}
	return names
}

func TestGitRefRevisionUsesLockedBase(t *testing.T) {
	pinned := strings.Repeat("a", 40)
	walked := strings.Repeat("b", 40)
	f := newGitRevisionFixture(t, nil, walked, nil)

	lock := workspace.NewLock()
	inputs, err := gitLockInputs(f.repo, "HEAD")
	require.NoError(t, err)
	require.NoError(t, lock.SetLookup(workspace.CoreLockNamespace, workspace.LockOperationGitSHA, inputs, pinned))
	ctx := withWorkspaceLookupLockOverride(f.ctx, lock)

	ref, err := f.ref(ctx, "HEAD~3")
	require.NoError(t, err)
	require.Equal(t, walked, ref.Self().Ref.SHA)
	require.Equal(t, walked, ref.Self().Ref.Name, "a revision resolves to a detached ref")

	require.Len(t, f.walks, 1)
	require.Equal(t, pinned, f.walks[0].base.SHA, "the base resolves through the HEAD pin, like head")
	require.Equal(t, "HEAD", f.walks[0].base.Name)
	require.Equal(t, []gitutil.RevisionStep{{Op: '~', N: 3}}, f.walks[0].rev.Steps)
	require.Nil(t, f.repo.Remote, "a pinned base must not list the remote")
	require.Equal(t, []string{"HEAD"}, gitLockNames(t, lock), "no entry for the full expression")
}

func TestGitRefRevisionWritesBaseLockOnly(t *testing.T) {
	head := strings.Repeat("c", 40)
	walked := strings.Repeat("d", 40)
	remote := &gitutil.Remote{
		Refs:    []*gitutil.Ref{{Name: "HEAD", SHA: head}, {Name: "refs/heads/main", SHA: head}},
		Symrefs: map[string]string{"HEAD": "refs/heads/main"},
	}

	for _, expr := range []string{"main~2", "main^2", "main~1^2~1"} {
		t.Run(expr, func(t *testing.T) {
			f := newGitRevisionFixture(t, remote, walked, nil)
			lock := workspace.NewLock()
			ctx := withWorkspaceLookupLockOverride(f.ctx, lock)

			ref, err := f.ref(ctx, expr)
			require.NoError(t, err)
			require.Equal(t, walked, ref.Self().Ref.SHA)
			require.Equal(t, walked, ref.Self().Ref.Name)
			require.Len(t, f.walks, 1)
			require.Equal(t, head, f.walks[0].base.SHA)
			require.Equal(t, "refs/heads/main", f.walks[0].base.Name)
			require.Equal(t, expr, f.walks[0].rev.Expr)

			require.Equal(t, []string{"main"}, gitLockNames(t, lock), "only the base is locked, as ref(name: \"main\") would be")
			inputs, err := gitLockInputs(f.repo, "main")
			require.NoError(t, err)
			value, ok := lock.GetLookup(workspace.CoreLockNamespace, workspace.LockOperationGitSHA, inputs)
			require.True(t, ok)
			require.Equal(t, head, value, "the base pin records the base commit, not the walked one")
		})
	}
}

func TestGitRefRevisionFullSHABase(t *testing.T) {
	base := strings.Repeat("e", 40)
	walked := strings.Repeat("f", 40)
	f := newGitRevisionFixture(t, nil, walked, nil)
	lock := workspace.NewLock()
	ctx := withWorkspaceLookupLockOverride(f.ctx, lock)

	ref, err := f.ref(ctx, base+"~2^2")
	require.NoError(t, err)
	require.Equal(t, walked, ref.Self().Ref.SHA)
	require.Len(t, f.walks, 1)
	require.Equal(t, base, f.walks[0].base.SHA)
	require.Nil(t, f.repo.Remote, "a full SHA base needs no remote listing")
	require.Empty(t, lock.Entries(), "a full SHA base is never locked")

	scope, err := gitLockScopedInput("name").Resolver(ctx, map[string]dagql.Input{"name": dagql.String(base + "~2^2")})
	require.NoError(t, err)
	require.Equal(t, dagql.NewString(""), scope, "a revision of a full SHA is shared across clients")
}

func TestGitRefRevisionPinnedCommit(t *testing.T) {
	pinned := strings.Repeat("1", 40)
	f := newGitRevisionFixture(t, nil, "", errors.New("must not walk"))

	ref, err := f.ref(f.ctx, "main~2", dagql.NamedInput{Name: "commit", Value: dagql.String(pinned)})
	require.NoError(t, err)
	require.Equal(t, pinned, ref.Self().Ref.SHA)
	require.Equal(t, pinned, ref.Self().Ref.Name)
	require.Empty(t, f.walks)
	require.Nil(t, f.repo.Remote)
}

func TestGitRefRevisionErrors(t *testing.T) {
	head := strings.Repeat("2", 40)
	remote := &gitutil.Remote{
		Refs:    []*gitutil.Ref{{Name: "HEAD", SHA: head}, {Name: "refs/heads/main", SHA: head}},
		Symrefs: map[string]string{"HEAD": "refs/heads/main"},
	}

	t.Run("unsupported syntax", func(t *testing.T) {
		for _, expr := range []string{"HEAD^{tree}", "main@{1}", "HEAD:go.mod", "main..HEAD", "HEAD^!", "HEAD~x"} {
			f := newGitRevisionFixture(t, remote, "", errors.New("must not walk"))
			lock := workspace.NewLock()
			_, err := f.ref(withWorkspaceLookupLockOverride(f.ctx, lock), expr)
			require.ErrorIs(t, err, gitutil.ErrInvalidRevision, expr)
			require.ErrorContains(t, err, "supported forms are", expr)
			require.Empty(t, f.walks)
			require.Empty(t, lock.Entries())
		}
	})

	t.Run("unknown base", func(t *testing.T) {
		f := newGitRevisionFixture(t, remote, "", errors.New("must not walk"))
		lock := workspace.NewLock()
		_, err := f.ref(withWorkspaceLookupLockOverride(f.ctx, lock), "nope~1")
		require.ErrorContains(t, err, `resolve "nope~1": repository does not contain ref "nope"`)
		require.Empty(t, f.walks)
		require.Empty(t, lock.Entries())
	})

	t.Run("walk failure", func(t *testing.T) {
		f := newGitRevisionFixture(t, remote, "", errors.New(`resolve "HEAD~9": HEAD~8 is a root commit`))
		_, err := f.ref(withWorkspaceLookupLockOverride(f.ctx, workspace.NewLock()), "HEAD~9")
		require.ErrorContains(t, err, `resolve "HEAD~9": HEAD~8 is a root commit`)
	})
}
