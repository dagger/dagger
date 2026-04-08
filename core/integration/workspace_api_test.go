package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
)

// WorkspaceAPISuite owns behavior of the Workspace object once a Workspace has
// already been injected or passed explicitly.
type WorkspaceAPISuite struct{}

func TestWorkspaceAPI(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceAPISuite{})
}

// TestWorkspaceFileAndDirectory should cover the core file-system accessors on
// Workspace.
func (WorkspaceAPISuite) TestWorkspaceFileAndDirectory(ctx context.Context, t *testctx.T) {
	t.Run("file reads workspace content", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement Workspace.file coverage.

Move the current coverage that reads a file from the injected Workspace into
this file.`)
	})

	t.Run("directory reads entries and subdirectories", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement Workspace.directory coverage.

Move the current coverage for listing workspace directories and subdirectories
into this file.`)
	})

	t.Run("directory exclude and gitignore filters apply", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement Workspace.directory filtering coverage.

Move the current exclude-pattern and gitignore coverage into this file.`)
	})
}

// TestWorkspacePathSafety should cover path normalization and traversal
// protection on Workspace APIs.
func (WorkspaceAPISuite) TestWorkspacePathSafety(ctx context.Context, t *testctx.T) {
	t.Run("parent-directory traversal is rejected", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement Workspace traversal rejection coverage.

Move the current '..' traversal coverage for Workspace.file and
Workspace.directory into this file.`)
	})

	t.Run("absolute paths resolve from the workspace boundary", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement absolute-path Workspace coverage.

Move the current coverage that resolves absolute paths from the workspace
boundary into this file.`)
	})
}

// TestWorkspaceFindUp should cover upward search behavior on Workspace.
func (WorkspaceAPISuite) TestWorkspaceFindUp(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement Workspace.findUp coverage.

Move the current coverage for finding files and directories up the workspace
tree into this file.`)
}

// TestWorkspaceCLIExposure should cover the fact that Workspace arguments are
// injected by the engine and not exposed as user-settable CLI flags.
func (WorkspaceAPISuite) TestWorkspaceCLIExposure(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement Workspace CLI exposure coverage.

Move the current coverage proving that Workspace arguments are callable without
flags and do not appear as --source or similar CLI arguments.`)
}
