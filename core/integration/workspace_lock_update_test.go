package core

import (
	"context"
	"strings"

	workspacecfg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestWorkspaceLockUpdateCommand(ctx context.Context, t *testctx.T) {
	t.Run("refreshes only the selected workspace module lock entry", func(ctx context.Context, t *testctx.T) {
		const (
			wolfiSource = "github.com/dagger/dagger/modules/wolfi@main"
			ghaSource   = "github.com/dagger/dagger/modules/gha@main"
			wolfiPin    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			ghaPin      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		)

		lock := workspacecfg.NewLock()
		require.NoError(t, lock.SetModuleResolve(wolfiSource, workspacecfg.LookupResult{
			Value:  wolfiPin,
			Policy: workspacecfg.PolicyFloat,
		}))
		require.NoError(t, lock.SetModuleResolve(ghaSource, workspacecfg.LookupResult{
			Value:  ghaPin,
			Policy: workspacecfg.PolicyFloat,
		}))
		lockBytes, err := lock.Marshal()
		require.NoError(t, err)

		configTOML := `[modules.wolfi]
source = "` + wolfiSource + `"

[modules.gha]
source = "` + ghaSource + `"
`

		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithNewFile(".dagger/config.toml", configTOML).
			WithNewFile(".dagger/lock", string(lockBytes))

		updated := ctr.With(daggerExec("lock", "update", "wolfi"))
		out, err := updated.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Updated .dagger/lock", strings.TrimSpace(out))

		upToDate := updated.With(daggerExec("lock", "update", "wolfi"))
		out, err = upToDate.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Lockfile already up to date", strings.TrimSpace(out))

		lockOut, err := upToDate.File(".dagger/lock").Contents(ctx)
		require.NoError(t, err)

		wolfiEntry := requireModuleResolveLockEntry(t, []byte(lockOut), wolfiSource)
		require.NotEqual(t, wolfiPin, wolfiEntry.Value)
		require.Equal(t, workspacecfg.PolicyFloat, wolfiEntry.Policy)

		ghaEntry := requireModuleResolveLockEntry(t, []byte(lockOut), ghaSource)
		require.Equal(t, ghaPin, ghaEntry.Value)
		require.Equal(t, workspacecfg.PolicyFloat, ghaEntry.Policy)
	})

	t.Run("explicit local modules error", func(ctx context.Context, t *testctx.T) {
		configTOML := `[modules.counter]
source = "../counter"
`

		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "counter"}).
			WithWorkdir("counter").
			With(initStandaloneDangModule("counter", `
type Counter {
  pub value: String! {
    "ok"
  }
}
`)).
			WithWorkdir("..").
			WithNewFile(".dagger/config.toml", configTOML)

		_, err := ctr.With(daggerExec("lock", "update", "counter")).Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, `module "counter" source "../counter" is not a git module`)
	})

	t.Run("unknown modules error", func(ctx context.Context, t *testctx.T) {
		configTOML := `[modules.wolfi]
source = "github.com/dagger/dagger/modules/wolfi@main"
`

		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithNewFile(".dagger/config.toml", configTOML)

		_, err := ctr.With(daggerExec("lock", "update", "missing")).Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "workspace module(s) not found: missing")
	})
}

func requireModuleResolveLockEntry(t *testctx.T, lockBytes []byte, source string) workspacecfg.LookupResult {
	t.Helper()

	lock, err := workspacecfg.ParseLock(lockBytes)
	require.NoError(t, err)

	entry, ok, err := lock.GetModuleResolve(source)
	require.NoError(t, err)
	require.True(t, ok)
	return entry
}
