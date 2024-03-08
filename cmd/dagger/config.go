package main

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
)

func init() {
	configCmd.PersistentFlags().AddFlagSet(moduleFlags)
	configCmd.AddGroup(moduleGroup)

	configCmd.AddCommand(
		configIncludeCmd,
		configViewsCmd,
	)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Get or set the configuration of a Dagger module",
	Long:  "Get or set the configuration of a Dagger module. By default, print the configuration of the specified module.",
	Example: strings.TrimSpace(`
dagger config -m /path/to/some/dir
dagger config -m github.com/dagger/hello-dagger
`,
	),
	Args:    cobra.NoArgs,
	GroupID: moduleGroup.ID,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			ctx, vtx := progrock.Span(ctx, idtui.PrimaryVertex, cmd.CommandPath())
			defer func() { vtx.Done(err) }()
			cmd.SetContext(ctx)
			setCmdOutput(cmd, vtx)

			modConf, err := getDefaultModuleConfiguration(ctx, engineClient.Dagger(), true, true)
			if err != nil {
				return fmt.Errorf("failed to load module: %w", err)
			}
			if !modConf.FullyInitialized() {
				return fmt.Errorf("module must be fully initialized")
			}
			mod := modConf.Source.AsModule()

			name, err := mod.Name(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module name: %w", err)
			}
			sdk, err := mod.SDK(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module SDK: %w", err)
			}

			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
			fmt.Fprintf(tw, "%s\t%s\n",
				"Name:",
				name,
			)
			fmt.Fprintf(tw, "%s\t%s\n",
				"SDK:",
				sdk,
			)
			fmt.Fprintf(tw, "%s\t%s\n",
				"Root Directory:",
				modConf.LocalContextPath,
			)
			fmt.Fprintf(tw, "%s\t%s\n",
				"Source Directory:",
				modConf.LocalRootSourcePath,
			)

			return tw.Flush()
		})
	},
}

var configIncludeCmd = configSubcmd{
	Use:   "include",
	Short: "Get or set the include paths of a Dagger module",
	Long:  "Get or set the include paths of a Dagger module. By default, print the include paths of the specified module.",

	GetExample: `dagger config -m ./path/to/module include`,
	GetCmd: func(ctx context.Context, cmd *cobra.Command, _ []string, modConf *configuredModule) error {
		include, err := modConf.Source.Include(ctx)
		if err != nil {
			return fmt.Errorf("failed to get include paths: %w", err)
		}
		return printFunctionResult(cmd.OutOrStdout(), include)
	},

	SetExample: `dagger config -m ./path/to/module include set **/*.txt ./some/path`,
	SetCmd: func(ctx context.Context, cmd *cobra.Command, args []string, modConf *configuredModule) error {
		// TODO: validation engine-side
		// TODO: validation engine-side
		// TODO: validation engine-side

		include := args
		if len(include) == 0 {
			return fmt.Errorf("no include paths provided")
		}

		updatedMod := modConf.Source.WithInclude(include).AsModule()
		updatedInclude, err := updatedMod.Source().Include(ctx)
		if err != nil {
			return fmt.Errorf("failed to get include paths: %w", err)
		}

		_, err = updatedMod.
			GeneratedContextDiff().
			Export(ctx, modConf.LocalContextPath)
		if err != nil {
			return fmt.Errorf("failed to update include paths: %w", err)
		}

		return printFunctionResult(cmd.OutOrStdout(), updatedInclude)
	},

	AppendExample: `dagger config -m ./path/to/module include append ./some/path ./some/other/path`,
	AppendCmd: func(ctx context.Context, cmd *cobra.Command, args []string, modConf *configuredModule) error {
		// TODO: validation engine-side
		// TODO: validation engine-side
		// TODO: validation engine-side

		include, err := modConf.Source.Include(ctx)
		if err != nil {
			return fmt.Errorf("failed to get current include paths: %w", err)
		}
		// TODO: dedupe paths engine-side
		include = append(include, args...)

		updatedMod := modConf.Source.WithInclude(include).AsModule()
		updatedInclude, err := updatedMod.Source().Include(ctx)
		if err != nil {
			return fmt.Errorf("failed to get include paths: %w", err)
		}

		_, err = updatedMod.
			GeneratedContextDiff().
			Export(ctx, modConf.LocalContextPath)
		if err != nil {
			return fmt.Errorf("failed to update include paths: %w", err)
		}

		return printFunctionResult(cmd.OutOrStdout(), updatedInclude)
	},

	ClearExample: strings.TrimSpace(`
dagger config -m ./path/to/module include clear
dagger config -m ./path/to/module include clear ./some/path **/*.txt
`),
	ClearCmd: func(ctx context.Context, cmd *cobra.Command, args []string, modConf *configuredModule) error {
		var updatedIncludes []string
		if len(args) > 0 {
			curIncludes, err := modConf.Source.Include(ctx)
			if err != nil {
				return fmt.Errorf("failed to get current include paths: %w", err)
			}
			// just be n^2, there will never be 1000s of includes, right? right?!
			for _, curInclude := range curIncludes {
				keep := true
				for _, arg := range args {
					if curInclude == arg {
						keep = false
						break
					}
				}
				if keep {
					updatedIncludes = append(updatedIncludes, curInclude)
				}
			}
		}

		updatedMod := modConf.Source.WithInclude(updatedIncludes).AsModule()
		_, err := updatedMod.
			GeneratedContextDiff().
			Export(ctx, modConf.LocalContextPath)
		if err != nil {
			return fmt.Errorf("failed to update include paths: %w", err)
		}

		return printFunctionResult(cmd.OutOrStdout(), updatedIncludes)
	},
}.Command()

var configViewsCmd = configSubcmd{
	Use: "views",
	// TODO:
	// TODO:
	// TODO:
	Short: "TODO",
	Long:  "TODO",
	PersistentFlags: func(fs *pflag.FlagSet) {
		fs.StringP("name", "n", "", "The name of the view to get or set")
	},

	GetExample: `dagger config -m ./path/to/module views`,
	GetCmd: func(ctx context.Context, cmd *cobra.Command, _ []string, modConf *configuredModule) error {
		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return fmt.Errorf("failed to get view name: %w", err)
		}
		if name == "" {
			// print'em all
			views, err := modConf.Source.Views(ctx)
			if err != nil {
				return fmt.Errorf("failed to get views: %w", err)
			}
			viewMap := make(map[string][]string)
			for _, view := range views {
				name, err := view.Name(ctx)
				if err != nil {
					return fmt.Errorf("failed to get view name: %w", err)
				}
				viewMap[name], err = view.Include(ctx)
				if err != nil {
					return fmt.Errorf("failed to get view include: %w", err)
				}
			}
			return printFunctionResult(cmd.OutOrStdout(), viewMap)
		}

		includes, err := modConf.Source.View(name).Include(ctx)
		if err != nil {
			return fmt.Errorf("failed to get view include: %w", err)
		}

		// TODO: make output look nice
		return printFunctionResult(cmd.OutOrStdout(), map[string][]string{name: includes})
	},

	SetExample: `dagger config -m ./path/to/module views set --name my-view **/*.txt ./some/path`,
	SetCmd: func(ctx context.Context, cmd *cobra.Command, args []string, modConf *configuredModule) error {
		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return fmt.Errorf("failed to get view name: %w", err)
		}
		if name == "" {
			return fmt.Errorf("view name must be provided")
		}

		include := args
		if len(include) == 0 {
			return fmt.Errorf("no include paths provided")
		}

		updatedMod := modConf.Source.WithView(name, include).AsModule()
		updatedView, err := updatedMod.Source().View(name).Include(ctx)
		if err != nil {
			return fmt.Errorf("failed to get view: %w", err)
		}

		_, err = updatedMod.
			GeneratedContextDiff().
			Export(ctx, modConf.LocalContextPath)
		if err != nil {
			return fmt.Errorf("failed to update view paths: %w", err)
		}

		// TODO: make output look nice
		return printFunctionResult(cmd.OutOrStdout(), map[string][]string{
			name: updatedView,
		})
	},

	AppendExample: `dagger config -m ./path/to/module views append --name my-view ./some/path ./some/other/path`,
	AppendCmd: func(ctx context.Context, cmd *cobra.Command, args []string, modConf *configuredModule) error {
		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return fmt.Errorf("failed to get view name: %w", err)
		}
		if name == "" {
			return fmt.Errorf("view name must be provided")
		}

		viewInclude, err := modConf.Source.View(name).Include(ctx)
		if err != nil {
			return fmt.Errorf("failed to get current include paths: %w", err)
		}
		// TODO: dedupe paths engine-side
		viewInclude = append(viewInclude, args...)

		updatedMod := modConf.Source.WithView(name, viewInclude).AsModule()
		updatedView, err := updatedMod.Source().View(name).Include(ctx)
		if err != nil {
			return fmt.Errorf("failed to get updated view paths: %w", err)
		}

		_, err = updatedMod.
			GeneratedContextDiff().
			Export(ctx, modConf.LocalContextPath)
		if err != nil {
			return fmt.Errorf("failed to update view paths: %w", err)
		}

		// TODO: make output look nice
		return printFunctionResult(cmd.OutOrStdout(), map[string][]string{
			name: updatedView,
		})
	},

	ClearExample: strings.TrimSpace(`
dagger config -m ./path/to/module view clear --name my-view
dagger config -m ./path/to/module include clear --name my-view ./some/path **/*.txt
`),
	ClearCmd: func(ctx context.Context, cmd *cobra.Command, args []string, modConf *configuredModule) error {
		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return fmt.Errorf("failed to get view name: %w", err)
		}
		if name == "" {
			return fmt.Errorf("view name must be provided")
		}

		var updatedIncludes []string
		if len(args) > 0 {
			curIncludes, err := modConf.Source.View(name).Include(ctx)
			if err != nil {
				return fmt.Errorf("failed to get current view paths: %w", err)
			}
			// just be n^2, there will never be 1000s of includes, right? right?!
			for _, curInclude := range curIncludes {
				keep := true
				for _, arg := range args {
					if curInclude == arg {
						keep = false
						break
					}
				}
				if keep {
					updatedIncludes = append(updatedIncludes, curInclude)
				}
			}
		}

		updatedMod := modConf.Source.WithView(name, updatedIncludes).AsModule()
		_, err = updatedMod.
			GeneratedContextDiff().
			Export(ctx, modConf.LocalContextPath)
		if err != nil {
			return fmt.Errorf("failed to update view: %w", err)
		}

		// TODO: make output look nice
		return printFunctionResult(cmd.OutOrStdout(), updatedIncludes)
	},
}.Command()

type configSubcmd struct {
	Use             string
	Short           string
	Long            string
	PersistentFlags func(*pflag.FlagSet)

	GetCmd     configSubcmdRun
	GetExample string

	SetCmd     configSubcmdRun
	SetExample string

	AppendCmd     configSubcmdRun
	AppendExample string

	ClearCmd     configSubcmdRun
	ClearExample string
}

func (c configSubcmd) Command() *cobra.Command {
	if c.GetCmd == nil {
		panic("GetCmd is not set for " + c.Use)
	}
	if c.GetExample == "" {
		panic("GetExample is not set for " + c.Use)
	}

	cmd := &cobra.Command{
		Use:     c.Use,
		Short:   c.Short,
		Long:    c.Long,
		GroupID: moduleGroup.ID,
		RunE:    c.GetCmd.RunE,
	}
	examples := []string{c.GetExample}

	if c.PersistentFlags != nil {
		c.PersistentFlags(cmd.PersistentFlags())
	}

	if c.SetCmd != nil {
		setCmd := &cobra.Command{
			Use: "set",
			// TODO: make these specific based on the parent
			Short:   "Set the configuration value",
			Long:    "Set the configuration value",
			Example: "TODO",
			RunE:    c.SetCmd.LocalOnlyRunE,
		}
		cmd.AddCommand(setCmd)

		if c.SetExample == "" {
			panic("SetExample is not set for " + c.Use)
		}
		examples = append(examples, c.SetExample)
	}

	if c.AppendCmd != nil {
		appendCmd := &cobra.Command{
			Use: "append",
			// TODO: make these specific based on the parent
			Short:   "Append a value to the configuration",
			Long:    "Append a value to the configuration",
			Example: "TODO",
			RunE:    c.AppendCmd.LocalOnlyRunE,
		}
		cmd.AddCommand(appendCmd)

		if c.AppendExample == "" {
			panic("AppendExample is not set for " + c.Use)
		}
		examples = append(examples, c.AppendExample)
	}

	if c.ClearCmd != nil {
		removeCmd := &cobra.Command{
			Use: "clear",
			// TODO: make these specific based on the parent
			Short:   "Clear a value from the configuration",
			Long:    "Clear a value from the configuration",
			Example: "TODO",
			RunE:    c.ClearCmd.LocalOnlyRunE,
		}
		cmd.AddCommand(removeCmd)

		if c.ClearExample == "" {
			panic("ClearExample is not set for " + c.Use)
		}
		examples = append(examples, c.ClearExample)
	}

	cmd.Example = strings.Join(examples, "\n")

	return cmd
}

type configSubcmdRun func(ctx context.Context, cmd *cobra.Command, args []string, modConf *configuredModule) error

type cobraRunE func(cmd *cobra.Command, args []string) error

func (run configSubcmdRun) LocalOnlyRunE(cmd *cobra.Command, args []string) error {
	return run.runE(true)(cmd, args)
}

func (run configSubcmdRun) RunE(cmd *cobra.Command, args []string) error {
	return run.runE(false)(cmd, args)
}

func (run configSubcmdRun) runE(localOnly bool) cobraRunE {
	return func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			ctx, vtx := progrock.Span(ctx, idtui.PrimaryVertex, cmd.CommandPath())
			defer func() { vtx.Done(err) }()
			cmd.SetContext(ctx)
			setCmdOutput(cmd, vtx)

			modConf, err := getDefaultModuleConfiguration(ctx, engineClient.Dagger(), true, true)
			if err != nil {
				return fmt.Errorf("failed to load module: %w", err)
			}
			if localOnly {
				kind, err := modConf.Source.Kind(ctx)
				if err != nil {
					return fmt.Errorf("failed to get module kind: %w", err)
				}
				if kind != dagger.LocalSource {
					return fmt.Errorf("command only valid for local modules")
				}
			}
			if !modConf.FullyInitialized() {
				return fmt.Errorf("module must be fully initialized")
			}

			return run(ctx, cmd, args, modConf)
		})
	}
}
