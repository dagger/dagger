package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
)

// WorkspaceEntrypointSuite owns current workspace entrypoint behavior.
// Legacy dagger.json compatibility belongs in workspace_compat_test.go.
type WorkspaceEntrypointSuite struct{}

func TestWorkspaceEntrypoint(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceEntrypointSuite{})
}

// TestConfiguredEntrypoint should cover the current replacement for legacy
// blueprint behavior: one workspace module marked `entrypoint = true` in
// config.toml owns the Query root, while sibling modules remain namespaced.
func (WorkspaceEntrypointSuite) TestConfiguredEntrypoint(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement current workspace entrypoint coverage.

Build a normal initialized workspace with .dagger/config.toml and verify:
- the configured entrypoint module exposes its functions at Query root
- sibling workspace modules remain callable under their module names
- dagger functions and dagger call both reflect the same layout

This test should exercise current workspace config only, not legacy compat.`)
}

// TestEntrypointWorkspaceArg should prove that an entrypoint module can still
// take a Workspace argument and access the caller's workspace files.
func (WorkspaceEntrypointSuite) TestEntrypointWorkspaceArg(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement workspace entrypoint + Workspace argument coverage.

Set up a workspace where the entrypoint module constructor takes Workspace.
Verify it can read workspace files and that the same functions are callable
from the Query root.`)
}

// TestEntrypointValidation should lock down ambient workspace validation.
// Multiple distinct modules marked entrypoint in config.toml must be rejected.
func (WorkspaceEntrypointSuite) TestEntrypointValidation(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement workspace entrypoint validation coverage.

Create a workspace config with more than one distinct ambient module marked
entrypoint = true and verify workspace load fails with a clear validation
error instead of serving an ambiguous Query root.`)
}

// TestEntrypointArbitration should cover the runtime precedence rules after
// module deduplication: extra modules (-m) > CWD module > ambient workspace.
func (WorkspaceEntrypointSuite) TestEntrypointArbitration(ctx context.Context, t *testctx.T) {
	t.Run("cwd module overrides ambient entrypoint", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement CWD-vs-ambient entrypoint arbitration coverage.

Build a workspace with an ambient entrypoint module, then invoke Dagger from a
nested module directory. Verify the nested CWD module becomes the active
entrypoint and the ambient workspace remains loaded as context.`)
	})

	t.Run("extra module suppresses cwd module", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement extra-module-vs-CWD entrypoint arbitration coverage.

Invoke Dagger with -m from inside a nested module directory. Verify the extra
module becomes the active entrypoint and the CWD module is not loaded as a
second entrypoint.`)
	})

	t.Run("duplicate nominations are deduped before arbitration", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement duplicate nomination dedupe coverage.

Nominate the same module through more than one path, for example via workspace
config and -m, and verify it is loaded once before entrypoint arbitration
runs.`)
	})
}
