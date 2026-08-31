package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

func withCurrentLockView(ctx context.Context) context.Context {
	return dagql.ContextWithCall(ctx, &dagql.ResultCall{View: call.View(workspaceLockingVersion)})
}

func TestResolveLookupFromLock(t *testing.T) {
	t.Parallel()

	const operation = "oci-sha"
	inputs := []any{"alpine:latest"}

	makeLock := func(t *testing.T, pin string) *workspace.Lock {
		t.Helper()
		lock := workspace.NewLock()
		require.NoError(t, lock.SetLookup(workspace.CoreLockNamespace, operation, inputs, pin))
		return lock
	}

	t.Run("disabled without a lock", func(t *testing.T) {
		t.Parallel()

		res := resolveLookupFromLoadedLock(nil, operation, inputs)
		require.Empty(t, res.Pin)
		require.False(t, res.ShouldWrite)
	})

	t.Run("existing pin entry", func(t *testing.T) {
		t.Parallel()

		loaded := &workspaceLookupLock{lock: makeLock(t, "sha256:abc123")}
		res := resolveLookupFromLoadedLock(loaded, operation, inputs)
		require.Equal(t, "sha256:abc123", res.Pin)
		require.False(t, res.ShouldWrite)
	})

	t.Run("missing entry is written", func(t *testing.T) {
		t.Parallel()

		loaded := &workspaceLookupLock{lock: workspace.NewLock()}
		res := resolveLookupFromLoadedLock(loaded, operation, inputs)
		require.Empty(t, res.Pin)
		require.True(t, res.ShouldWrite)
	})

	t.Run("refresh ignores existing pin without writing", func(t *testing.T) {
		t.Parallel()

		loaded := &workspaceLookupLock{
			lock:    makeLock(t, "sha256:abc123"),
			refresh: true,
		}
		res := resolveLookupFromLoadedLock(loaded, operation, inputs)
		require.Empty(t, res.Pin)
		require.False(t, res.ShouldWrite)
	})
}

func TestLookupLockForAPI(t *testing.T) {
	t.Parallel()

	const operation = "oci-sha"

	t.Run("older API view disables locking", func(t *testing.T) {
		t.Parallel()

		ctx := dagql.ContextWithCall(context.Background(), &dagql.ResultCall{
			View: call.View("v1.0.0-beta.9"),
		})
		lock, err := lookupLockForAPI(ctx, nil, operation)
		require.NoError(t, err)
		require.Nil(t, lock)
	})

	t.Run("current API view uses available workspace lock", func(t *testing.T) {
		t.Parallel()

		query := &core.Query{Server: &currentTypeDefsTestServer{
			workspaceLock:   workspace.NewLock(),
			workspaceLockOK: true,
		}}
		lock, err := lookupLockForAPI(withCurrentLockView(context.Background()), query, operation)
		require.NoError(t, err)
		require.NotNil(t, lock)
	})

	t.Run("private context disables locking", func(t *testing.T) {
		t.Parallel()

		ctx := withoutWorkspaceLookupLock(withCurrentLockView(context.Background()))
		lock, err := lookupLockForAPI(ctx, nil, operation)
		require.NoError(t, err)
		require.Nil(t, lock)
	})

	t.Run("uses an in-memory overlay lock without a host binding", func(t *testing.T) {
		t.Parallel()

		overlay := workspace.NewLock()
		ctx := withWorkspaceLookupLockOverride(withCurrentLockView(context.Background()), overlay)

		lock, err := lookupLockForAPI(ctx, nil, operation)
		require.NoError(t, err)
		require.NotNil(t, lock)

		inputs := []any{"alpine:latest"}
		want := "sha256:abc123"
		require.NoError(t, lock.SetLookup(workspace.CoreLockNamespace, operation, inputs, want))
		got, ok := overlay.GetLookup(workspace.CoreLockNamespace, operation, inputs)
		require.True(t, ok)
		require.Equal(t, want, got)
	})
}
