package core

import (
	"context"

	"github.com/dagger/testctx"
)

// TestGoCodegenPhase1Parity compares dagger.gen.go output produced by the
// new AST-based Go codegen path (Phase 1 via astscan + schematool) against
// the legacy packages.Load path built with -tags legacy_typedefs.
//
// Skipped until PR 2 adds the dual-build harness that lets the test
// swap between the two cmd/codegen binaries within a single run.
//
// Tracked in hack/designs/no-codegen-at-runtime-pr1-plan.md
// (see Task 5.3 in the "Commit 5" section).
func (ModuleSuite) TestGoCodegenPhase1Parity(ctx context.Context, t *testctx.T) {
	t.Skip("rebuild-with-tag harness not yet implemented; tracked in PR 2")
}
