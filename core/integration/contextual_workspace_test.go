package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
)

// ContextualWorkspaceSuite owns how the ambient/default Workspace is inferred
// from invocation context. This includes whether a Workspace is injected at
// all, which workspace wins, and how cache invalidation works for that input.
type ContextualWorkspaceSuite struct{}

func TestContextualWorkspace(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ContextualWorkspaceSuite{})
}

// TestContextualWorkspaceSelection should cover which workspace gets injected
// from context before any module code runs.
func (ContextualWorkspaceSuite) TestContextualWorkspaceSelection(ctx context.Context, t *testctx.T) {
	t.Run("initialized workspace is inferred from nearest config boundary", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement initialized workspace inference coverage.

Invoke Dagger from nested directories under an initialized workspace and verify
the injected Workspace resolves to the nearest .dagger/config.toml boundary.`)
	})

	t.Run("legacy compat workspace is inferred when no config exists", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat workspace inference coverage.

Invoke Dagger from a compat-eligible legacy project with no .dagger/config.toml
and verify the injected Workspace is the inferred compat workspace.`)
	})

	t.Run("non-eligible legacy module does not inject a workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement negative workspace inference coverage.

Use a standalone module that is not an initialized workspace and not
compat-eligible. Verify no ambient Workspace is inferred for injection.`)
	})
}

// TestContextualWorkspaceShape should pin down the observable properties of
// the injected Workspace once it has been selected.
func (ContextualWorkspaceSuite) TestContextualWorkspaceShape(ctx context.Context, t *testctx.T) {
	t.Run("workspace path and address reflect injected boundary", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement contextual workspace path/address coverage.

Verify the injected Workspace reports the correct path and address relative to
the invocation location and selected workspace boundary.`)
	})

	t.Run("workspace findUp is rooted at the injected boundary", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement contextual workspace boundary coverage for findUp.

Verify Workspace.findUp uses the inferred workspace boundary rather than the
module root or current working directory.`)
	})
}

// TestContextualWorkspaceCaching should cover cache behavior for functions
// that receive a Workspace from ambient context.
func (ContextualWorkspaceSuite) TestContextualWorkspaceCaching(ctx context.Context, t *testctx.T) {
	t.Run("same relevant workspace content hits cache", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement contextual workspace cache-hit coverage.

Run the same function twice with unchanged relevant workspace content and
verify the second call is served from cache.`)
	})

	t.Run("unrelated file changes do not invalidate scoped workspace inputs", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement contextual workspace unrelated-change cache coverage.

Change files outside the constructor's selected Workspace subpath and verify
the cached result is still reused.`)
	})

	t.Run("relevant file changes invalidate cache", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement contextual workspace invalidation coverage.

Change files the constructor actually reads from the injected Workspace and
verify the function re-executes.`)
	})
}
