package core

// These tests cover how the current workspace is identified in the cache: a
// workspace that holds its own content (a Git ref) is identified by it, so
// functions that take it hit across sessions; a host checkout is read live
// and keeps a key per call.
//
// See also:
// - engine_persistence_test.go: the same workspace across an engine restart.
// - workspace_content_keyed_test.go: content-keyed reads from a workspace.

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// workspaceIdentityRepo is a workspace whose main module, probe, takes the
// workspace in its constructor and in a check, and whose config includes
// another, so reading the effective config goes through the workspace's
// client.
func workspaceIdentityRepo(t testing.TB, c *dagger.Client) *core.Directory {
	t.Helper()
	return core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", `[modules.probe]
source = ".dagger/modules/probe"
main = true

[[include]]
source = "./base"
`).
		WithNewFile("base/dagger.toml", `ignore = ["included-ignore"]
`).
		WithDirectory(".dagger/modules/probe",
			core.NewQuery(c).Host().Directory(testDataPath(t, "modules", "go", "workspace-identity")))
}

// workspaceIdentityCall runs dagger call in a new session each time, so
// nothing but the cache connects one call to the next.
func workspaceIdentityCall(ctx context.Context, t *testctx.T, ctr *core.Container, args ...string) string {
	t.Helper()
	return workspaceIdentityExec(ctx, t, ctr, true, append([]string{"call"}, args...)...)
}

// workspaceIdentityExec runs the dagger CLI as a new client, in a new session
// or as another client of the test's session.
func workspaceIdentityExec(ctx context.Context, t *testctx.T, ctr *core.Container, newSession bool, args ...string) string {
	t.Helper()
	out, err := ctr.
		WithEnvVariable("CACHE_BUST", rand.Text()).
		WithExec(append([]string{"dagger", "--progress=report"}, args...), core.ContainerWithExecOpts{
			UseEntrypoint:            true,
			DaggerInDaggerNewSession: newSession,
		}).
		Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

// workspaceIdentityExtraModule is a module, with a check so artifact
// discovery lists it, that a client can load beside the workspace's own with
// -m.
func workspaceIdentityExtraModule(c *dagger.Client, name, typeName string) *core.Directory {
	return core.NewQuery(c).Directory().
		WithNewFile("dagger.json", `{"name":"`+name+`","sdk":{"source":"dang"}}`).
		WithNewFile("main.dang", `
type `+typeName+` {
  pub verify: Void @check {
    null
  }
}
`)
}

func (WorkspaceSuite) TestGitWorkspaceIdentity(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := core.NewQuery(c).Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/empty")
	// Each subtest serves its own content, so subtests running at the same
	// time can't produce or reuse each other's results.
	remote := func(ctx context.Context, t *testctx.T) string {
		return workspaceSelectionRemoteRef(ctx, t, c,
			workspaceIdentityRepo(t, c).WithNewFile("nonce.txt", rand.Text()))
	}

	t.Run("a check on the same commit is a cache hit in a new session", func(ctx context.Context, t *testctx.T) {
		remoteRef := remote(ctx, t)
		first := workspaceIdentityCall(ctx, t, ctr, "-W", remoteRef, "check")
		second := workspaceIdentityCall(ctx, t, ctr, "-W", remoteRef, "check")
		require.Equal(t, first, second, "a check given the same Git workspace must not run again")
	})

	// The object constructed with the workspace is reused from another
	// session's call, so it holds a workspace loaded by a client that this
	// session doesn't have. Reading the effective config needs a client.
	t.Run("an object holding the workspace works in a session that reuses it", func(ctx context.Context, t *testctx.T) {
		remoteRef := remote(ctx, t)
		workspaceIdentityCall(ctx, t, ctr, "-W", remoteRef, "check")
		out := workspaceIdentityCall(ctx, t, ctr, "-W", remoteRef, "config", "--nonce", rand.Text())
		require.Contains(t, out, "included-ignore")
	})

	// Another client of the same session reuses the object, whose workspace
	// was loaded by a client that has since exited. Artifact discovery loads
	// modules through a client the caller can use.
	t.Run("a sibling client lists artifacts through a reused object", func(ctx context.Context, t *testctx.T) {
		remoteRef := remote(ctx, t)
		workspaceIdentityExec(ctx, t, ctr, false, "call", "-W", remoteRef, "check")
		out := workspaceIdentityExec(ctx, t, ctr, false, "call", "-W", remoteRef, "artifact-modules", "--nonce", rand.Text())
		require.Contains(t, out, "probe")
	})

	// Artifact discovery covers the modules a client loads beside the
	// workspace's own, which aren't part of the workspace's content. The two
	// clients share the test's session, so the first list is still live when
	// the second client asks.
	t.Run("clients loading different modules list their own artifacts", func(ctx context.Context, t *testctx.T) {
		remoteRef := remote(ctx, t)
		const query = `{currentWorkspace{artifacts{modules{name}}}}`
		withExtras := ctr.
			WithDirectory("/extra-a", workspaceIdentityExtraModule(c, "extra-a", "ExtraA")).
			WithDirectory("/extra-b", workspaceIdentityExtraModule(c, "extra-b", "ExtraB")).
			WithNewFile("/query.graphql", query)
		listA := workspaceIdentityExec(ctx, t, withExtras, false,
			"query", "--doc", "/query.graphql", "-W", remoteRef, "-m", "/extra-a")
		listB := workspaceIdentityExec(ctx, t, withExtras, false,
			"query", "--doc", "/query.graphql", "-W", remoteRef, "-m", "/extra-b")
		require.Contains(t, listA, "extra-a")
		require.NotContains(t, listA, "extra-b")
		require.Contains(t, listB, "extra-b")
		require.NotContains(t, listB, "extra-a")
	})

	// The check itself, not only a function, is reused: a check's result is
	// cached across sessions, and the workspace it takes has the same key.
	t.Run("a check given the same Git workspace runs once across sessions", func(ctx context.Context, t *testctx.T) {
		remoteRef := remote(ctx, t)
		workspaceIdentityExec(ctx, t, ctr, true, "check", "-W", remoteRef, "recorded")
		workspaceIdentityExec(ctx, t, ctr, true, "check", "-W", remoteRef, "recorded")
		require.Equal(t, "1", workspaceIdentityCall(ctx, t, ctr, "-W", remoteRef, "runs"))
	})

	t.Run("a check on other content runs again", func(ctx context.Context, t *testctx.T) {
		require.NotEqual(t,
			workspaceIdentityCall(ctx, t, ctr, "-W", remote(ctx, t), "check"),
			workspaceIdentityCall(ctx, t, ctr, "-W", remote(ctx, t), "check"))
	})
}

// TestLiveWorkspaceKeyedPerCall checks that a workspace read live from the
// host keeps a key per call: its files can change between calls without the
// workspace changing.
func (WorkspaceSuite) TestLiveWorkspaceKeyedPerCall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("host checkout", func(ctx context.Context, t *testctx.T) {
		ctr := goGitBase(t, c).
			WithDirectory("/work", workspaceIdentityRepo(t, c)).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"})
		require.NotEqual(t,
			workspaceIdentityCall(ctx, t, ctr, "check"),
			workspaceIdentityCall(ctx, t, ctr, "check"))
	})

	t.Run("rootless", func(ctx context.Context, t *testctx.T) {
		ctr := core.NewQuery(c).Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory("/mod", core.NewQuery(c).Host().Directory(testDataPath(t, "modules", "go", "workspace-identity"))).
			WithWorkdir("/empty")
		require.NotEqual(t,
			workspaceIdentityCall(ctx, t, ctr, "-m", "/mod", "check"),
			workspaceIdentityCall(ctx, t, ctr, "-m", "/mod", "check"))
	})
}
