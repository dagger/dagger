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
		params := client.Params{
			Stdin:  stdin,
			Stdout: stdout,
		}
		// Pass -m to engine so the module is loaded at connect time
		if !moduleNoURL {
			modRef, _ := getExplicitModuleSourceRef()
			if modRef != "" {
				params.Module = modRef
			}
		}
		return withEngine(ctx, params, mcpStart)
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
	// -m modules are loaded at engine connect time as extra modules.
	modDef, _ := initializeWorkspace(ctx, engineClient.Dagger())
	if modDef != nil && !modDef.HasModule() {
		// When -m is used, modDef.Name is "" but the module constructor
		// is at Query root. Find it.
		modDef = findAutoAliasedModule(modDef)
	}

	if modDef == nil && !envPrivileged {
		return fmt.Errorf("no module found and --env-privileged not specified")
	}

	q := querybuilder.Query().Client(engineClient.Dagger().GraphQLClient())
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

		logMsg = fmt.Sprintf("Exposing module %q%s as an MCP server on standard input/output", modName, extraCore)
	} else {
		q = q.Root().Select("env").Arg("privileged", envPrivileged).Select("id")
		logMsg = "Exposing Dagger core as an MCP server"
	}

	var envID string
	if err := makeRequest(ctx, q, &envID); err != nil {
		return fmt.Errorf("error making environment: %w", err)
	}

	fmt.Fprintln(stderr, logMsg)
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

// findAutoAliasedModule detects a module loaded with auto-alias at Query root.
// When -m is used, the module's constructor appears as a Query root function
// whose return type has a non-empty SourceModuleName. If exactly one such
// constructor is found, return a moduleDef pointing to it as the main object.
func findAutoAliasedModule(def *moduleDef) *moduleDef {
	if def == nil || def.MainObject == nil {
		return nil
	}
	fp := def.MainObject.AsFunctionProvider()
	if fp == nil {
		return nil
	}
	for _, fn := range fp.GetFunctions() {
		if fn.ReturnType.AsObject != nil && fn.ReturnType.AsObject.SourceModuleName != "" {
			// Found a module constructor — set up modDef to point to this module
			return &moduleDef{
				Name:       fn.ReturnType.AsObject.SourceModuleName,
				MainObject: fn.ReturnType,
				Objects:    def.Objects,
				Interfaces: def.Interfaces,
				Enums:      def.Enums,
				Inputs:     def.Inputs,
			}
		}
	}
	return nil
}
