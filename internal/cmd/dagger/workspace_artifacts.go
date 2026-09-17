package daggercmd

import (
	"context"
	"fmt"

	"github.com/iancoleman/strcase"
	"github.com/jinzhu/inflection"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/engine/client"
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
	if len(args) > 0 {
		switch args[0] {
		case "help", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			args = args[1:]
		}
	}
	cmd, _ := resolveCommand(root, args)
	if cmd != workspaceCmd && cmd != artifactsCmd && cmd.Parent() != artifactsCmd {
		return nil
	}
	return withEngineSilent(ctx, client.Params{SkipWorkspaceModules: true}, func(ctx context.Context, ec *client.Client) error {
		types, err := ec.Dagger().CurrentWorkspace().Artifacts().Types(ctx)
		if err != nil {
			return err
		}
		if cmd != workspaceCmd {
			collections, err := ec.Dagger().CurrentWorkspace().Artifacts().Collections(ctx)
			if err != nil {
				return err
			}
			registerArtifactCollectionFlags(artifactsCmd, collections)
			return artifactsCmd.RegisterFlagCompletionFunc("type", cobra.FixedCompletions(types, cobra.ShellCompDirectiveNoFileComp))
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
			setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace)
			workspaceCmd.AddCommand(cmd)
		}
		return nil
	})
}

func runWorkspaceArtifacts(cmd *cobra.Command, typeName string) error {
	return withEngine(cmd.Context(), client.Params{SkipWorkspaceModules: true}, func(ctx context.Context, ec *client.Client) error {
		lines, err := ec.Dagger().CurrentWorkspace().Artifacts().FilterTypes([]string{typeName}).Pretty(ctx)
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
