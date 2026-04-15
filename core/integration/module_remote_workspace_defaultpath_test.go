package core

import (
	"context"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestRemoteWorkspaceToolchainDefaultPath reproduces the bug where
//
//	dagger -m <remote-repo-root>@<ref> check <toolchain>:<check>
//
// fails when the CLI runs from a directory that is NOT itself a dagger
// workspace. The toolchain's Workspace argument (annotated with
// defaultPath="/" + @ignorePatterns) then resolves against the empty CWD
// instead of against the remote repo that -m pointed at.
//
// Fixture: github.com/dagger/dagger-test-modules, branch
// `workspace-default-path`. That branch extends the root dagger.json with
//
//	"toolchains": [{"name": "greeter", "source": "workspace-default-path/greeter"}]
//
// and adds a greeter module that takes a constructor workspace arg
// (defaultPath="/" + ignore=["*", "!workspace-default-path/target-subdir/"])
// and reads workspace-default-path/target-subdir/maven/hello.txt. This
// mirrors toolchains/java-sdk-dev's shape exactly.
//
// Expected outcomes:
//   - empty CWD + remote root ref + check: FAILS today, PASSES after
//     the "auto-promote remote -m as workspace when CWD isn't a workspace"
//     fix lands.
//   - workspace CWD (local clone) + remote root ref + check: PASSES
//     today and after — serves as a non-regression guard.
func (ModuleSuite) TestRemoteWorkspaceToolchainDefaultPath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const (
		remoteRepo = "github.com/dagger/dagger-test-modules"
		fixtureRef = "workspace-default-path"
	)

	// Resolve the fixture commit so the test fails cleanly if the branch
	// was force-moved or not yet pushed. Using the sha below pins the test
	// to a known-good fixture state.
	g := c.Git(remoteRepo).Ref(fixtureRef)
	commit, err := g.Commit(ctx)
	require.NoError(t, err,
		"fixture branch %q on %s is required by this test", fixtureRef, remoteRepo)

	// -m points at the REPO ROOT, not the toolchain subpath. This matches
	// the production invocation `dagger -m github.com/dagger/dagger@main
	// check java-sdk:lint`: -m is the whole repo, `greeter:read-check`
	// asks the workspace for its greeter toolchain and runs its check.
	modPath := remoteRepo + "@" + commit

	// emptyCtr: an alpine container with the dagger CLI mounted but no
	// workspace markers — no .git, no dagger.json, nothing up the tree.
	// This is the failing case: CWD has no workspace, so defaultPath="/"
	// resolves to an empty host directory.
	emptyCtr := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/empty")

	// workspaceCtr: same dagger CLI but inside a real git workspace.
	// This is the non-regression case: CWD is a workspace so defaultPath
	// resolves against a populated host directory.
	workspaceCtr := goGitBase(t, c)

	t.Run("empty cwd", func(ctx context.Context, t *testctx.T) {
		// The target bug. Expected to fail until the engine auto-promotes
		// the -m remote ref to the workspace when CWD has no workspace
		// markers.
		out, err := emptyCtr.
			With(daggerExec("-m", modPath, "--progress=report", "check", "greeter:read-check")).
			CombinedOutput(ctx)
		require.NoError(t, err,
			"CWD has no workspace markers; the -m remote ref must stand in as the workspace, got:\n%s", out)
		require.Regexp(t, `read-check.*OK`, out)
	})

	t.Run("workspace cwd", func(ctx context.Context, t *testctx.T) {
		// Non-regression: existing behavior where the CWD is itself a
		// workspace (has .git). The defaultPath="/" workspace arg
		// resolves against the local repo, which on a real clone would
		// contain the expected files.
		//
		// NOTE: goGitBase produces /work with a fresh `git init` — empty
		// tree, no files. We additionally materialise the fixture files
		// at workspace-default-path/... under /work so the greeter can
		// actually read them via currentWorkspace.directory(...).
		layeredCtr := workspaceCtr.
			WithDirectory(
				"/work/workspace-default-path/target-subdir",
				g.Tree().Directory("workspace-default-path/target-subdir"),
			).
			WithExec([]string{"sh", "-c", `git add . && git commit -m "seed"`})

		out, err := layeredCtr.
			With(daggerExec("-m", modPath, "--progress=report", "check", "greeter:read-check")).
			CombinedOutput(ctx)
		require.NoError(t, err, "workspace CWD must continue to satisfy defaultPath=\"/\": %s", out)
		require.Regexp(t, `read-check.*OK`, out)
	})
}
