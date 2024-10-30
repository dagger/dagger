package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine/client"
)

var configJSONOutput bool

func init() {
	configCmd.PersistentFlags().BoolVar(&configJSONOutput, "json", false, "output in JSON format")
	configCmd.AddGroup(moduleGroup)

	configCmd.AddCommand(
		configViewsCmd,
	)
}

var configCmd = &cobra.Command{
	Use:   "config [options]",
	Short: "Get or set module configuration",
	Long:  "Get or set the configuration of a Dagger module. By default, print the configuration of the specified module.",
	Example: strings.TrimSpace(`
dagger config -m /path/to/some/dir
dagger config -m github.com/dagger/hello-dagger
`,
	),
	Args:    cobra.NoArgs,
	GroupID: moduleGroup.ID,
	RunE: configSubcmdRun(func(ctx context.Context, cmd *cobra.Command, _ []string, modConf *configuredModule) (err error) {
		if configJSONOutput {
			cfgContents, err := modConf.Source.Directory(".").File(modules.Filename).Contents(ctx)
			if err != nil {
				return fmt.Errorf("failed to read module config: %w", err)
			}
			cmd.OutOrStdout().Write([]byte(cfgContents))
			return nil
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
	}).RunE,
}

var configViewsCmd = configSubcmd{
	Use:   "views [options]",
	Short: "Get or set the views of a Dagger module",
	Long:  "Get or set the views of a Dagger module. By default, print the views of the specified module.",
	PersistentFlags: func(fs *pflag.FlagSet) {
		fs.StringP("name", "n", "", "The name of the view to get or set")
	},

	Hidden: true,

	GetExample: strings.TrimSpace(`
dagger config views
dagger config views -n my-view
`),
	GetPositionalArgs: cobra.NoArgs,
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
			viewMap := make(map[string][]string, len(views))
			viewStrs := make([]string, 0, len(views))
			for _, view := range views {
				name, err := view.Name(ctx)
				if err != nil {
					return fmt.Errorf("failed to get view name: %w", err)
				}
				patterns, err := view.Patterns(ctx)
				if err != nil {
					return fmt.Errorf("failed to get view patterns: %w", err)
				}
				viewMap[name] = patterns
				viewStrs = append(viewStrs, fmt.Sprintf("%s\n<%s>\n",
					termenv.String(fmt.Sprintf("%q", name)).Bold().Underline(),
					strings.Join(patterns, "\n"),
				))
			}

			if configJSONOutput {
				bs, err := json.MarshalIndent(viewMap, "", "  ")
				if err != nil {
					return err
				}
				cmd.OutOrStdout().Write(bs)
				return nil
			}
			w := cmd.OutOrStdout()
			fmt.Fprint(w, strings.Join(viewStrs, "\n"))
			return nil
		}

		patterns, err := modConf.Source.View(name).Patterns(ctx)
		if err != nil {
			return fmt.Errorf("failed to get view patterns: %w", err)
		}

		w := cmd.OutOrStdout()
		if configJSONOutput {
			bs, err := json.MarshalIndent(patterns, "", "  ")
			if err != nil {
				return err
			}
			w.Write(bs)
			return nil
		}
		fmt.Fprint(w, strings.Join(patterns, "\n"))
		return nil
	},

	SetExample:        `dagger config views set -n my-view '**/*.txt' ./some/path`,
	SetPositionalArgs: cobra.MinimumNArgs(1),
	SetCmd: func(ctx context.Context, cmd *cobra.Command, args []string, modConf *configuredModule) error {
		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return fmt.Errorf("failed to get view name: %w", err)
		}
		if name == "" {
			cmd.Help()
			return fmt.Errorf("--name (-n) is required")
		}

		updatedMod := modConf.Source.WithView(name, args).AsModule()
		updatedView, err := updatedMod.Source().View(name).Patterns(ctx)
		if err != nil {
			return fmt.Errorf("failed to get view: %w", err)
		}

		_, err = updatedMod.
			GeneratedContextDiff().
			Export(ctx, modConf.LocalContextPath)
		if err != nil {
			return fmt.Errorf("failed to update view paths: %w", err)
		}

		w := cmd.OutOrStdout()
		if configJSONOutput {
			bs, err := json.MarshalIndent(updatedView, "", "  ")
			if err != nil {
				return err
			}
			w.Write(bs)
			return nil
		}
		fmt.Fprint(w, termenv.String(fmt.Sprintf("View %q set to:\n", name)).Bold().Underline())
		fmt.Fprint(w, strings.Join(updatedView, "\n"))
		return nil
	},

	AddExample:        `dagger config views add -n my-view ./some/path ./some/other/path`,
	AddPositionalArgs: cobra.MinimumNArgs(1),
	AddCmd: func(ctx context.Context, cmd *cobra.Command, args []string, modConf *configuredModule) error {
		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return fmt.Errorf("failed to get view name: %w", err)
		}
		if name == "" {
			cmd.Help()
			return fmt.Errorf("--name (-n) is required")
		}

		viewPatterns, err := modConf.Source.View(name).Patterns(ctx)
		if err != nil {
			return fmt.Errorf("failed to get current include paths: %w", err)
		}
		viewPatterns = append(viewPatterns, args...)

		updatedMod := modConf.Source.WithView(name, viewPatterns).AsModule()
		updatedView, err := updatedMod.Source().View(name).Patterns(ctx)
		if err != nil {
			return fmt.Errorf("failed to get updated view paths: %w", err)
		}

		_, err = updatedMod.
			GeneratedContextDiff().
			Export(ctx, modConf.LocalContextPath)
		if err != nil {
			return fmt.Errorf("failed to update view paths: %w", err)
		}

		w := cmd.OutOrStdout()
		if configJSONOutput {
			bs, err := json.MarshalIndent(updatedView, "", "  ")
			if err != nil {
				return err
			}
			w.Write(bs)
			return nil
		}
		fmt.Fprint(w, termenv.String(fmt.Sprintf("View %q updated to:\n", name)).Bold().Underline())
		fmt.Fprint(w, strings.Join(updatedView, "\n"))
		return nil
	},

	RemoveExample: strings.TrimSpace(`
dagger config views remove -n my-view
dagger config views remove -n my-view ./some/path '**/*.txt'
`),
	RemoveCmd: func(ctx context.Context, cmd *cobra.Command, args []string, modConf *configuredModule) error {
		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return fmt.Errorf("failed to get view name: %w", err)
		}
		if name == "" {
			cmd.Help()
			return fmt.Errorf("--name (-n) is required")
		}

		var updatedPatterns []string
		if len(args) > 0 {
			curPatterns, err := modConf.Source.View(name).Patterns(ctx)
			if err != nil {
				return fmt.Errorf("failed to get current view paths: %w", err)
			}
			// just be n^2, there will never be 1000s of includes, right? right?!
			for _, curPattern := range curPatterns {
				keep := true
				for _, arg := range args {
					if curPattern == arg {
						keep = false
						break
					}
				}
				if keep {
					updatedPatterns = append(updatedPatterns, curPattern)
				}
			}
		}

		updatedMod := modConf.Source.WithView(name, updatedPatterns).AsModule()
		_, err = updatedMod.
			GeneratedContextDiff().
			Export(ctx, modConf.LocalContextPath)
		if err != nil {
			return fmt.Errorf("failed to update view: %w", err)
		}

		w := cmd.OutOrStdout()
		if configJSONOutput {
			bs, err := json.MarshalIndent(updatedPatterns, "", "  ")
			if err != nil {
				return err
			}
			w.Write(bs)
			return nil
		}
		if len(args) > 0 {
			fmt.Fprint(w, termenv.String(fmt.Sprintf("View %q updated to:\n", name)).Bold().Underline())
			fmt.Fprint(w, strings.Join(updatedPatterns, "\n"))
		} else {
			fmt.Fprint(w, termenv.String(fmt.Sprintf("View %q removed", name)).Bold())
		}
		return nil
	},
}.Command()

type configSubcmd struct {
	Use             string
	Short           string
	Long            string
	PersistentFlags func(*pflag.FlagSet)
	Hidden          bool

	GetCmd            configSubcmdRun
	GetExample        string
	GetPositionalArgs cobra.PositionalArgs

	SetCmd            configSubcmdRun
	SetExample        string
	SetPositionalArgs cobra.PositionalArgs

	AddCmd            configSubcmdRun
	AddExample        string
	AddPositionalArgs cobra.PositionalArgs

	RemoveCmd            configSubcmdRun
	RemoveExample        string
	RemovePositionalArgs cobra.PositionalArgs
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
		Args:    c.GetPositionalArgs,
		RunE:    c.GetCmd.RunE,
		Hidden:  c.Hidden,
	}
	examples := []string{c.GetExample}

	if c.PersistentFlags != nil {
		c.PersistentFlags(cmd.PersistentFlags())
	}

	if c.Hidden {
		cmd.Annotations = map[string]string{
			"experimental": "true",
		}
	}

	if c.SetCmd != nil {
		setCmd := &cobra.Command{
			// TODO: make these specific based on the parent
			Use:     "set [options] <pattern>...",
			Short:   "Set the configuration value",
			Long:    "Set the configuration value",
			Example: c.SetExample,
			Args:    c.SetPositionalArgs,
			RunE:    c.SetCmd.LocalOnlyRunE,
			Hidden:  c.Hidden,
		}
		cmd.AddCommand(setCmd)

		if c.SetExample == "" {
			panic("SetExample is not set for " + c.Use)
		}
		examples = append(examples, c.SetExample)
	}

	if c.AddCmd != nil {
		addCmd := &cobra.Command{
			// TODO: make these specific based on the parent
			Use:     "add [options] <pattern>...",
			Short:   "Add a value to the configuration",
			Long:    "Add a value to the configuration",
			Example: c.AddExample,
			Args:    c.AddPositionalArgs,
			RunE:    c.AddCmd.LocalOnlyRunE,
			Hidden:  c.Hidden,
		}
		cmd.AddCommand(addCmd)

		if c.AddExample == "" {
			panic("AddExample is not set for " + c.Use)
		}
		examples = append(examples, c.AddExample)
	}

	if c.RemoveCmd != nil {
		removeCmd := &cobra.Command{
			// TODO: make these specific based on the parent
			Use:     "remove [options] <pattern>...",
			Short:   "Remove a value from the configuration",
			Long:    "Remove a value from the configuration",
			Example: c.RemoveExample,
			Args:    c.RemovePositionalArgs,
			RunE:    c.RemoveCmd.LocalOnlyRunE,
			Hidden:  c.Hidden,
		}
		cmd.AddCommand(removeCmd)

		if c.RemoveExample == "" {
			panic("RemoveExample is not set for " + c.Use)
		}
		examples = append(examples, c.RemoveExample)
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

		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			modConf, err := getDefaultModuleConfiguration(ctx, engineClient.Dagger(), true, true)
			if err != nil {
				return fmt.Errorf("failed to load module: %w", err)
			}
			if localOnly {
				kind, err := modConf.Source.Kind(ctx)
				if err != nil {
					return fmt.Errorf("failed to get module kind: %w", err)
				}
				if kind != dagger.ModuleSourceKindLocalSource {
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
