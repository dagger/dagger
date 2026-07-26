package core

// These tests cover `dagger mcp`, the Model Context Protocol server exposed by
// the CLI over stdio. Under the object-tools scheme
// (hack/designs/workspace-agents.md) the CLI binds each workspace module's
// main object via LLM.withTools, so the module's eligible methods are served
// directly as MCP tools alongside the builtins (ReadLogs, skills).

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type MCPSuite struct{}

func TestMCP(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(MCPSuite{})
}

func (MCPSuite) TestModuleWithoutPrivilegedExposesModuleMethods(ctx context.Context, t *testctx.T) {
	modDir := initMCPTestModule(ctx, t)
	cli := startMCPClient(ctx, t, modDir)

	tools := listToolNames(ctx, t, cli)
	require.Contains(t, tools, "greeting")
	require.NotContains(t, tools, "container")

	require.Contains(t, callToolText(ctx, t, cli, "greeting", map[string]any{}), "hello from module")
}

func (MCPSuite) TestWithoutModuleAndWithoutPrivilegedFails(ctx context.Context, t *testctx.T) {
	emptyDir := t.TempDir()

	_, err := hostDaggerExec(ctx, t, emptyDir, "--progress=plain", "mcp")
	require.Error(t, err)
	requireErrOut(t, err, "no module found and --env-privileged not specified")
}

func (MCPSuite) TestWithoutModuleAndWithPrivilegedServesBuiltins(ctx context.Context, t *testctx.T) {
	emptyDir := t.TempDir()
	cli := startMCPClient(ctx, t, emptyDir, "--env-privileged")

	// With no module there is no object to bind, so only the builtin tools
	// are served. (The old Env-based core-API surface was retired with the
	// object-tools scheme.)
	tools := listToolNames(ctx, t, cli)
	require.Contains(t, tools, "ReadLogs")
	require.NotContains(t, tools, "container")
}

func (MCPSuite) TestModuleWithPrivilegedStillExposesModuleMethods(ctx context.Context, t *testctx.T) {
	modDir := initMCPTestModule(ctx, t)
	cli := startMCPClient(ctx, t, modDir, "--env-privileged")

	tools := listToolNames(ctx, t, cli)
	require.Contains(t, tools, "greeting")

	require.Contains(t, callToolText(ctx, t, cli, "greeting", map[string]any{}), "hello from module")
}

func initMCPTestModule(ctx context.Context, t testing.TB) string {
	t.Helper()

	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "workspaces", "mcp-greeting")
	initGitRepo(ctx, t, modDir)

	functionsOut, err := hostDaggerExec(ctx, t, modDir, "api", "functions")
	require.NoError(t, err)
	require.Contains(t, string(functionsOut), "greeting")

	return modDir
}

// MCP tests run the CLI on the host so the stdio transport can talk directly
// to the subprocess.
func startMCPClient(ctx context.Context, t testing.TB, workdir string, extraArgs ...string) *mcpclient.Client {
	t.Helper()

	args := append([]string{"--progress=plain", "mcp"}, extraArgs...)
	stdio := transport.NewStdioWithOptions(
		daggerCliPath(t),
		nil,
		args,
		transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(ctx, command, args...)
			cleanupExec(t, cmd)
			cmd.Dir = workdir
			cmd.Env = append(os.Environ(), env...)
			return cmd, nil
		}),
	)
	cli := mcpclient.NewClient(stdio)
	t.Cleanup(func() {
		_ = cli.Close()
	})

	require.NoError(t, cli.Start(ctx))

	_, err := cli.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{
				Name:    "dagger-integration-test",
				Version: "1.0.0",
			},
		},
	})
	require.NoError(t, err)

	return cli
}

// listToolNames lists the MCP server's tools via the MCP protocol. Tools are
// the bound module objects' methods plus builtins — there is no discovery
// indirection (the old ListMethods/SelectMethods tools are gone).
func listToolNames(ctx context.Context, t testing.TB, cli *mcpclient.Client) []string {
	t.Helper()

	res, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	require.NoError(t, err)

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// callToolText invokes a tool by name and returns its text output.
func callToolText(
	ctx context.Context,
	t testing.TB,
	cli *mcpclient.Client,
	tool string,
	args map[string]any,
) string {
	t.Helper()

	res, err := cli.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      tool,
			Arguments: args,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool %q errored: %s", tool, toolResultText(t, res))

	return strings.TrimSpace(toolResultText(t, res))
}

func toolResultText(t testing.TB, result *mcp.CallToolResult) string {
	t.Helper()

	var content strings.Builder
	for _, item := range result.Content {
		text, ok := item.(mcp.TextContent)
		require.Truef(t, ok, "unexpected MCP content type %T", item)
		content.WriteString(text.Text)
	}

	return content.String()
}
