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
	"github.com/dagger/dagger/engine"
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

	t.Run("disabled ignores lockfile", func(t *testing.T) {
		t.Parallel()
		lock := makeLock(t, "sha256:abc123", workspace.PolicyPin)

		res, err := resolveLookupFromLock(workspace.LockModeDisabled, lock, operation, inputs, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyFloat, res.Policy)
		require.False(t, res.ShouldWrite)
	})

	t.Run("live always resolves and writes", func(t *testing.T) {
		t.Parallel()
		lock := makeLock(t, "sha256:abc123", workspace.PolicyPin)

		res, err := resolveLookupFromLock(workspace.LockModeLive, lock, operation, inputs, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyPin, res.Policy)
		require.True(t, res.ShouldWrite)
	})

	t.Run("existing pin entry", func(t *testing.T) {
		t.Parallel()
		lock := makeLock(t, "sha256:abc123", workspace.PolicyPin)

		res, err := resolveLookupFromLock(workspace.LockModeFrozen, lock, operation, inputs, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Equal(t, "sha256:abc123", res.Pin)
		require.Equal(t, workspace.PolicyPin, res.Policy)
		require.False(t, res.ShouldWrite)

		res, err = resolveLookupFromLock(workspace.LockModePinned, lock, operation, inputs, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Equal(t, "sha256:abc123", res.Pin)
		require.Equal(t, workspace.PolicyPin, res.Policy)
		require.False(t, res.ShouldWrite)
	})

	t.Run("existing float entry", func(t *testing.T) {
		t.Parallel()
		lock := makeLock(t, "sha256:def456", workspace.PolicyFloat)

		res, err := resolveLookupFromLock(workspace.LockModeFrozen, lock, operation, inputs, workspace.PolicyPin)
		require.NoError(t, err)
		require.Equal(t, "sha256:def456", res.Pin)
		require.Equal(t, workspace.PolicyFloat, res.Policy)
		require.False(t, res.ShouldWrite)

		res, err = resolveLookupFromLock(workspace.LockModePinned, lock, operation, inputs, workspace.PolicyPin)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyFloat, res.Policy)
		require.True(t, res.ShouldWrite)
	})

	t.Run("missing entry with requested pin policy", func(t *testing.T) {
		t.Parallel()

		res, err := resolveLookupFromLock(workspace.LockModeFrozen, nil, operation, inputs, workspace.PolicyPin)
		require.ErrorContains(t, err, "missing lock entry")
		require.Equal(t, workspace.PolicyPin, res.Policy)

		res, err = resolveLookupFromLock(workspace.LockModePinned, nil, operation, inputs, workspace.PolicyPin)
		require.NoError(t, err)
		require.Equal(t, workspace.PolicyPin, res.Policy)
		require.Empty(t, res.Pin)
		require.True(t, res.ShouldWrite)
	})

	t.Run("missing entry with requested float policy", func(t *testing.T) {
		t.Parallel()

		res, err := resolveLookupFromLock(workspace.LockModeFrozen, nil, operation, inputs, workspace.PolicyFloat)
		require.ErrorContains(t, err, "missing lock entry")
		require.Equal(t, workspace.PolicyFloat, res.Policy)

		res, err = resolveLookupFromLock(workspace.LockModePinned, nil, operation, inputs, workspace.PolicyFloat)
		require.NoError(t, err)
		require.Empty(t, res.Pin)
		require.Equal(t, workspace.PolicyFloat, res.Policy)
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

		_, err = resolveLookupFromLock(workspace.LockModePinned, lock, operation, inputs, workspace.PolicyFloat)
		require.ErrorContains(t, err, "invalid lock entry")
	})
}

func TestCurrentLookupLockMode(t *testing.T) {
	t.Parallel()

	t.Run("defaults to disabled", func(t *testing.T) {
		t.Parallel()

		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{})
		mode, err := currentLookupLockMode(ctx)
		require.NoError(t, err)
		require.Equal(t, workspace.LockModeDisabled, mode)
	})

	t.Run("uses explicit mode", func(t *testing.T) {
		t.Parallel()

		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
			LockMode: string(workspace.LockModeLive),
		})
		mode, err := currentLookupLockMode(ctx)
		require.NoError(t, err)
		require.Equal(t, workspace.LockModeLive, mode)
	})
}

func TestLookupLockForMode(t *testing.T) {
	t.Parallel()

	const operation = "container.from"

	t.Run("pinned without workspace lock resolves live without writes", func(t *testing.T) {
		t.Parallel()

		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
			LockMode: string(workspace.LockModePinned),
		})
		query := &core.Query{Server: &currentTypeDefsTestServer{}}

		mode, lock, err := lookupLockForMode(ctx, query, operation)
		require.NoError(t, err)
		require.Equal(t, workspace.LockModeDisabled, mode)
		require.Nil(t, lock)
	})

	t.Run("frozen without workspace lock fails", func(t *testing.T) {
		t.Parallel()

		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
			LockMode: string(workspace.LockModeFrozen),
		})
		query := &core.Query{Server: &currentTypeDefsTestServer{}}

		_, _, err := lookupLockForMode(ctx, query, operation)
		require.ErrorContains(t, err, "no writable workspace lockfile is available")
	})

	t.Run("uses available workspace lock", func(t *testing.T) {
		t.Parallel()

		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
			LockMode: string(workspace.LockModePinned),
		})
		query := &core.Query{Server: &currentTypeDefsTestServer{
			workspaceLock:   workspace.NewLock(),
			workspaceLockOK: true,
		}}

		mode, lock, err := lookupLockForMode(ctx, query, operation)
		require.NoError(t, err)
		require.Equal(t, workspace.LockModePinned, mode)
		require.NotNil(t, lock)
	})

	t.Run("uses an in-memory overlay lock without a host binding", func(t *testing.T) {
		t.Parallel()

		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
			LockMode: string(workspace.LockModePinned),
		})
		overlay := workspace.NewLock()
		ctx = withWorkspaceLookupLockOverride(ctx, overlay)

		mode, lock, err := lookupLockForMode(ctx, nil, operation)
		require.NoError(t, err)
		require.Equal(t, workspace.LockModePinned, mode)
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

func TestWorkspaceInstallLookupContext(t *testing.T) {
	t.Parallel()

	t.Run("defaults unspecified mode to pinned", func(t *testing.T) {
		t.Parallel()

		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{})
		ctx = workspaceInstallLookupContext(ctx)

		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		require.NoError(t, err)
		require.Equal(t, string(workspace.LockModePinned), clientMetadata.LockMode)
	})

	t.Run("preserves explicit mode", func(t *testing.T) {
		t.Parallel()

		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
			LockMode: string(workspace.LockModeDisabled),
		})
		ctx = workspaceInstallLookupContext(ctx)

		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		require.NoError(t, err)
		require.Equal(t, string(workspace.LockModeDisabled), clientMetadata.LockMode)
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
