package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
)

// ModuleLoadingSuite owns runtime module loading from every nomination path:
// workspace config, CWD module, -m, and extra modules. This file is about
// what actually loads and which module wins as the active entrypoint.
type ModuleLoadingSuite struct{}

func TestModuleLoading(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleLoadingSuite{})
}

// TestAmbientWorkspaceModuleLoading should pin down the baseline runtime shape
// of a configured workspace: one ambient entrypoint promoted to Query root,
// sibling modules still loaded under their names, and the same layout visible
// through dagger functions, dagger call, and GraphQL.
func (ModuleLoadingSuite) TestAmbientWorkspaceModuleLoading(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement ambient workspace module loading coverage.

Build a normal initialized workspace with config-owned modules and verify:
- the configured ambient entrypoint owns the Query root
- sibling workspace modules remain callable under their module names
- dagger functions, dagger call, and GraphQL all reflect the same module layout

Move the current entrypoint/root-promotion coverage here as it is implemented.`)
}

// TestAmbientWorkspaceValidation should lock down invalid ambient workspace
// configurations before any runtime loading occurs.
func (ModuleLoadingSuite) TestAmbientWorkspaceValidation(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement ambient workspace validation coverage.

Create an invalid workspace config, for example with multiple distinct ambient
entrypoint modules, and verify workspace load fails with a clear validation
error instead of serving an ambiguous Query root.`)
}

// TestModuleLoadingPrecedence should cover the explicit runtime precedence
// rules after dedupe: extra modules > CWD module > ambient workspace modules.
func (ModuleLoadingSuite) TestModuleLoadingPrecedence(ctx context.Context, t *testctx.T) {
	t.Run("cwd module overrides ambient entrypoint", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement CWD-vs-ambient precedence coverage.

Invoke Dagger from inside a nested module directory under an initialized
workspace. Verify the nested CWD module becomes the active entrypoint while the
ambient workspace remains loaded as context.`)
	})

	t.Run("extra module suppresses cwd module", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement extra-module-vs-CWD precedence coverage.

Invoke Dagger with -m from inside a nested module directory. Verify the extra
module becomes the active entrypoint and the CWD module is not loaded as a
second entrypoint.`)
	})

	t.Run("extra modules override ambient workspace entrypoint", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement extra-vs-ambient precedence coverage.

Nominate an ambient workspace entrypoint and a distinct extra module in the
same invocation. Verify the extra module wins as the active entrypoint.`)
	})
}

// TestModuleLoadingDedupeAndConflicts should cover generic module dedupe and
// the same-tier conflict errors introduced by entrypoint arbitration.
func (ModuleLoadingSuite) TestModuleLoadingDedupeAndConflicts(ctx context.Context, t *testctx.T) {
	t.Run("duplicate nominations are deduped before arbitration", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement duplicate nomination dedupe coverage.

Nominate the same module through more than one path, for example via workspace
config and -m, and verify it is loaded once before entrypoint arbitration
runs.`)
	})

	t.Run("multiple distinct extra entrypoints are rejected", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement same-tier extra entrypoint conflict coverage.

Request more than one distinct extra module as an entrypoint candidate and
verify the runtime rejects the invocation with a clear error.`)
	})

	t.Run("multiple distinct ambient entrypoints are rejected", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement same-tier ambient entrypoint conflict coverage.

Serve a workspace config that nominates more than one distinct ambient
entrypoint and verify load fails with a clear error.`)
	})
}

// TestEntryPointRootRouting should cover the root-level routing and shadowing
// edge cases once an entrypoint module wins arbitration.
func (ModuleLoadingSuite) TestEntrypointRootRouting(ctx context.Context, t *testctx.T) {
	t.Run("root field shadowing", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement root-field shadowing coverage.

Move the current coverage for entrypoint methods that shadow core API fields
like container, file, and directory into this file.`)
	})

	t.Run("constructor argument overlap", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement entrypoint constructor-arg overlap coverage.

Move the current coverage for overlapping constructor and method arg names into
this file.`)
	})

	t.Run("self-named method does not recurse", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement self-named entrypoint method coverage.

Move the current coverage for an entrypoint method whose name matches the
module/main object name into this file.`)
	})

	t.Run("core return types still work through entrypoint proxies", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement core return type proxy coverage.

Move the current coverage for Directory/File/Container return types through
entrypoint proxies into this file.`)
	})

	t.Run("directory field does not recurse in container runtime plumbing", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement directory-field recursion coverage.

Move the current coverage for an entrypoint module with a Directory field,
which used to recurse through the outer Query root, into this file.`)
	})
}
