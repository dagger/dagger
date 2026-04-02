package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/util/lockfile"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type LockfileSuite struct{}

func TestLockfile(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(LockfileSuite{})
}

const containerFromQuery = `{
  container {
    from(address: "alpine:latest") {
      file(path: "/etc/alpine-release") {
        contents
      }
    }
  }
}
`

const (
	lockTestGitRepoURL      = "https://github.com/dagger/dagger.git"
	lockTestGitBranchName   = "main"
	lockTestGitBranchCommit = "c80ac2c13df7d573a069938e01ca13f7a81f0345"
	lockTestGitTagName      = "v0.18.2"
	lockTestGitTagOldCommit = "9ea5ea7c848fef2a2c47cce0716d5fcb8d6bedeb"
)

const gitBranchCommitQuery = `{
  git(url: "` + lockTestGitRepoURL + `") {
    branch(name: "main") {
      commit
    }
  }
}
`

const gitBranchAndTagCommitQuery = `{
  git(url: "` + lockTestGitRepoURL + `") {
    branch(name: "main") {
      commit
    }
    tag(name: "` + lockTestGitTagName + `") {
      commit
    }
  }
}
`

const workspaceUpdateQuery = `{
  currentWorkspace {
    update {
      modifiedPaths
    }
  }
}
`

const workspaceUpdateExportQuery = `{
  currentWorkspace {
    update {
      export(path: ".")
    }
  }
}
`

func (LockfileSuite) TestFromLockfileDisabledIgnoresEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)
	lockPath, originalLock := writeContainerFromLock(t, workdir, lockTestPlatform(ctx, t), "not-a-digest", workspace.PolicyPin)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=disabled", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.Equal(t, originalLock, string(lockBytes))
}

func (LockfileSuite) TestFromLockfileLiveRefreshesEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)
	lockPath, originalLock := writeContainerFromLock(t, workdir, lockTestPlatform(ctx, t), "not-a-digest", workspace.PolicyPin)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=live", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertContainerFromLockEntry(t, lockBytes, workspace.PolicyPin)
}

func (LockfileSuite) TestFromLockfilePinnedUsesPinEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)

	_, _ = writeContainerFromLock(t, workdir, lockTestPlatform(ctx, t), "not-a-digest", workspace.PolicyPin)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=pinned", "query", "--doc", queryPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `invalid lock digest "not-a-digest"`)
}

func (LockfileSuite) TestFromLockfilePinnedRefreshesFloatEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)
	lockPath, originalLock := writeContainerFromLock(t, workdir, lockTestPlatform(ctx, t), "not-a-digest", workspace.PolicyFloat)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=pinned", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertContainerFromLockEntry(t, lockBytes, workspace.PolicyFloat)
}

func (LockfileSuite) TestFromLockfileFrozenUsesFloatEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)

	_, _ = writeContainerFromLock(t, workdir, lockTestPlatform(ctx, t), "not-a-digest", workspace.PolicyFloat)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "query", "--doc", queryPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `invalid lock digest "not-a-digest"`)
}

func (LockfileSuite) TestFromLockfileFrozenRequiresEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "query", "--doc", queryPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "missing lock entry for container.from")

	_, err = os.Stat(filepath.Join(workdir, ".dagger", "lock"))
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func (LockfileSuite) TestGitBranchPinnedRefreshesFloatEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	queryPath := writeQueryDoc(t, workdir, "git-branch.graphql", gitBranchCommitQuery)
	lockPath, originalLock := writeGitRefLock(t, workdir, "git.branch", lockTestGitBranchName, lockTestGitBranchCommit, workspace.PolicyFloat)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=pinned", "query", "--doc", queryPath)
	require.NoError(t, err)
	require.NotContains(t, string(out), lockTestGitBranchCommit)

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertGitLockEntry(t, lockBytes, "git.branch", []any{lockTestGitRepoURL, lockTestGitBranchName}, workspace.PolicyFloat)
}

func (LockfileSuite) TestGitBranchFrozenUsesFloatEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	queryPath := writeQueryDoc(t, workdir, "git-branch.graphql", gitBranchCommitQuery)

	_, _ = writeGitRefLock(t, workdir, "git.branch", lockTestGitBranchName, lockTestGitBranchCommit, workspace.PolicyFloat)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "query", "--doc", queryPath)
	require.NoError(t, err)
	require.Contains(t, string(out), lockTestGitBranchCommit)
}

func (LockfileSuite) TestLockUpdateRefreshesExistingEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	lockPath, originalLock := writeContainerFromLock(t, workdir, lockTestPlatform(ctx, t), "sha256:"+strings.Repeat("0", 64), workspace.PolicyPin)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "lock", "update")
	require.NoError(t, err)

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertContainerFromLockEntry(t, lockBytes, workspace.PolicyPin)
}

func (LockfileSuite) TestLockUpdateRefreshesExistingGitEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	lockPath, originalLock := writeGitRefLock(t, workdir, "git.branch", lockTestGitBranchName, lockTestGitBranchCommit, workspace.PolicyFloat)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "lock", "update")
	require.NoError(t, err)

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertGitLockEntry(t, lockBytes, "git.branch", []any{lockTestGitRepoURL, lockTestGitBranchName}, workspace.PolicyFloat)
	require.NotContains(t, string(lockBytes), lockTestGitBranchCommit)
}

func (LockfileSuite) TestLiveDiscoversQueryEntries(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=live", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockPath := filepath.Join(workdir, ".dagger", "lock")
	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assertContainerFromLockEntry(t, lockBytes, workspace.PolicyPin)
}

func (LockfileSuite) TestLiveDiscoversGitEntries(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	queryPath := writeQueryDoc(t, workdir, "git.graphql", gitBranchAndTagCommitQuery)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=live", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockPath := filepath.Join(workdir, ".dagger", "lock")
	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assertGitLockEntry(t, lockBytes, "git.branch", []any{lockTestGitRepoURL, lockTestGitBranchName}, workspace.PolicyFloat)
	assertGitLockEntry(t, lockBytes, "git.tag", []any{lockTestGitRepoURL, lockTestGitTagName}, workspace.PolicyPin)
}

func (LockfileSuite) TestLiveNestedQuery(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	updated := daggerCliBase(t, c).
		WithNewFile("query.graphql", containerFromQuery).
		With(daggerExec("--silent", "--lock=live", "query", "--doc", "query.graphql"))

	_, err := updated.Stdout(ctx)
	require.NoError(t, err)

	lockContents, err := updated.File("/work/.dagger/lock").Contents(ctx)
	require.NoError(t, err)
	assertContainerFromLockEntry(t, []byte(lockContents), workspace.PolicyPin)
}

func (LockfileSuite) TestLiveModuleCall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		With(initStandaloneDangModule("lockmod", `
type Lockmod {
  pub release: String! {
    Dagger.container.from("alpine:latest").file("/etc/alpine-release").contents
  }
}
`))

	updated := base.With(daggerExec("--silent", "--lock=live", "call", "release"))
	out, err := updated.Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(out))

	lockContents, err := updated.File("/work/.dagger/lock").Contents(ctx)
	require.NoError(t, err)
	assertContainerFromLockEntry(t, []byte(lockContents), workspace.PolicyPin)

	frozen := updated.With(daggerExec("--silent", "--lock=frozen", "call", "release"))
	out, err = frozen.Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(out))

	lockContentsAfter, err := frozen.File("/work/.dagger/lock").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, lockContents, lockContentsAfter)
}

func (LockfileSuite) TestLiveDiscoversModuleSourceEntries(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	gitDaemon, source := startModuleResolveGitService(ctx, t, c)
	updated := moduleResolveClientContainer(ctx, t, c, gitDaemon, source).
		WithNewFile("query.graphql", moduleSourceCommitQuery(source)).
		WithExec([]string{"dagger", "--silent", "--lock=live", "query", "--doc", "query.graphql"})

	out, err := updated.Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(out))

	lockContents, err := updated.File("/work/.dagger/lock").Contents(ctx)
	require.NoError(t, err)
	assertModuleResolveLockEntry(t, []byte(lockContents), source, workspace.PolicyFloat)
}

func (LockfileSuite) TestModuleSourceFrozenUsesLockedEntry(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	gitDaemon, repoURL, source := startModuleResolveGitServiceWithRepo(ctx, t, c)
	initialCommit := gitRepoHeadCommit(ctx, t, c, gitDaemon, repoURL)
	newCommit := advanceGitRepo(ctx, t, c, gitDaemon, repoURL, "README.md", "second revision")
	require.NotEqual(t, initialCommit, newCommit)

	frozen := moduleResolveClientContainer(ctx, t, c, gitDaemon, source).
		WithNewFile(".dagger/lock", mustMarshalModuleResolveLock(t, source, initialCommit, workspace.PolicyFloat)).
		WithNewFile("query.graphql", moduleSourceCommitQuery(source)).
		WithExec([]string{"dagger", "--silent", "--lock=frozen", "query", "--doc", "query.graphql"})

	out, err := frozen.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, initialCommit)
	require.NotContains(t, out, newCommit)
}

func (LockfileSuite) TestLockUpdateRefreshesExistingModuleResolveEntry(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	gitDaemon, repoURL, source := startModuleResolveGitServiceWithRepo(ctx, t, c)
	initialCommit := gitRepoHeadCommit(ctx, t, c, gitDaemon, repoURL)
	newCommit := advanceGitRepo(ctx, t, c, gitDaemon, repoURL, "README.md", "second revision")
	require.NotEqual(t, initialCommit, newCommit)

	updated := moduleResolveClientContainer(ctx, t, c, gitDaemon, source).
		WithNewFile(".dagger/lock", mustMarshalModuleResolveLock(t, source, initialCommit, workspace.PolicyFloat)).
		WithExec([]string{"dagger", "--silent", "lock", "update"})

	_, err := updated.Stdout(ctx)
	require.NoError(t, err)

	lockContents, err := updated.File("/work/.dagger/lock").Contents(ctx)
	require.NoError(t, err)
	assertModuleResolveLockEntry(t, []byte(lockContents), source, workspace.PolicyFloat)
	require.NotContains(t, lockContents, initialCommit)
	require.Contains(t, lockContents, newCommit)
}

func (LockfileSuite) TestWorkspaceUpdate(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	lockPath, originalLock := writeContainerFromLock(t, workdir, lockTestPlatform(ctx, t), "sha256:"+strings.Repeat("0", 64), workspace.PolicyFloat)
	updateQueryPath := writeQueryDoc(t, workdir, "update.graphql", workspaceUpdateQuery)
	updateExportQueryPath := writeQueryDoc(t, workdir, "update-export.graphql", workspaceUpdateExportQuery)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", updateQueryPath)
	require.NoError(t, err)
	require.Contains(t, string(out), ".dagger/lock")

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.Equal(t, originalLock, string(lockBytes))

	_, err = hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", updateExportQueryPath)
	require.NoError(t, err)

	lockBytes, err = os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertContainerFromLockEntry(t, lockBytes, workspace.PolicyFloat)
}

func (LockfileSuite) TestWorkspaceUpdateRequiresLockfile(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	updateQueryPath := writeQueryDoc(t, workdir, "update.graphql", workspaceUpdateQuery)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", updateQueryPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "workspace lockfile does not exist")
}

func (LockfileSuite) TestWorkspaceUpdateNestedQuery(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	staleLock := mustMarshalContainerFromLock(t, lockTestPlatform(ctx, t), "sha256:"+strings.Repeat("1", 64), workspace.PolicyFloat)
	updated := daggerCliBase(t, c).
		WithNewFile(".dagger/lock", staleLock).
		WithNewFile("update.graphql", workspaceUpdateExportQuery).
		With(daggerExec("--silent", "query", "--doc", "update.graphql"))

	_, err := updated.Stdout(ctx)
	require.NoError(t, err)

	lockContents, err := updated.File("/work/.dagger/lock").Contents(ctx)
	require.NoError(t, err)
	require.NotEqual(t, staleLock, lockContents)
	assertContainerFromLockEntry(t, []byte(lockContents), workspace.PolicyFloat)
}

func writeContainerFromQuery(t *testctx.T, workdir string) string {
	return writeQueryDoc(t, workdir, "query.graphql", containerFromQuery)
}

func writeQueryDoc(t *testctx.T, workdir, name, contents string) string {
	t.Helper()

	queryPath := filepath.Join(workdir, name)
	require.NoError(t, os.WriteFile(queryPath, []byte(contents), 0o600))
	return queryPath
}

func writeContainerFromLock(t *testctx.T, workdir, platform, digest string, policy workspace.LockPolicy) (string, string) {
	t.Helper()

	lockPath := filepath.Join(workdir, ".dagger", "lock")
	require.NoError(t, os.MkdirAll(filepath.Dir(lockPath), 0o755))

	lockContents := mustMarshalContainerFromLock(t, platform, digest, policy)
	require.NoError(t, os.WriteFile(lockPath, []byte(lockContents), 0o600))
	return lockPath, lockContents
}

func writeGitRefLock(t *testctx.T, workdir, operation, name, commit string, policy workspace.LockPolicy) (string, string) {
	t.Helper()

	lockPath := filepath.Join(workdir, ".dagger", "lock")
	require.NoError(t, os.MkdirAll(filepath.Dir(lockPath), 0o755))

	lockContents := mustMarshalGitRefLock(t, operation, name, commit, policy)
	require.NoError(t, os.WriteFile(lockPath, []byte(lockContents), 0o600))
	return lockPath, lockContents
}

func mustMarshalContainerFromLock(t *testctx.T, platform, digest string, policy workspace.LockPolicy) string {
	t.Helper()

	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup("", "container.from", []any{"docker.io/library/alpine:latest", platform}, workspace.LookupResult{
		Value:  digest,
		Policy: policy,
	}))

	lockBytes, err := lock.Marshal()
	require.NoError(t, err)
	return string(lockBytes)
}

func mustMarshalGitRefLock(t *testctx.T, operation, name, commit string, policy workspace.LockPolicy) string {
	t.Helper()

	lock := workspace.NewLock()
	inputs := []any{lockTestGitRepoURL}
	if name != "" {
		inputs = append(inputs, name)
	}
	require.NoError(t, lock.SetLookup("", operation, inputs, workspace.LookupResult{
		Value:  commit,
		Policy: policy,
	}))

	lockBytes, err := lock.Marshal()
	require.NoError(t, err)
	return string(lockBytes)
}

func mustMarshalModuleResolveLock(t *testctx.T, source, commit string, policy workspace.LockPolicy) string {
	t.Helper()

	lock := workspace.NewLock()
	require.NoError(t, lock.SetModuleResolve(source, workspace.LookupResult{
		Value:  commit,
		Policy: policy,
	}))

	lockBytes, err := lock.Marshal()
	require.NoError(t, err)
	return string(lockBytes)
}

func lockTestPlatform(ctx context.Context, t *testctx.T) string {
	t.Helper()

	c := connect(ctx, t)
	platform, err := c.DefaultPlatform(ctx)
	require.NoError(t, err)
	return string(platform)
}

func assertContainerFromLockEntry(t *testctx.T, lockBytes []byte, expectedPolicy workspace.LockPolicy) {
	t.Helper()
	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)

	var found bool
	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != "container.from" {
			continue
		}
		found = true
		require.Len(t, entry.Inputs, 2)

		ref, ok := entry.Inputs[0].(string)
		require.True(t, ok)
		require.Contains(t, ref, "alpine:latest")

		require.Equal(t, string(expectedPolicy), entry.Policy)

		value, ok := entry.Value.(string)
		require.True(t, ok)
		require.True(t, strings.HasPrefix(value, "sha256:"))
	}

	require.True(t, found, "expected container.from entry in lockfile")
}

func assertGitLockEntry(t *testctx.T, lockBytes []byte, operation string, expectedInputs []any, expectedPolicy workspace.LockPolicy) {
	t.Helper()
	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)

	var found bool
	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != operation {
			continue
		}
		if !equalLockInputs(entry.Inputs, expectedInputs) {
			continue
		}

		found = true
		require.Equal(t, string(expectedPolicy), entry.Policy)

		value, ok := entry.Value.(string)
		require.True(t, ok)
		require.True(t, len(value) == 40 || strings.HasPrefix(value, "sha256:"))
	}

	require.True(t, found, "expected %s entry in lockfile", operation)
}

func assertModuleResolveLockEntry(t *testctx.T, lockBytes []byte, source string, expectedPolicy workspace.LockPolicy) {
	t.Helper()
	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)

	var found bool
	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != "modules.resolve" {
			continue
		}
		if !equalLockInputs(entry.Inputs, []any{source}) {
			continue
		}

		found = true
		require.Equal(t, string(expectedPolicy), entry.Policy)

		value, ok := entry.Value.(string)
		require.True(t, ok)
		require.Len(t, value, 40)
	}

	require.True(t, found, "expected modules.resolve entry in lockfile")
}

func equalLockInputs(actual, expected []any) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return false
		}
	}
	return true
}

func startModuleResolveGitService(ctx context.Context, t *testctx.T, c *dagger.Client) (*dagger.Service, string) {
	t.Helper()
	gitDaemon, _, source := startModuleResolveGitServiceWithRepo(ctx, t, c)
	return gitDaemon, source
}

func startModuleResolveGitServiceWithRepo(ctx context.Context, t *testctx.T, c *dagger.Client) (*dagger.Service, string, string) {
	t.Helper()

	content := c.Directory().
		WithNewFile("dagger.json", `{"name":"lockmod","engineVersion":"latest"}`).
		WithNewFile("README.md", "first revision")

	gitDir := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/root/srv", makeGitDir(c, content, "main")).
		WithExec([]string{"git", "config", "-f", "/root/srv/repo.git/config", "http.receivepack", "true"}).
		Directory("/root/srv")

	hostname := identity.NewID() + ".test"
	gitDaemon, repoBaseURL := gitSmartHTTPServiceDirAuth(ctx, t, c, hostname, gitDir, "", nil)
	repoURL := repoBaseURL + "/repo.git"
	gitDaemon, err := gitDaemon.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := gitDaemon.Stop(ctx)
		require.NoError(t, err)
	})

	return gitDaemon, repoURL, repoURL + "@main"
}

func moduleSourceCommitQuery(source string) string {
	return fmt.Sprintf(`{
  moduleSource(refString: %q) {
    commit
  }
}
`, source)
}

func moduleResolveClientContainer(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	gitSvc *dagger.Service,
	source string,
) *dagger.Container {
	t.Helper()

	host := moduleResolveServiceHost(t, source)
	devEngine := devEngineContainerAsService(devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithServiceBinding(host, gitSvc)
	}))
	return engineClientContainer(ctx, t, c, devEngine).WithWorkdir("/work")
}

func moduleResolveServiceHost(t *testctx.T, rawURL string) string {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)

	host := parsed.Hostname()
	require.NotEmpty(t, host)
	return host
}

func gitRepoHeadCommit(ctx context.Context, t *testctx.T, c *dagger.Client, svc *dagger.Service, repoURL string) string {
	t.Helper()

	commit, err := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithServiceBinding(moduleResolveServiceHost(t, repoURL), svc).
		WithWorkdir("/src").
		WithExec([]string{"git", "clone", repoURL, "."}).
		WithExec([]string{"git", "rev-parse", "HEAD"}).
		Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(commit)
}

func advanceGitRepo(ctx context.Context, t *testctx.T, c *dagger.Client, svc *dagger.Service, repoURL, path, contents string) string {
	t.Helper()

	commit, err := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithServiceBinding(moduleResolveServiceHost(t, repoURL), svc).
		WithWorkdir("/src").
		WithExec([]string{"git", "clone", repoURL, "."}).
		WithNewFile(path, contents).
		WithExec([]string{"git", "add", path}).
		WithExec([]string{"git", "commit", "-m", "update " + path}).
		WithExec([]string{"git", "push", "origin", "main"}).
		WithExec([]string{"git", "rev-parse", "HEAD"}).
		Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(commit)
}
