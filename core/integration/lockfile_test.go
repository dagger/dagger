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

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/util/lockfile"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/semver"
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

const containerFromImageRefQuery = `{
  container {
    from(address: "alpine:latest") {
      imageRef
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

func (LockfileSuite) TestFromLockfileFrozenRemoteCommitUsesPinEntry(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	lockContents := mustMarshalContainerFromLock(t, lockTestPlatform(ctx, t), "not-a-digest", workspace.PolicyPin)
	remote := newRemoteLockWorkspace(ctx, t, c, lockContents)

	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)
	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "-W", remote.commitRef, "query", "--doc", queryPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `invalid lock digest "not-a-digest"`)
}

func (LockfileSuite) TestFromLockfileFrozenRemoteCommitUsesValidPin(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	imageRef, err := c.Container().From("alpine:3.20").ImageRef(ctx)
	require.NoError(t, err)
	_, digest, found := strings.Cut(imageRef, "@")
	require.True(t, found, "expected canonical image ref with digest: %q", imageRef)
	require.True(t, strings.HasPrefix(digest, "sha256:"), digest)

	lockContents := mustMarshalContainerFromLock(t, lockTestPlatform(ctx, t), digest, workspace.PolicyPin)
	remote := newRemoteLockWorkspace(ctx, t, c, lockContents)
	workdir := t.TempDir()
	queryPath := writeQueryDoc(t, workdir, "image-ref.graphql", containerFromImageRefQuery)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "-W", remote.commitRef, "query", "--doc", queryPath)
	require.NoError(t, err)
	require.Contains(t, string(out), digest)
	require.Contains(t, string(out), "3.20")
}

func (LockfileSuite) TestFromLockfileFrozenRemoteCommitModuleCallUsesValidPin(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	imageRef, err := c.Container().From("alpine:3.20").ImageRef(ctx)
	require.NoError(t, err)
	_, digest, found := strings.Cut(imageRef, "@")
	require.True(t, found, "expected canonical image ref with digest: %q", imageRef)

	lockContents := mustMarshalContainerFromLock(t, lockTestPlatform(ctx, t), digest, workspace.PolicyPin)
	remote := newRemoteWorkspace(ctx, t, c, c.Directory().
		WithNewFile("dagger.toml", `[modules.lockmod]
source = ".dagger/modules/lockmod"
entrypoint = true
`).
		WithNewFile(workspace.LockFileName, lockContents).
		WithDirectory(".dagger/modules/lockmod", c.Host().Directory(testDataPath(t, "modules", "dang", "lockmod"))))

	workdir := t.TempDir()
	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "-W", remote.commitRef, "call", "release")
	require.NoError(t, err)
	require.Contains(t, string(out), "3.20")
}

func (LockfileSuite) TestFromLockfileFrozenRemoteCommitRequiresEntry(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	remote := newRemoteLockWorkspace(ctx, t, c, "")
	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "-W", remote.commitRef, "query", "--doc", queryPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "missing lock entry for container.from")
}

func (LockfileSuite) TestFromLockfileLiveRemoteCommitDoesNotMutateLock(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	lockContents := mustMarshalContainerFromLock(t, lockTestPlatform(ctx, t), "not-a-digest", workspace.PolicyPin)
	remote := newRemoteLockWorkspace(ctx, t, c, lockContents)
	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=live", "-W", remote.commitRef, "query", "--doc", queryPath)
	require.NoError(t, err)
	committedLock, err := c.Git(remote.repoURL).Commit(remote.commit).Tree().File(workspace.LockFileName).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, lockContents, committedLock)
}

func (LockfileSuite) TestFromLockfileFrozenRemoteBranchRemainsUnavailable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	lockContents := mustMarshalContainerFromLock(t, lockTestPlatform(ctx, t), "not-a-digest", workspace.PolicyPin)
	remote := newRemoteLockWorkspace(ctx, t, c, lockContents)
	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "-W", remote.branchRef, "query", "--doc", queryPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "no writable workspace lockfile is available")
	require.NotContains(t, err.Error(), `invalid lock digest "not-a-digest"`)
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

type remoteLockWorkspace struct {
	repoURL   string
	branchRef string
	commitRef string
	commit    string
}

func newRemoteLockWorkspace(ctx context.Context, t *testctx.T, c *dagger.Client, lockContents string) remoteLockWorkspace {
	t.Helper()
	return newRemoteWorkspace(ctx, t, c, c.Directory().
		WithNewFile("dagger.toml", "").
		WithNewFile(workspace.LockFileName, lockContents))
}

func newRemoteWorkspace(ctx context.Context, t *testctx.T, c *dagger.Client, content *dagger.Directory) remoteLockWorkspace {
	t.Helper()
	branchRef := workspaceSelectionRemoteRef(ctx, t, c, content)
	repoURL := strings.TrimSuffix(branchRef, "@main")
	commit, err := c.Git(repoURL).Branch("main").CommitSHA(ctx)
	require.NoError(t, err)
	require.Len(t, commit, 40)
	return remoteLockWorkspace{
		repoURL:   repoURL,
		branchRef: branchRef,
		commitRef: repoURL + "@" + commit,
		commit:    commit,
	}
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

const gitLatestCommitQuery = `{
  git(url: "` + lockTestGitRepoURL + `") {
    latest {
      ref
      commit
    }
  }
}
`

func (LockfileSuite) TestGitLatestLiveCreatesPin(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeQueryDoc(t, workdir, "git-latest.graphql", gitLatestCommitQuery)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=live", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockBytes, err := os.ReadFile(filepath.Join(workdir, workspace.LockFileName))
	require.NoError(t, err)
	assertGitLatestLockEntry(t, lockBytes)
}

func (LockfileSuite) TestGitLatestFrozenUsesPin(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeQueryDoc(t, workdir, "git-latest.graphql", gitLatestCommitQuery)
	writeGitLatestLock(t, workdir, "refs/tags/"+lockTestGitTagName+"@"+lockTestGitTagOldCommit)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "query", "--doc", queryPath)
	require.NoError(t, err)
	require.Contains(t, string(out), "refs/tags/"+lockTestGitTagName)
	require.Contains(t, string(out), lockTestGitTagOldCommit)
}

func (LockfileSuite) TestGitLatestFrozenDoesNotLoadRemoteMetadata(ctx context.Context, t *testctx.T) {
	const unavailableRemote = "git://example.invalid/dagger.git"
	const pinnedCommit = "0123456789abcdef0123456789abcdef01234567"

	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeQueryDoc(t, workdir, "git-latest.graphql", `{
  git(url: "`+unavailableRemote+`") {
    latest {
      ref
      commit
    }
  }
}
`)
	writeGitLatestLockForRemote(
		t,
		workdir,
		unavailableRemote,
		"refs/tags/v1.2.3@"+pinnedCommit,
	)

	out, err := hostDaggerExec(
		ctx,
		t,
		workdir,
		"--silent",
		"--lock=frozen",
		"query",
		"--doc",
		queryPath,
	)
	require.NoError(t, err)
	require.Contains(t, string(out), "refs/tags/v1.2.3")
	require.Contains(t, string(out), pinnedCommit)
}

func (LockfileSuite) TestGitLatestFrozenRejectsInvalidRef(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeQueryDoc(t, workdir, "git-latest.graphql", `{
  git(url: "git://example.invalid/dagger.git") {
    latest { commit }
  }
}`)
	writeGitLatestLockForRemote(
		t,
		workdir,
		"git://example.invalid/dagger.git",
		"refs/pull/1/head@0123456789abcdef0123456789abcdef01234567",
	)

	_, err := hostDaggerExec(
		ctx,
		t,
		workdir,
		"--silent",
		"--lock=frozen",
		"query",
		"--doc",
		queryPath,
	)
	require.ErrorContains(t, err, `invalid git.latest ref "refs/pull/1/head"`)
}

func (LockfileSuite) TestUpdateRefreshesExistingGitLatestEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	lockPath, originalLock := writeGitLatestLock(t, workdir, "refs/tags/"+lockTestGitTagName+"@"+lockTestGitTagOldCommit)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "update")
	require.NoError(t, err)
	require.Equal(t, "Updated dagger.lock", strings.TrimSpace(string(out)))

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertGitLatestLockEntry(t, lockBytes)
	require.NotContains(t, string(lockBytes), lockTestGitTagOldCommit)
}

func writeGitLatestLock(t *testctx.T, workdir, pin string) (string, string) {
	return writeGitLatestLockForRemote(
		t,
		workdir,
		lockTestGitRepoURL,
		pin,
	)
}

func writeGitLatestLockForRemote(
	t *testctx.T,
	workdir,
	remoteURL,
	pin string,
) (string, string) {
	t.Helper()

	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup("", "git.latest", []any{remoteURL}, workspace.LookupResult{
		Value:  pin,
		Policy: workspace.PolicyPin,
	}))
	lockBytes, err := lock.Marshal()
	require.NoError(t, err)

	lockPath := filepath.Join(workdir, workspace.LockFileName)
	require.NoError(t, os.WriteFile(lockPath, lockBytes, 0o600))
	return lockPath, string(lockBytes)
}

func assertGitLatestLockEntry(t *testctx.T, lockBytes []byte) {
	t.Helper()

	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)
	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != "git.latest" {
			continue
		}
		require.Equal(t, []any{lockTestGitRepoURL}, entry.Inputs)
		require.Equal(t, string(workspace.PolicyPin), entry.Policy)
		value, ok := entry.Value.(string)
		require.True(t, ok)
		ref, commit, found := strings.Cut(value, "@")
		require.True(t, found)
		require.True(t, strings.HasPrefix(ref, "refs/tags/") || strings.HasPrefix(ref, "refs/heads/"), ref)
		require.Len(t, commit, 40)
		return
	}
	require.Fail(t, "expected git.latest entry in lockfile")
}

const containerFromLatestImageRefQuery = `{
  container {
    from(address: "alpine") {
      imageRef
    }
  }
}
`

func (LockfileSuite) TestContainerFromLatestLockLifecycle(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeQueryDoc(t, workdir, "latest-image-ref.graphql", containerFromLatestImageRefQuery)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=live", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockPath := filepath.Join(workdir, workspace.LockFileName)
	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	pin := assertContainerFromLatestLockEntry(t, lockBytes)
	require.Contains(t, string(out), pin)

	frozenOut, err := hostDaggerExec(ctx, t, workdir, "--silent", "--lock=frozen", "query", "--doc", queryPath)
	require.NoError(t, err)
	require.Contains(t, string(frozenOut), pin)

	frozenLockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.Equal(t, lockBytes, frozenLockBytes)

	stalePin := "docker.io/library/alpine:1.0.0@sha256:" + strings.Repeat("0", 64)
	writeContainerFromLatestLock(t, workdir, lockTestPlatform(ctx, t), stalePin)

	_, err = hostDaggerExec(ctx, t, workdir, "--silent", "lock", "update")
	require.NoError(t, err)

	updatedLockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	updatedPin := assertContainerFromLatestLockEntry(t, updatedLockBytes)
	require.NotEqual(t, stalePin, updatedPin)
}

func writeContainerFromLatestLock(t *testctx.T, workdir, platform, pin string) {
	t.Helper()

	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup(
		"",
		"container.from.latest",
		[]any{"docker.io/library/alpine", platform},
		workspace.LookupResult{
			Value:  pin,
			Policy: workspace.PolicyPin,
		},
	))
	lockBytes, err := lock.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(workdir, workspace.LockFileName),
		lockBytes,
		0o600,
	))
}

func assertContainerFromLatestLockEntry(t *testctx.T, lockBytes []byte) string {
	t.Helper()

	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)

	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != "container.from.latest" {
			continue
		}
		require.Len(t, entry.Inputs, 2)
		require.Equal(t, "docker.io/library/alpine", entry.Inputs[0])
		require.Equal(t, string(workspace.PolicyPin), entry.Policy)

		value, ok := entry.Value.(string)
		require.True(t, ok)
		tagAndDigest := strings.TrimPrefix(value, "docker.io/library/alpine:")
		tag, imageDigest, found := strings.Cut(tagAndDigest, "@")
		require.True(t, found, "expected tag and digest in %q", value)
		require.True(t, strings.HasPrefix(imageDigest, "sha256:"), imageDigest)

		version := tag
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		require.True(t, semver.IsValid(version), "expected semantic-version tag in %q", value)
		require.Empty(t, semver.Prerelease(version), "expected stable tag in %q", value)
		return value
	}

	require.FailNow(t, "expected container.from.latest entry in lockfile")
	return ""
}
