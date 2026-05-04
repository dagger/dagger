package core

import (
	"context"

	"github.com/dagger/testctx"
)

// TestGoSDKSkipCodegenAtRuntimeOptIn verifies that `dagger init --sdk=go`
// writes codegen.legacyCodegenAtRuntime=false and
// codegen.automaticGitignore=false into the generated dagger.json, and
// that a subsequent `dagger call` succeeds without re-running codegen
// (i.e. Runtime() takes the baseForCommittedCodegen path).
//
// TODO(yves): replace the Skip with a real harness run. The test should:
//   - run `dagger init --sdk=go` in a temp context directory
//   - assert the exported dagger.json contains both flags set to false
//   - run `dagger call container-echo --string-arg "hi"` and assert success
//   - ideally assert the span trace for the call has no
//     `codegen generate-module` span for the module
func (ModuleSuite) TestGoSDKSkipCodegenAtRuntimeOptIn(ctx context.Context, t *testctx.T) {
	t.Skip("integration harness wiring added in a follow-up")
}

// TestGoSDKSkipCodegenAtRuntimeValidation verifies that setting
// codegen.legacyCodegenAtRuntime=false without also setting
// codegen.automaticGitignore=false produces a clear validation error
// at module load.
//
// TODO(yves): replace the Skip with a real harness run. The test should:
//   - create a Go module with hand-edited dagger.json containing
//     `"codegen":{"legacyCodegenAtRuntime":false}` (automaticGitignore
//     unset or true)
//   - attempt to load it (e.g. `dagger functions`)
//   - assert the error contains both `legacyCodegenAtRuntime` and
//     `automaticGitignore=false`
func (ModuleSuite) TestGoSDKSkipCodegenAtRuntimeValidation(ctx context.Context, t *testctx.T) {
	t.Skip("integration harness wiring added in a follow-up")
}

// TestGoSDKSkipCodegenAtRuntimeMissingFiles verifies that when
// codegen.legacyCodegenAtRuntime=false is set but the required generated
// files (dagger.gen.go or internal/dagger/dagger.gen.go) are missing,
// Runtime() returns the specific "run dagger develop" error rather than
// a generic container / build failure.
//
// TODO(yves): replace the Skip with a real harness run. The test should:
//   - init a Go module with the opt-in flags (via dagger init)
//   - delete <srcSubpath>/dagger.gen.go from the module source
//   - run `dagger call container-echo --string-arg "hi"` and assert the
//     error message contains `dagger develop`
func (ModuleSuite) TestGoSDKSkipCodegenAtRuntimeMissingFiles(ctx context.Context, t *testctx.T) {
	t.Skip("integration harness wiring added in a follow-up")
}
