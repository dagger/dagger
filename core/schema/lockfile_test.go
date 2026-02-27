package schema

import (
	"strings"
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestResolveLookupFromLock(t *testing.T) {
	t.Parallel()

	const operation = "container.from"
	inputs := []any{"alpine:latest", "linux/amd64"}

	makeLock := func(t *testing.T, pin string, policy workspace.LockPolicy) *workspace.Lock {
		t.Helper()
		lock := workspace.NewLock()
		require.NoError(t, lock.SetLookup(lockCoreNamespace, operation, inputs, workspace.LookupResult{
			Value:  pin,
			Policy: policy,
		}))
		return lock
	}

	t.Run("entry exists with pin policy", func(t *testing.T) {
		t.Parallel()
		lock := makeLock(t, "sha256:abc123", workspace.PolicyPin)

		res, err := resolveLookupFromLock(workspace.LockModeStrict, lock, lockCoreNamespace, operation, inputs, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Equal(t, "sha256:abc123", res.Pin)
		require.Equal(t, workspace.PolicyPin, res.Policy)
		require.False(t, res.ShouldWrite)

		res, err = resolveLookupFromLock(workspace.LockModeAuto, lock, lockCoreNamespace, operation, inputs, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Equal(t, "sha256:abc123", res.Pin)
		require.Equal(t, workspace.PolicyPin, res.Policy)
		require.False(t, res.ShouldWrite)

		res, err = resolveLookupFromLock(workspace.LockModeUpdate, lock, lockCoreNamespace, operation, inputs, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyPin, res.Policy)
		require.True(t, res.ShouldWrite)
	})

	t.Run("entry exists with float policy", func(t *testing.T) {
		t.Parallel()
		lock := makeLock(t, "sha256:def456", workspace.PolicyFloat)

		res, err := resolveLookupFromLock(workspace.LockModeStrict, lock, lockCoreNamespace, operation, inputs, workspace.PolicyPin)
		require.NoError(t, err)
		require.Equal(t, "sha256:def456", res.Pin)
		require.Equal(t, workspace.PolicyFloat, res.Policy)
		require.False(t, res.ShouldWrite)

		res, err = resolveLookupFromLock(workspace.LockModeAuto, lock, lockCoreNamespace, operation, inputs, workspace.PolicyPin)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyFloat, res.Policy)
		require.False(t, res.ShouldWrite)

		res, err = resolveLookupFromLock(workspace.LockModeUpdate, lock, lockCoreNamespace, operation, inputs, workspace.PolicyPin)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyFloat, res.Policy)
		require.True(t, res.ShouldWrite)
	})

	t.Run("entry missing with requested pin policy", func(t *testing.T) {
		t.Parallel()

		res, err := resolveLookupFromLock(workspace.LockModeStrict, nil, lockCoreNamespace, operation, inputs, workspace.PolicyPin)
		require.ErrorContains(t, err, "missing lock entry")
		require.Equal(t, workspace.PolicyPin, res.Policy)

		res, err = resolveLookupFromLock(workspace.LockModeAuto, nil, lockCoreNamespace, operation, inputs, workspace.PolicyPin)
		require.ErrorContains(t, err, "missing lock entry for pinned")
		require.Equal(t, workspace.PolicyPin, res.Policy)

		res, err = resolveLookupFromLock(workspace.LockModeUpdate, nil, lockCoreNamespace, operation, inputs, workspace.PolicyPin)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyPin, res.Policy)
		require.True(t, res.ShouldWrite)
	})

	t.Run("entry missing with requested float policy", func(t *testing.T) {
		t.Parallel()

		res, err := resolveLookupFromLock(workspace.LockModeStrict, nil, lockCoreNamespace, operation, inputs, workspace.PolicyFloat)
		require.ErrorContains(t, err, "missing lock entry")
		require.Equal(t, workspace.PolicyFloat, res.Policy)

		res, err = resolveLookupFromLock(workspace.LockModeAuto, nil, lockCoreNamespace, operation, inputs, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyFloat, res.Policy)
		require.False(t, res.ShouldWrite)

		res, err = resolveLookupFromLock(workspace.LockModeUpdate, nil, lockCoreNamespace, operation, inputs, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyFloat, res.Policy)
		require.True(t, res.ShouldWrite)
	})

	t.Run("invalid lock entry result", func(t *testing.T) {
		t.Parallel()

		data := strings.Join([]string{
			`[["version","1"]]`,
			`["","container.from",["alpine:latest","linux/amd64"],{"value":"sha256:abc123","policy":"invalid"}]`,
		}, "\n")
		lock, err := workspace.ParseLock([]byte(data))
		require.NoError(t, err)

		_, err = resolveLookupFromLock(workspace.LockModeAuto, lock, lockCoreNamespace, operation, inputs, workspace.PolicyFloat)
		require.ErrorContains(t, err, "invalid lock entry")
	})
}
