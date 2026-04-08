package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
)

// WorkspaceCompatSuite owns legacy dagger.json compatibility behavior:
// fallback compat loading, migration handoff, and direct-load error handling.
// Current workspace entrypoint behavior belongs in workspace_entrypoint_test.go.
type WorkspaceCompatSuite struct{}

func TestWorkspaceCompat(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceCompatSuite{})
}

// TestLegacyBlueprintInit replaces the old blueprint_test.go coverage.
// It should pin down what legacy --blueprint init still supports, and what it
// should reject, now that blueprint is a compatibility concept rather than a
// current workspace feature.
func (WorkspaceCompatSuite) TestLegacyBlueprintInit(ctx context.Context, t *testctx.T) {
	t.Run("local legacy blueprint init still works", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement legacy --blueprint init coverage.

Recreate the old local blueprint init flow and verify the initialized project
still works as intended under the compatibility contract.`)
	})

	t.Run("legacy blueprint init covers dependency-bearing and multi-sdk cases", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement expanded legacy --blueprint init coverage.

Carry forward the old dependency-bearing / TypeScript / Python blueprint cases
from blueprint_test.go and decide which of them remain supported as compat.`)
	})

	t.Run("legacy blueprint init still rejects --sdk with --blueprint", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement legacy --blueprint flag validation coverage.

Verify the CLI still rejects --sdk together with --blueprint and that the
error message stays clear.`)
	})
}

// TestCompatDetection should lock down which legacy dagger.json files become a
// compat workspace and which do not.
func (WorkspaceCompatSuite) TestCompatDetection(ctx context.Context, t *testctx.T) {
	t.Run("blueprint config creates a compat workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat detection coverage for legacy blueprint configs.`)
	})

	t.Run("toolchains config creates a compat workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat detection coverage for legacy toolchain configs.`)
	})

	t.Run("non-dot source creates a compat workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat detection coverage for legacy non-dot source configs.`)
	})

	t.Run("sdk-only root source does not create a compat workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement the negative compat detection case for sdk-only root-source modules.`)
	})
}

// TestCompatWarning should pin down the user-facing warning emitted when the
// engine infers workspace behavior from a legacy dagger.json.
func (WorkspaceCompatSuite) TestCompatWarning(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement compat warning coverage.

Run a compat-backed invocation and verify the warning text is emitted once,
with wording that clearly says workspace behavior is being inferred from a
legacy module config.`)
}

// TestLegacyWorkspaceDirectLoadErrors should cover the new hard failures when
// legacy workspace concepts are used through generic module loading.
func (WorkspaceCompatSuite) TestLegacyWorkspaceDirectLoadErrors(ctx context.Context, t *testctx.T) {
	t.Run("direct load tells the user to use -W", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement direct legacy workspace load error coverage.

Load a legacy workspace as a module with -m and verify the error tells the
user to load it as a workspace instead, for example with -W.`)
	})

	t.Run("local workspace module source tells the user to migrate that project", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement nested local legacy workspace source error coverage.

Point a workspace module source at another local legacy workspace and verify
the error tells the user to run dagger migrate there and retarget one of its
migrated modules.`)
	})

	t.Run("remote workspace module source requires a migrated upstream", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement nested remote legacy workspace source error coverage.

Point a workspace module source at a remote legacy workspace and verify the
error clearly says a migrated upstream ref is required.`)
	})
}

// TestCompatMigration should cover the explicit handoff from compat runtime to
// workspace migration.
func (WorkspaceCompatSuite) TestCompatMigration(ctx context.Context, t *testctx.T) {
	t.Run("migrate converts a compat workspace into workspace config plus modules", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat migration coverage.

Run dagger migrate -y on a compat-eligible project and verify the legacy
dagger.json is replaced by .dagger/config.toml plus migrated module files.`)
	})

	t.Run("migrate is a no-op for sdk-only root-source modules", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat migration no-op coverage for sdk-only root-source modules.`)
	})

	t.Run("migrate writes a migration report for unsupported gaps", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat migration gap-report coverage.

Verify gap warnings are surfaced to the user and .dagger/migration-report.md is
written when manual follow-up is required.`)
	})
}

// TestCompatAndMigratedWorkspaceMatch should prove the core contract of the
// new design: compat mode and migrated workspace mode expose the same runtime
// behavior for the same legacy project.
func (WorkspaceCompatSuite) TestCompatAndMigratedWorkspaceMatch(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement compat-vs-migrated equivalence coverage.

For the same legacy project, compare a compat-backed invocation with the same
project after dagger migrate -y. Verify they expose the same entrypoint and
module behavior.`)
}
