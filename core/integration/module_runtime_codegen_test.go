package core

// These tests cover the Go SDK "no codegen at runtime" feature controlled by
// codegen.legacyCodegenAtRuntime in dagger.json. When set to false, the SDK
// trusts the committed generated files and skips the runtime codegen pass.
//
// They exercise the observable, end-to-end behaviors:
//   - config validation: legacyCodegenAtRuntime=false requires
//     automaticGitignore=false (the guardrail in ModuleCodegenConfig.Validate,
//     enforced during module load).
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

// TestValidationRejectsAutomaticGitignore verifies that loading a module whose
// dagger.json disables runtime codegen but does not also explicitly disable
// automaticGitignore is rejected, since the generated files must be committed.
func (RuntimeCodegenSuite) TestValidationRejectsAutomaticGitignore(ctx context.Context, t *testctx.T) {
	fal := false
	tru := true

	for _, tc := range []struct {
		name               string
		automaticGitignore *bool
	}{
		{name: "automaticGitignore=true", automaticGitignore: &tru},
		{name: "automaticGitignore unset", automaticGitignore: nil},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			_, err := moduleFixture(t, c, "go/minimal").
				With(configFile(".", &modules.ModuleConfig{
					Name: "minimal",
					SDK:  &modules.SDK{Source: "go"},
					Codegen: &modules.ModuleCodegenConfig{
						LegacyCodegenAtRuntime: &fal,
						AutomaticGitignore:     tc.automaticGitignore,
					},
				})).
				With(daggerFunctions()).
				Sync(ctx)

			requireErrOut(t, err, "codegen.legacyCodegenAtRuntime=false requires codegen.automaticGitignore=false")
		})
	}
}

// TestMissingGeneratedFiles verifies that with runtime codegen disabled and the
// committed generated files absent, calling the module fails with a clear error
// pointing the user at `dagger generate`.
func (RuntimeCodegenSuite) TestMissingGeneratedFiles(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	fal := false

	// The minimal fixture has no committed dagger.gen.go files.
	_, err := moduleFixture(t, c, "go/minimal").
		With(configFile(".", &modules.ModuleConfig{
			Name: "minimal",
			SDK:  &modules.SDK{Source: "go"},
			Codegen: &modules.ModuleCodegenConfig{
				LegacyCodegenAtRuntime: &fal,
				AutomaticGitignore:     &fal,
			},
		})).
		With(daggerCall("hello")).
		Sync(ctx)

	requireErrOut(t, err, "generated file")
	requireErrOut(t, err, "run `dagger generate`")
}
