package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"dagger.io/dagger/querybuilder"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var (
	mcpStdio   bool
	mcpSseAddr string
)

func init() {
	mcpCmd.PersistentFlags().BoolVar(&mcpStdio, "stdio", true, "Use standard input/output for communicating with the MCP server")
	mcpCmd.PersistentFlags().StringVar(&mcpSseAddr, "sse-addr", "", "Address of the MCP SSE server (no SSE server if empty)")
}

var mcpCmd = &cobra.Command{
	Use:   "mcp [options]",
	Short: "Expose a dagger module as an MCP server",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		cmd.SetContext(idtui.WithPrintTraceLink(ctx, true))
		return withEngine(ctx, client.Params{}, mcpStart)
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
	modDef, err := initializeDefaultModule(ctx, engineClient.Dagger())
	if err != nil {
		return err
	}
	q := querybuilder.Query().Client(engineClient.Dagger().GraphQLClient())
	// TODO: parse user args and pass them to constructor
	modName := modDef.MainObject.AsObject.Constructor.Name
	q = q.Root().Select(modName).Select("id")

	var modID string
	if err := makeRequest(ctx, q, &modID); err != nil {
		return fmt.Errorf("error making request: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Exposing module %q as an MCP server on standard input/output\n", modName)
	q = q.Root().
		Select("llm").
		Select("with"+modDef.MainObject.AsObject.Name).
		Arg("value", modID).
		Select("__mcp")

	var response any
	if err := makeRequest(ctx, q, &response); err != nil {
		return fmt.Errorf("error making request: %w", err)
	}

	return nil
}
