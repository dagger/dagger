package core

// These tests cover `dagger.lock`, the workspace lockfile that pins resolved
// Git refs and runtime lookups. They verify lock resolution and how workspace
// config changes affect the lockfile.
//
// See also:
// - workspace_config_test.go: workspace config read/write behavior.
// - workspace_compat_test.go: legacy config shapes before migration.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/workspace"
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
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
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
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
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
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeContainerFromQuery(t, workdir)

	_, _ = writeContainerFromLock(t, workdir, lockTestPlatform(ctx, t), "not-a-digest", workspace.PolicyPin)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=pinned", "query", "--doc", queryPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `invalid lock digest "not-a-digest"`)
}

func hostGitInit(t *testctx.T, dir string) {
	gitCmd := exec.Command("git", "init")
	gitCmd.Dir = dir
	out, err := gitCmd.CombinedOutput()
	require.NoError(t, err, out)
}

func (LockfileSuite) TestFromLockfilePinnedRefreshesFloatEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeContainerFromQuery(t, workdir)
	lockPath, originalLock := writeContainerFromLock(t, workdir, lockTestPlatform(ctx, t), "not-a-digest", workspace.PolicyFloat)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=pinned", "query", "--doc", queryPath) // TODO why is TestLockfile/TestFromLockfilePinnedRefreshesFloatEntry getting a nil lockfile?
	require.NoError(t, err)

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertContainerFromLockEntry(t, lockBytes, workspace.PolicyFloat)
}

func (LockfileSuite) TestFromLockfileFrozenUsesFloatEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeContainerFromQuery(t, workdir)

	_, _ = writeContainerFromLock(t, workdir, lockTestPlatform(ctx, t), "not-a-digest", workspace.PolicyFloat)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "query", "--doc", queryPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `invalid lock digest "not-a-digest"`)
}

func (LockfileSuite) TestFromLockfileFrozenRequiresEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeContainerFromQuery(t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "query", "--doc", queryPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "missing lock entry for container.from")

	_, err = os.Stat(filepath.Join(workdir, workspace.LockFileName))
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func (LockfileSuite) TestGitBranchPinnedRefreshesFloatEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
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
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeQueryDoc(t, workdir, "git-branch.graphql", gitBranchCommitQuery)

	_, _ = writeGitRefLock(t, workdir, "git.branch", lockTestGitBranchName, lockTestGitBranchCommit, workspace.PolicyFloat)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "query", "--doc", queryPath)
	require.NoError(t, err)
	require.Contains(t, string(out), lockTestGitBranchCommit)
}

func (LockfileSuite) TestLockUpdateCreatesNewFile(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	lockPath := filepath.Join(workdir, workspace.LockFileName)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "lock", "update")
	require.NoError(t, err)

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.Empty(t, lockBytes, "a lockfile with zero entries should not be serialized with a version")
}

func (LockfileSuite) TestLockUpdateRefreshesExistingEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
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
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	lockPath, originalLock := writeGitRefLock(t, workdir, "git.branch", lockTestGitBranchName, lockTestGitBranchCommit, workspace.PolicyFloat)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "lock", "update")
	require.NoError(t, err)
	require.Equal(t, "Updated dagger.lock", strings.TrimSpace(string(out)))

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertGitLockEntry(t, lockBytes, "git.branch", []any{lockTestGitRepoURL, lockTestGitBranchName}, workspace.PolicyFloat)
	require.NotContains(t, string(lockBytes), lockTestGitBranchCommit)
}

func (LockfileSuite) TestLiveDiscoversQueryEntries(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeContainerFromQuery(t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=live", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockPath := filepath.Join(workdir, workspace.LockFileName)
	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assertContainerFromLockEntry(t, lockBytes, workspace.PolicyPin)
}

func (LockfileSuite) TestLiveDiscoversGitEntries(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeQueryDoc(t, workdir, "git.graphql", gitBranchAndTagCommitQuery)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=live", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockPath := filepath.Join(workdir, workspace.LockFileName)
	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assertGitLockEntry(t, lockBytes, "git.branch", []any{lockTestGitRepoURL, lockTestGitBranchName}, workspace.PolicyFloat)
	assertGitLockEntry(t, lockBytes, "git.tag", []any{lockTestGitRepoURL, lockTestGitTagName}, workspace.PolicyPin)
}

func (LockfileSuite) TestLiveNestedQuery(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	updated := workspaceBase(t, c).
		WithNewFile("query.graphql", containerFromQuery).
		With(daggerExec("--silent", "--lock=live", "query", "--doc", "query.graphql"))

	_, err := updated.Stdout(ctx)
	require.NoError(t, err)

	lockContents, err := updated.File("/work/dagger.lock").Contents(ctx)
	require.NoError(t, err)
	assertContainerFromLockEntry(t, []byte(lockContents), workspace.PolicyPin)
}

func (LockfileSuite) TestLiveModuleCall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := moduleEntrypointFixture(t, c, "lockmod", "dang/lockmod")

	updated := base.With(daggerExec("--silent", "--lock=live", "call", "release"))
	out, err := updated.Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(out))

	lockContents, err := updated.File("/work/dagger.lock").Contents(ctx)
	require.NoError(t, err)
	assertContainerFromLockEntry(t, []byte(lockContents), workspace.PolicyPin)

	frozen := updated.With(daggerExec("--silent", "--lock=frozen", "call", "release"))
	out, err = frozen.Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(out))

	lockContentsAfter, err := frozen.File("/work/dagger.lock").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, lockContents, lockContentsAfter)
}

func (LockfileSuite) TestWorkspaceUpdate(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	lockPath, originalLock := writeContainerFromLock(t, workdir, lockTestPlatform(ctx, t), "sha256:"+strings.Repeat("0", 64), workspace.PolicyFloat)
	updateQueryPath := writeQueryDoc(t, workdir, "update.graphql", workspaceUpdateQuery)
	updateExportQueryPath := writeQueryDoc(t, workdir, "update-export.graphql", workspaceUpdateExportQuery)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", updateQueryPath)
	require.NoError(t, err)
	require.Contains(t, string(out), "dagger.lock")

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

func (LockfileSuite) TestWorkspaceUpdateCreatesLockfile(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	updateQueryPath := writeQueryDoc(t, workdir, "update.graphql", `{
  currentWorkspace {
    update {
      addedPaths
    }
  }
}
`)
	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", updateQueryPath)
	require.NoError(t, err)
	require.Contains(t, string(out), "dagger.lock")
}

func (LockfileSuite) TestWorkspaceUpdateNestedQuery(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	staleLock := mustMarshalContainerFromLock(t, lockTestPlatform(ctx, t), "sha256:"+strings.Repeat("1", 64), workspace.PolicyFloat)
	updated := nativeWorkspaceBase(t, c).
		WithNewFile("dagger.lock", staleLock).
		WithNewFile("update.graphql", workspaceUpdateExportQuery).
		With(daggerExec("--silent", "query", "--doc", "update.graphql"))

	_, err := updated.Stdout(ctx)
	require.NoError(t, err)

	lockContents, err := updated.File("/work/dagger.lock").Contents(ctx)
	require.NoError(t, err)
	require.NotEqual(t, staleLock, lockContents)
	assertContainerFromLockEntry(t, []byte(lockContents), workspace.PolicyFloat)
}

func (LockfileSuite) TestWorkspaceModuleLockUpdate(ctx context.Context, t *testctx.T) {
	t.Run("top-level update is a no-op with empty workspace config", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := nativeWorkspaceBase(t, c)

		ctr = ctr.With(daggerExecRaw("update"))
		out, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Updated dagger.lock", strings.TrimSpace(out))

		out, err = ctr.With(daggerExecRaw("update")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Lockfile already up to date", strings.TrimSpace(out))

		lockContents, err := ctr.File("dagger.lock").Contents(ctx)
		require.NoError(t, err)
		require.Empty(t, lockContents)
	})
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

	lockPath := filepath.Join(workdir, workspace.LockFileName)

	lockContents := mustMarshalContainerFromLock(t, platform, digest, policy)
	require.NoError(t, os.WriteFile(lockPath, []byte(lockContents), 0o600))

	// a valid workspace must contain a dagger.toml file
	configPath := filepath.Join(workdir, "dagger.toml")
	require.NoError(t, os.WriteFile(configPath, []byte{}, 0o600))

	return lockPath, lockContents
}

func writeEmptyWorkspaceConfig(t *testctx.T, workdir string) {
	t.Helper()

	// a valid workspace must contain a dagger.toml file
	configPath := filepath.Join(workdir, "dagger.toml")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(configPath, []byte{}, 0o600))
}

func writeGitRefLock(t *testctx.T, workdir, operation, name, commit string, policy workspace.LockPolicy) (string, string) {
	t.Helper()

	lockPath := filepath.Join(workdir, workspace.LockFileName)

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

func assertNoModuleResolveLockEntry(t *testctx.T, lockBytes []byte) {
	t.Helper()
	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)

	for _, entry := range parsed.Entries() {
		require.NotEqual(t, "modules.resolve", entry.Operation)
	}
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
