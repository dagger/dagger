package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalLockFilePath(t *testing.T) {
	require.Equal(t, "dagger.lock", CanonicalLockFilePath(filepath.Join(".dagger", "lock")))
	require.Equal(t, filepath.Join("app", "dagger.lock"), CanonicalLockFilePath(filepath.Join("app", ".dagger", "lock")))
	require.Equal(t, filepath.Join("app", "dagger.lock"), CanonicalLockFilePath(filepath.Join("app", "dagger.lock")))
	require.Equal(t, filepath.Join("app", "lock"), CanonicalLockFilePath(filepath.Join("app", "lock")))
}

func TestLookupSetGet(t *testing.T) {
	lock := NewLock()
	inputs := []any{"alpine:latest", "linux/amd64"}

	require.NoError(t, lock.SetLookup("", "oci-sha", inputs, "sha256:deadbeef"))

	result, ok := lock.GetLookup("", "oci-sha", inputs)
	require.True(t, ok)
	require.Equal(t, "sha256:deadbeef", result)
}

func TestGitLookupNormalizesTransport(t *testing.T) {
	t.Parallel()

	lock := NewLock()
	const commit = "0123456789abcdef0123456789abcdef01234567"
	reference := []any{"https://GitHub.com/acme/api.git", "refs/heads/main"}
	require.NoError(t, lock.SetLookup("", LockOperationGitSHA, reference, commit))

	for _, remote := range []string{
		"github.com/acme/api",
		"https://github.com/acme/api.git",
		"ssh://git@github.com/acme/api.git",
		"git@github.com:acme/api.git",
	} {
		value, ok := lock.GetLookup("", LockOperationGitSHA, []any{remote, "refs/heads/main"})
		require.True(t, ok, remote)
		require.Equal(t, commit, value)
	}

	entries := lock.Entries()
	require.Len(t, entries, 1)
	require.Equal(t, []any{"github.com/acme/api", "refs/heads/main"}, entries[0].Inputs)
}

func TestParseGitLookupNormalizesLegacyTransport(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		`[["version","2"]]`,
		`["","git-latest",["https://github.com/acme/api.git"],"refs/heads/main"]`,
		`["","git-sha",["git@github.com:acme/api.git","refs/heads/main"],"0123456789abcdef0123456789abcdef01234567"]`,
	}, "\n")

	lock, err := ParseLock([]byte(input))
	require.NoError(t, err)
	value, ok := lock.GetLookup("", LockOperationGitLatest, []any{"ssh://git@github.com/acme/api"})
	require.True(t, ok)
	require.Equal(t, "refs/heads/main", value)
	value, ok = lock.GetLookup("", LockOperationGitSHA, []any{"https://github.com/acme/api", "refs/heads/main"})
	require.True(t, ok)
	require.Equal(t, "0123456789abcdef0123456789abcdef01234567", value)

	output, err := lock.Marshal()
	require.NoError(t, err)
	require.Contains(t, string(output), `"github.com/acme/api"`)
	require.NotContains(t, string(output), `https://github.com/acme/api.git`)
	require.NotContains(t, string(output), `git@github.com:acme/api.git`)
}

func TestParseGitLookupCollapsesEquivalentLegacyEntries(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		`[["version","2"]]`,
		`["","git-latest",["https://github.com/acme/api.git"],"refs/heads/main"]`,
		`["","git-latest",["ssh://git@github.com/acme/api"],"refs/heads/main"]`,
	}, "\n")

	lock, err := ParseLock([]byte(input))
	require.NoError(t, err)
	require.Len(t, lock.Entries(), 1)
}

func TestParseGitLookupRejectsConflictingEquivalentEntries(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		`[["version","2"]]`,
		`["","git-latest",["https://github.com/acme/api.git"],"refs/heads/main"]`,
		`["","git-latest",["ssh://git@github.com/acme/api"],"refs/heads/release"]`,
	}, "\n")

	_, err := ParseLock([]byte(input))
	require.ErrorIs(t, err, ErrGitLockIdentityConflict)
	require.ErrorContains(t, err, "different values for git-latest inputs")
	require.ErrorContains(t, LockfileMergeConflictError(err), "workspace lockfile contains conflicting pins for the same Git repository")
}

func TestLookupConcurrentWrites(t *testing.T) {
	t.Parallel()

	lock := NewLock()
	const writes = 100
	errs := make(chan error, writes)
	var wg sync.WaitGroup
	for i := range writes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- lock.SetLookup(
				"",
				"git-sha",
				[]any{"repo", fmt.Sprint(i)},
				fmt.Sprintf("%040d", i),
			)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	entries := lock.Entries()
	require.Len(t, entries, writes)
}

func TestParseLockRejectsV1(t *testing.T) {
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["","container.from",["alpine:latest","linux/amd64"],"sha256:deadbeef","weird"]`,
	}, "\n")

	_, err := ParseLock([]byte(input))
	require.ErrorContains(t, err, `unsupported lockfile version "1"`)
}

func TestFutureLockfileVersionError(t *testing.T) {
	_, err := ParseLock([]byte(`[["version","3"]]`))
	require.EqualError(t, FutureLockfileVersionError(err),
		`lockfile version "3" is newer than supported version "2"; upgrade Dagger to continue`,
	)

	_, err = ParseLock([]byte(`[["version","1"]]`))
	require.NoError(t, FutureLockfileVersionError(err))
}

func TestLockfileMergeConflictError(t *testing.T) {
	_, err := ParseLock([]byte("<<<<<<< HEAD\n=======\n>>>>>>> branch\n"))
	require.EqualError(t, LockfileMergeConflictError(err),
		"workspace lockfile contains merge conflict markers; resolve the conflict before running Dagger",
	)

	_, err = ParseLock([]byte(`[["version","2"]]`))
	require.NoError(t, LockfileMergeConflictError(err))
}

func TestEntries(t *testing.T) {
	lock := NewLock()
	inputs := []any{"alpine:latest", "linux/amd64"}

	require.NoError(t, lock.SetLookup("", "oci-sha", inputs, "sha256:deadbeef"))

	entries := lock.Entries()
	require.Len(t, entries, 1)
	require.Equal(t, LookupEntry{
		Namespace: "",
		Operation: "oci-sha",
		Inputs:    inputs,
		Value:     "sha256:deadbeef",
	}, entries[0])
}

func TestClone(t *testing.T) {
	lock := NewLock()
	require.NoError(t, lock.SetLookup("", "oci-sha", []any{"alpine:latest"}, "sha256:deadbeef"))

	cloned, err := lock.Clone()
	require.NoError(t, err)

	require.NoError(t, cloned.SetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"}, "0123456789abcdef0123456789abcdef01234567"))

	_, ok := lock.GetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"})
	require.False(t, ok)
}

func TestMerge(t *testing.T) {
	base := NewLock()
	require.NoError(t, base.SetLookup("", "oci-sha", []any{"alpine:latest"}, "sha256:deadbeef"))

	delta := NewLock()
	require.NoError(t, delta.SetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"}, "0123456789abcdef0123456789abcdef01234567"))

	require.NoError(t, base.Merge(delta))

	result, ok := base.GetLookup("", "oci-sha", []any{"alpine:latest"})
	require.True(t, ok)
	require.Equal(t, "sha256:deadbeef", result)

	result, ok = base.GetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"})
	require.True(t, ok)
	require.Equal(t, "0123456789abcdef0123456789abcdef01234567", result)
}
