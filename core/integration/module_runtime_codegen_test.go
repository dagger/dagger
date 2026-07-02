package core

// These tests cover the Go SDK "no codegen at runtime" feature controlled by
// codegen.automaticGitignore in dagger.json. When set to false, the module
// commits its generated files and the Go SDK trusts them at runtime, skipping
// the runtime codegen pass.
//
// They exercise the observable, end-to-end behavior:
//   - the missing-generated-files error surfaced by the no-codegen runtime
//     path when the committed files are absent.

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

// TestMissingGeneratedFiles verifies that with runtime codegen disabled
// (automaticGitignore=false), the module pinned to the running engine, and the
// committed generated files absent, calling the module fails with a clear error
// pointing the user at `dagger generate`.
//
// The module pins engineVersion=latest so the no-codegen path is taken: it is
// only honored when the committed files were generated for this engine (see
// useRuntimeCodegen). Without the pin the version gate would fall back to
// runtime codegen and the files would be regenerated rather than required.
func (RuntimeCodegenSuite) TestMissingGeneratedFiles(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	fal := false

	// The minimal fixture has no committed dagger.gen.go files.
	_, err := moduleFixture(t, c, "go/minimal").
		With(configFile(".", &modules.ModuleConfig{
			Name:          "minimal",
			EngineVersion: modules.EngineVersionLatest,
			SDK:           &modules.SDK{Source: "go"},
			Codegen: &modules.ModuleCodegenConfig{
				AutomaticGitignore: &fal,
			},
		})).
		With(daggerCall("hello")).
		Sync(ctx)

	requireErrOut(t, err, "generated file")
	requireErrOut(t, err, "run `dagger generate`")
}

// TestVersionSkewFallsBackToCodegen verifies that when a module opts out of
// runtime codegen (automaticGitignore=false) but pins an engineVersion older
// than the running engine, the SDK falls back to runtime codegen instead of
// trusting the (potentially incompatible) committed files. The committed files
// are absent here, so the missing-files error must NOT surface — the call
// regenerates and succeeds. This mirrors the repo's own dev modules, which pin
// the last stable release but run against an in-development engine.
func (RuntimeCodegenSuite) TestVersionSkewFallsBackToCodegen(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	fal := false

	out, err := moduleFixture(t, c, "go/minimal").
		With(configFile(".", &modules.ModuleConfig{
			Name: "minimal",
			// An older engine version than the one running this test: the
			// committed files (none here) must not be trusted as-is.
			EngineVersion: "v0.21.0",
			SDK:           &modules.SDK{Source: "go"},
			Codegen: &modules.ModuleCodegenConfig{
				AutomaticGitignore: &fal,
			},
		})).
		With(daggerCall("hello")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, out, "hello")
}
