package main

import (
	"context"
	"errors"
	"fmt"

	"dagger.io/dagger"
	"dagger.io/dagger/dag"
	"github.com/dagger/querybuilder"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var (
	mcpStdio      bool
	mcpSseAddr    string
	envPrivileged bool
)

func init() {
	mcpCmd.PersistentFlags().BoolVar(&mcpStdio, "stdio", true, "Use standard input/output for communicating with the MCP server")
	mcpCmd.PersistentFlags().BoolVar(&envPrivileged, "env-privileged", false, "Expose the core API as tools")
	mcpCmd.PersistentFlags().StringVar(&mcpSseAddr, "sse-addr", "", "Address of the MCP SSE server (no SSE server if empty)")
}

var mcpCmd = &cobra.Command{
	Use:   "mcp [options]",
	Short: "Expose a dagger module as an MCP server",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if progress == "tty" {
			return fmt.Errorf("cannot use tty progress output: it interferes with mcp stdio")
		}

		if progress == "auto" && hasTTY {
			fmt.Fprintln(stderr, "overriding 'auto' progress mode to 'plain' to avoid interference with mcp stdio")

			Frontend = idtui.NewPlain(stderr)
		}

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		cmd.SetContext(idtui.WithPrintTraceLink(ctx, true))
		return withEngine(ctx, client.Params{
			Stdin:  stdin,
			Stdout: stdout,
		}, mcpStart)
	},
	Hidden: true,
	Annotations: map[string]string{
		"experimental": "true",
	},
}

// dagger -m github.com/org/repo mcp
func mcpStart(ctx context.Context, engineClient *client.Client) error {
	if mcpSseAddr != "" || !mcpStdio {
		return errors.New("currently MCP only works with stdio")
	}

	modDef, err := initializeWorkspace(ctx, engineClient.Dagger())
	if err != nil {
		return err
	}

	if modDef == nil && !envPrivileged {
		return fmt.Errorf("no module found and --env-privileged not specified")
	}

	envID, err := dag.Env(dagger.EnvOpts{Privileged: envPrivileged}).ID(ctx)
	if err != nil {
		return fmt.Errorf("error making environment: %w", err)
	}

	q := querybuilder.Query().Client(engineClient.Dagger().GraphQLClient())
	q = q.Root().
		Select("llm").
		Select("withStaticTools").
		Select("withEnv").Arg("env", envID).
		Select("__mcp")

	var response any
	if err := makeRequest(ctx, q, &response); err != nil {
		return fmt.Errorf("error starting MCP server: %w", err)
	}

	return nil
}
