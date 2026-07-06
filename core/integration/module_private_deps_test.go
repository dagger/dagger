package core

// These tests cover module code and module sources that depend on private Git
// repositories. They verify SSH agent setup, `SSH_AUTH_SOCK` path handling, and
// private non-Dagger language dependencies fetched during module execution.
//
// See also:
// - gitcredential_test.go: Git credential forwarding.
// - module_dependency_runtime_test.go: runtime behavior after dependencies are installed.

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// gitlabTestPAT returns the read-only PAT for the private gitlab fixture
// repo, base64-encoded in source to dodge token scanners.
func gitlabTestPAT(t *testctx.T) string {
	t.Helper()
	token, err := base64.StdEncoding.DecodeString("Z2xwYXQtMGF2bWZBbHBxWENwOXpuazZfZ2JmbTg2TVFwMU9tTjRhV3BqQ3cuMDEuMTIxbWF0b2Rx")
	require.NoError(t, err)
	return strings.TrimSpace(string(token))
}

func (ModuleSuite) TestSSHAgentConnection(ctx context.Context, t *testctx.T) {
	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("ConcurrentSetupAndCleanup", func(ctx context.Context, t *testctx.T) {
			var wg sync.WaitGroup
			for range 100 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, cleanup := setupPrivateRepoSSHAgent(t)
					time.Sleep(10 * time.Millisecond) // Simulate some work
					cleanup()
				}()
			}
			wg.Wait()
		})
	})
}

func (ModuleSuite) TestSSHAuthSockPathHandling(ctx context.Context, t *testctx.T) {
	tc := getVCSTestCase(t, "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")

	t.Run("SSH auth with home expansion and symlink", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		privateSetup, cleanup := privateRepoSetup(c, t, tc)
		defer cleanup()

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(privateSetup).
			WithExec([]string{"mkdir", "-p", "/home/dagger"}).
			WithEnvVariable("HOME", "/home/dagger").
			WithExec([]string{"ln", "-s", "/sock/unix-socket", "/home/dagger/.ssh-sock"}).
			WithEnvVariable("SSH_AUTH_SOCK", "~/.ssh-sock")

		out, err := ctr.
			WithWorkdir("/work/some/subdir").
			WithExec([]string{"mkdir", "-p", "/home/dagger"}).
			WithExec([]string{"sh", "-c", "cd", "/work/some/subdir"}).
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")
	})

	t.Run("SSH auth from different relative paths", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		privateSetup, cleanup := privateRepoSetup(c, t, tc)
		defer cleanup()

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(privateSetup).
			WithExec([]string{"mkdir", "-p", "/work/subdir"})

		out, err := ctr.
			WithWorkdir("/sock").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")

		out, err = ctr.
			WithWorkdir("/work/subdir").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines = strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")

		out, err = ctr.
			WithWorkdir("/").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines = strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")
	})
}

// TestGeneratePrivateGitDependency verifies that `dagger generate` can resolve a
// module's private Git dependency.
//
// Codegen runs through the go-sdk *module*'s `generate-all` generator
// (github.com/dagger/go-sdk). That generator executes as module code, i.e. under
// a nested module client rather than the user's main client. The dependency is
// resolved inside that nested execution (generatedContextChangeset -> codegen ->
// loadDependencyModules -> ResolveDepToSource), so the engine must forward the
// non-module parent client's Git credentials. Before the fix in
// ResolveDepToSource, the git resolver only authenticated for the main client
// (core/schema/git.go), and this failed with "git authentication failed" even
// though `dagger -m <private-ref> ...` and `dagger develop` worked.
//
// This runs `dagger` on the host (not nested in a container) so the
// credential-resolving client has git and the configured credential helper,
// matching how git credential forwarding is exercised in gitcredential_test.go.
func (ModuleSuite) TestGeneratePrivateGitDependency(ctx context.Context, t *testctx.T) {
	// HTTPS GitLab private repo authenticated with a read-only PAT, matching the
	// originally reported scenario.
	tc := getVCSTestCase(t, "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")

	workDir := t.TempDir()

	// Isolated git credential helper for the private repo's host.
	gitConfigPath := filepath.Join(workDir, ".gitconfig")
	err := os.WriteFile(gitConfigPath, []byte(makeGitCredentials("https://"+tc.expectedHost, "x-token-auth", tc.token())), 0600)
	require.NoError(t, err)

	// run executes a dagger command on the host in workDir with a git
	// environment scoped to the isolated credential helper above.
	run := func(args ...string) ([]byte, error) {
		cmd := hostDaggerCommandRaw(ctx, t, workDir, args...)
		env := make([]string, 0, len(os.Environ()))
		for _, e := range os.Environ() {
			if !strings.Contains(strings.ToLower(strings.SplitN(e, "=", 2)[0]), "git") {
				env = append(env, e)
			}
		}
		env = append(env,
			"GIT_CONFIG_GLOBAL="+gitConfigPath,
			"GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_TERMINAL_PROMPT=0",
		)
		cmd.Env = env
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			runErr = fmt.Errorf("%s: %w", out, runErr)
		}
		return out, runErr
	}

	// Initialize a workspace with go-sdk installed and marked as an SDK.
	// Workspace creation is implicit on first install (it creates dagger.toml
	// at the workspace root), so there is no separate `workspace init` step.
	require.NoError(t, exec.Command("git", "-C", workDir, "init").Run())
	out, err := run("sdk", "install", "go")
	require.NoError(t, err, string(out))

	// A Go SDK module (discovered by go-sdk's generate-all) that declares the
	// private repo as a dependency.
	daggerJSON := fmt.Sprintf(`{
  "name": "consumer",
  "engineVersion": "latest",
  "sdk": { "source": "go" },
  "source": ".",
  "dependencies": [ { "name": "dep", "source": %q } ]
}`, testGitModuleRef(tc, ""))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "dagger.json"), []byte(daggerJSON), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main\n\ntype Consumer struct{}\n\nfunc (m *Consumer) Hello() string { return \"hello\" }\n"), 0644))

	out, err = run("generate", "-y", "--progress=plain")
	require.NoError(t, err, string(out))
	require.NotContains(t, string(out), "authentication failed")
}

func (ModuleSuite) TestPrivateDeps(ctx context.Context, t *testctx.T) {
	t.Run("golang", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		sockPath, cleanup := setupPrivateRepoSSHAgent(t)
		defer cleanup()

		socket := c.Host().UnixSocket(sockPath)

		modGen := goGitBase(t, c).
			WithExec([]string{"apk", "add", "openssh", "openssl"}).
			WithUnixSocket("/sock/unix-socket", socket).
			WithEnvVariable("SSH_AUTH_SOCK", "/sock/unix-socket").
			WithNewFile("/root/.gitconfig", `
[url "ssh://git@github.com/"]
	insteadOf = https://github.com/
`).
			With(withModuleEntrypointFixture(t, c, ".", "foo", "go/private-language-dep"))

		howCoolIsDagger, err := modGen.
			With(daggerExec("call", "how-cool-is-dagger")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ubercool", howCoolIsDagger)
	})

	t.Run("golang transitive existing go.mod", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		sockPath, cleanup := setupPrivateRepoSSHAgent(t)
		defer cleanup()

		socket := c.Host().UnixSocket(sockPath)

		const (
			privateDep        = "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git/privatewrapper"
			privateDepVersion = "v0.0.1"
		)

		modGen := goGitBase(t, c).
			WithExec([]string{"apk", "add", "openssh", "openssl"}).
			WithUnixSocket("/sock/unix-socket", socket).
			WithEnvVariable("SSH_AUTH_SOCK", "/sock/unix-socket").
			WithNewFile("/root/.gitconfig", `
[url "ssh://git@gitlab.com/"]
	insteadOf = https://gitlab.com/
`).
			WithEnvVariable("GIT_SSH_COMMAND", "ssh -o StrictHostKeyChecking=no").
			WithNewFile("/work/dagger.toml", `[modules.foo]
source = ".dagger/modules/foo"
entrypoint = true
`).
			WithNewFile("/work/.dagger/modules/foo/dagger.json", `{
  "name": "foo",
  "engineVersion": "latest",
  "sdk": {
    "source": "go",
    "config": {
      "goprivate": "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git"
    }
  }
}`).
			WithNewFile("/work/.dagger/modules/foo/go.mod", fmt.Sprintf(`module dagger/foo

go 1.21.3

require %s %s
`, privateDep, privateDepVersion)).
			WithNewFile("/work/.dagger/modules/foo/main.go", fmt.Sprintf(`package main

import "%s/pkg/coolwrapper"

type Foo struct{}

func (m *Foo) HowCoolIsDagger() string {
	return coolwrapper.HowCoolIsThat()
}
`, privateDep)).
			WithWorkdir("/work")

		howCoolIsDagger, err := modGen.
			With(daggerExec("call", "how-cool-is-dagger")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "private-transitive-go-dep:ubercool", howCoolIsDagger)
	})

	// same private go.mod dependency, but over HTTPS: the install resolves it
	// through the host's git credential helper via the engine's git-credential
	// socket, which must be gone by the time user code runs
	t.Run("golang transitive existing go.mod https", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		token := gitlabTestPAT(t)

		const (
			privateDep        = "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git/privatewrapper"
			privateDepVersion = "v0.0.1"
		)

		modGen := goGitBase(t, c).
			WithNewFile("/root/.gitconfig", makeGitCredentials("https://gitlab.com", "git", token)).
			// goprivate through the workspace settings namespace; the legacy
			// dagger.json sdk.config form is covered by TestSDKConfig
			WithNewFile("/work/dagger.toml", `[modules.foo]
source = ".dagger/modules/foo"
entrypoint = true

[modules.foo.settings]
goprivate = "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git"
`).
			WithNewFile("/work/.dagger/modules/foo/dagger-module.toml", `name = "foo"
engineVersion = "latest"

[runtime]
source = "go"
`).
			WithNewFile("/work/.dagger/modules/foo/go.mod", fmt.Sprintf(`module dagger/foo

go 1.21.3

require %s %s
`, privateDep, privateDepVersion)).
			WithNewFile("/work/.dagger/modules/foo/main.go", fmt.Sprintf(`package main

import (
	"os"

	"%s/pkg/coolwrapper"
)

type Foo struct{}

func (m *Foo) HowCoolIsDagger() string {
	return coolwrapper.HowCoolIsThat()
}

// Leaks reports any git credential plumbing still visible to user code.
func (m *Foo) Leaks() string {
	if os.Getenv("GIT_CONFIG_COUNT") != "" {
		return "env leaked"
	}
	if _, err := os.Stat("/.git-credential"); err == nil {
		return "helper leaked"
	}
	if _, err := os.Stat("/tmp/dagger-git-credential.sock"); err == nil {
		return "socket leaked"
	}
	return "clean"
}
`, privateDep)).
			WithWorkdir("/work")

		howCoolIsDagger, err := modGen.
			With(daggerExec("call", "how-cool-is-dagger")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "private-transitive-go-dep:ubercool", howCoolIsDagger)

		leaks, err := modGen.
			With(daggerExec("call", "leaks")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "clean", leaks)
	})
}

// Public git+https dependencies in Python/TypeScript modules exercise the
// full git-credential plumbing (manifest scan, socket mint, mount and helper
// injection inside the runtime module's install execs, scrub before user
// code) without needing private-repo credentials: git only consults the
// helper on an auth challenge, so the install succeeds with an empty scope.
func (ModuleSuite) TestGitDepInstall(ctx context.Context, t *testctx.T) {
	t.Run("python", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(withPythonModule(t, c, "python/base-test")).
			// the [tool.uv.sources] sub-table form is what `uv add git+https://...` writes
			With(pyprojectExtra([]string{"packaging"}, `
[tool.uv.sources.packaging]
git = "https://github.com/pypa/packaging"
tag = "25.0"
`)).
			With(pythonSource(`
import os
import importlib.metadata

import dagger

@dagger.object_type
class Test:
    @dagger.function
    def check(self) -> str:
        version = importlib.metadata.version("packaging")
        leaks = []
        if os.environ.get("GIT_CONFIG_COUNT"):
            leaks.append("env")
        if os.path.exists("/.git-credential"):
            leaks.append("helper")
        if os.path.exists("/tmp/dagger-git-credential.sock"):
            leaks.append("socket")
        return version + ":" + (",".join(leaks) or "clean")
`)).
			With(daggerCallAt(".", "check")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "25.0:clean", out)
	})

	t.Run("python private", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		token := gitlabTestPAT(t)

		const privateDep = "coolpy @ git+https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git@92ee0a59a10856b61ed9a18f0ff921cc31fb8f7d#subdirectory=privatepy"

		out, err := daggerCliBase(t, c).
			// the nested dagger session serves `git credential fill` from this container
			WithExec([]string{"apk", "add", "git"}).
			WithNewFile("/root/.gitconfig", makeGitCredentials("https://gitlab.com", "git", token)).
			With(withPythonModule(t, c, "python/base-test")).
			With(pyprojectExtra([]string{privateDep}, "")).
			With(pythonSource(`
import os

import coolpy
import dagger

@dagger.object_type
class Test:
    @dagger.function
    def check(self) -> str:
        leaks = []
        if os.environ.get("GIT_CONFIG_COUNT"):
            leaks.append("env")
        if os.path.exists("/.git-credential"):
            leaks.append("helper")
        if os.path.exists("/tmp/dagger-git-credential.sock"):
            leaks.append("socket")
        return coolpy.how_cool() + ":" + (",".join(leaks) or "clean")
`)).
			With(daggerCallAt(".", "check")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "private-python-git-dep:ubercool:clean", out)
	})

	t.Run("typescript", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(withModuleFixture(t, c, ".", "typescript/base-test")).
			WithNewFile("package.json", `{
  "type": "module",
  "dependencies": {
    "typescript": "^5.5.4",
    "ms": "git+https://github.com/vercel/ms.git#2.1.3"
  }
}`).
			With(sdkSource("typescript", `
import * as fs from "fs"
import ms from "ms"
import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  check(): string {
    const leaks = []
    if (process.env.GIT_CONFIG_COUNT) leaks.push("env")
    if (fs.existsSync("/.git-credential")) leaks.push("helper")
    if (fs.existsSync("/tmp/dagger-git-credential.sock")) leaks.push("socket")
    return ms(60000) + ":" + (leaks.join(",") || "clean")
  }
}
`)).
			With(daggerCallAt(".", "check")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "1m:clean", out)
	})

	t.Run("typescript private", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		token := gitlabTestPAT(t)

		out, err := daggerCliBase(t, c).
			// the nested dagger session serves `git credential fill` from this container
			WithExec([]string{"apk", "add", "git"}).
			WithNewFile("/root/.gitconfig", makeGitCredentials("https://gitlab.com", "git", token)).
			With(withModuleFixture(t, c, ".", "typescript/base-test")).
			WithNewFile("package.json", `{
  "type": "module",
  "dependencies": {
    "typescript": "^5.5.4",
    "cooljs": "git+https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git#d62b69ec32be39bf3c8b7386a066732a40d9f632"
  }
}`).
			With(sdkSource("typescript", `
import * as fs from "fs"
import cooljs from "cooljs"
import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  check(): string {
    const leaks = []
    if (process.env.GIT_CONFIG_COUNT) leaks.push("env")
    if (fs.existsSync("/.git-credential")) leaks.push("helper")
    if (fs.existsSync("/tmp/dagger-git-credential.sock")) leaks.push("socket")
    return cooljs + ":" + (leaks.join(",") || "clean")
  }
}
`)).
			With(daggerCallAt(".", "check")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "private-js-git-dep:ubercool:clean", out)
	})

	// bun never runs git for github.com dependencies (it downloads tarballs
	// from the github API instead, currently without applying credentials:
	// https://github.com/oven-sh/bun/issues/19618), so a github URL would
	// bypass the credential helper entirely and prove nothing; a non-github
	// host is what makes bun fall back to a real `git clone`
	t.Run("typescript private bun", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		token := gitlabTestPAT(t)

		out, err := daggerCliBase(t, c).
			// the nested dagger session serves `git credential fill` from this container
			WithExec([]string{"apk", "add", "git"}).
			WithNewFile("/root/.gitconfig", makeGitCredentials("https://gitlab.com", "git", token)).
			With(withModuleFixture(t, c, ".", "typescript/base-test")).
			WithNewFile("package.json", `{
  "type": "module",
  "dagger": {
    "runtime": "bun"
  },
  "dependencies": {
    "typescript": "^5.5.4",
    "cooljs": "git+https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git#d62b69ec32be39bf3c8b7386a066732a40d9f632"
  }
}`).
			With(sdkSource("typescript", `
import * as fs from "fs"
import cooljs from "cooljs"
import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  check(): string {
    const leaks = []
    if (process.env.GIT_CONFIG_COUNT) leaks.push("env")
    if (fs.existsSync("/.git-credential")) leaks.push("helper")
    if (fs.existsSync("/tmp/dagger-git-credential.sock")) leaks.push("socket")
    return cooljs + ":" + (leaks.join(",") || "clean")
  }
}
`)).
			With(daggerCallAt(".", "check")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "private-js-git-dep:ubercool:clean", out)
	})
}
