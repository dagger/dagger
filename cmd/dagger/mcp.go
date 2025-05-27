package main

import (
	"context"
	"errors"
	"fmt"

	"dagger.io/dagger/querybuilder"
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

	q := querybuilder.Query().Client(engineClient.Dagger().GraphQLClient())
	mcpQ := q.Root().Select("__mcp")

	errCh := make(chan error)
	fmt.Fprintln(stderr, "Initializing MCP on stdio")
	go func() {
		defer close(errCh)
		var response any
		if err := makeRequest(ctx, mcpQ.Select("__serve"), &response); err != nil {
			select {
			case <-ctx.Done():
			case errCh <- fmt.Errorf("error starting MCP server: %w", err):
			}
		}
	}()

	modDef, err := initializeDefaultModule(ctx, engineClient.Dagger())
	if err != nil && err != errModuleNotFound {
		return err
	}

	if err == errModuleNotFound && !envPrivileged {
		return fmt.Errorf("%w and --env-privileged not specified", errModuleNotFound)
	}

	var logMsg string
	if modDef != nil {
		// TODO: parse user args and pass them to constructor
		modName := modDef.MainObject.AsObject.Constructor.Name
		q = q.Root().Select(modName).Select("id")

		var modID string
		if err := makeRequest(ctx, q, &modID); err != nil {
			return fmt.Errorf("error instantiating module: %w", err)
		}

		q = q.Root().Select("env")

		extraCore := ""
		if envPrivileged {
			q = q.Arg("privileged", envPrivileged)
			extraCore = " and Dagger core"
		}

		q = q.Select("with"+modDef.MainObject.AsObject.Name+"Input").
			Arg("name", modName).
			Arg("value", modID).
			Arg("description", modDef.MainObject.Description()).
			Select("id")

		logMsg = fmt.Sprintf("Serving module %q%s as an MCP server", modName, extraCore)
	} else {
		q = q.Root().Select("env").Arg("privileged", envPrivileged).Select("id")
		logMsg = "Serving Dagger core as an MCP server"
	}

	var envID string
	if err := makeRequest(ctx, q, &envID); err != nil {
		return fmt.Errorf("error making environment: %w", err)
	}

	q = mcpQ.Select("__setEnv").
		Arg("env", envID)

	var response any
	if err := makeRequest(ctx, q, &response); err != nil {
		return fmt.Errorf("error starting MCP server: %w", err)
	}

	fmt.Fprintln(stderr, logMsg)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}
