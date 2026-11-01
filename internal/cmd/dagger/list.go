package daggercmd

import (
	"bytes"
	"context"

	"dagger.io/dagger/core"

	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
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
		// the same plural: GoModules selects its collection items.
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

// prepareArtifactCommands registers the artifact commands and flags that the
// workspace defines, before Cobra parses the command line. When the command
// will run in the session that discovers them, it returns that discovery
// instead of running it: see artifactDiscovery.
func prepareArtifactCommands(ctx context.Context, root *cobra.Command, args, rawArgs []string) (*artifactDiscovery, error) {
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
		return nil, nil
	}
	_, rawCommandArgs := resolveCommand(root, rawArgs)
	if !discover {
		discover = needsArtifactDiscovery(cmd, rawCommandArgs)
	}
	withDiscovery := func(action string, params client.Params, fn runClientCallback) (*artifactDiscovery, error) {
		if completing {
			return nil, withEngineSilent(ctx, params, fn)
		}
		if !helpCommand && runsInDiscoverySession(cmd) {
			help, err := helpFlag(cmd, rawCommandArgs)
			if err != nil {
				return nil, err
			}
			if !help {
				params, err := artifactSessionParams(cmd, commandArgs)
				if err != nil {
					return nil, err
				}
				return &artifactDiscovery{cmd: cmd, root: root, rawArgs: rawArgs, action: action, params: params, discover: fn}, nil
			}
		}
		return nil, withEngineMetadata(ctx, action, params, fn)
	}
	if cmd == listCmd {
		// Root help discovers commands after the progress frontend is ready.
		if !completing && len(commandArgs) == 0 && (!discover || helpCommand) {
			return nil, nil
		}
		if !completing && len(commandArgs) == 0 {
			help, err := helpFlag(cmd, rawCommandArgs)
			if err != nil {
				return nil, err
			}
			if help {
				return nil, nil
			}
		}
		requested := ""
		if len(commandArgs) > 0 {
			requested = commandArgs[0]
		}
		params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, commandArgs)
		if err != nil {
			return nil, err
		}
		return withDiscovery("Load available types", params, func(ctx context.Context, ec *client.Client) error {
			if err := loadListCommands(ctx, ec, requested); err != nil {
				return err
			}
			return listCmd.RegisterFlagCompletionFunc("type", completeArtifactTypes)
		})
	}
	if !discover {
		return nil, nil
	}
	params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, commandArgs)
	if err != nil {
		return nil, err
	}
	return withDiscovery("Load "+cmd.Name()+" filters", params, func(ctx context.Context, ec *client.Client) error {
		id, err := core.NewQuery(ec.Dagger()).CurrentWorkspace().Artifacts().ID(ctx)
		if err != nil {
			return err
		}
		all := core.Ref[*core.Artifacts](core.NewQuery(ec.Dagger()), id)
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

// artifactDiscovery discovers a command's artifact commands and flags as the
// first request of the session the command then runs in. Its execute opens
// the command's frontend, telemetry and session before Cobra parses the
// command line, discovers, then runs Cobra in that session: the command's
// engine call takes the open session (see takeOpenSession), with the
// workspace's modules already loaded.
type artifactDiscovery struct {
	cmd *cobra.Command
	// root and rawArgs resolve the command again once discovery has
	// registered its flags and commands.
	root     *cobra.Command
	rawArgs  []string
	action   string
	params   client.Params
	discover runClientCallback
}

func (d *artifactDiscovery) execute(ctx context.Context, run func(context.Context) error) error {
	// PersistentPreRunE runs these once the frontend is up. Signing in waits
	// for the user, so do it before the frontend hides its instructions.
	if err := checkCloudToken(ctx, rootCmd.OutOrStdout()); err != nil {
		return err
	}
	// The frontend's options are set when it starts.
	applyCommandProgressDefaults(d.cmd)

	// What Cobra writes outside the command's engine call (usage, deprecation
	// and update notices) goes to the terminal once the frontend is done, as
	// it would without the open session.
	var stdout, stderr bytes.Buffer
	termOut, termErr := rootCmd.OutOrStdout(), rootCmd.ErrOrStderr()
	defer func() {
		termOut.Write(stdout.Bytes())
		termErr.Write(stderr.Bytes())
	}()
	parsed := false
	err := withEngine(ctx, d.params, func(ctx context.Context, ec *client.Client) (rerr error) {
		discoverCtx, span := Tracer().Start(ctx, d.action, telemetry.Encapsulate())
		err := d.discover(discoverCtx, ec)
		telemetry.EndWithCause(span, &err)
		if err != nil {
			return err
		}
		if needsArtifactDiscovery(resolveCommand(d.root, d.rawArgs)) {
			// Cobra will report the command line's error, after the frontend.
			return nil
		}
		parsed = true
		ctx = withOpenSession(ctx, d.params, ec, rootCmd.OutOrStdout(), rootCmd.ErrOrStderr())
		rootCmd.SetOut(&stdout)
		rootCmd.SetErr(&stderr)
		return run(ctx)
	})
	if err != nil || parsed {
		return err
	}
	return run(ctx)
}

// runsInDiscoverySession reports whether cmd can run in the session that
// discovers its flags. That session's frontend is already running when Cobra
// runs the command, so a command that changes frontend options or reads stdin
// before it connects can't: start sets a root filter, shell reads piped
// commands, and agent may prompt or pick an archive.
func runsInDiscoverySession(cmd *cobra.Command) bool {
	return cmd == checksCmd || cmd == generateCmd || cmd == listCmd || cmd.Parent() == listCmd
}

// artifactSessionParams returns the params cmd's own engine call connects
// with, before Cobra has parsed its flags.
func artifactSessionParams(cmd *cobra.Command, args []string) (client.Params, error) {
	if cmd == checksCmd {
		return checksClientParams(args)
	}
	return artifactClientParams(client.Params{SkipWorkspaceModules: true}, args)
}

// helpFlag reports whether args ask for cmd's help.
func helpFlag(cmd *cobra.Command, args []string) (bool, error) {
	cmd.InitDefaultHelpFlag()
	flags := copyCommandFlags(cmd, "help")
	help := false
	err := flags.ParseAll(args, func(flag *pflag.Flag, value string) error {
		if flag.Name == "help" {
			help = value == "true"
		}
		return nil
	})
	return help, err
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
	id, err := core.NewQuery(dag).CurrentWorkspace().Artifacts().ID(ctx)
	if err != nil {
		return err
	}
	artifacts := core.Ref[*core.Artifacts](core.NewQuery(dag), id)
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
		if dimension.Kind == "COLLECTION" {
			collectionTypes[dimension.CollectionType] = true
		}
	}
	if err := registerArtifactFilters(ctx, dag, listCmd, dimensions, dimensions, artifacts); err != nil {
		return err
	}
	var selectedCommand *cobra.Command
	for name, typeName := range artifactTypeCommands(artifactTypeNames(types), collectionTypes) {
		short := "List " + typeName + " artifacts"
		for _, typ := range types {
			if typ.Name == typeName && typ.Comment != "" {
				short = typ.Comment
				break
			}
		}
		var cmd *cobra.Command
		if collectionTypes[typeName] {
			cmd = addListCommand(name, "List "+typeName+" items", "collections", artifactListCollection, typeName)
		} else {
			cmd = addListCommand(name, short, "types", artifactListType, typeName)
		}
		if name == requested {
			selectedCommand = cmd
		}
	}
	if selectedCommand != nil {
		selected, err := artifactDimensions(ctx, dag, listArtifactTargets(artifacts, selectedCommand, dimensions))
		if err != nil {
			return err
		}
		return registerArtifactFilters(ctx, dag, selectedCommand, dimensions, selected, artifacts)
	}
	return nil
}

func addListCommand(name, short, group, key, value string) *cobra.Command {
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
	return cmd
}

// Unknown flags require schema metadata before Cobra can distinguish keys from
// whole-collection booleans. Do not guess from singular or plural spelling.
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
