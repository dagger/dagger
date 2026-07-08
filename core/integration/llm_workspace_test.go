package core

// Tests for binding an LLM directly to a workspace (LLM.withWorkspace).

import (
	"context"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceToolParity locks in that binding an LLM to a workspace exposes
// exactly the functions the Dagger CLI serves for that workspace (via the
// ambient served schema), and does not auto-construct modules' methods as extra
// top-level tools (which would exceed the CLI's view and collide names).
func (LLMSuite) TestWorkspaceToolParity(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-managed")

	tools, err := base.With(daggerShell(
		"llm | with-workspace --workspace $(current-workspace) | tools",
	)).Stdout(ctx)
	require.NoError(t, err)

	// The workspace's served functions surface as tools: the entrypoint
	// module's method (greet) plus the other configured modules (greeter,
	// objects) as constructors — matching `dagger api functions`.
	require.Contains(t, tools, "## greet\n")
	require.Contains(t, tools, "## greeter\n")
	require.Contains(t, tools, "## objects\n")

	// Modules' own methods are NOT auto-constructed as top-level tools.
	require.NotContains(t, tools, "## objectA")
}
