package daggercmd

import (
	"context"
	"errors"
	"fmt"

	"dagger.io/dagger"
	"github.com/iancoleman/strcase"
	"github.com/jinzhu/inflection"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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

func prepareArtifactCommands(ctx context.Context, root *cobra.Command, args, rawArgs []string) error {
	discover := false
	if len(args) > 0 {
		switch args[0] {
		case "help", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			discover = true
			args = args[1:]
		}
	}
	cmd, commandArgs := resolveCommand(root, args)
	if cmd != workspaceCmd && cmd != artifactsCmd && cmd.Parent() != artifactsCmd {
		return nil
	}
	if cmd != workspaceCmd {
		if !discover {
			_, rawCommandArgs := resolveCommand(root, rawArgs)
			var err error
			discover, err = prepareArtifactDimensionFlags(cmd, rawCommandArgs)
			if err != nil {
				return err
			}
		}
		if !discover {
			return artifactsCmd.RegisterFlagCompletionFunc("type", completeArtifactTypes)
		}
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
			definitions, err := artifactDimensions(ctx, ec.Dagger(), ec.Dagger().CurrentWorkspace().Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths(addresses)}))
			if err != nil {
				return err
			}
			var names []string
			for _, def := range definitions {
				names = append(names, def.Name, def.QualifiedName, def.Identifier)
			}
			registerArtifactDimensionFlags(artifactsCmd, names)
			return nil
		}); err != nil {
			return err
		}
		return artifactsCmd.RegisterFlagCompletionFunc("type", completeArtifactTypes)
	}
	err := withEngineSilent(ctx, client.Params{SkipWorkspaceModules: true}, func(ctx context.Context, ec *client.Client) error {
		types, err := ec.Dagger().CurrentWorkspace().Artifacts().Types(ctx)
		if err != nil {
			return err
		}
		for name, typeName := range workspaceArtifactCommands(types) {
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
	})
	// Type shortcuts are optional for group help and completion. Keep errors
	// for actual shortcut invocations, where discovery is required to execute.
	if err != nil && (discover || len(commandArgs) == 0) {
		slog.Debug("skip workspace artifact commands", "error", err)
		return nil
	}
	return err
}

// Register supplied dimension flags without loading the workspace. runArtifacts
// validates them against the schema inside the command's visible engine session.
// Only help and completion need the full flag list before the command runs.
func prepareArtifactDimensionFlags(cmd *cobra.Command, args []string) (bool, error) {
	cmd.InitDefaultHelpFlag()
	flags := copyCommandFlags(cmd, "artifact dimensions")
	flags.ParseErrorsAllowlist.UnknownFlags = false
	for {
		var help bool
		err := flags.ParseAll(args, func(flag *pflag.Flag, value string) error {
			if flag.Name == "help" && value == "true" {
				help = true
			}
			return nil
		})
		var unknown *pflag.NotExistError
		if !errors.As(err, &unknown) || unknown.GetSpecifiedShortnames() != "" {
			return help, err
		}
		name := unknown.GetSpecifiedName()
		registerArtifactDimensionFlags(cmd, []string{name})
		flags.AddFlag(cmd.PersistentFlags().Lookup(name))
	}
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
