package core

// These tests cover resuming a saved LLM session in a session other than the
// one that saved it.
//
// # The single root cause
//
// Every failure in this file has one cause: the recipe-load path serves a
// recorded call from the dagql cache instead of re-executing it.
//
// `recipeLoadState.loadRecipeVertex` (dagql/server.go) does its digest lookup
// BEFORE loading any of the call's inputs, so a hit short-circuits the entire
// subtree beneath that node. And the digest is reproducible across sessions:
// `loadedResultCallFromRecipeID` replays the recorded implicit inputs verbatim
// rather than re-resolving them, so a replayed call reproduces the exact digest
// the saving session minted.
//
// That is fine for pure data. It is wrong for the two impure things a saved
// session's recipe reaches through — a client-bound workspace and a host read —
// because their recorded digest is a stable key for an unstable value.
//
// Verified by experiment: disabling both cache lookups in loadRecipeVertex
// turns this file from 5/20 passing to 20/20.
//
// # Failure family 1: the workspace's client ID
//
// A saved recipe records the `currentWorkspace` call that bound the workspace.
// The resulting `*core.Workspace` carries `ClientID` — the client that built it
// (engine/server/session_workspaces.go). Client IDs only resolve within their
// own session: `SpecificClientMetadata` looks the ID up via
// `clientFromIDs(currentSessionID, clientID)`. So a replayed workspace carrying
// another session's client can never be routed, and every consumer that
// switches into the workspace's owning client fails with:
//
//	workspace client metadata: failed to retrieve session main client:
//	client "..." not found
//
// This family is liveness-sensitive. `Cache.ReleaseSession` decrements
// ownership on teardown and collects results that drop to zero owners, and the
// `currentWorkspace` result has no persisted edge — so a save / exit / resume
// with nothing else running garbage-collects it and re-resolves live, passing
// for a reason that has nothing to do with correctness. Keep any session alive
// that still owns it — most naturally the saving session, which is exactly what
// autosave-then-resume-in-another-terminal looks like — and it fails.
//
// That is why every scenario runs over [resumeArrangements] rather than
// hardcoding one teardown order. `saving-session-exited` is the direction that
// has always passed; `saving-session-alive` is the one that carries the
// coverage.
//
// # Failure family 2: the overlay's recorded base
//
// A recorded overlay is `before.withPatch(patch, LEAVE_CONFLICT_MARKERS)`,
// where `before` is the workspace directory read the tool ran against, wrapped
// as `.changes(from: before)` by core.MCP.applyChangeset. The design intent is
// that `before` re-resolves against the live tree on resume, so a patch whose
// hunks no longer fit degrades to conflict markers — that is what
// LEAVE_CONFLICT_MARKERS is recorded for, and what the CLI's conflictMarkerCue
// reports to the model.
//
// Instead the whole `changes` node hits on its recipe digest (route=recipe),
// short-circuiting `withPatch`, `workspace.directory` and the `host.directory`
// beneath it. Replay reproduces the saving session's snapshot-plus-patch, which
// `Directory.withChanges` then applies to the live tree as a structural diff
// layer — a file-level overwrite, with no merge and no conflict detection. An
// out-of-band host edit is silently clobbered even when it touches lines the
// patch never goes near, and because the patch never meets changed content,
// conflict markers never arise at all.
//
// This family is NOT liveness-sensitive: the changeset result survives session
// teardown (observed in-memory with owners=1, not imported, no persisted edge),
// so both arrangements fail.
//
// Note the contrast that isolates it: plain workspace reads DO re-resolve live,
// because a resumed session's own `Workspace.file` call is minted fresh rather
// than replayed. It is specifically the recorded base underneath the overlay
// that is frozen.
//
// # Hazard when extending these tests
//
// Give each scenario distinctive file contents. Directories are
// content-addressed, so two tests that write byte-identical trees can share
// cache entries across temp workdirs and mask or manufacture failures. This bit
// during development: with the load cache disabled, an identical-content
// neighbour made the conflict-marker scenario fail on its own.
//
// # What is still owed
//
// TODO: extend to the rest of the state a saved recipe can reach but not own:
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

// resumeArrangement describes what happens to the saving session between the
// save and the resume. See the liveness note at the top of this file: this is
// the variable that decides whether the saved recipe's client-bound values are
// still in the cache to be replayed onto.
type resumeArrangement struct {
	name string
	// keepSavingSessionAlive leaves session A connected across the resume, so
	// it still owns the saved recipe's results and they survive in the cache.
	keepSavingSessionAlive bool
}

func resumeArrangements() []resumeArrangement {
	return []resumeArrangement{
		// Nothing else owns the saved recipe's results, so session teardown
		// collects them and the resume re-resolves from scratch. This is the
		// easy direction, and the one that has always passed.
		{name: "saving-session-exited"},
		// The saving session still owns them, so the resume replays onto its
		// cached values. This is the arrangement that reproduces.
		{name: "saving-session-alive", keepSavingSessionAlive: true},
	}
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

// savePolicy selects which of the CLI's two save shapes a scenario exercises.
// They differ in whether pending workspace edits are meant to survive, so a
// resume test has to say which one it means.
type savePolicy int

const (
	// saveAfterReset mirrors ctrl+s (export + reset): the recipe is re-emitted
	// flat and rebound to the live workspace, deliberately dropping the
	// accumulated overlay because its edits are already on disk.
	saveAfterReset savePolicy = iota
	// saveAutosave mirrors LLMSession.AutoSaveSession: a plain portableID of
	// the conversation as it stands, overlay and all. Pending edits are
	// expected to come back on resume.
	saveAutosave
)

// saveAndResume runs the save/resume dance under one arrangement: it stands up
// session A in workdir, saves the conversation the caller builds under the
// given policy, applies the arrangement's teardown, then connects session B
// against the same engine and workdir and hands the caller the resumed LLM.
func saveAndResume(
	ctx context.Context,
	t *testctx.T,
	arrangement resumeArrangement,
	policy savePolicy,
	workdir string,
	build func(cA *dagger.Client) *dagger.LLM,
	primeA func(llmA *dagger.LLM),
) *dagger.LLM {
	t.Helper()

	cA := connect(ctx, t, dagger.WithWorkdir(workdir))
	llmA := build(cA)
	if primeA != nil {
		primeA(llmA)
	}

	toSave := llmA
	if policy == saveAfterReset {
		toSave = llmA.WithResetWorkspace().WithWorkspace(cA.CurrentWorkspace())
	}
	savedID, err := toSave.PortableID(ctx)
	require.NoError(t, err)

	// When kept alive, connect's own t.Cleanup closes session A at the end of
	// the test — after the resumed session has done its work.
	if !arrangement.keepSavingSessionAlive {
		require.NoError(t, cA.Close())
	}

	cB := connect(ctx, t, dagger.WithWorkdir(workdir))
	return dagger.Ref[*dagger.LLM](cB, savedID)
}

// overlayToolEdit mirrors what core.MCP.applyChangeset does when a
// workspace-mutating tool returns a Changeset: normalize the changeset to pure
// patch data, then overlay it onto the bound workspace with withChanges.
//
// The normalization is what makes a saved overlay replayable at all. A
// tool-built changeset's After is an operation chain rooted at live host reads
// (File.withReplaced and friends); replaying that chain once the files have
// moved on fails with "search string not found", or silently re-applies when
// they haven't. Capturing the patch while the content it ran against is still
// known turns the recorded overlay into data, and its replay into a tolerant
// application: hunks that fit apply, hunks that don't leave conflict markers.
//
// Tests go through this rather than calling withChanges directly, so they
// exercise the shape a real tool call records rather than one no agent
// produces.
func overlayToolEdit(
	ctx context.Context,
	t *testctx.T,
	llm *dagger.LLM,
	before, after *dagger.Directory,
) *dagger.LLM {
	t.Helper()

	patch, err := after.Changes(before).AsPatch().Contents(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, patch, "tool edit produced no patch")

	normalized := before.
		WithPatch(patch, dagger.DirectoryWithPatchOpts{
			OnConflict: dagger.PatchConflictLeaveConflictMarkers,
		}).
		Changes(before)

	return llm.WithWorkspace(llm.Workspace().WithChanges(normalized))
}

// editorWrite emulates github.com/vito/editor's write() tool: a whole-file
// create or replace, recorded onto the LLM's workspace the way a tool call
// would be.
func editorWrite(ctx context.Context, t *testctx.T, llm *dagger.LLM, path, contents string) *dagger.LLM {
	t.Helper()
	before := llm.Workspace().Directory(".")
	return overlayToolEdit(ctx, t, llm, before, before.WithNewFile(path, contents))
}

// editorEdit emulates editor's edit() tool: an exact-string replacement,
// expressed as the File.withReplaced operation chain the real tool builds.
func editorEdit(ctx context.Context, t *testctx.T, llm *dagger.LLM, path, oldText, newText string) *dagger.LLM {
	t.Helper()
	before := llm.Workspace().Directory(".")
	after := before.WithFile(path, before.File(path).WithReplaced(oldText, newText))
	return overlayToolEdit(ctx, t, llm, before, after)
}

// conflictedFiles reports the workspace files carrying restore-time conflict
// markers, by the same search the CLI's conflictMarkerCue runs to decide what
// to tell the model (internal/cmd/dagger/llm.go). This is the "visible to the
// LLM" half: markers that no search surfaces are markers the agent never
// learns about.
func conflictedFiles(ctx context.Context, t *testctx.T, llm *dagger.LLM) []string {
	t.Helper()

	changes := llm.Workspace().Changes()
	added, err := changes.AddedPaths(ctx)
	require.NoError(t, err)
	modified, err := changes.ModifiedPaths(ctx)
	require.NoError(t, err)
	paths := append(append([]string{}, added...), modified...)
	if len(paths) == 0 {
		return nil
	}

	results, err := changes.After().Search(ctx, "<<<<<<< workspace", dagger.DirectorySearchOpts{
		Literal:   true,
		FilesOnly: true,
		Paths:     paths,
	})
	require.NoError(t, err)

	var files []string
	seen := map[string]bool{}
	for _, res := range results {
		fp, err := res.FilePath(ctx)
		require.NoError(t, err)
		if seen[fp] {
			continue
		}
		seen[fp] = true
		files = append(files, fp)
	}
	return files
}

// TestResumeKeepsPendingEdits covers the autosave shape: a session with
// unexported workspace edits — a write() and an edit(), as the editor tools
// produce them — is saved mid-flight and resumed. The overlay must come back,
// because the conversation above it describes a workspace that only exists as
// those pending edits. Losing them silently strands the model in a tree that
// contradicts its own history.
func (LLMSuite) TestResumeKeepsPendingEdits(ctx context.Context, t *testctx.T) {
	for _, arrangement := range resumeArrangements() {
		t.Run(arrangement.name, func(ctx context.Context, t *testctx.T) {
			workdir := t.TempDir()
			initGitRepo(ctx, t, workdir)
			require.NoError(t, os.WriteFile(filepath.Join(workdir, "a.txt"),
				[]byte("keep-pending one\nORIGINAL\nkeep-pending three\n"), 0o644))

			resumed := saveAndResume(ctx, t, arrangement, saveAutosave, workdir,
				func(cA *dagger.Client) *dagger.LLM {
					llmA := savedSessionConversation(cA, "keep-pending one\nORIGINAL\nkeep-pending three\n")
					// edit(): replace a line in an existing file.
					llmA = editorEdit(ctx, t, llmA, "a.txt", "ORIGINAL", "EDITED")
					// write(): create a new file.
					llmA = editorWrite(ctx, t, llmA, "b.txt", "keep-pending BRAND NEW\n")
					return llmA
				},
				func(llmA *dagger.LLM) {
					// Both edits are visible in the saving session.
					contents, err := llmA.Workspace().File("a.txt").Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, "keep-pending one\nEDITED\nkeep-pending three\n", contents)

					created, err := llmA.Workspace().File("b.txt").Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, "keep-pending BRAND NEW\n", created)
				})

			// The edit survives the resume.
			contents, err := resumed.Workspace().File("a.txt").Contents(ctx)
			require.NoError(t, err,
				"a resumed session must be able to read its own pending edits")
			require.Equal(t, "keep-pending one\nEDITED\nkeep-pending three\n", contents,
				"the pending edit() must replay onto the resumed workspace")

			created, err := resumed.Workspace().File("b.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "keep-pending BRAND NEW\n", created,
				"the pending write() must replay onto the resumed workspace")

			// And they are still reported as pending, so the CLI keeps
			// showing them as unsaved changes rather than as already
			// exported.
			changes := resumed.Workspace().Changes()
			modified, err := changes.ModifiedPaths(ctx)
			require.NoError(t, err)
			require.Contains(t, modified, "a.txt")
			added, err := changes.AddedPaths(ctx)
			require.NoError(t, err)
			require.Contains(t, added, "b.txt")

			// Nothing conflicted: the host tree never moved.
			require.Empty(t, conflictedFiles(ctx, t, resumed),
				"a clean replay must not leave conflict markers")
		})
	}
}

// TestResumeLeavesConflictMarkersForOutOfBandEdits covers the case the
// normalization exists for: the host file moved underneath a pending edit
// while the session was away. The recorded patch no longer applies cleanly, so
// replay must degrade to conflict markers rather than failing the load or
// silently clobbering the out-of-band change — and those markers must be
// findable by the search the CLI uses to tell the model what to resolve.
func (LLMSuite) TestResumeLeavesConflictMarkersForOutOfBandEdits(ctx context.Context, t *testctx.T) {
	for _, arrangement := range resumeArrangements() {
		t.Run(arrangement.name, func(ctx context.Context, t *testctx.T) {
			workdir := t.TempDir()
			initGitRepo(ctx, t, workdir)
			path := filepath.Join(workdir, "a.txt")
			require.NoError(t, os.WriteFile(path,
				[]byte("conflict one\nORIGINAL\nconflict three\n"), 0o644))

			resumed := saveAndResume(ctx, t, arrangement, saveAutosave, workdir,
				func(cA *dagger.Client) *dagger.LLM {
					llmA := savedSessionConversation(cA, "conflict one\nORIGINAL\nconflict three\n")
					return editorEdit(ctx, t, llmA, "a.txt", "ORIGINAL", "EDITED")
				},
				func(llmA *dagger.LLM) {
					contents, err := llmA.Workspace().File("a.txt").Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, "conflict one\nEDITED\nconflict three\n", contents)

					// Someone else edits the same region on the host while
					// the session is away.
					require.NoError(t, os.WriteFile(path,
						[]byte("conflict one\nCHANGED ON HOST\nconflict three\n"), 0o644))
				})

			contents, err := resumed.Workspace().File("a.txt").Contents(ctx)
			require.NoError(t, err,
				"a patch that no longer applies must degrade to conflict markers, "+
					"not fail the resume")
			require.Contains(t, contents, "<<<<<<< workspace",
				"the out-of-band host content must be preserved behind a conflict marker")
			require.Contains(t, contents, ">>>>>>> patch",
				"the pending edit must be preserved behind a conflict marker")
			require.Contains(t, contents, "CHANGED ON HOST",
				"the resumed workspace must not clobber the out-of-band change")
			require.Contains(t, contents, "EDITED",
				"the resumed workspace must not silently drop the pending edit")

			// The markers must be discoverable the way the CLI discovers them,
			// or the model is never told to resolve them.
			require.Equal(t, []string{"a.txt"}, conflictedFiles(ctx, t, resumed),
				"conflict markers must be findable by the search that builds the "+
					"model's restore-time cue")
		})
	}
}

// TestResumeAppliesNonConflictingOutOfBandEdits is the other half of the
// tolerance contract: when the host moved somewhere the pending edit does not
// touch, replay must apply cleanly and keep both changes. Degrading to
// conflict markers here would train the model to expect them, and make the
// signal from a real conflict worthless.
func (LLMSuite) TestResumeAppliesNonConflictingOutOfBandEdits(ctx context.Context, t *testctx.T) {
	for _, arrangement := range resumeArrangements() {
		t.Run(arrangement.name, func(ctx context.Context, t *testctx.T) {
			workdir := t.TempDir()
			initGitRepo(ctx, t, workdir)
			path := filepath.Join(workdir, "a.txt")
			require.NoError(t, os.WriteFile(path,
				[]byte("first\nsecond\nthird\nfourth\nfifth\nsixth\nseventh\neighth\nninth\n"), 0o644))

			resumed := saveAndResume(ctx, t, arrangement, saveAutosave, workdir,
				func(cA *dagger.Client) *dagger.LLM {
					llmA := savedSessionConversation(cA, "unused")
					return editorEdit(ctx, t, llmA, "a.txt", "second", "SECOND EDITED")
				},
				func(llmA *dagger.LLM) {
					// Far enough from the edit that the hunks do not overlap.
					require.NoError(t, os.WriteFile(path,
						[]byte("first\nsecond\nthird\nfourth\nfifth\nsixth\nseventh\neighth\nNINTH ON HOST\n"), 0o644))
				})

			contents, err := resumed.Workspace().File("a.txt").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, contents, "SECOND EDITED",
				"the pending edit must still apply")
			require.Contains(t, contents, "NINTH ON HOST",
				"the untouched out-of-band change must survive")
			require.NotContains(t, contents, "<<<<<<< workspace",
				"a hunk that still applies must not be reported as a conflict")

			require.Empty(t, conflictedFiles(ctx, t, resumed))
		})
	}
}

// TestResumeDerivesToolsInNewSession covers the path the CLI takes on the
// first prompt after a resume, and the one the zombie-client bug actually
// surfaces through: building the tool list derives the served schema from the
// bound Workspace, which switches into that Workspace's owning client
// (core.WorkspaceServedSchema -> workspaceClientContext). A resumed session
// that inherited the saving session's workspace cannot make that switch.
func (LLMSuite) TestResumeDerivesToolsInNewSession(ctx context.Context, t *testctx.T) {
	for _, arrangement := range resumeArrangements() {
		t.Run(arrangement.name, func(ctx context.Context, t *testctx.T) {
			workdir := t.TempDir()
			initGitRepo(ctx, t, workdir)
			require.NoError(t, os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("ORIGINAL"), 0o644))

			resumed := saveAndResume(ctx, t, arrangement, saveAfterReset, workdir,
				func(cA *dagger.Client) *dagger.LLM {
					return savedSessionConversation(cA, "ORIGINAL")
				},
				func(llmA *dagger.LLM) {
					// Deriving tools works in the session that owns the
					// workspace, so a failure after resume is about the
					// resume and not about the conversation.
					_, err := llmA.Tools(ctx)
					require.NoError(t, err)
				})

			_, err := resumed.Tools(ctx)
			require.NoError(t, err,
				"deriving tools on a resumed session must resolve the workspace's "+
					"served schema against the resuming session, not the saving "+
					"session's client")
		})
	}
}

// TestResumeHostReadsInNewSession covers the basic save/exit/resume flow:
// session A binds its workspace, converses, and saves; session B connects
// fresh against the same engine and workdir and loads the saved ID. Host reads
// through the resumed LLM must work and must observe session B's live tree.
func (LLMSuite) TestResumeHostReadsInNewSession(ctx context.Context, t *testctx.T) {
	for _, arrangement := range resumeArrangements() {
		t.Run(arrangement.name, func(ctx context.Context, t *testctx.T) {
			workdir := t.TempDir()
			initGitRepo(ctx, t, workdir)
			require.NoError(t, os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("ORIGINAL"), 0o644))

			resumed := saveAndResume(ctx, t, arrangement, saveAfterReset, workdir,
				func(cA *dagger.Client) *dagger.LLM {
					return savedSessionConversation(cA, "ORIGINAL")
				},
				func(llmA *dagger.LLM) {
					// The host read works in the session that owns the workspace.
					beforeSave, err := llmA.Workspace().File("x.txt").Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, "ORIGINAL", beforeSave)
				})

			// The conversation survives the reload.
			reply, err := resumed.LastReply(ctx)
			require.NoError(t, err)
			require.Equal(t, "reading x.txt", reply)

			// The workspace must re-resolve against session B rather than
			// replaying session A's binding.
			afterResume, err := resumed.Workspace().File("x.txt").Contents(ctx)
			require.NoError(t, err,
				"resumed session must re-resolve its own workspace instead of reaching "+
					"into the saving session's client")
			require.Equal(t, "ORIGINAL", afterResume)
		})
	}
}

// TestResumeSeesEditedHostFiles covers the staleness half of the same bug:
// even when the resumed workspace resolves without erroring, it must not serve
// the snapshot the saving session cached. Session A primes its per-client host
// read cache and saves; the file is then edited on disk; session B must
// observe the edit.
func (LLMSuite) TestResumeSeesEditedHostFiles(ctx context.Context, t *testctx.T) {
	for _, arrangement := range resumeArrangements() {
		t.Run(arrangement.name, func(ctx context.Context, t *testctx.T) {
			workdir := t.TempDir()
			initGitRepo(ctx, t, workdir)
			require.NoError(t, os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("ORIGINAL"), 0o644))

			resumed := saveAndResume(ctx, t, arrangement, saveAfterReset, workdir,
				func(cA *dagger.Client) *dagger.LLM {
					return savedSessionConversation(cA, "ORIGINAL")
				},
				func(llmA *dagger.LLM) {
					// Prime the per-client host read cache with the original
					// contents, exactly as an agent reading a file before
					// editing it would.
					primed, err := llmA.Workspace().File("x.txt").Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, "ORIGINAL", primed)
				})

			// The tree moves on after the save.
			require.NoError(t, os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("EDITED"), 0o644))

			afterResume, err := resumed.Workspace().File("x.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "EDITED", afterResume,
				"a resumed session must read the live tree, not the snapshot the "+
					"saving session cached for its own client")
		})
	}
}

// TestResumeAcrossEngineRestart drives the same flow through the persistence
// cache across an engine stop/start, so the resume path is exercised where the
// saved recipe can only come back from disk. Uses the containerized dev-engine
// harness so the engine can be restarted under a stable state key.
//
// The restart is its own arrangement: it drops the in-memory cache entirely,
// so what comes back is whatever the persistence layer chose to keep. Note
// that `*core.Workspace` serializes its `ClientID` (see its
// EncodePersistedObject), so a restored workspace can carry a client that has
// not existed since before the restart.
func (LLMSuite) TestResumeAcrossEngineRestart(ctx context.Context, t *testctx.T) {
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

	_, err = resumed.Tools(ctx)
	require.NoError(t, err,
		"deriving tools after an engine restart must not route through a "+
			"client that has not existed since before the restart")
}
