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

func TestLookupSetGetDelete(t *testing.T) {
	lock := NewLock()
	inputs := []any{"alpine:latest", "linux/amd64"}

	require.NoError(t, lock.SetLookup("", "container.from", inputs, LookupResult{
		Value:  "sha256:deadbeef",
		Policy: PolicyFloat,
	}))

	result, ok, err := lock.GetLookup("", "container.from", inputs)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "sha256:deadbeef", result.Value)
	require.Equal(t, PolicyPin, result.Policy)

	require.True(t, lock.DeleteLookup("", "container.from", inputs))
	_, ok, err = lock.GetLookup("", "container.from", inputs)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestLookupSetValidation(t *testing.T) {
	lock := NewLock()

	err := lock.SetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"}, LookupResult{
		Value:  "sha256:deadbeef",
		Policy: LockPolicy("weird"),
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid lock policy")
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
			errs <- lock.SetLookup("", "git.ref", []any{"repo", fmt.Sprint(i)}, LookupResult{
				Value:  fmt.Sprintf("%040d", i),
				Policy: PolicyPin,
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	entries, err := lock.Entries()
	require.NoError(t, err)
	require.Len(t, entries, writes)
}

func TestLookupGetValidation(t *testing.T) {
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["","container.from",["alpine:latest","linux/amd64"],"sha256:deadbeef","weird"]`,
	}, "\n")

	lock, err := ParseLock([]byte(input))
	require.NoError(t, err)

	_, _, err = lock.GetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"})
	require.ErrorContains(t, err, "invalid policy")
}

func TestEntries(t *testing.T) {
	lock := NewLock()
	inputs := []any{"alpine:latest", "linux/amd64"}

	require.NoError(t, lock.SetLookup("", "container.from", inputs, LookupResult{
		Value:  "sha256:deadbeef",
		Policy: PolicyPin,
	}))

	entries, err := lock.Entries()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, LookupEntry{
		Namespace: "",
		Operation: "container.from",
		Inputs:    inputs,
		Result: LookupResult{
			Value:  "sha256:deadbeef",
			Policy: PolicyPin,
		},
	}, entries[0])
}

func TestClone(t *testing.T) {
	lock := NewLock()
	require.NoError(t, lock.SetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"}, LookupResult{
		Value:  "sha256:deadbeef",
		Policy: PolicyPin,
	}))

	cloned, err := lock.Clone()
	require.NoError(t, err)

	require.NoError(t, cloned.SetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"}, LookupResult{
		Value:  "0123456789abcdef0123456789abcdef01234567",
		Policy: PolicyFloat,
	}))

	_, ok, err := lock.GetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"})
	require.NoError(t, err)
	require.False(t, ok)
}

func TestClonePreservesLegacyV1PolicyInMemory(t *testing.T) {
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["","git.branch",["https://github.com/dagger/dagger.git","main"],"0123456789abcdef0123456789abcdef01234567","float"]`,
	}, "\n")
	lock, err := ParseLock([]byte(input))
	require.NoError(t, err)

	cloned, err := lock.Clone()
	require.NoError(t, err)
	result, ok, err := cloned.GetLookup("", "git.ref", []any{
		"https://github.com/dagger/dagger.git",
		"refs/heads/main",
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, PolicyFloat, result.Policy)
	gitResult, err := ParseGitRefLockResult(result.Value)
	require.NoError(t, err)
	require.Equal(t, GitRefLockResult{
		SHA: "0123456789abcdef0123456789abcdef01234567",
		Ref: "refs/heads/main",
	}, gitResult)

	data, err := cloned.Marshal()
	require.NoError(t, err)
	require.Contains(t, string(data), `[["version","2"]]`)
	require.Contains(t, string(data), `"git.ref"`)
	require.NotContains(t, string(data), `"git.branch"`)
	require.NotContains(t, string(data), `"float"`)
}

func TestParseMigratesLegacyGitEntries(t *testing.T) {
	const (
		remote = "https://github.com/dagger/dagger.git"
		sha    = "0123456789abcdef0123456789abcdef01234567"
	)
	tests := []struct {
		name      string
		entry     string
		selector  string
		resultRef string
	}{
		{
			name:     "head does not invent its symbolic ref",
			entry:    `["","git.head",["` + remote + `"],"` + sha + `","pin"]`,
			selector: "HEAD",
		},
		{
			name:      "branch is canonicalized",
			entry:     `["","git.branch",["` + remote + `","main"],"` + sha + `","float"]`,
			selector:  "refs/heads/main",
			resultRef: "refs/heads/main",
		},
		{
			name:      "tag is canonicalized",
			entry:     `["","git.tag",["` + remote + `","v1.2.3"],"` + sha + `","pin"]`,
			selector:  "refs/tags/v1.2.3",
			resultRef: "refs/tags/v1.2.3",
		},
		{
			name:     "generic selector is preserved",
			entry:    `["","git.ref",["` + remote + `","main"],"` + sha + `","pin"]`,
			selector: "main",
		},
		{
			name:      "fully qualified generic ref is retained",
			entry:     `["","git.ref",["` + remote + `","refs/changes/1"],"` + sha + `","pin"]`,
			selector:  "refs/changes/1",
			resultRef: "refs/changes/1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lock, err := ParseLock([]byte(strings.Join([]string{
				`[["version","1"]]`,
				test.entry,
			}, "\n")))
			require.NoError(t, err)

			result, ok, err := lock.GetLookup("", "git.ref", []any{remote, test.selector})
			require.NoError(t, err)
			require.True(t, ok)
			locked, err := ParseGitRefLockResult(result.Value)
			require.NoError(t, err)
			require.Equal(t, sha, locked.SHA)
			require.Equal(t, test.resultRef, locked.Ref)

			data, err := lock.Marshal()
			require.NoError(t, err)
			require.Contains(t, string(data), `[["version","2"]]`)
			require.Contains(t, string(data), `"git.ref"`)
			require.NotContains(t, string(data), `"git.head"`)
			require.NotContains(t, string(data), `"git.branch"`)
			require.NotContains(t, string(data), `"git.tag"`)
		})
	}
}

func TestParseLegacyGitMigrationCollisions(t *testing.T) {
	const (
		remote = "https://github.com/dagger/dagger.git"
		sha    = "0123456789abcdef0123456789abcdef01234567"
	)

	t.Run("matching entries merge and preserve float", func(t *testing.T) {
		input := strings.Join([]string{
			`[["version","1"]]`,
			`["","git.branch",["` + remote + `","main"],"` + sha + `","pin"]`,
			`["","git.ref",["` + remote + `","refs/heads/main"],"` + sha + `","float"]`,
		}, "\n")

		lock, err := ParseLock([]byte(input))
		require.NoError(t, err)
		result, ok, err := lock.GetLookup(
			"",
			"git.ref",
			[]any{remote, "refs/heads/main"},
		)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, PolicyFloat, result.Policy)
	})

	t.Run("different commits conflict", func(t *testing.T) {
		input := strings.Join([]string{
			`[["version","1"]]`,
			`["","git.branch",["` + remote + `","main"],"` + sha + `","pin"]`,
			`["","git.ref",["` + remote + `","refs/heads/main"],"89abcdef0123456789abcdef0123456789abcdef","pin"]`,
		}, "\n")

		_, err := ParseLock([]byte(input))
		require.ErrorContains(t, err, "conflicting lock entries")
	})
}

func TestParseDoesNotMigrateNamespacedGitOperation(t *testing.T) {
	const (
		namespace = "example.com/custom"
		remote    = "https://github.com/dagger/dagger.git"
		sha       = "0123456789abcdef0123456789abcdef01234567"
	)
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["` + namespace + `","git.branch",["` + remote + `","main"],"` + sha + `","pin"]`,
	}, "\n")

	lock, err := ParseLock([]byte(input))
	require.NoError(t, err)

	result, ok, err := lock.GetLookup(
		namespace,
		"git.branch",
		[]any{remote, "main"},
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, sha, result.Value)

	_, ok, err = lock.GetLookup(
		namespace,
		"git.ref",
		[]any{remote, "refs/heads/main"},
	)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestMerge(t *testing.T) {
	base := NewLock()
	require.NoError(t, base.SetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"}, LookupResult{
		Value:  "sha256:deadbeef",
		Policy: PolicyPin,
	}))

	delta := NewLock()
	require.NoError(t, delta.SetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"}, LookupResult{
		Value:  "0123456789abcdef0123456789abcdef01234567",
		Policy: PolicyFloat,
	}))

	require.NoError(t, base.Merge(delta))

	result, ok, err := base.GetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, LookupResult{Value: "sha256:deadbeef", Policy: PolicyPin}, result)

	result, ok, err = base.GetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, LookupResult{Value: "0123456789abcdef0123456789abcdef01234567", Policy: PolicyPin}, result)
}
