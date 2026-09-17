package daggercmd

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

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

func runWorkspaceArtifacts(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	return withEngine(cmd.Context(), client.Params{SkipWorkspaceModules: true}, func(ctx context.Context, ec *client.Client) error {
		artifacts := ec.Dagger().CurrentWorkspace().Artifacts()
		types, err := artifacts.Types(ctx)
		if err != nil {
			return err
		}
		commands := workspaceArtifactCommands(types)
		typeName, ok := commands[args[0]]
		if !ok {
			return fmt.Errorf("unknown workspace command %q; available artifact listings: %s", args[0], strings.Join(slices.Sorted(maps.Keys(commands)), ", "))
		}
		lines, err := artifacts.FilterTypes([]string{typeName}).Pretty(ctx)
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

func completeWorkspaceArtifacts(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []string
	err := withEngineSilent(cmd.Context(), client.Params{SkipWorkspaceModules: true}, func(ctx context.Context, ec *client.Client) error {
		types, err := ec.Dagger().CurrentWorkspace().Artifacts().Types(ctx)
		if err != nil {
			return err
		}
		commands := workspaceArtifactCommands(types)
		for _, name := range slices.Sorted(maps.Keys(commands)) {
			if strings.HasPrefix(name, toComplete) {
				completions = append(completions, name+"\tList "+commands[name]+" artifacts")
			}
		}
		return nil
	})
	if err != nil {
		cobra.CompErrorln(err.Error())
		return nil, cobra.ShellCompDirectiveError
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
