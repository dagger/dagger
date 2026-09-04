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

// A dagger-module.toml module with a configured client and no committed
// generated files must still generate on the first run: client generation
// needs the module's schema, i.e. the module built, and the files it builds
// from are exactly what the preceding codegen step produced. Loading the
// module from the original source instead used to fail every first
// `dagger generate` of such a module with the SDK's "generated file is
// missing; run `dagger generate`" hint — from inside `dagger generate`
// (https://github.com/dagger/dagger/issues/13973, "broken state 1").
func (RuntimeCodegenSuite) TestClientGenerationFollowsCodegen(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	withClient := moduleFixture(t, c, "go/minimal").
		With(configFile(".", &modules.ModuleConfig{
			Name:          "minimal",
			EngineVersion: modules.EngineVersionLatest,
			SDK:           &modules.SDK{Source: "go"},
			Clients:       []*modules.ModuleConfigClient{{Generator: "go", Directory: "./client"}},
		}))

	t.Run("generates module and client", func(ctx context.Context, t *testctx.T) {
		generated := withClient.
			With(daggerQuery(`{moduleSource(refString:"."){generatedContextDirectory{export(path:".")}}}`))

		entries, err := generated.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, entries, "dagger.gen.go")
		require.Contains(t, entries, "client/")
		clientEntries, err := generated.Directory("client").Entries(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, clientEntries)
	})

	t.Run("a build failure is the error, not the missing-files hint", func(ctx context.Context, t *testctx.T) {
		// With the module loaded from the generated context, the only thing
		// that can stop client generation is the module itself not building —
		// and that is what the user has to see.
		_, err := withClient.
			WithNewFile("main.go", `package main

type Minimal struct{}

func (m *Minimal) Hello() string {
	return intentionallyUndefinedSymbol()
}
`).
			With(daggerQuery(`{moduleSource(refString:"."){generatedContextDirectory{export(path:".")}}}`)).
			Sync(ctx)
		requireErrOut(t, err, "undefined: intentionallyUndefinedSymbol")
		require.NotContains(t, err.Error(), "run `dagger generate`")
	})
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
		WithoutFile("dagger.json").
		WithNewFile("dagger.toml", `[modules.minimal]
source = "."
entrypoint = true
`).
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

// A trusted Python module must reject project metadata that no longer matches
// its committed lockfile. Otherwise a dependency added to pyproject.toml can
// be silently omitted from the runtime environment until codegen is rerun.
func (RuntimeCodegenSuite) TestPythonTrustedRejectsStaleLockfile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	generated, err := moduleFixture(t, c, "python/minimal").
		WithoutFile("dagger.json").
		WithNewFile("dagger.toml", `[modules.minimal]
source = "."
entrypoint = true
`).
		With(configFile(".", &modules.ModuleConfig{
			Name:          "minimal",
			EngineVersion: modules.EngineVersionLatest,
			SDK:           &modules.SDK{Source: "python"},
		})).
		// Existing uv-managed modules already have a lockfile; codegen
		// refreshes it rather than introducing one into an existing project.
		WithNewFile("uv.lock", `version = 1
revision = 3
requires-python = ">=3.13"
`).
		With(daggerQuery(`{moduleSource(refString:"."){generatedContextDirectory{export(path:".")}}}`)).
		Sync(ctx)
	require.NoError(t, err)

	lock, err := generated.File("uv.lock").Contents(ctx)
	require.NoError(t, err)
	require.NotContains(t, lock, `name = "numpy"`)

	generated = generated.
		// Deliberately change the project dependencies without regenerating
		// uv.lock. The committed lockfile still describes only dagger-io.
		WithNewFile("pyproject.toml", `[project]
name = "minimal"
version = "0.0.0"
requires-python = ">=3.13"
dependencies = ["dagger-io", "numpy"]

[tool.uv.sources]
dagger-io = { path = "sdk", editable = true }

[build-system]
requires = ["uv_build>=0.8.4,<0.9.0"]
build-backend = "uv_build"
`)

	_, err = generated.With(daggerCall("hello")).Sync(ctx)
	requireErrOut(t, err, "lockfile")
	requireErrOut(t, err, "needs to be updated")
}
