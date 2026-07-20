package core

// These tests cover the config-format runtime-codegen rule: dagger.json
// modules regenerate bindings at runtime (legacy behavior),
// dagger-module.toml modules build from committed generated files.

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
