package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
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
// Fixture layout (built inline, no external dependencies):
//
//	/work                                  (git repo root, workspace root)
//	├── .git/
//	├── dagger.json                        (workspace, registers greeter toolchain)
//	├── workspace-default-path/
//	│   ├── greeter/                       (toolchain, Go SDK)
//	│   │   ├── dagger.json
//	│   │   └── main.go                    (constructor workspace arg + ReadCheck)
//	│   └── target-subdir/
//	│       └── maven/
//	│           └── hello.txt              (what ReadCheck must reach)
//	└── ...
//
// The fixture is committed, pushed into a bare repo inside the test
// container, and served via gitSmartHTTPServiceDirAuth. The service
// hostname is auto-generated (dot-less), so the test resolves it to an
// IP at runtime and uses the IP in the -m URL — that satisfies both the
// vcs URL parser's "host must contain a dot" requirement and the
// engine's network reachability requirement without any external
// fixture.
//
// Expected outcomes:
//   - empty CWD + remote root ref + check: FAILS without the engine
//     auto-promoting the -m remote ref to the workspace. PASSES with
//     the fix.
//   - workspace CWD + remote root ref + check: PASSES always — the
//     local workspace already has the files the toolchain needs.
func (ModuleSuite) TestRemoteWorkspaceToolchainDefaultPath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const subdirFileContent = "hello from workspace subdir"

	// The toolchain. Workspace is a constructor field annotated with
	// defaultPath="/" + ignore that re-includes only the target subtree —
	// the same shape as toolchains/java-sdk-dev. ReadCheck accesses a
	// sub-sub-directory (maven) of the un-excluded parent, mirroring
	// workspace.directory("sdk/java/runtime/images/maven").
	const greeterSource = `package main

import (
	"context"
	"dagger/greeter/internal/dagger"
)

type Greeter struct {
	Workspace *dagger.Directory
}

func New(
	// +defaultPath="/"
	// +ignore=["*", "!workspace-default-path/target-subdir/"]
	workspace *dagger.Directory,
) *Greeter {
	return &Greeter{Workspace: workspace}
}

func (m *Greeter) Read(ctx context.Context) (string, error) {
	return m.Workspace.
		Directory("workspace-default-path/target-subdir/maven").
		File("hello.txt").
		Contents(ctx)
}

// +check
func (m *Greeter) ReadCheck(ctx context.Context) error {
	_, err := m.Workspace.
		Directory("workspace-default-path/target-subdir/maven").
		File("hello.txt").
		Contents(ctx)
	return err
}
`

	// Build the fixture workspace at /work and commit it to a fresh git
	// repo. Both the remote-server path and the workspace-CWD subtest
	// re-use this container as their starting point.
	workspace := func() *dagger.Container {
		return goGitBase(t, c).
			// Target file that ReadCheck must reach through the workspace.
			WithNewFile("/work/workspace-default-path/target-subdir/maven/hello.txt", subdirFileContent).
			// Scaffold the greeter toolchain.
			WithWorkdir("/work/workspace-default-path/greeter").
			With(daggerExec("init", "--sdk=go", "--name=greeter", "--source=.")).
			With(sdkSource("go", greeterSource)).
			// Scaffold the workspace root and register greeter as a toolchain.
			WithWorkdir("/work").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "./workspace-default-path/greeter")).
			// Commit everything so the tree can be pushed to a bare repo.
			WithExec([]string{"sh", "-c", `git add . && git commit -m "init"`})
	}

	// Push the committed workspace into a bare repo and serve it over
	// smart HTTP. The bare repo lives inside the container that defines
	// the service, so the fixture never leaves the session.
	bareSetup := workspace().
		WithExec([]string{"sh", "-c", `
set -eux
git init --bare --initial-branch=main /srv/repo.git
git remote add origin /srv/repo.git
git push origin HEAD:refs/heads/main
# update-server-info is harmless for smart HTTP and required for dumb HTTP.
git --git-dir=/srv/repo.git update-server-info
`})

	commitOut, err := bareSetup.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	commit := strings.TrimSpace(commitOut)

	gitSrv, _ := gitSmartHTTPServiceDirAuth(
		ctx, t, c,
		"", // auto-generated hostname — its dot-less short form is what the session DNS knows.
		bareSetup.Directory("/srv"),
		"", nil,
	)
	gitSrv, err = gitSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = gitSrv.Stop(ctx) })

	shortHost, err := gitSrv.Hostname(ctx)
	require.NoError(t, err)

	// The vcs URL parser (engine/vcs/vcs.go) requires at least one dot
	// in the host component. The auto-generated service hostname is
	// dot-less ("<hash>"), but it IS registered in the session DNS via
	// search-domain expansion to "<hash>.<sessionhash>.dagger.local".
	// Resolve it to an IP in any container that shares the session DNS
	// — buildkit already puts the session search domain in resolv.conf —
	// and use the IP in the URL. IPs satisfy the vcs regex (they have
	// dots) and need no DNS at all.
	getentOut, err := c.Container().From(alpineImage).
		WithExec([]string{"getent", "hosts", shortHost}).
		Stdout(ctx)
	require.NoError(t, err, "could not resolve git service hostname %q", shortHost)
	fields := strings.Fields(getentOut)
	require.NotEmpty(t, fields, "unexpected getent output: %q", getentOut)
	serviceIP := fields[0]

	modPath := "http://" + serviceIP + "/repo.git@" + commit

	t.Run("empty cwd", func(ctx context.Context, t *testctx.T) {
		// Target regression. The CLI runs from an alpine container with
		// no .git or dagger.json anywhere up the tree, so
		// workspace.Detect falls back to the empty CWD. Without the
		// fix, the toolchain's defaultPath="/" workspace arg resolves
		// against that empty directory and ReadCheck cannot find
		// workspace-default-path/target-subdir/maven/hello.txt. With
		// the fix, the -m remote ref is auto-promoted to the workspace
		// and the file resolves against the cloned repo tree.
		emptyCtr := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/empty")

		out, err := emptyCtr.
			With(daggerExec("-m", modPath, "--progress=report", "check", "greeter:read-check")).
			CombinedOutput(ctx)
		require.NoError(t, err,
			"CWD has no workspace markers; the -m remote ref must stand in as the workspace:\n%s", out)
		require.Regexp(t, `read-check.*OK`, out)
	})

	t.Run("workspace cwd", func(ctx context.Context, t *testctx.T) {
		// Non-regression. CWD is the fixture workspace itself (goGitBase
		// + committed tree), so currentWorkspace resolves to a
		// populated local repo and nothing in the engine changes shape.
		// This guards against any accidental regression in the "running
		// from a local clone" path while the empty-CWD fix lands.
		out, err := workspace().
			With(daggerExec("-m", modPath, "--progress=report", "check", "greeter:read-check")).
			CombinedOutput(ctx)
		require.NoError(t, err,
			"workspace CWD must continue to satisfy defaultPath=\"/\":\n%s", out)
		require.Regexp(t, `read-check.*OK`, out)
	})
}

var _ = dagger.ReturnTypeAny // keep the dagger import live for *dagger.Container types
