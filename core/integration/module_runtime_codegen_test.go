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

	"github.com/dagger/dagger/core/modules"
)

type RuntimeCodegenSuite struct{}

func TestRuntimeCodegen(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(RuntimeCodegenSuite{})
}

// TestMissingGeneratedFiles verifies that with runtime codegen disabled
// (automaticGitignore=false) and the committed generated files absent, calling
// the module fails with a clear error pointing the user at `dagger generate`.
func (RuntimeCodegenSuite) TestMissingGeneratedFiles(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	fal := false

	// The minimal fixture has no committed dagger.gen.go files.
	_, err := moduleFixture(t, c, "go/minimal").
		With(configFile(".", &modules.ModuleConfig{
			Name: "minimal",
			SDK:  &modules.SDK{Source: "go"},
			Codegen: &modules.ModuleCodegenConfig{
				AutomaticGitignore: &fal,
			},
		})).
		With(daggerCall("hello")).
		Sync(ctx)

	requireErrOut(t, err, "generated file")
	requireErrOut(t, err, "run `dagger generate`")
}
