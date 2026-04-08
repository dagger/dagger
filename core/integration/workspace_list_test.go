package core

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestWorkspaceList(ctx context.Context, t *testctx.T) {
	workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true

[modules.wolfi]
source = "github.com/dagger/dagger/modules/wolfi"
`)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "list")
	require.NoError(t, err)

	output := string(out)
	require.Contains(t, output, "Source paths below are resolved and shown relative to the workspace root")
	require.Contains(t, output, "* indicates a module is the workspace entrypoint")
	require.Contains(t, output, "greeter*")
	require.Contains(t, output, ".dagger/modules/greeter")
	require.Contains(t, output, "wolfi")
	require.Contains(t, output, "github.com/dagger/dagger/modules/wolfi")
	require.Less(t, strings.Index(output, "greeter*"), strings.Index(output, "wolfi"))
}
