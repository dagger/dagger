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
	require.Equal(t, PolicyFloat, result.Policy)

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
		Policy: PolicyFloat,
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
			Policy: PolicyFloat,
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
	require.Equal(t, LookupResult{Value: "0123456789abcdef0123456789abcdef01234567", Policy: PolicyFloat}, result)
}

func TestDiff(t *testing.T) {
	base := NewLock()
	require.NoError(t, base.SetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"}, LookupResult{
		Value:  "sha256:deadbeef",
		Policy: PolicyPin,
	}))
	require.NoError(t, base.SetLookup("", "git.tag", []any{"https://github.com/dagger/dagger.git", "v0.1.0"}, LookupResult{
		Value:  "0123456789abcdef0123456789abcdef01234567",
		Policy: PolicyPin,
	}))

	updated, err := base.Clone()
	require.NoError(t, err)
	// New entry: should appear in the diff.
	require.NoError(t, updated.SetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"}, LookupResult{
		Value:  "89abcdef0123456789abcdef0123456789abcdef",
		Policy: PolicyFloat,
	}))
	// Changed result for an existing tuple: should appear in the diff.
	require.NoError(t, updated.SetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"}, LookupResult{
		Value:  "sha256:cafebabe",
		Policy: PolicyPin,
	}))

	diff, err := updated.Diff(base)
	require.NoError(t, err)

	entries, err := diff.Entries()
	require.NoError(t, err)
	require.Len(t, entries, 2)

	result, ok, err := diff.GetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, LookupResult{Value: "89abcdef0123456789abcdef0123456789abcdef", Policy: PolicyFloat}, result)

	result, ok, err = diff.GetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, LookupResult{Value: "sha256:cafebabe", Policy: PolicyPin}, result)

	// Unchanged entries stay out of the diff.
	_, ok, err = diff.GetLookup("", "git.tag", []any{"https://github.com/dagger/dagger.git", "v0.1.0"})
	require.NoError(t, err)
	require.False(t, ok)

	// A diff against an empty base contains everything.
	full, err := updated.Diff(NewLock())
	require.NoError(t, err)
	fullEntries, err := full.Entries()
	require.NoError(t, err)
	require.Len(t, fullEntries, 3)

	// Diffing identical locks yields nothing.
	empty, err := base.Diff(base)
	require.NoError(t, err)
	emptyEntries, err := empty.Entries()
	require.NoError(t, err)
	require.Empty(t, emptyEntries)
}

func TestParseLockMode(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		mode, err := ParseLockMode("disabled")
		require.NoError(t, err)
		require.Equal(t, LockModeDisabled, mode)
	})

	t.Run("live", func(t *testing.T) {
		mode, err := ParseLockMode("live")
		require.NoError(t, err)
		require.Equal(t, LockModeLive, mode)
	})

	t.Run("pinned", func(t *testing.T) {
		mode, err := ParseLockMode("pinned")
		require.NoError(t, err)
		require.Equal(t, LockModePinned, mode)
	})

	t.Run("frozen", func(t *testing.T) {
		mode, err := ParseLockMode("frozen")
		require.NoError(t, err)
		require.Equal(t, LockModeFrozen, mode)
	})

	t.Run("legacy update alias", func(t *testing.T) {
		mode, err := ParseLockMode("update")
		require.NoError(t, err)
		require.Equal(t, LockModeLive, mode)
	})

	t.Run("legacy auto alias", func(t *testing.T) {
		mode, err := ParseLockMode("auto")
		require.NoError(t, err)
		require.Equal(t, LockModePinned, mode)
	})

	t.Run("legacy strict alias", func(t *testing.T) {
		mode, err := ParseLockMode("strict")
		require.NoError(t, err)
		require.Equal(t, LockModeFrozen, mode)
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := ParseLockMode("weird")
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid lock mode")
	})
}

func TestResolveLockMode(t *testing.T) {
	mode, err := ResolveLockMode("")
	require.NoError(t, err)
	require.Equal(t, DefaultLockMode, mode)
}
