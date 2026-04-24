package core

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

	methods := listMethodNames(ctx, t, cli)
	require.Contains(t, methods, "greeting")
	require.NotContains(t, methods, "container")

	selectMethods(ctx, t, cli, "greeting")
	require.Equal(t, "hello from module", callMethodText(ctx, t, cli, "greeting", nil, map[string]any{}))
}

func (MCPSuite) TestWithoutModuleAndWithoutPrivilegedFails(ctx context.Context, t *testctx.T) {
	emptyDir := t.TempDir()

	_, err := hostDaggerExec(ctx, t, emptyDir, "--progress=plain", "mcp")
	require.Error(t, err)
	requireErrOut(t, err, "no module found and --env-privileged not specified")
}

func (MCPSuite) TestWithoutModuleAndWithPrivilegedExposesCoreMethods(ctx context.Context, t *testctx.T) {
	emptyDir := t.TempDir()
	cli := startMCPClient(ctx, t, emptyDir, "--env-privileged")

	methods := listMethodNames(ctx, t, cli)
	require.Contains(t, methods, "container")

	selectMethods(ctx, t, cli, "container")
	require.Contains(t, callMethodText(ctx, t, cli, "container", nil, map[string]any{}), "Container#")
}

func (MCPSuite) TestModuleWithPrivilegedExposesModuleAndCoreMethods(ctx context.Context, t *testctx.T) {
	modDir := initMCPTestModule(ctx, t)
	cli := startMCPClient(ctx, t, modDir, "--env-privileged")

	methods := listMethodNames(ctx, t, cli)
	require.Contains(t, methods, "greeting")
	require.Contains(t, methods, "container")

	selectMethods(ctx, t, cli, "greeting", "container")
	require.Equal(t, "hello from module", callMethodText(ctx, t, cli, "greeting", nil, map[string]any{}))
	require.Contains(t, callMethodText(ctx, t, cli, "container", nil, map[string]any{}), "Container#")
}

func initMCPTestModule(ctx context.Context, t testing.TB) string {
	t.Helper()

	modDir := t.TempDir()

	_, err := hostDaggerExec(ctx, t, modDir, "init", "--name=test", "--sdk=go", "--source=.")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main

type Test struct{}

func (m *Test) Greeting() string {
	return "hello from module"
}
`), 0o644))

	functionsOut, err := hostDaggerExec(ctx, t, modDir, "functions")
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

func listMethodNames(ctx context.Context, t testing.TB, cli *mcpclient.Client) []string {
	t.Helper()

	listMethods, err := cli.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "ListMethods",
			Arguments: map[string]any{},
		},
	})
	require.NoError(t, err)
	require.False(t, listMethods.IsError)

	var methods []struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(toolResultText(t, listMethods)), &methods))

	names := make([]string, 0, len(methods))
	for _, method := range methods {
		names = append(names, method.Name)
	}
	return names
}

func selectMethods(ctx context.Context, t testing.TB, cli *mcpclient.Client, methods ...string) {
	t.Helper()

	_, err := cli.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "SelectMethods",
			Arguments: map[string]any{
				"methods": methods,
			},
		},
	})
	require.NoError(t, err)
}

func callMethodText(
	ctx context.Context,
	t testing.TB,
	cli *mcpclient.Client,
	method string,
	self any,
	args map[string]any,
) string {
	t.Helper()

	callMethod, err := cli.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "CallMethod",
			Arguments: map[string]any{
				"method": method,
				"self":   self,
				"args":   args,
			},
		},
	})
	require.NoError(t, err)
	require.False(t, callMethod.IsError)

	return strings.TrimSpace(toolResultText(t, callMethod))
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
