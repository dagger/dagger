package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
)

type workspaceRelationFixture struct {
	t     *testing.T
	ctx   context.Context
	cache *dagql.Cache
	srv   *dagql.Server
}

func newWorkspaceRelationFixture(t *testing.T) *workspaceRelationFixture {
	t.Helper()
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRepository]{}))
	return &workspaceRelationFixture{t: t, ctx: ctx, cache: cache, srv: srv}
}

// directory returns a cache-attached Directory result; the same op yields the
// same result (and ID), distinct ops distinct ones.
func (f *workspaceRelationFixture) directory(op string) dagql.ObjectResult[*Directory] {
	f.t.Helper()
	return volumeTestCachedObjectResult(f.t, f.ctx, f.cache, f.srv, "workspace-relation", op, &Directory{})
}

func (f *workspaceRelationFixture) gitRef(repoURL, name, sha string) dagql.Result[*GitRef] {
	f.t.Helper()
	repo := &GitRepository{URL: dagql.NonNull(dagql.String(repoURL))}
	repoRes, err := dagql.NewObjectResultForCall(repo, f.srv, volumeTestCall("repo:"+repoURL, repo))
	require.NoError(f.t, err)
	ref := &GitRef{Repo: repoRes, Ref: &gitutil.Ref{Name: name, SHA: sha}}
	refRes, err := dagql.NewResultForCall(ref, volumeTestCall("ref:"+name+"@"+sha, ref))
	require.NoError(f.t, err)
	return refRes
}

func localWorkspace(hostPath, clientID, userConfigKey string) *Workspace {
	ws := &Workspace{Address: "file://" + hostPath, ClientID: clientID}
	ws.SetHostPath(hostPath)
	ws.SetSource(NewWorkspaceSourceClientLocal(hostPath))
	ws.SetUserConfigKey(userConfigKey)
	return ws
}

func gitRefWorkspace(ref dagql.Result[*GitRef]) *Workspace {
	ws := &Workspace{Address: "git-ref://" + ref.Self().Ref.SHA}
	ws.SetSource(NewWorkspaceSourceGitRef(ref, false))
	return ws
}

func directoryWorkspace(root dagql.ObjectResult[*Directory], address string) *Workspace {
	ws := &Workspace{Address: address}
	ws.SetRootfs(root)
	ws.SetSource(NewWorkspaceSourceDirectory(root))
	return ws
}

// withOverlay stacks an (empty) overlay on the workspace's source, the shape
// an agent's own edits produce.
func withOverlay(ws *Workspace) *Workspace {
	cp := ws.Clone()
	cp.SetSource(NewWorkspaceSourceOverlay(ws.Source(), []string{"a.txt"}, nil, dagql.ObjectResult[*Changeset]{}))
	return cp
}

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestWorkspaceRelation(t *testing.T) {
	f := newWorkspaceRelationFixture(t)
	relation := func(prev, next *Workspace) WorkspaceRelationKind {
		return WorkspaceRelation(f.ctx, prev, next)
	}

	t.Run("nil is unrelated", func(t *testing.T) {
		ws := localWorkspace("/src/api", "c1", "github.com/acme/api")
		require.Equal(t, WorkspaceRelationUnrelated, relation(nil, ws))
		require.Equal(t, WorkspaceRelationUnrelated, relation(ws, nil))
	})

	t.Run("local checkout with its own edits is same base", func(t *testing.T) {
		ws := localWorkspace("/src/api", "c1", "github.com/acme/api")
		require.Equal(t, WorkspaceRelationSameBase, relation(ws, ws))
		require.Equal(t, WorkspaceRelationSameBase, relation(ws, withOverlay(ws)))
		require.Equal(t, WorkspaceRelationSameBase, relation(withOverlay(ws), ws))
		// cwd does not participate: it is a view on the same tree.
		moved := ws.Clone()
		moved.Cwd = "sub/dir"
		require.Equal(t, WorkspaceRelationSameBase, relation(ws, moved))
	})

	t.Run("same host path on another client is not the same base", func(t *testing.T) {
		a := localWorkspace("/src/api", "c1", "")
		b := localWorkspace("/src/api", "c2", "")
		require.Equal(t, WorkspaceRelationUnrelated, relation(a, b))
	})

	t.Run("two clones of one remote are same origin", func(t *testing.T) {
		a := localWorkspace("/src/api", "c1", "github.com/acme/api")
		b := localWorkspace("/tmp/api-copy", "c1", "github.com/acme/api")
		require.Equal(t, WorkspaceRelationSameOrigin, relation(a, b))
	})

	t.Run("unknown origins never match", func(t *testing.T) {
		a := localWorkspace("/src/api", "c1", "")
		b := localWorkspace("/tmp/api-copy", "c1", "")
		require.Equal(t, WorkspaceRelationUnrelated, relation(a, b))
	})

	t.Run("different remotes are unrelated", func(t *testing.T) {
		a := localWorkspace("/src/api", "c1", "github.com/acme/api")
		b := localWorkspace("/src/web", "c1", "github.com/acme/web")
		require.Equal(t, WorkspaceRelationUnrelated, relation(a, b))
	})

	t.Run("rootless and client-local checkouts of one path differ", func(t *testing.T) {
		a := localWorkspace("/src/api", "c1", "")
		b := &Workspace{ClientID: "c1"}
		b.SetHostPath("/src/api")
		b.SetSource(NewWorkspaceSourceRootlessLocal("/src/api"))
		require.Equal(t, WorkspaceRelationUnrelated, relation(a, b))
		require.Equal(t, WorkspaceRelationSameBase, relation(b, withOverlay(b)))
	})

	t.Run("git refs compare by repository and commit", func(t *testing.T) {
		main := gitRefWorkspace(f.gitRef("https://github.com/acme/api", "refs/heads/main", shaA))
		sameSpelledDifferently := gitRefWorkspace(f.gitRef("git@github.com:acme/api.git", "refs/heads/main", shaA))
		older := gitRefWorkspace(f.gitRef("https://github.com/acme/api", "refs/tags/v1.0.0", shaB))
		other := gitRefWorkspace(f.gitRef("https://github.com/acme/web", "refs/heads/main", shaA))

		require.Equal(t, WorkspaceRelationSameBase, relation(main, main))
		require.Equal(t, WorkspaceRelationSameBase, relation(main, withOverlay(main)))
		require.Equal(t, WorkspaceRelationSameBase, relation(main, sameSpelledDifferently))
		require.Equal(t, WorkspaceRelationSameOrigin, relation(main, older))
		require.Equal(t, WorkspaceRelationUnrelated, relation(main, other))
	})

	t.Run("local remotes compare verbatim rather than colliding", func(t *testing.T) {
		a := gitRefWorkspace(f.gitRef("/repos/api", "refs/heads/main", shaA))
		b := gitRefWorkspace(f.gitRef("/repos/web", "refs/heads/main", shaA))
		require.Equal(t, WorkspaceRelationUnrelated, relation(a, b))
		require.Equal(t, WorkspaceRelationSameBase, relation(a, withOverlay(a)))
	})

	t.Run("local checkout and remote ref of one repository are same origin", func(t *testing.T) {
		local := localWorkspace("/src/api", "c1", "github.com/acme/api")
		remote := gitRefWorkspace(f.gitRef("https://github.com/acme/api.git", "refs/heads/main", shaA))
		require.Equal(t, WorkspaceRelationSameOrigin, relation(local, remote))
		require.Equal(t, WorkspaceRelationSameOrigin, relation(remote, local))

		unknown := localWorkspace("/src/api", "c1", "")
		require.Equal(t, WorkspaceRelationUnrelated, relation(unknown, remote))
	})

	t.Run("directories compare by root", func(t *testing.T) {
		root := f.directory("root-a")
		a := directoryWorkspace(root, "directory://a")
		alsoA := directoryWorkspace(f.directory("root-a"), "directory://a")
		b := directoryWorkspace(f.directory("root-b"), "directory://b")
		require.Equal(t, WorkspaceRelationSameBase, relation(a, alsoA))
		require.Equal(t, WorkspaceRelationSameBase, relation(a, withOverlay(a)))
		require.Equal(t, WorkspaceRelationUnrelated, relation(a, b))
	})

	t.Run("directory and checkout are unrelated", func(t *testing.T) {
		dir := directoryWorkspace(f.directory("root-c"), "directory://c")
		local := localWorkspace("/src/api", "c1", "github.com/acme/api")
		remote := gitRefWorkspace(f.gitRef("https://github.com/acme/api", "refs/heads/main", shaA))
		require.Equal(t, WorkspaceRelationUnrelated, relation(dir, local))
		require.Equal(t, WorkspaceRelationUnrelated, relation(remote, dir))
	})
}

func TestWorkspaceIdentity(t *testing.T) {
	f := newWorkspaceRelationFixture(t)

	t.Run("git ref on a branch", func(t *testing.T) {
		ws := gitRefWorkspace(f.gitRef("https://github.com/acme/api.git", "refs/heads/main", shaA))
		id := DescribeWorkspace(ws)
		require.Equal(t, "github.com/acme/api", id.Origin)
		require.Equal(t, "https://github.com/acme/api.git", id.Repo)
		require.Equal(t, shaA, id.SHA)
		require.Equal(t, "main", id.Ref)
		require.Equal(t, "github.com/acme/api", id.Location())
		require.Equal(t, "@"+shaA+" (main)", id.Revision())
		require.Equal(t, "github.com/acme/api @"+shaA+" (main) [git-ref://"+shaA+"]", id.String())
	})

	t.Run("git ref by bare commit has no ref name", func(t *testing.T) {
		ws := gitRefWorkspace(f.gitRef("https://github.com/acme/api", shaA, shaA))
		id := DescribeWorkspace(ws)
		require.Empty(t, id.Ref)
		require.Equal(t, "@"+shaA, id.Revision())
	})

	t.Run("local checkout carries its address and origin, no revision", func(t *testing.T) {
		id := DescribeWorkspace(localWorkspace("/src/api", "c1", "github.com/acme/api"))
		require.Equal(t, "github.com/acme/api", id.Location())
		require.Empty(t, id.Revision())
		require.Equal(t, "github.com/acme/api [file:///src/api]", id.String())

		head := f.gitRef("https://github.com/acme/api", "refs/heads/feature", shaB).Self()
		require.Equal(t, "github.com/acme/api @"+shaB+" (feature) [file:///src/api]", id.WithGitRef(head).String())
	})

	t.Run("local checkout without origin falls back to its address", func(t *testing.T) {
		id := DescribeWorkspace(localWorkspace("/src/api", "c1", ""))
		require.Equal(t, "file:///src/api", id.Location())
		require.Equal(t, "file:///src/api", id.String())
	})

	t.Run("synthetic directory", func(t *testing.T) {
		id := DescribeWorkspace(directoryWorkspace(f.directory("root-d"), "directory://sha256:abc"))
		require.Equal(t, "directory://sha256:abc", id.String())
	})

	t.Run("nil and empty", func(t *testing.T) {
		require.Equal(t, "(unknown)", DescribeWorkspace(nil).String())
		require.Equal(t, WorkspaceIdentity{}, DescribeWorkspace(nil))
	})

	t.Run("origin resolves through overlays", func(t *testing.T) {
		ws := withOverlay(gitRefWorkspace(f.gitRef("ssh://git@github.com/acme/api", "refs/heads/main", shaA)))
		require.Equal(t, "github.com/acme/api", WorkspaceOrigin(ws))
		require.Equal(t, "github.com/acme/api", WorkspaceOrigin(withOverlay(localWorkspace("/src/api", "c1", "github.com/acme/api"))))
		require.Empty(t, WorkspaceOrigin(nil))
	})
}
