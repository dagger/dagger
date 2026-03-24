package workspace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestModuleResolveSetGet(t *testing.T) {
	lock := NewLock()
	source := "github.com/dagger/dagger@main"

	require.NoError(t, lock.SetModuleResolve(source, LookupResult{
		Value:  "0123456789abcdef0123456789abcdef01234567",
		Policy: PolicyFloat,
	}))

	result, ok, err := lock.GetModuleResolve(source)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "0123456789abcdef0123456789abcdef01234567", result.Value)
	require.Equal(t, PolicyFloat, result.Policy)
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
