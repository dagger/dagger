package daggercmd

import (
	"context"
	"errors"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/jinzhu/inflection"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func artifactTypeCommands(types []string, collectionTypes map[string]bool) map[string]string {
	byName := map[string][]string{}
	for _, typeName := range types {
		name := cliName(typeName)
		if !collectionTypes[typeName] {
			name = inflection.Plural(name)
		}
		byName[name] = append(byName[name], typeName)
	}
	commands := map[string]string{}
	for name, matches := range byName {
		// A collection owns its declared name, even when an item type has
		// the same plural: GoModules lists keys, not GoModule artifacts.
		for _, typeName := range matches {
			if collectionTypes[typeName] {
				commands[name] = typeName
				break
			}
		}
		if commands[name] != "" {
			continue
		}
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
		var err error
		discover, err = prepareArtifactDimensionFlags(cmd, rawCommandArgs)
		if err != nil {
			return err
		}
	}
	if cmd == listCmd {
		var all bool
		_, rawCommandArgs := resolveCommand(root, rawArgs)
		flags := copyCommandFlags(cmd, "list")
		if err := flags.ParseAll(rawCommandArgs, func(flag *pflag.Flag, value string) error {
			if flag.Name == "all" {
				all = value == "true"
			}
			return nil
		}); err != nil {
			return err
		}
		// Root help discovers commands after the progress frontend is ready.
		if !completing && len(commandArgs) == 0 && !all {
			return nil
		}
		if !all && (completing || len(commandArgs) > 0 || discover) {
			paths := commandArgs
			if !all && len(paths) > 0 {
				paths = paths[1:]
			}
			params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, paths)
			if err != nil {
				return err
			}
			if err := withEngineSilent(ctx, params, loadListCommands); err != nil {
				return err
			}
			cmd, _ = resolveCommand(root, args)
		}
		if err := listCmd.RegisterFlagCompletionFunc("type", completeArtifactTypes); err != nil {
			return err
		}
		if cmd != listCmd {
			_, rawCommandArgs := resolveCommand(root, rawArgs)
			_, err := prepareArtifactDimensionFlags(cmd, rawCommandArgs)
			return err
		}
		return nil
	}
	if !discover {
		return nil
	}
	addresses, err := parseArtifactAddresses(commandArgs)
	if err != nil {
		return err
	}
	params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, commandArgs)
	if err != nil {
		return err
	}
	return withEngineSilent(ctx, params, func(ctx context.Context, ec *client.Client) error {
		definitions, err := artifactDimensions(ctx, ec.Dagger(), ec.Dagger().CurrentWorkspace().Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths(addresses)}))
		if err != nil {
			return err
		}
		registerArtifactDimensionHelp(cmd, definitions)
		return nil
	})
}

func listHelp(cmd *cobra.Command, args []string) {
	if cmd == listCmd && !cmd.HasSubCommands() {
		applyCommandProgressDefaults(cmd)
		if err := withEngine(cmd.Context(), client.Params{SkipWorkspaceModules: true}, loadListCommands); err != nil {
			slog.Debug("skip list commands", "error", err)
		}
	}
	rootCmd.HelpFunc()(cmd, args)
}

func loadListCommands(ctx context.Context, ec *client.Client) error {
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
	collectionTypes := map[string]bool{}
	for _, dimension := range dimensions {
		collectionTypes[dimension.CollectionType] = true
	}
	registerArtifactDimensionHelp(listCmd, dimensions)
	for name, typeName := range artifactTypeCommands(artifactTypeNames(types), collectionTypes) {
		if collectionTypes[typeName] {
			addListCommand(name, "List "+typeName+" keys", "collections", artifactListCollection, typeName)
			continue
		}
		short := "List " + typeName + " artifacts"
		for _, typ := range types {
			if typ.Name == typeName && typ.Comment != "" {
				short = typ.Comment
				break
			}
		}
		addListCommand(name, short, "types", artifactListType, typeName)
	}
	return nil
}

func addListCommand(name, short, group, key, value string) {
	cmd := &cobra.Command{
		Use:               name + " [address...]",
		Short:             short,
		GroupID:           group,
		Args:              cobra.ArbitraryArgs,
		RunE:              runArtifacts,
		Annotations:       map[string]string{key: value},
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	registerArtifactListFlags(cmd)
	cmd.Flags().StringP("format", "f", "table", "Output `FORMAT`: table, link, or cli")
	setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig)
	listCmd.AddCommand(cmd)
}

// Register supplied dimension flags before workspace discovery. Execution
// validates them against the schema inside the visible engine session.
func prepareArtifactDimensionFlags(cmd *cobra.Command, args []string) (bool, error) {
	cmd.InitDefaultHelpFlag()
	flags := copyCommandFlags(cmd, "artifact dimensions")
	flags.ParseErrorsAllowlist.UnknownFlags = false
	var names []string
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
			if !help {
				registerArtifactDimensionFlags(cmd, names)
			}
			return help, err
		}
		name := unknown.GetSpecifiedName()
		names = append(names, name)
		flags.StringArray(name, nil, "")
	}
}
