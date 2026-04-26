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
//   - workspace CWD with the target subtree removed + remote root ref +
//     check: MUST pass. Today it FAILS — the CWD-as-workspace wins over
//     the explicit -m remote ref, so the toolchain resolves against the
//     partial local tree and misses the file that the remote ref still
//     has. Drives the follow-up fix: an explicit -m <remote>@<ref> must
//     take precedence over the local workspace.
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

	serviceIP := resolveServiceIP(ctx, t, c, shortHost)

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

	t.Run("workspace cwd missing target subtree", func(ctx context.Context, t *testctx.T) {
		// Target regression for the follow-up fix. CWD is still the
		// fixture workspace — valid .git, dagger.json, greeter toolchain
		// — but the "maven" directory that ReadCheck reads has been
		// removed from the local working tree. The remote ref passed via
		// -m still contains it. The user's intent with `-m <remote>@<ref>`
		// is "run this module from that ref", so the toolchain's
		// defaultPath="/" workspace must resolve against the remote ref,
		// not against a partial local checkout that happens to be the
		// CWD. Today this FAILS: CWD-as-workspace wins over the explicit
		// -m remote ref. The follow-up fix must make an explicit remote
		// -m take precedence.
		out, err := workspace().
			WithExec([]string{"rm", "-rf", "/work/workspace-default-path/target-subdir/maven"}).
			With(daggerExec("-m", modPath, "--progress=report", "check", "greeter:read-check")).
			CombinedOutput(ctx)
		require.NoError(t, err,
			"explicit -m remote ref must take precedence over a local workspace missing the target subtree:\n%s", out)
		require.Regexp(t, `read-check.*OK`, out)
	})
}

// TestLocalModuleRemoteToolchainDefaultPath covers the local-module shape
// where the toolchain is a remote git module from another repo and the user
// explicitly targets the local module with -m. The toolchain's +defaultPath
// must resolve from that local module's source root, not from the parent
// workspace or the remote toolchain repo.
func (ModuleSuite) TestLocalModuleRemoteToolchainDefaultPath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const moduleFileContent = "hello from the local module repo"
	const parentWorkspaceFileContent = "hello from the parent workspace"
	const toolchainFileContent = "hello from the remote toolchain repo"

	const greeterSource = `package main

import (
	"context"
	"dagger/greeter/internal/dagger"
)

type Greeter struct {
	Workspace *dagger.Directory
}

func New(
	// +defaultPath="."
	workspace *dagger.Directory,
) *Greeter {
	return &Greeter{Workspace: workspace}
}

func (m *Greeter) Read(ctx context.Context) (string, error) {
	return m.Workspace.
		Directory("target-subdir/nested").
		File("hello.txt").
		Contents(ctx)
}
`

	toolchainBareSetup := goGitBase(t, c).
		WithNewFile("/work/target-subdir/nested/hello.txt", toolchainFileContent).
		With(daggerExec("init", "--sdk=go", "--name=greeter", "--source=.")).
		With(sdkSource("go", greeterSource)).
		WithExec([]string{"sh", "-c", `git add . && git commit -m "init toolchain"`}).
		WithExec([]string{"sh", "-c", `
set -eux
git init --bare --initial-branch=main /srv/toolchain.git
git remote add origin /srv/toolchain.git
git push origin HEAD:refs/heads/main
git --git-dir=/srv/toolchain.git update-server-info
`})

	toolchainCommitOut, err := toolchainBareSetup.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	toolchainCommit := strings.TrimSpace(toolchainCommitOut)

	toolchainSrv, _ := gitSmartHTTPServiceDirAuth(
		ctx, t, c,
		"",
		toolchainBareSetup.Directory("/srv"),
		"", nil,
	)
	toolchainSrv, err = toolchainSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = toolchainSrv.Stop(ctx) })

	toolchainHost, err := toolchainSrv.Hostname(ctx)
	require.NoError(t, err)
	toolchainRef := "http://" + resolveServiceIP(ctx, t, c, toolchainHost) + "/toolchain.git@" + toolchainCommit

	out, err := goGitBase(t, c).
		WithNewFile("/work/target-subdir/nested/hello.txt", parentWorkspaceFileContent).
		WithWorkdir("/work/local-module").
		WithNewFile("/work/local-module/target-subdir/nested/hello.txt", moduleFileContent).
		With(daggerExec("init")).
		With(daggerExec("toolchain", "install", "--name", "greeter", toolchainRef)).
		WithWorkdir("/work").
		With(daggerExec("-m", "./local-module", "--progress=report", "call", "greeter", "read")).
		CombinedOutput(ctx)
	require.NoError(t, err,
		"explicit -m ./local-module must resolve +defaultPath from the local module source root, not the parent workspace or remote toolchain repo:\n%s", out)
	require.Contains(t, out, moduleFileContent)
	require.NotContains(t, out, parentWorkspaceFileContent)
	require.NotContains(t, out, toolchainFileContent)
}

// TestRemoteModuleSameRepoToolchainDefaultPath covers the remote-module shape
// where the toolchain lives in the same repo as the module targeted by -m. No
// Workspace argument is involved; this is plain +defaultPath resolution.
func (ModuleSuite) TestRemoteModuleSameRepoToolchainDefaultPath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const moduleFileContent = "hello from the remote module repo"
	const localWorkspaceFileContent = "hello from the local workspace"

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
	workspace *dagger.Directory,
) *Greeter {
	return &Greeter{Workspace: workspace}
}

func (m *Greeter) Read(ctx context.Context) (string, error) {
	return m.Workspace.
		Directory("target-subdir/maven").
		File("hello.txt").
		Contents(ctx)
}
`

	moduleRepo := func() *dagger.Container {
		return goGitBase(t, c).
			WithNewFile("/work/target-subdir/maven/hello.txt", moduleFileContent).
			WithWorkdir("/work/toolchains/greeter").
			With(daggerExec("init", "--sdk=go", "--name=greeter", "--source=.")).
			With(sdkSource("go", greeterSource)).
			WithWorkdir("/work").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "./toolchains/greeter")).
			WithExec([]string{"sh", "-c", `git add . && git commit -m "init module"`})
	}

	moduleBareSetup := moduleRepo().
		WithExec([]string{"sh", "-c", `
set -eux
git init --bare --initial-branch=main /srv/module.git
git remote add origin /srv/module.git
git push origin HEAD:refs/heads/main
git --git-dir=/srv/module.git update-server-info
`})

	moduleCommitOut, err := moduleBareSetup.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	moduleCommit := strings.TrimSpace(moduleCommitOut)

	moduleSrv, _ := gitSmartHTTPServiceDirAuth(
		ctx, t, c,
		"",
		moduleBareSetup.Directory("/srv"),
		"", nil,
	)
	moduleSrv, err = moduleSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = moduleSrv.Stop(ctx) })

	moduleHost, err := moduleSrv.Hostname(ctx)
	require.NoError(t, err)
	modPath := "http://" + resolveServiceIP(ctx, t, c, moduleHost) + "/module.git@" + moduleCommit

	t.Run("empty cwd", func(ctx context.Context, t *testctx.T) {
		out, err := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/empty").
			With(daggerExec("-m", modPath, "--progress=report", "call", "greeter", "read")).
			CombinedOutput(ctx)
		require.NoError(t, err,
			"module-local toolchain +defaultPath must resolve from the remote -m module repo:\n%s", out)
		require.Contains(t, out, moduleFileContent)
	})

	t.Run("workspace cwd", func(ctx context.Context, t *testctx.T) {
		out, err := moduleRepo().
			WithNewFile("/work/target-subdir/maven/hello.txt", localWorkspaceFileContent).
			With(daggerExec("-m", modPath, "--progress=report", "call", "greeter", "read")).
			CombinedOutput(ctx)
		require.NoError(t, err,
			"explicit -m remote module must beat the local workspace for module-local toolchain +defaultPath:\n%s", out)
		require.Contains(t, out, moduleFileContent)
		require.NotContains(t, out, localWorkspaceFileContent)
	})
}

// TestRemoteModuleCrossRepoToolchainDefaultPath covers the remote-module shape
// where the toolchain is another remote git module from a different repo. No
// Workspace argument is involved; plain +defaultPath must still resolve from
// the remote module repo the user targeted with -m, not from the toolchain
// repo.
func (ModuleSuite) TestRemoteModuleCrossRepoToolchainDefaultPath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const moduleFileContent = "hello from the remote module repo"
	const toolchainFileContent = "hello from the remote toolchain repo"
	const localWorkspaceFileContent = "hello from the local workspace"

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
	workspace *dagger.Directory,
) *Greeter {
	return &Greeter{Workspace: workspace}
}

func (m *Greeter) Read(ctx context.Context) (string, error) {
	return m.Workspace.
		Directory("target-subdir/maven").
		File("hello.txt").
		Contents(ctx)
}
`

	toolchainBareSetup := goGitBase(t, c).
		WithNewFile("/work/target-subdir/maven/hello.txt", toolchainFileContent).
		With(daggerExec("init", "--sdk=go", "--name=greeter", "--source=.")).
		With(sdkSource("go", greeterSource)).
		WithExec([]string{"sh", "-c", `git add . && git commit -m "init toolchain"`}).
		WithExec([]string{"sh", "-c", `
set -eux
git init --bare --initial-branch=main /srv/toolchain.git
git remote add origin /srv/toolchain.git
git push origin HEAD:refs/heads/main
git --git-dir=/srv/toolchain.git update-server-info
`})

	toolchainCommitOut, err := toolchainBareSetup.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	toolchainCommit := strings.TrimSpace(toolchainCommitOut)

	toolchainSrv, _ := gitSmartHTTPServiceDirAuth(
		ctx, t, c,
		"",
		toolchainBareSetup.Directory("/srv"),
		"", nil,
	)
	toolchainSrv, err = toolchainSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = toolchainSrv.Stop(ctx) })

	toolchainHost, err := toolchainSrv.Hostname(ctx)
	require.NoError(t, err)
	toolchainRef := "http://" + resolveServiceIP(ctx, t, c, toolchainHost) + "/toolchain.git@" + toolchainCommit

	moduleRepo := func() *dagger.Container {
		return goGitBase(t, c).
			WithNewFile("/work/target-subdir/maven/hello.txt", moduleFileContent).
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "--name", "greeter", toolchainRef)).
			WithExec([]string{"sh", "-c", `git add . && git commit -m "init module"`})
	}

	moduleBareSetup := moduleRepo().
		WithExec([]string{"sh", "-c", `
set -eux
git init --bare --initial-branch=main /srv/module.git
git remote add origin /srv/module.git
git push origin HEAD:refs/heads/main
git --git-dir=/srv/module.git update-server-info
`})

	moduleCommitOut, err := moduleBareSetup.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	moduleCommit := strings.TrimSpace(moduleCommitOut)

	moduleSrv, _ := gitSmartHTTPServiceDirAuth(
		ctx, t, c,
		"",
		moduleBareSetup.Directory("/srv"),
		"", nil,
	)
	moduleSrv, err = moduleSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = moduleSrv.Stop(ctx) })

	moduleHost, err := moduleSrv.Hostname(ctx)
	require.NoError(t, err)
	modPath := "http://" + resolveServiceIP(ctx, t, c, moduleHost) + "/module.git@" + moduleCommit

	t.Run("empty cwd", func(ctx context.Context, t *testctx.T) {
		out, err := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/empty").
			With(daggerExec("-m", modPath, "--progress=report", "call", "greeter", "read")).
			CombinedOutput(ctx)
		require.NoError(t, err,
			"remote toolchain +defaultPath must resolve from the -m module repo, not the toolchain repo:\n%s", out)
		require.Contains(t, out, moduleFileContent)
		require.NotContains(t, out, toolchainFileContent)
	})

	t.Run("workspace cwd", func(ctx context.Context, t *testctx.T) {
		out, err := moduleRepo().
			WithNewFile("/work/target-subdir/maven/hello.txt", localWorkspaceFileContent).
			With(daggerExec("-m", modPath, "--progress=report", "call", "greeter", "read")).
			CombinedOutput(ctx)
		require.NoError(t, err,
			"explicit -m remote module must beat both the local workspace and the remote toolchain repo for +defaultPath:\n%s", out)
		require.Contains(t, out, moduleFileContent)
		require.NotContains(t, out, toolchainFileContent)
		require.NotContains(t, out, localWorkspaceFileContent)
	})
}

func resolveServiceIP(ctx context.Context, t *testctx.T, c *dagger.Client, hostname string) string {
	t.Helper()

	// The vcs URL parser (engine/vcs/vcs.go) requires at least one dot in the
	// host component. Auto-generated service hostnames are dot-less, but they
	// are registered in the session DNS via search-domain expansion. Resolve
	// them to an IP so the URL is both parser-compatible and reachable.
	getentOut, err := c.Container().From(alpineImage).
		WithExec([]string{"getent", "hosts", hostname}).
		Stdout(ctx)
	require.NoError(t, err, "could not resolve git service hostname %q", hostname)
	fields := strings.Fields(getentOut)
	require.NotEmpty(t, fields, "unexpected getent output: %q", getentOut)
	return fields[0]
}

var _ = dagger.ReturnTypeAny // keep the dagger import live for *dagger.Container types
