package core

// These tests cover resuming a saved LLM session in a session other than the
// one that saved it.
//
// They are SKIPPED. They were written to pin down the zombie-client class of
// resume failures: a saved recipe records the `currentWorkspace` call that
// bound the workspace, and on resume that call can replay against the cache
// and hit a value pinned to the saving session's now-dead client — surfacing
// as "failed to retrieve session main client: client ... not found", or as the
// dead session's stale snapshot of the tree. A per-client session-resource
// handle on the workspace used to filter those values and force live
// re-resolution; it was dropped deliberately, as a point fix for one instance
// of a broader class.
//
// As written, these three scenarios pass — the plain save/exit/resume path
// re-resolves correctly without the handle. They are kept as the skeleton for
// the coverage that is actually owed, and skipped so they neither claim to
// verify the class nor rot.
//
// TODO: extend these to the situations that plausibly do break, and unskip as
// each is made to work:
//   - a session saved with workspace overlay edits still pending (not
//     exported), so the resumed recipe must replay patches against a tree it
//     no longer owns
//   - the same, but exported before saving, so the patches are already applied
//     on disk and must degrade to conflict markers rather than re-apply
//   - an installed tool that started a Service still running at save time, so
//     the resumed session references a service bound to the dead client
//   - a module function returning a bound LLM, resumed from a different
//     session (the shape the dead-client bug was first seen in)
//   - other state reachable from a saved recipe but owned by the saving
//     client: secrets, sockets, host tunnels
//
// See internal-docs/session_resources.md for the session-resource rule these
// interact with.
//
// See also:
// - llm_test.go: the reset/save family these build on.
// - engine_persistence_test.go: the engine-restart harness idioms.

import (
	"context"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

// skipPendingResumeCoverage skips a resume scenario that passes as written but
// does not yet cover the state that makes resume interesting. See the TODO at
// the top of this file.
func skipPendingResumeCoverage(t *testctx.T) {
	t.Helper()
	t.Skip("pending: extend to overlays, running services, and other dead-client state")
}

// savedSessionConversation builds the canned conversation the resume tests
// save and reload: a bound workspace, one host read, no real model traffic.
func savedSessionConversation(c *dagger.Client, contents string) *dagger.LLM {
	return c.LLM().
		WithWorkspace(c.CurrentWorkspace()).
		WithModel("openai/gpt-4o").
		WithSystemPrompt("be helpful").
		WithPrompt("read x.txt").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "reading x.txt"},
			{
				Kind:      dagger.LLMContentBlockKindToolCall,
				CallID:    "call_1",
				ToolName:  "read",
				Arguments: dagger.JSON(`{"path":"x.txt"}`),
			},
		}).
		WithToolResult("call_1", contents, false)
}

// TestResumeHostReadsInNewSession covers the basic save/exit/resume flow:
// session A binds its workspace, converses, saves, and disconnects; session B
// connects fresh against the same engine and workdir and loads the saved ID.
// Host reads through the resumed LLM must work and must observe session B's
// live tree.
//
// Passes as written; skipped pending the coverage described at the top of
// this file.
func (LLMSuite) TestResumeHostReadsInNewSession(ctx context.Context, t *testctx.T) {
	skipPendingResumeCoverage(t)

	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("ORIGINAL"), 0o644))

	// Session A.
	cA := connect(ctx, t, dagger.WithWorkdir(workdir))
	llmA := savedSessionConversation(cA, "ORIGINAL")

	// The host read works in the session that owns the workspace.
	beforeSave, err := llmA.Workspace().File("x.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "ORIGINAL", beforeSave)

	// Save, mirroring ctrl+s: reset re-emits a flat recipe and drops the
	// workspace binding, so rebind the live workspace before persisting.
	savedID, err := llmA.WithResetWorkspace().
		WithWorkspace(cA.CurrentWorkspace()).
		PortableID(ctx)
	require.NoError(t, err)

	// Exit session A.
	require.NoError(t, cA.Close())

	// Session B: fresh connect, same engine, same workdir.
	cB := connect(ctx, t, dagger.WithWorkdir(workdir))
	resumed := dagger.Ref[*dagger.LLM](cB, savedID)

	// The conversation survives the reload.
	reply, err := resumed.LastReply(ctx)
	require.NoError(t, err)
	require.Equal(t, "reading x.txt", reply)

	// The workspace must re-resolve against session B rather than replaying
	// session A's dead binding.
	afterResume, err := resumed.Workspace().File("x.txt").Contents(ctx)
	require.NoError(t, err,
		"resumed session must re-resolve its own workspace instead of reaching "+
			"into the saving session's dead client")
	require.Equal(t, "ORIGINAL", afterResume)
}

// TestResumeSeesEditedHostFiles covers the staleness half of the same bug:
// even when the resumed workspace resolves without erroring, it must not serve
// the snapshot the saving session cached. Session A primes its per-client host
// read cache, saves and exits; the file is then edited on disk; session B must
// observe the edit.
//
// Passes as written; skipped pending the coverage described at the top of
// this file.
func (LLMSuite) TestResumeSeesEditedHostFiles(ctx context.Context, t *testctx.T) {
	skipPendingResumeCoverage(t)

	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("ORIGINAL"), 0o644))

	// Session A primes the per-client host read cache with the original
	// contents, exactly as an agent reading a file before editing it would.
	cA := connect(ctx, t, dagger.WithWorkdir(workdir))
	llmA := savedSessionConversation(cA, "ORIGINAL")

	primed, err := llmA.Workspace().File("x.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "ORIGINAL", primed)

	savedID, err := llmA.WithResetWorkspace().
		WithWorkspace(cA.CurrentWorkspace()).
		PortableID(ctx)
	require.NoError(t, err)
	require.NoError(t, cA.Close())

	// The tree moves on while no session holds it.
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("EDITED"), 0o644))

	// Session B must read through to the edited file, not session A's pinned
	// snapshot.
	cB := connect(ctx, t, dagger.WithWorkdir(workdir))
	resumed := dagger.Ref[*dagger.LLM](cB, savedID)

	afterResume, err := resumed.Workspace().File("x.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "EDITED", afterResume,
		"a resumed session must read the live tree, not the snapshot the "+
			"saving session cached for its own client")
}

// TestResumeAcrossEngineRestart drives the same flow through the persistence
// cache across an engine stop/start, so the resume path is exercised where the
// saved recipe can only come back from disk. Uses the containerized dev-engine
// harness so the engine can be restarted under a stable state key.
//
// Passes as written; skipped pending the coverage described at the top of
// this file.
func (LLMSuite) TestResumeAcrossEngineRestart(ctx context.Context, t *testctx.T) {
	skipPendingResumeCoverage(t)

	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("ORIGINAL"), 0o644))

	c := connect(ctx, t)
	stateKey := "llm-resume-restart-state-" + identity.NewID()

	startEngine := func() (*dagger.Service, *dagger.Service, *dagger.Client) {
		t.Helper()
		engineCtr := devEngineContainerWithStateKey(c, stateKey)
		upstreamSvc := devEngineContainerAsService(engineCtr)
		engineSvc, err := c.Host().Tunnel(upstreamSvc).Start(ctx)
		require.NoError(t, err)
		endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		engineClient, err := dagger.Connect(ctx,
			dagger.WithRunnerHost(endpoint),
			dagger.WithWorkdir(workdir),
			dagger.WithLogOutput(testutil.NewTWriter(t)),
		)
		require.NoError(t, err)
		return upstreamSvc, engineSvc, engineClient
	}

	stopEngine := func(upstreamSvc, engineSvc *dagger.Service, engineClient *dagger.Client) {
		t.Helper()
		if engineClient != nil {
			require.NoError(t, engineClient.Close())
		}
		if upstreamSvc != nil {
			_, err := upstreamSvc.Stop(ctx)
			require.NoError(t, err)
		}
		if engineSvc != nil {
			_, err := engineSvc.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
			require.NoError(t, err)
		}
	}

	// Session A, on the first engine boot.
	upstreamA, svcA, clientA := startEngine()
	llmA := savedSessionConversation(clientA, "ORIGINAL")

	beforeSave, err := llmA.Workspace().File("x.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "ORIGINAL", beforeSave)

	savedID, err := llmA.WithResetWorkspace().
		WithWorkspace(clientA.CurrentWorkspace()).
		PortableID(ctx)
	require.NoError(t, err)

	stopEngine(upstreamA, svcA, clientA)

	// Session B, after the engine comes back on the same state.
	upstreamB, svcB, clientB := startEngine()
	defer stopEngine(upstreamB, svcB, clientB)

	resumed := dagger.Ref[*dagger.LLM](clientB, savedID)

	reply, err := resumed.LastReply(ctx)
	require.NoError(t, err)
	require.Equal(t, "reading x.txt", reply)

	afterResume, err := resumed.Workspace().File("x.txt").Contents(ctx)
	require.NoError(t, err,
		"a session resumed after an engine restart must re-resolve its own "+
			"workspace from the persistence cache, not the saved session's client")
	require.Equal(t, "ORIGINAL", afterResume)
}
