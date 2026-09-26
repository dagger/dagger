package daggercmd

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/jinzhu/inflection"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func artifactTypeCommands(types []string) map[string]string {
	byName := map[string][]string{}
	for _, typeName := range types {
		name := inflection.Plural(cliName(typeName))
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
	helpCommand := len(args) > 0 && args[0] == "help"
	completing := false
	discover := false
	if len(args) > 0 {
		switch args[0] {
		case "help", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			completing = args[0] != "help"
			discover = true
			args = args[1:]
		}
	}
	cmd, commandArgs := resolveCommand(root, args)
	if cmd != listCmd && cmd.Parent() != listCmd && !isArtifactCommand(cmd) {
		return nil
	}
	if !discover {
		_, rawCommandArgs := resolveCommand(root, rawArgs)
		discover = needsArtifactDiscovery(cmd, rawCommandArgs)
	}
	withDiscovery := func(action string, params client.Params, fn runClientCallback) error {
		if completing {
			return withEngineSilent(ctx, params, fn)
		}
		return withEngineMetadata(ctx, action, params, fn)
	}
	if cmd == listCmd {
		// Root help discovers commands after the progress frontend is ready.
		if !completing && len(commandArgs) == 0 && (!discover || helpCommand) {
			return nil
		}
		if !completing && len(commandArgs) == 0 {
			_, rawCommandArgs := resolveCommand(root, rawArgs)
			flags := copyCommandFlags(cmd, "list")
			help := false
			if err := flags.ParseAll(rawCommandArgs, func(flag *pflag.Flag, value string) error {
				if flag.Name == "help" {
					help = value == "true"
				}
				return nil
			}); err != nil {
				return err
			}
			if help {
				return nil
			}
		}
		requested := ""
		if len(commandArgs) > 0 {
			requested = commandArgs[0]
		}
		params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, commandArgs)
		if err != nil {
			return err
		}
		if err := withDiscovery("Load available types", params, func(ctx context.Context, ec *client.Client) error {
			return loadListCommands(ctx, ec, requested)
		}); err != nil {
			return err
		}
		return listCmd.RegisterFlagCompletionFunc("type", completeArtifactTypes)
	}
	if !discover {
		return nil
	}
	params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, commandArgs)
	if err != nil {
		return err
	}
	return withDiscovery("Load "+cmd.Name()+" filters", params, func(ctx context.Context, ec *client.Client) error {
		id, err := ec.Dagger().CurrentWorkspace().Artifacts().ID(ctx)
		if err != nil {
			return err
		}
		all := dagger.Ref[*dagger.Artifacts](ec.Dagger(), id)
		definitions, err := artifactDimensions(ctx, ec.Dagger(), all)
		if err != nil {
			return err
		}
		selected, err := artifactDimensions(ctx, ec.Dagger(), all.FilterTypes(commandArtifactTypes(cmd)))
		if err != nil {
			return err
		}
		if err := registerArtifactFilters(ctx, ec.Dagger(), cmd, definitions, selected, all); err != nil {
			return err
		}
		return nil
	})
}

func listHelp(cmd *cobra.Command, args []string) {
	if cmd == listCmd && !cmd.HasSubCommands() {
		if err := withEngineMetadata(cmd.Context(), "Load available types", client.Params{SkipWorkspaceModules: true}, func(ctx context.Context, ec *client.Client) error {
			return loadListCommands(ctx, ec, "")
		}); err != nil {
			slog.Debug("skip list commands", "error", err)
		}
	}
	rootCmd.HelpFunc()(cmd, args)
}

func loadListCommands(ctx context.Context, ec *client.Client, requested string) error {
	dag := ec.Dagger()
	id, err := dag.CurrentWorkspace().Artifacts().ID(ctx)
	if err != nil {
		return err
	}
	artifacts := dagger.Ref[*dagger.Artifacts](dag, id)
	types, err := readArtifactTypes(ctx, dag, artifacts)
	if err != nil {
		return err
	}
	dimensions, err := artifactDimensions(ctx, dag, artifacts)
	if err != nil {
		return err
	}
	if err := registerArtifactFilters(ctx, dag, listCmd, dimensions, dimensions, artifacts); err != nil {
		return err
	}
	var selectedCommand *cobra.Command
	for name, typeName := range artifactTypeCommands(artifactTypeNames(types)) {
		short := "List " + typeName + " artifacts"
		for _, typ := range types {
			if typ.Name == typeName && typ.Comment != "" {
				short = typ.Comment
				break
			}
		}
		cmd := addListCommand(name, short, typeName)
		if name == requested {
			selectedCommand = cmd
		}
	}
	if selectedCommand != nil {
		selected, err := artifactDimensions(ctx, dag, listArtifactTargets(artifacts, selectedCommand))
		if err != nil {
			return err
		}
		return registerArtifactFilters(ctx, dag, selectedCommand, dimensions, selected, artifacts)
	}
	return nil
}

func addListCommand(name, short, typeName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:               name + " [address...]",
		Short:             short,
		GroupID:           "types",
		Args:              cobra.ArbitraryArgs,
		RunE:              runArtifacts,
		Annotations:       map[string]string{artifactListType: typeName},
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	registerArtifactListFlags(cmd)
	cmd.Flags().StringP("format", "f", "table", "Output `FORMAT`: table, link, or cli")
	setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig)
	listCmd.AddCommand(cmd)
	return cmd
}

// Unknown flags require schema metadata before Cobra can parse them.
func needsArtifactDiscovery(cmd *cobra.Command, args []string) bool {
	cmd.InitDefaultHelpFlag()
	flags := copyCommandFlags(cmd, "artifact dimensions")
	flags.ParseErrorsAllowlist.UnknownFlags = false
	help := false
	err := flags.ParseAll(args, func(flag *pflag.Flag, value string) error {
		if flag.Name == "help" && value == "true" {
			help = true
		}
		return nil
	})
	return err != nil || help
}
