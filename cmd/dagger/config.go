package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine/client"
	"github.com/juju/ansiterm/tabwriter"
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
		ctx := cmd.Context()

		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			modRef, err := getModuleSourceRefWithDefault()
			if err != nil {
				return err
			}
			modSrc := dag.ModuleSource(modRef)

			sourceRootSubpath, err := modSrc.SourceRootSubpath(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module source root subpath: %w", err)
			}

			if configJSONOutput {
				cfgContents, err := modSrc.ContextDirectory().File(filepath.Join(sourceRootSubpath, modules.Filename)).Contents(ctx)
				if err != nil {
					return fmt.Errorf("failed to read module config: %w", err)
				}
				cmd.OutOrStdout().Write([]byte(cfgContents))
				return nil
			}

			name, err := modSrc.ModuleName(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module name: %w", err)
			}
			sdk, err := modSrc.SDK().Source(ctx)
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

			kind, err := modSrc.Kind(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module kind: %w", err)
			}
			if kind == dagger.ModuleSourceKindLocalSource {
				contextDirPath, err := modSrc.LocalContextDirectoryPath(ctx)
				if err != nil {
					return fmt.Errorf("failed to get local context directory path: %w", err)
				}
				sourceRootPath := filepath.Join(contextDirPath, sourceRootSubpath)

				fmt.Fprintf(tw, "%s\t%s\n",
					"Context Directory:",
					contextDirPath,
				)
				fmt.Fprintf(tw, "%s\t%s\n",
					"Source Root Directory:",
					sourceRootPath,
				)
			}

			return tw.Flush()
		})
	},
}
