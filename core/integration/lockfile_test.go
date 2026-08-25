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
	"encoding/base64"
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

const (
	lockTestGitRepoURL      = "https://github.com/dagger/dagger.git"
	lockTestGitBranchName   = "main"
	lockTestGitBranchCommit = "c80ac2c13df7d573a069938e01ca13f7a81f0345"
	lockTestGitTagName      = "v0.18.2"
	lockTestGitTagCommit    = "0b46ea3c49b5d67509f67747742e5d8b24be9ef7"
	lockTestGitStaleCommit  = "9ea5ea7c848fef2a2c47cce0716d5fcb8d6bedeb"
)

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

func (LockfileSuite) TestDefaultUsesPinEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeContainerFromQuery(t, workdir)

	_, _ = writeOCISHALock(t, workdir, "not-a-digest")

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `invalid lock digest "not-a-digest"`)
}

func hostGitInit(t *testctx.T, dir string) {
	gitCmd := exec.Command("git", "init")
	gitCmd.Dir = dir
	out, err := gitCmd.CombinedOutput()
	require.NoError(t, err, out)
}

func (LockfileSuite) TestDefaultRejectsV1Lockfile(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeContainerFromQuery(t, workdir)
	lockPath := filepath.Join(workdir, workspace.LockFileName)
	lockContents := strings.Join([]string{
		`[["version","1"]]`,
		`["","container.from",["alpine:latest"],"not-a-digest","pin"]`,
	}, "\n")
	require.NoError(t, os.WriteFile(lockPath, []byte(lockContents), 0o600))

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.ErrorContains(t, err, `unsupported lockfile version "1"`)
}

func (LockfileSuite) TestDefaultRemoteCommitDoesNotMutateLock(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	lockContents := mustMarshalOCISHALock(t, "not-a-digest")
	remote := newRemoteLockWorkspace(ctx, t, c, lockContents)
	workdir := t.TempDir()
	queryPath := writeContainerFromQuery(t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "-W", remote.commitRef, "query", "--doc", queryPath)
	require.NoError(t, err)
	committedLock, err := c.Git(remote.repoURL).Commit(remote.commit).Tree().File(workspace.LockFileName).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, lockContents, committedLock)
}

func (LockfileSuite) TestUpdateCreatesNewFile(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	lockPath := filepath.Join(workdir, workspace.LockFileName)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "update")
	require.NoError(t, err)

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.Empty(t, lockBytes, "a lockfile with zero entries should not be serialized with a version")
}

func (LockfileSuite) TestUpdateRefreshesExistingEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	lockPath, originalLock := writeOCISHALock(t, workdir, "sha256:"+strings.Repeat("0", 64))

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "update")
	require.NoError(t, err)

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertOCISHALockEntry(t, lockBytes)
}

func (LockfileSuite) TestUpdateRefreshesExistingGitEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	lockPath, originalLock := writeGitRefLock(t, workdir, "git.branch", lockTestGitBranchName, lockTestGitBranchCommit)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "update")
	require.NoError(t, err)
	require.Equal(t, "Updated dagger.lock", strings.TrimSpace(string(out)))

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertGitLockEntry(t, lockBytes, []any{
		lockTestGitRepoURL,
		"refs/heads/" + lockTestGitBranchName,
	})
	require.NotContains(t, string(lockBytes), lockTestGitBranchCommit)
}

func (LockfileSuite) TestDefaultDiscoversQueryEntries(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeContainerFromQuery(t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockPath := filepath.Join(workdir, workspace.LockFileName)
	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assertOCISHALockEntry(t, lockBytes)
}

func (LockfileSuite) TestDefaultDiscoversGitEntries(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeQueryDoc(t, workdir, "git.graphql", gitBranchAndTagCommitQuery)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockPath := filepath.Join(workdir, workspace.LockFileName)
	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assertGitLockEntry(t, lockBytes, []any{
		lockTestGitRepoURL,
		"refs/heads/" + lockTestGitBranchName,
	})
	assertGitLockEntry(t, lockBytes, []any{
		lockTestGitRepoURL,
		"refs/tags/" + lockTestGitTagName,
	})
}

func (LockfileSuite) TestDefaultNestedQuery(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	updated := workspaceBase(t, c).
		WithNewFile("query.graphql", containerFromQuery).
		With(daggerExec("--silent", "query", "--doc", "query.graphql"))

	_, err := updated.Stdout(ctx)
	require.NoError(t, err)

	lockContents, err := updated.File("/work/dagger.lock").Contents(ctx)
	require.NoError(t, err)
	assertOCISHALockEntry(t, []byte(lockContents))
}

func (LockfileSuite) TestDefaultModuleCall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := moduleEntrypointFixture(t, c, "lockmod", "dang/lockmod")

	updated := base.With(daggerExec("--silent", "call", "release"))
	out, err := updated.Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(out))

	lockContents, err := updated.File("/work/dagger.lock").Contents(ctx)
	require.NoError(t, err)
	assertOCISHALockEntry(t, []byte(lockContents))

	reused := updated.With(daggerExec("--silent", "call", "release"))
	out, err = reused.Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(out))

	lockContentsAfter, err := reused.File("/work/dagger.lock").Contents(ctx)
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

func writeOCISHALock(t *testctx.T, workdir, digest string) (string, string) {
	t.Helper()

	lockPath := filepath.Join(workdir, workspace.LockFileName)

	lockContents := mustMarshalOCISHALock(t, digest)
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

func writeGitRefLock(t *testctx.T, workdir, operation, name, commit string) (string, string) {
	t.Helper()

	lockPath := filepath.Join(workdir, workspace.LockFileName)

	lockContents := mustMarshalGitRefLock(t, operation, name, commit)
	require.NoError(t, os.WriteFile(lockPath, []byte(lockContents), 0o600))
	return lockPath, lockContents
}

func mustMarshalOCISHALock(t *testctx.T, digest string) string {
	t.Helper()

	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup("", "oci-sha", []any{"docker.io/library/alpine:latest"}, digest))

	lockBytes, err := lock.Marshal()
	require.NoError(t, err)
	return string(lockBytes)
}

func mustMarshalGitRefLock(t *testctx.T, operation, name, commit string) string {
	t.Helper()

	selector := name
	switch operation {
	case "git.head":
		selector = "HEAD"
	case "git.branch":
		selector = "refs/heads/" + strings.TrimPrefix(name, "refs/heads/")
	case "git.tag":
		selector = "refs/tags/" + strings.TrimPrefix(name, "refs/tags/")
	case "git.ref":
	default:
		require.FailNow(t, "unsupported Git lock operation", operation)
	}

	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup("", "git-sha", []any{lockTestGitRepoURL, selector}, commit))

	lockBytes, err := lock.Marshal()
	require.NoError(t, err)
	return string(lockBytes)
}

func assertOCISHALockEntry(t *testctx.T, lockBytes []byte) {
	t.Helper()
	require.True(t, strings.HasPrefix(string(lockBytes), `[["version","2"]]`), "lockfile: %q", string(lockBytes))
	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)

	var found bool
	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != "oci-sha" {
			continue
		}
		found = true
		require.Len(t, entry.Inputs, 1)

		ref, ok := entry.Inputs[0].(string)
		require.True(t, ok)
		require.Contains(t, ref, "alpine:latest")

		value := entry.Value
		require.True(t, strings.HasPrefix(value, "sha256:"))
	}

	require.True(t, found, "expected oci-sha entry in lockfile")
}

func assertGitLockEntry(t *testctx.T, lockBytes []byte, expectedInputs []any) {
	t.Helper()
	require.True(t, strings.HasPrefix(string(lockBytes), `[["version","2"]]`), "lockfile: %q", string(lockBytes))
	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)

	var found bool
	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != "git-sha" {
			continue
		}
		if !equalLockInputs(entry.Inputs, expectedInputs) {
			continue
		}

		found = true
		result := entry.Value
		require.Len(t, result, 40)
	}

	require.True(t, found, "expected git-sha entry in lockfile")
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

func (LockfileSuite) TestGitLatestCreatesPin(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeQueryDoc(t, workdir, "git-latest.graphql", gitLatestCommitQuery)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockBytes, err := os.ReadFile(filepath.Join(workdir, workspace.LockFileName))
	require.NoError(t, err)
	assertGitLatestLockEntry(t, lockBytes)
}

func (LockfileSuite) TestGitLatestUsesPin(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeQueryDoc(t, workdir, "git-latest.graphql", gitLatestCommitQuery)
	writeGitLatestLock(t, workdir, "refs/tags/"+lockTestGitTagName+"@"+lockTestGitStaleCommit)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)
	require.Contains(t, string(out), "refs/tags/"+lockTestGitTagName)
	require.Contains(t, string(out), lockTestGitStaleCommit)
}

func (LockfileSuite) TestGitLatestPinnedDoesNotLoadRemoteMetadata(ctx context.Context, t *testctx.T) {
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
		"query",
		"--doc",
		queryPath,
	)
	require.NoError(t, err)
	require.Contains(t, string(out), "refs/tags/v1.2.3")
	require.Contains(t, string(out), pinnedCommit)
}

// Unlike git://, an https:// URL without explicit auth makes the parent git
// resolver probe the remote for visibility before latest can use the workspace
// pin. That probe must not fail the query when the remote is unreachable.
func (LockfileSuite) TestGitLatestPinnedHTTPSUnavailableRemoteUsesPin(ctx context.Context, t *testctx.T) {
	const unavailableRemote = "https://git.example.invalid/dagger.git"
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
		"query",
		"--doc",
		queryPath,
	)
	require.NoError(t, err)
	require.Contains(t, string(out), "refs/tags/v1.2.3")
	require.Contains(t, string(out), pinnedCommit)
}

func (LockfileSuite) TestGitLatestPinnedRejectsInvalidRef(ctx context.Context, t *testctx.T) {
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
		"query",
		"--doc",
		queryPath,
	)
	require.ErrorContains(t, err, `invalid git-latest ref "refs/pull/1/head"`)
}

// dagger update must refresh git-latest entries for private repositories: the
// lock stores only the remote URL, so the update path has to recover the same
// credential-helper access that created the pin.
func (LockfileSuite) TestUpdateRefreshesPrivateGitLatestEntry(ctx context.Context, t *testctx.T) {
	const privateRepoURL = "https://github.com/grouville/daggerverse-private.git"
	stalePin := "refs/heads/main@" + strings.Repeat("0", 40)

	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	lockPath, originalLock := writeGitLatestLockForRemote(t, workdir, privateRepoURL, stalePin)

	// same committed read-only PAT as TestGitCredentialErrors
	encodedPAT := "Z2l0aHViX3BhdF8xMUFIUlpENFEwMnVKQm5ESVBNZ0h5X2lHYUVPZTZaR2xOTjB4Y2o2WEdRWjNSalhwdHQ0c2lSMmw0aUJTellKUmFKUFdERlNUVU1hRXlDYXNQCg=="
	decodedPAT, err := base64.StdEncoding.DecodeString(encodedPAT)
	require.NoError(t, err)
	token := strings.TrimSpace(string(decodedPAT))

	gitConfigPath := filepath.Join(workdir, ".gitconfig")
	require.NoError(t, os.WriteFile(gitConfigPath, []byte(makeGitCredentials("github.com", "x-token-auth", token)), 0o600))

	cmd := hostDaggerCommandRaw(ctx, t, workdir, "--silent", "update")
	cmd.Env = append(cmd.Env,
		"GIT_CONFIG_GLOBAL="+gitConfigPath,
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	require.NotContains(t, string(lockBytes), stalePin)
}

func (LockfileSuite) TestUpdateRefreshesExistingGitLatestEntry(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	staleCommit := strings.Repeat("0", 40)
	lockPath, originalLock := writeGitLatestLock(
		t,
		workdir,
		"refs/tags/"+lockTestGitTagName+"@"+staleCommit,
	)

	out, err := hostDaggerExec(ctx, t, workdir, "--progress=plain", "update")
	require.NoError(t, err)
	require.Contains(t, string(out), "git tag points to a different commit")
	require.Contains(t, string(out), "Updated dagger.lock")

	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.NotEqual(t, originalLock, string(lockBytes))
	assertGitLatestLockEntry(t, lockBytes)
	assertGitLockEntryResult(t, lockBytes, []any{
		lockTestGitRepoURL,
		"refs/tags/" + lockTestGitTagName,
	}, lockTestGitTagCommit)
	require.NotContains(t, string(lockBytes), staleCommit)
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
	ref, commit, found := strings.Cut(pin, "@")
	require.True(t, found)
	require.NoError(t, lock.SetLookup("", "git-latest", []any{remoteURL}, ref))
	require.NoError(t, lock.SetLookup("", "git-sha", []any{remoteURL, ref}, commit))
	lockBytes, err := lock.Marshal()
	require.NoError(t, err)

	lockPath := filepath.Join(workdir, workspace.LockFileName)
	require.NoError(t, os.WriteFile(lockPath, lockBytes, 0o600))
	return lockPath, string(lockBytes)
}

func assertGitLockEntryResult(
	t *testctx.T,
	lockBytes []byte,
	expectedInputs []any,
	expectedResult string,
) {
	t.Helper()

	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)
	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != "git-sha" {
			continue
		}
		if !equalLockInputs(entry.Inputs, expectedInputs) {
			continue
		}
		result := entry.Value
		require.Equal(t, expectedResult, result)
		return
	}
	require.FailNow(t, "expected git-sha entry")
}

func assertGitLatestLockEntry(t *testctx.T, lockBytes []byte) {
	t.Helper()

	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)
	var selectedRef string
	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != "git-latest" {
			continue
		}
		require.Equal(t, []any{lockTestGitRepoURL}, entry.Inputs)
		selectedRef = entry.Value
		require.True(t, strings.HasPrefix(selectedRef, "refs/tags/"), selectedRef)
		break
	}
	require.NotEmpty(t, selectedRef, "expected git-latest entry in lockfile")
	assertGitLockEntry(t, lockBytes, []any{lockTestGitRepoURL, selectedRef})
}

const ociLatestImageRefQuery = `{
  container {
    from(address: "alpine") {
      imageRef
    }
  }
}
`

func (LockfileSuite) TestOCILatestLockLifecycle(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	queryPath := writeQueryDoc(t, workdir, "latest-image-ref.graphql", ociLatestImageRefQuery)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)

	lockPath := filepath.Join(workdir, workspace.LockFileName)
	lockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	pin := assertOCILatestLockEntry(t, lockBytes)
	require.Contains(t, string(out), pin)

	pinnedOut, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)
	require.Contains(t, string(pinnedOut), pin)

	pinnedLockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	require.Equal(t, lockBytes, pinnedLockBytes)

	staleSelectedRef := "docker.io/library/alpine:3.20"
	staleSelectedDigest := "sha256:" + strings.Repeat("0", 64)
	stalePin := staleSelectedRef + "@" + staleSelectedDigest
	staleLatestDigest := "sha256:" + strings.Repeat("1", 64)
	writeOCILatestLock(t, workdir, stalePin, staleLatestDigest)

	_, err = hostDaggerExec(ctx, t, workdir, "--silent", "update")
	require.NoError(t, err)

	updatedLockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	updatedPin := assertOCILatestLockEntry(t, updatedLockBytes)
	require.NotEqual(t, stalePin, updatedPin)
	require.NotEqual(
		t,
		staleSelectedDigest,
		requireOCISHALockValue(t, updatedLockBytes, staleSelectedRef),
	)
	require.NotEqual(
		t,
		staleLatestDigest,
		requireOCISHALockValue(t, updatedLockBytes, "docker.io/library/alpine:latest"),
	)
}

func writeOCILatestLock(
	t *testctx.T,
	workdir,
	pin,
	latestDigest string,
) {
	t.Helper()

	ref, imageDigest, found := strings.Cut(pin, "@")
	require.True(t, found)
	tag := strings.TrimPrefix(ref, "docker.io/library/alpine:")
	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup(
		"",
		"oci-latest",
		[]any{"docker.io/library/alpine"},
		tag,
	))
	require.NoError(t, lock.SetLookup(
		"",
		"oci-sha",
		[]any{"docker.io/library/alpine:latest"},
		latestDigest,
	))
	require.NoError(t, lock.SetLookup(
		"",
		"oci-sha",
		[]any{ref},
		imageDigest,
	))
	lockBytes, err := lock.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(workdir, workspace.LockFileName),
		lockBytes,
		0o600,
	))
}

func requireOCISHALockValue(t *testctx.T, lockBytes []byte, ref string) string {
	t.Helper()

	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)
	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != "oci-sha" {
			continue
		}
		if !equalLockInputs(entry.Inputs, []any{ref}) {
			continue
		}
		return entry.Value
	}
	require.FailNow(t, "expected oci-sha entry", ref)
	return ""
}

func assertOCILatestLockEntry(t *testctx.T, lockBytes []byte) string {
	t.Helper()

	parsed, err := lockfile.Parse(lockBytes)
	require.NoError(t, err)

	var selectedTag string
	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != "oci-latest" {
			continue
		}
		require.Len(t, entry.Inputs, 1)
		require.Equal(t, "docker.io/library/alpine", entry.Inputs[0])
		selectedTag = entry.Value

		version := selectedTag
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		require.True(t, semver.IsValid(version), "expected semantic-version tag in %q", selectedTag)
		require.Empty(t, semver.Prerelease(version), "expected stable tag in %q", selectedTag)
		break
	}

	require.NotEmpty(t, selectedTag, "expected oci-latest entry in lockfile")
	taggedRef := "docker.io/library/alpine:" + selectedTag
	for _, entry := range parsed.Entries() {
		if entry.Namespace != "" || entry.Operation != "oci-sha" {
			continue
		}
		if !equalLockInputs(entry.Inputs, []any{taggedRef}) {
			continue
		}
		imageDigest := entry.Value
		require.True(t, strings.HasPrefix(imageDigest, "sha256:"), imageDigest)
		return taggedRef + "@" + imageDigest
	}
	require.FailNow(t, "expected oci-sha entry for selected tag")
	return ""
}
