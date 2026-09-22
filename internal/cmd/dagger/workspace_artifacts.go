package daggercmd

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/iancoleman/strcase"
	"github.com/jinzhu/inflection"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
)

func workspaceArtifactCommands(types []string) map[string]string {
	byName := map[string][]string{}
	for _, typeName := range types {
		name := inflection.Plural(strcase.ToKebab(typeName))
		byName[name] = append(byName[name], typeName)
	}
	commands := map[string]string{}
	for name, matches := range byName {
		if len(matches) == 1 {
			commands[name] = matches[0]
		} else {
			for _, typeName := range matches {
				commands[typeName] = typeName
			}
		}
	}
	return commands
}

func prepareArtifactCommands(ctx context.Context, root *cobra.Command, args []string) error {
	completing := false
	helping := false
	if len(args) > 0 {
		switch args[0] {
		case "help", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			completing = args[0] != "help"
			helping = true
			args = args[1:]
		}
	}
	cmd, commandArgs := resolveCommand(root, args)
	if cmd != workspaceCmd && cmd != artifactsCmd && cmd.Parent() != artifactsCmd {
		return nil
	}
	if cmd != workspaceCmd {
		// parseGlobalFlags already removed flags and their values.
		paths := commandArgs
		if cmd.Name() == "keys" && len(paths) > 0 {
			paths = paths[1:]
		}
		addresses, err := parseArtifactAddresses(paths)
		if err != nil {
			return err
		}
		params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, paths)
		if err != nil {
			return err
		}
		if err := withEngineSilent(ctx, params, func(ctx context.Context, ec *client.Client) error {
			dimensions, err := ec.Dagger().CurrentWorkspace().Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths(addresses)}).Dimensions(ctx)
			if err != nil {
				return err
			}
			registerArtifactDimensionFlags(artifactsCmd, dimensions)
			return nil
		}); err != nil {
			return err
		}
		return artifactsCmd.RegisterFlagCompletionFunc("type", completeArtifactTypes)
	}
	// Group help discovers type shortcuts after the progress frontend is ready.
	if len(commandArgs) == 0 && !completing {
		return nil
	}
	err := withEngineSilent(ctx, client.Params{SkipWorkspaceModules: true}, loadWorkspaceArtifactCommands)
	// Type shortcuts are optional for group help and completion. Keep errors
	// for actual shortcut invocations, where discovery is required to execute.
	if err != nil && (helping || len(commandArgs) == 0) {
		slog.Debug("skip workspace artifact commands", "error", err)
		return nil
	}
	return err
}

func workspaceHelp(cmd *cobra.Command, args []string) {
	if cmd == workspaceCmd {
		applyCommandProgressDefaults(cmd)
		if err := withEngine(cmd.Context(), client.Params{SkipWorkspaceModules: true}, loadWorkspaceArtifactCommands); err != nil {
			slog.Debug("skip workspace artifact commands", "error", err)
		}
	}
	rootCmd.HelpFunc()(cmd, args)
}

func loadWorkspaceArtifactCommands(ctx context.Context, ec *client.Client) error {
	types, err := readArtifactTypes(ctx, ec.Dagger(), ec.Dagger().CurrentWorkspace().Artifacts())
	if err != nil {
		return err
	}
	for name, typeName := range workspaceArtifactCommands(artifactTypeNames(types)) {
		cmd := &cobra.Command{
			Use:   name,
			Short: "List " + typeName + " artifacts",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runWorkspaceArtifacts(cmd, typeName)
			},
		}
		registerArtifactListFlags(cmd)
		setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace)
		workspaceCmd.AddCommand(cmd)
	}
	return nil
}

func runWorkspaceArtifacts(cmd *cobra.Command, typeName string) error {
	return withEngine(cmd.Context(), client.Params{SkipWorkspaceModules: true}, func(ctx context.Context, ec *client.Client) error {
		absolute, _ := cmd.Flags().GetBool("absolute")
		lines, err := artifactURIs(ctx, ec.Dagger(), ec.Dagger().CurrentWorkspace().Artifacts().FilterTypes([]string{typeName}), absolute)
		if err != nil {
			return err
		}
		for _, line := range lines {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), line); err != nil {
				return err
			}
		}
		return nil
	})
}
