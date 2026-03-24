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
}`), 0o644))

	_, err := Migrate(context.Background(), LocalMigrationIO{}, &ErrMigrationRequired{
		ConfigPath:  cfgPath,
		ProjectRoot: root,
	})
	require.NoError(t, err)

	lockPath := filepath.Join(root, LockDirName, LockFileName)
	lockData, err := os.ReadFile(lockPath)
	require.NoError(t, err)

	lock, err := ParseLock(lockData)
	require.NoError(t, err)

	result, ok, err := lock.GetModuleResolve("github.com/acme/toolchain@main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "1111111", result.Value)
	require.Equal(t, PolicyPin, result.Policy)

	result, ok, err = lock.GetModuleResolve("github.com/acme/blueprint@main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "2222222", result.Value)
	require.Equal(t, PolicyPin, result.Policy)
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
}`), 0o644))

	_, err := Migrate(context.Background(), LocalMigrationIO{}, &ErrMigrationRequired{
		ConfigPath:  cfgPath,
		ProjectRoot: root,
	})
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(root, LockDirName, LockFileName))
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
}`), 0o644))

	_, err := Migrate(context.Background(), LocalMigrationIO{}, &ErrMigrationRequired{
		ConfigPath:  cfgPath,
		ProjectRoot: root,
	})
	require.ErrorContains(t, err, "conflicting pins for source")
}

func TestMigrateConvertsStandaloneRootModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := filepath.Join(root, ModuleConfigFileName)
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{
  "name": "myapp",
  "sdk": {"source": "dang"}
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.dang"), []byte("type Myapp {}\n"), 0o644))

	_, err := Migrate(context.Background(), LocalMigrationIO{}, &ErrMigrationRequired{
		ConfigPath:  cfgPath,
		ProjectRoot: root,
	})
	require.NoError(t, err)

	configData, err := os.ReadFile(filepath.Join(root, LockDirName, ConfigFileName))
	require.NoError(t, err)
	cfg, err := ParseConfig(configData)
	require.NoError(t, err)
	require.Equal(t, ModuleEntry{
		Source:    "modules/myapp",
		Blueprint: true,
	}, cfg.Modules["myapp"])

	moduleData, err := os.ReadFile(filepath.Join(root, LockDirName, "modules", "myapp", ModuleConfigFileName))
	require.NoError(t, err)
	moduleCfg, err := parseLegacyConfig(moduleData)
	require.NoError(t, err)
	require.Equal(t, "../../../", moduleCfg.Source)

	_, err = os.Stat(filepath.Join(root, "main.dang"))
	require.NoError(t, err)
}
