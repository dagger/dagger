package core

// Workspace alignment: aligned structurally, but coverage is still incomplete.
// Scope: Explicit workspace selection and binding via --workspace/-W, including declared local or remote refs, --workdir interaction, command policy, metadata-only commands, and explicit env overlays.
// Intent: Own the declared-workspace contract end to end so contextual inference, compat opt-in, and native loading arbitration can evolve independently.

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
)

// WorkspaceSelectionSuite owns the explicit workspace-selection contract:
// how a declared workspace is chosen, which commands accept it, and how that
// binding propagates through the session once selected.
type WorkspaceSelectionSuite struct{}

func TestWorkspaceSelection(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceSelectionSuite{})
}

// TestDeclaredWorkspaceSelection should pin down the main user-visible
// selection contract for --workspace/-W before any compat or ambient find-up
// behavior is involved.
func (WorkspaceSelectionSuite) TestDeclaredWorkspaceSelection(ctx context.Context, t *testctx.T) {
	t.Run("local -W selects that workspace instead of cwd inference", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement explicit local workspace selection coverage.

Create two distinct initialized native workspaces with different entrypoint
behavior. Invoke Dagger from inside one workspace while passing -W to the
other. Verify currentWorkspace, root-level calls, and workspace-backed module
loading all come from the declared workspace rather than the caller's cwd.`)
	})

	t.Run("remote -W selects a git workspace without relying on host cwd", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement explicit remote workspace selection coverage.

Point -W at a remote git workspace ref and verify a read-only command such as
functions, call, or workspace info resolves that workspace successfully. The
assertion should prove the selected workspace is remote-backed and does not
depend on the caller already being inside a matching local checkout.`)
	})

	t.Run("relative -W is resolved after --workdir changes cwd", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement --workdir plus relative -W coverage.

Run a command with both --workdir and -W using a relative workspace path.
Verify the selected workspace is resolved relative to the post-workdir cwd, not
the original shell cwd. This should lock down the documented ordering contract
for path resolution.`)
	})

	t.Run("declared workspace wins over ambient workspace and cwd module nomination", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement declared-workspace precedence coverage.

Invoke Dagger from a nested module directory inside one initialized workspace
while passing -W to a second workspace. Verify the declared workspace binding
wins over both ambient workspace inference and CWD-module nomination.`)
	})
}

// TestWorkspaceSelectionCommandPolicy should pin down which commands accept
// --workspace and where local-only restrictions are enforced.
func (WorkspaceSelectionSuite) TestWorkspaceSelectionCommandPolicy(ctx context.Context, t *testctx.T) {
	t.Run("module-centric commands reject -W in integration", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement unsupported-command policy coverage for --workspace.

Exercise commands like dagger module ... and dagger migrate with -W and verify
they fail before execution with the intended CLI policy error. This should
prove the user-facing contract, not just the unit-level flag policy helper.`)
	})

	t.Run("local-only workspace mutations accept a local selected workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement positive local-only mutation coverage for --workspace.

Run commands like workspace init, workspace config set, install, update, or
lock update against a selected local workspace path and verify they mutate the
declared workspace rather than the caller's cwd.`)
	})

	t.Run("local-only workspace mutations reject a remote selected workspace at execution time", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement remote local-only mutation coverage for --workspace.

Run a local-only mutating command with -W pointing at a remote git workspace.
Verify the command is accepted by CLI parsing but fails later with the intended
engine or schema local-only error, rather than being classified as remote in
the CLI.`)
	})
}

// TestSelectedWorkspaceMetadataCommands should own commands whose purpose is to
// inspect the selected workspace rather than to run one of its modules.
func (WorkspaceSelectionSuite) TestSelectedWorkspaceMetadataCommands(ctx context.Context, t *testctx.T) {
	t.Run("workspace info reports the selected local workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace info coverage for explicit local selection.

Invoke dagger workspace info with -W against a local initialized workspace from
outside that repo. Verify address, path, and config path describe the selected
workspace rather than the caller's cwd.`)
	})

	t.Run("workspace info reports the selected remote workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace info coverage for explicit remote selection.

Invoke dagger workspace info with -W against a remote git workspace and verify
the reported address and path come from the declared remote workspace. This
should exercise the metadata-only path that skips workspace module loading.`)
	})

	t.Run("workspace list uses the selected workspace instead of cwd", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace list coverage for explicit selection.

Create a workspace with a distinctive module list, invoke dagger workspace list
from an unrelated cwd with -W, and verify the listed modules come from the
selected workspace. This should prove workspace list honors declared binding
without relying on contextual inference.`)
	})
}

// TestSelectedWorkspaceEnvOverlay should cover the end-to-end interaction
// between declared workspace selection and --env.
func (WorkspaceSelectionSuite) TestSelectedWorkspaceEnvOverlay(ctx context.Context, t *testctx.T) {
	t.Run("env overlay applies to the explicitly selected workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement explicit selection plus env overlay coverage.

Create at least two initialized workspaces, each with different env overlays,
and invoke Dagger with both -W and --env. Verify the overlay is resolved from
the selected workspace config and affects that workspace's modules at runtime.`)
	})

	t.Run("undefined env name fails against the selected workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement missing env overlay coverage for explicit selection.

Invoke a selected native workspace with --env set to a nonexistent overlay and
verify the failure names the missing env in the selected workspace config.`)
	})

	t.Run("env overlay does not work for selections without native workspace config", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement no-config env overlay coverage for explicit selection.

Use --env with a selected workspace binding that does not have .dagger/config.toml
and verify the failure is the explicit "requires .dagger/config.toml" path.
This should make the contract clear for compat and bare module selections.`)
	})
}

// TestDeclaredWorkspaceBindingPropagation should pin down how an explicit
// workspace binding survives once a session is established and other clients
// are created from it.
func (WorkspaceSelectionSuite) TestDeclaredWorkspaceBindingPropagation(ctx context.Context, t *testctx.T) {
	t.Run("nested clients inherit the declared workspace binding", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement nested-client declared-workspace propagation coverage.

Start a session with -W, then create a nested client from inside module code or
inside a nested CLI invocation. Verify currentWorkspace in the nested client is
still bound to the originally declared workspace rather than falling back to
host detection or inheritance from the nested cwd.`)
	})

	t.Run("nested clients inherit the declared workspace env overlay", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement nested-client workspace env propagation coverage.

Start a session with both -W and --env, then create a nested client and verify
the nested client still sees the same selected workspace plus the same env
overlay. This should cover the end-to-end behavior behind forwarded client
metadata, not just the unit helper that copies it.`)
	})
}
