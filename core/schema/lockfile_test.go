package schema

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

type fakeWorkspaceLockReader struct {
	data  []byte
	err   error
	files map[string][]byte
}

func (r fakeWorkspaceLockReader) ReadCallerHostFile(_ context.Context, path string) ([]byte, error) {
	if r.files != nil {
		if data, ok := r.files[path]; ok {
			return data, nil
		}
		return nil, os.ErrNotExist
	}
	if r.err != nil {
		return nil, r.err
	}
	return r.data, nil
}

func withCurrentLockView(ctx context.Context) context.Context {
	return dagql.ContextWithCall(ctx, &dagql.ResultCall{View: call.View(workspaceLockingVersion)})
}

func TestResolveLookupFromLock(t *testing.T) {
	t.Parallel()

	const operation = "container.from"
	inputs := []any{"alpine:latest", "linux/amd64"}

	makeLock := func(t *testing.T, pin string, policy workspace.LockPolicy) *workspace.Lock {
		t.Helper()
		if policy == workspace.PolicyFloat {
			data := fmt.Sprintf(`[["version","1"]]
["","container.from",["alpine:latest","linux/amd64"],%q,%q]`, pin, policy)
			lock, err := workspace.ParseLock([]byte(data))
			require.NoError(t, err)
			return lock
		}

		lock := workspace.NewLock()
		require.NoError(t, lock.SetLookup(lockCoreNamespace, operation, inputs, workspace.LookupResult{
			Value:  pin,
			Policy: policy,
		}))
		return lock
	}

	t.Run("disabled without a lock", func(t *testing.T) {
		t.Parallel()

		res, err := resolveLookupFromLoadedLock(nil, operation, inputs, workspace.PolicyPin)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyPin, res.Policy)
		require.False(t, res.ShouldWrite)
	})

	t.Run("existing pin entry", func(t *testing.T) {
		t.Parallel()

		loaded := &workspaceLookupLock{lock: makeLock(t, "sha256:abc123", workspace.PolicyPin)}
		res, err := resolveLookupFromLoadedLock(loaded, operation, inputs, workspace.PolicyPin)
		require.NoError(t, err)
		require.Equal(t, "sha256:abc123", res.Pin)
		require.Equal(t, workspace.PolicyPin, res.Policy)
		require.False(t, res.ShouldWrite)
	})

	t.Run("version 1 float entry is refreshed", func(t *testing.T) {
		t.Parallel()

		loaded := &workspaceLookupLock{lock: makeLock(t, "sha256:def456", workspace.PolicyFloat)}
		res, err := resolveLookupFromLoadedLock(loaded, operation, inputs, workspace.PolicyPin)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyFloat, res.Policy)
		require.True(t, res.ShouldWrite)
	})

	t.Run("missing entry is written", func(t *testing.T) {
		t.Parallel()

		loaded := &workspaceLookupLock{lock: workspace.NewLock()}
		res, err := resolveLookupFromLoadedLock(loaded, operation, inputs, workspace.PolicyPin)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyPin, res.Policy)
		require.True(t, res.ShouldWrite)
	})

	t.Run("invalid lock entry result", func(t *testing.T) {
		t.Parallel()

		data := strings.Join([]string{
			`[["version","1"]]`,
			`["","container.from",["alpine:latest","linux/amd64"],"sha256:abc123","invalid"]`,
		}, "\n")
		lock, err := workspace.ParseLock([]byte(data))
		require.NoError(t, err)

		_, err = resolveLookupFromLoadedLock(
			&workspaceLookupLock{lock: lock},
			operation,
			inputs,
			workspace.PolicyPin,
		)
		require.ErrorContains(t, err, "invalid lock entry")
	})
}

func TestLookupLockForAPI(t *testing.T) {
	t.Parallel()

	const operation = "container.from"

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

		inputs := []any{"alpine:latest", "linux/amd64"}
		want := workspace.LookupResult{Value: "sha256:abc123", Policy: workspace.PolicyPin}
		require.NoError(t, lock.SetLookup(lockCoreNamespace, operation, inputs, want))
		got, ok, err := overlay.GetLookup(lockCoreNamespace, operation, inputs)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, want, got)
	})
}

func TestLockHostPath(t *testing.T) {
	t.Parallel()

	ws := &core.Workspace{
		ConfigFile: filepath.Join("apps", "api", "dagger.toml"),
		LockFile:   filepath.Join("apps", "api", "dagger.lock"),
	}
	ws.SetHostPath("/repo")

	lockPath, err := lockHostPath(ws)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/repo", "apps", "api", "dagger.lock"), lockPath)
}

func TestReadWorkspaceLock(t *testing.T) {
	t.Parallel()

	makeWorkspace := func() *core.Workspace {
		ws := &core.Workspace{
			ConfigFile: "dagger.toml",
			LockFile:   "dagger.lock",
		}
		ws.SetHostPath("/repo")
		return ws
	}

	t.Run("missing lockfile returns empty lock", func(t *testing.T) {
		t.Parallel()

		lock, err := readWorkspaceLock(context.Background(), fakeWorkspaceLockReader{
			err: fmt.Errorf("failed to read file: %w", os.ErrNotExist),
		}, makeWorkspace())
		require.NoError(t, err)

		lockBytes, err := lock.Marshal()
		require.NoError(t, err)
		require.Empty(t, lockBytes)
	})

	t.Run("invalid lockfile returns parse error", func(t *testing.T) {
		t.Parallel()

		_, err := readWorkspaceLock(context.Background(), fakeWorkspaceLockReader{
			data: []byte("not-json"),
		}, makeWorkspace())
		require.Error(t, err)
		require.ErrorContains(t, err, "parsing lock")
	})

	t.Run("missing lockfile reports exists false", func(t *testing.T) {
		t.Parallel()

		lock, exists, err := readWorkspaceLockState(context.Background(), fakeWorkspaceLockReader{
			err: fmt.Errorf("failed to read file: %w", os.ErrNotExist),
		}, makeWorkspace())
		require.NoError(t, err)
		require.False(t, exists)

		lockBytes, err := lock.Marshal()
		require.NoError(t, err)
		require.Empty(t, lockBytes)
	})

	t.Run("reads legacy lock when canonical lock is missing", func(t *testing.T) {
		t.Parallel()

		legacy := workspace.NewLock()
		require.NoError(t, legacy.SetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"}, workspace.LookupResult{
			Value:  "sha256:deadbeef",
			Policy: workspace.PolicyPin,
		}))
		legacyBytes, err := legacy.Marshal()
		require.NoError(t, err)

		lock, exists, err := readWorkspaceLockState(context.Background(), fakeWorkspaceLockReader{
			files: map[string][]byte{
				filepath.Join("/repo", ".dagger", "lock"): legacyBytes,
			},
		}, makeWorkspace())
		require.NoError(t, err)
		require.True(t, exists)

		got, ok, err := lock.GetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"})
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, workspace.LookupResult{Value: "sha256:deadbeef", Policy: workspace.PolicyPin}, got)
	})
}
