package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
)

// WorkspaceModulesSuite owns configuration-facing module behavior in a
// workspace: installing modules, listing them, naming them, and keeping their
// configured sources correct.
type WorkspaceModulesSuite struct{}

func TestWorkspaceModules(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceModulesSuite{})
}

// TestWorkspaceModuleInstall should cover module installation into a
// workspace, through both CLI commands and CurrentWorkspace.Install.
func (WorkspaceModulesSuite) TestWorkspaceModuleInstall(ctx context.Context, t *testctx.T) {
	t.Run("install initializes a workspace when needed", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace module install-init coverage.

Move the current coverage for installing into a repo with no existing workspace
config into this file.`)
	})

	t.Run("local installs are rewritten relative to .dagger", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement relative local module install coverage.

Move the current coverage that rewrites local module refs relative to .dagger
into this file.`)
	})

	t.Run("absolute local installs preserve absolute source paths", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement absolute local module install coverage.

Carry forward the absolute local source coverage and verify the configured
source path stays absolute in workspace config and workspace list output.`)
	})

	t.Run("install rejects non-module refs without corrupting config", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement non-module install rejection coverage.

Move the current coverage that rejects non-module directories without writing
[modules.] or otherwise corrupting the workspace config into this file.`)
	})
}

// TestWorkspaceModuleListing should cover how configured modules are rendered
// back to the user.
func (WorkspaceModulesSuite) TestWorkspaceModuleListing(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement workspace module listing coverage.

Move the current workspace list coverage into this file and expand it to cover
absolute local sources as well as entrypoint markers.`)
}

// TestWorkspaceModuleMutation should cover updates and config-level conflicts
// around configured modules.
func (WorkspaceModulesSuite) TestWorkspaceModuleMutation(ctx context.Context, t *testctx.T) {
	t.Run("name collisions are rejected without rewriting config", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace module collision coverage.

Move the current duplicate-name rejection coverage into this file.`)
	})

	t.Run("local dependency updates are rejected when unsupported", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace module update rejection coverage.

Move the current module update rejection coverage for local dependencies into
this file.`)
	})
}

// TestWorkspaceModuleInit should cover workspace-oriented module init flows.
func (WorkspaceModulesSuite) TestWorkspaceModuleInit(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement workspace module init coverage.

Move the current workspace_module_init_test.go coverage into this file once the
desired module-init UX for workspaces is locked.`)
}
