package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrateWritesLockForLegacyPins(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := filepath.Join(root, ModuleConfigFileName)
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{
  "name": "myapp",
  "toolchains": [
    {"name": "toolchain", "source": "github.com/acme/toolchain@main", "pin": "1111111"}
  ],
  "blueprint": {"name": "blueprint", "source": "github.com/acme/blueprint@main", "pin": "2222222"}
}`), 0644))

	_, err := Migrate(context.Background(), LocalMigrationIO{}, &ErrMigrationRequired{
		ConfigPath:  cfgPath,
		ProjectRoot: root,
	}, nil)
	require.NoError(t, err)

	lockPath := filepath.Join(root, WorkspaceDirName, LockFileName)
	lockData, err := os.ReadFile(lockPath)
	require.NoError(t, err)

	lock, err := ParseLock(lockData)
	require.NoError(t, err)

	pin, policy, ok := lock.GetModuleResolve("github.com/acme/toolchain@main")
	require.True(t, ok)
	require.Equal(t, "1111111", pin)
	require.Equal(t, PolicyPin, policy)

	pin, policy, ok = lock.GetModuleResolve("github.com/acme/blueprint@main")
	require.True(t, ok)
	require.Equal(t, "2222222", pin)
	require.Equal(t, PolicyPin, policy)
}

func TestMigrateSkipsLockWithoutPins(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := filepath.Join(root, ModuleConfigFileName)
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{
  "name": "myapp",
  "toolchains": [
    {"name": "toolchain", "source": "github.com/acme/toolchain@main"}
  ]
}`), 0644))

	_, err := Migrate(context.Background(), LocalMigrationIO{}, &ErrMigrationRequired{
		ConfigPath:  cfgPath,
		ProjectRoot: root,
	}, nil)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(root, WorkspaceDirName, LockFileName))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestMigrateFailsOnConflictingLegacyPins(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := filepath.Join(root, ModuleConfigFileName)
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{
  "name": "myapp",
  "toolchains": [
    {"name": "toolchain-a", "source": "github.com/acme/toolchain@main", "pin": "1111111"},
    {"name": "toolchain-b", "source": "github.com/acme/toolchain@main", "pin": "2222222"}
  ]
}`), 0644))

	_, err := Migrate(context.Background(), LocalMigrationIO{}, &ErrMigrationRequired{
		ConfigPath:  cfgPath,
		ProjectRoot: root,
	}, nil)
	require.ErrorContains(t, err, "conflicting pins for source")
}
