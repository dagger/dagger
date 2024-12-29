package main

import (
	"strings"

	"github.com/spf13/cobra"
)

var configJSONOutput bool

func init() {
	configCmd.PersistentFlags().BoolVar(&configJSONOutput, "json", false, "output in JSON format")
	configCmd.AddGroup(moduleGroup)
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
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		return nil
	},

	// TODO: FIX
	// TODO: FIX
	// TODO: FIX
	// TODO: FIX
	/*
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
	*/
}
