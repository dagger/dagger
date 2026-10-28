package core

// These tests cover the `engineVersion` value stored in module config. They
// verify the schema version seen by standalone module code and module
// dependencies.
//
// See also:
// - engine_test.go: engine lifecycle behavior.
// - module_config_test.go: general module config behavior.

import (
	"context"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func (ModuleSuite) TestModuleSchemaVersion(ctx context.Context, t *testctx.T) {
	t.Run("standalone", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := core.NewQuery(c).Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)

		require.NotEmpty(t, gjson.Get(out, "__schemaVersion").String())
	})

	t.Run("standalone explicit", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := core.NewQuery(c).Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v2.0.0").
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"__schemaVersion":"v2.0.0"}`, out)
	})

	t.Run("standalone explicit dev", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := core.NewQuery(c).Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v2.0.0-dev-123").
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"__schemaVersion":"v2.0.0"}`, out)
	})

	t.Run("module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := moduleMainFixture(t, c, "foo", "go/schema-version-module")
		out, err := work.
			With(daggerQuery("{getVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"getVersion": "v0.11.0"}`, out)

		out, err = work.
			With(daggerCall("get-version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "v0.11.0")
	})

	// A module declaring a view past the engine's loads, and is served the
	// engine's: a prerelease engine (main's VERSION) serves a module declaring
	// bare v1.0.0 its own view, not the release's.
	t.Run("module past engine", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := moduleMainFixture(t, c, "foo", "go/schema-version-module").
			WithNewFile(".dagger/modules/foo/dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v1.0.0"}`)
		out, err := work.
			With(daggerQuery("{getVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"getVersion": "`+engine.ModuleAPIViewVersion("v1.0.0")+`"}`, out)
	})

	t.Run("module deps", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := moduleMainFixture(t, c, "foo", "go/schema-version-parent-dep")

		out, err := work.
			With(daggerQuery("{getVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"getVersion": "v0.10.0 v0.11.0"}`, out)

		out, err = work.
			With(daggerCall("get-version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "v0.10.0 v0.11.0")
	})
}
