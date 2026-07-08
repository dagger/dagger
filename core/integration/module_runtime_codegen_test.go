package core

// These tests cover the config-format runtime-codegen rule: dagger.json
// modules regenerate bindings at runtime (legacy behavior),
// dagger-module.toml modules build from committed generated files.
//
// The Go SDK applies the rule natively; module SDKs adopt it by declaring
// their moduleRuntime introspectionJson argument optional — the Python tests
// cover that adoption.

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core/modules"
)

type RuntimeCodegenSuite struct{}

func TestRuntimeCodegen(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(RuntimeCodegenSuite{})
}

// A dagger-module.toml module with no committed generated files must fail
// with an error pointing at `dagger generate`, not silently regenerate.
func (RuntimeCodegenSuite) TestMissingGeneratedFiles(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The minimal fixture has no committed dagger.gen.go files.
	_, err := moduleFixture(t, c, "go/minimal").
		With(configFile(".", &modules.ModuleConfig{
			Name:          "minimal",
			EngineVersion: modules.EngineVersionLatest,
			SDK:           &modules.SDK{Source: "go"},
		})).
		With(daggerCall("hello")).
		Sync(ctx)

	requireErrOut(t, err, "generated file")
	requireErrOut(t, err, "run `dagger generate`")
}

// Legacy dagger.json modules keep runtime codegen unconditionally; a stale
// codegen.automaticGitignore=false opt-out is not read anymore. Nothing is
// committed here, so success requires runtime regeneration.
func (RuntimeCodegenSuite) TestLegacyConfigKeepsRuntimeCodegen(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := moduleFixture(t, c, "go/minimal").
		With(fileContents("dagger.json", `{
			"name": "minimal",
			"engineVersion": "latest",
			"sdk": {"source": "go"},
			"codegen": {"automaticGitignore": false}
		}`)).
		With(daggerCall("hello")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, out, "hello")
}

// TestTrustedFilesUsed covers the Go happy path: materialize the generated
// files, then call — the runtime serves the call from the committed files.
// Dropping the committed dagger.gen.go afterwards makes the same call fail
// with the missing-files error, proving the runtime reads the committed files
// rather than regenerating.
func (RuntimeCodegenSuite) TestTrustedFilesUsed(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	generated := moduleFixture(t, c, "go/minimal").
		With(configFile(".", &modules.ModuleConfig{
			Name:          "minimal",
			EngineVersion: modules.EngineVersionLatest,
			SDK:           &modules.SDK{Source: "go"},
		})).
		With(daggerQuery(`{moduleSource(refString:"."){generatedContextDirectory{export(path:".")}}}`))

	out, err := generated.
		With(daggerCall("hello")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello")

	_, err = generated.
		WithoutFile("dagger.gen.go").
		With(daggerCall("hello")).
		Sync(ctx)
	requireErrOut(t, err, "generated file")
	requireErrOut(t, err, "run `dagger generate`")
}

// TestPythonMissingGeneratedFiles is the Python analog of
// TestMissingGeneratedFiles: with nothing committed the call must fail
// pointing the user at `dagger generate` instead of silently regenerating.
func (RuntimeCodegenSuite) TestPythonMissingGeneratedFiles(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The minimal fixture has no committed vendored sdk (client bindings).
	_, err := moduleFixture(t, c, "python/minimal").
		With(configFile(".", &modules.ModuleConfig{
			Name:          "minimal",
			EngineVersion: modules.EngineVersionLatest,
			SDK:           &modules.SDK{Source: "python"},
		})).
		With(daggerCall("hello")).
		Sync(ctx)

	requireErrOut(t, err, "generated file")
	requireErrOut(t, err, "run `dagger generate`")
}

// TestPythonLegacyConfigKeepsRuntimeCodegen verifies that legacy dagger.json
// modules keep today's runtime-codegen behavior: nothing is committed and the
// call still succeeds, regenerating on the fly.
func (RuntimeCodegenSuite) TestPythonLegacyConfigKeepsRuntimeCodegen(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := moduleFixture(t, c, "python/minimal").
		With(fileContents("dagger.json", `{
			"name": "minimal",
			"engineVersion": "latest",
			"sdk": {"source": "python"}
		}`)).
		With(daggerCall("hello")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, out, "hello")
}

// TestPythonTrustedFilesUsed covers the Python happy path: materialize the
// generated files, then call — the runtime serves the call from the committed
// files. Dropping the committed vendored sdk afterwards makes the same call
// fail with the missing-files error, proving the runtime reads the committed
// files rather than regenerating.
//
// The first call also proves codegen no longer gitignores the vendored sdk
// for toml modules: local context loading is gitignore-filtered, so an
// ignored sdk would surface as the missing-files error.
//
// generatedContextDirectory runs codegen without loading the module runtime,
// so it works even while the trusted path would report missing files —
// `dagger generate` can't be used here: it only regenerates SDK-managed
// workspace modules, and enumerating generators loads the module, which
// dead-ends on the trusted path until the files exist (Go behaves the same).
func (RuntimeCodegenSuite) TestPythonTrustedFilesUsed(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	generated := moduleFixture(t, c, "python/minimal").
		With(configFile(".", &modules.ModuleConfig{
			Name:          "minimal",
			EngineVersion: modules.EngineVersionLatest,
			SDK:           &modules.SDK{Source: "python"},
		})).
		With(daggerQuery(`{moduleSource(refString:"."){generatedContextDirectory{export(path:".")}}}`))

	out, err := generated.
		With(daggerCall("hello")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello")

	_, err = generated.
		WithoutDirectory("sdk").
		With(daggerCall("hello")).
		Sync(ctx)
	requireErrOut(t, err, "generated file")
	requireErrOut(t, err, "run `dagger generate`")
}
