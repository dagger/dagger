package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
)

// WorkspaceMigrationSuite owns explicit workspace migration behavior through
// dagger migrate.
type WorkspaceMigrationSuite struct{}

func TestWorkspaceMigration(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceMigrationSuite{})
}

// TestWorkspaceMigratePreviewAndApply should cover the main CLI lifecycle now
// that migrate is preview-by-default and apply-with-yes.
func (WorkspaceMigrationSuite) TestWorkspaceMigratePreviewAndApply(ctx context.Context, t *testctx.T) {
	t.Run("preview reports changes without applying them", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement migration preview coverage.

Run dagger migrate without -y and verify it previews the changeset without
modifying files on disk.`)
	})

	t.Run("apply writes workspace config and migrated modules", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement migration apply coverage.

Run dagger migrate -y on a compat-eligible project and verify the legacy
dagger.json is replaced by .dagger/config.toml plus migrated module files.`)
	})
}

// TestWorkspaceMigrateOutcomes should cover the main result classes of a
// migration.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateOutcomes(ctx context.Context, t *testctx.T) {
	t.Run("non-local source moves into a workspace module", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement non-local source migration coverage.

Move the current coverage for migrating source = "ci" into this file.`)
	})

	t.Run("sdk-only root-source modules are a no-op", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement no-op migration coverage for sdk-only root-source modules.`)
	})

	t.Run("remote refs refresh lock entries", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement migration lock refresh coverage.

Move the current lock-entry refresh coverage for migrated remote refs into this
file.`)
	})
}

// TestWorkspaceMigrateUserFeedback should cover the user-facing output of
// explicit migration.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateUserFeedback(ctx context.Context, t *testctx.T) {
	t.Run("summary is printed for applied migrations", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement migration summary coverage.

Move the current 'Migrated to workspace format' output coverage into this
file.`)
	})

	t.Run("migration report is written for unsupported gaps", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement migration report coverage.

Move the current gap-report coverage into this file and ensure the config file
stays free of warning graffiti.`)
	})
}

// TestWorkspaceMigrateScope should lock down what the migration actually uses
// as input.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateScope(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement migration scope coverage.

Verify Workspace.migrate operates on the compat workspace already attached to
the loaded Workspace rather than rediscovering a target from disk.`)
}
