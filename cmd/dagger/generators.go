package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

var (
	generateListMode  bool
	generateNeedsHelp bool
)

//go:embed generators.graphql
var loadGeneratorsQuery string

func init() {
	generateCmd.Flags().BoolVarP(&generateListMode, "list", "l", false, "List available generators")
}

var generateCmd = &cobra.Command{
	Hidden:                true,
	Use:                   "generate [options] [pattern...]",
	Short:                 "Generate assets of your project",
	DisableFlagParsing:    true,
	DisableFlagsInUseLine: true,
	Long: `Generate assets of your project

Examples:
  dagger generate                            # Generate all assets
  dagger generate -l                         # List all available generators
  dagger generate go:bin                     # Generate by selecting the generator function
`,
	Args: cobra.ArbitraryArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return preparseDynamicFlags(cmd, args, &generateNeedsHelp)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		params := client.Params{
			EnableCloudScaleOut: enableScaleOut,
		}
		return withEngine(
			cmd.Context(),
			params,
			func(ctx context.Context, engineClient *client.Client) error {
				dag := engineClient.Dagger()
				def, err := initializeWorkspace(ctx, dag)
				if err != nil {
					return err
				}
				collectionFlags := discoverCollectionFilterFlags(def)
				addCollectionFilterFlags(cmd, collectionFlags)
				if err := cmd.ParseFlags(args); err != nil {
					if generateNeedsHelp && errors.Is(err, pflag.ErrHelp) {
						return cmd.Help()
					}
					return cmd.FlagErrorFunc()(cmd, err)
				}

				activeFilters, listFlags, err := activeCollectionFilters(cmd, collectionFlags)
				if err != nil {
					return err
				}
				if generateListMode && len(activeFilters) > 0 {
					if flagName, ok := firstActiveCollectionFilterFlag(cmd, collectionFlags); ok {
						return fmt.Errorf("can't use -l with --%s; use --list-%s instead", flagName, flagName)
					}
				}
				if generateListMode && len(listFlags) > 0 {
					return fmt.Errorf("can't use -l with --%s; choose one listing mode", listFlags[0].ListName)
				}

				patterns := cmd.Flags().Args()
				ws := dag.CurrentWorkspace()
				var generators *dagger.GeneratorGroup
				if len(patterns) > 0 || len(activeFilters) > 0 {
					generators = ws.Generators(dagger.WorkspaceGeneratorsOpts{
						Include: patterns,
						Filters: activeFilters,
					})
				} else {
					generators = ws.Generators()
				}

				if len(listFlags) > 0 {
					values, err := loadGeneratorCollectionFilterValues(ctx, dag, generators, collectionFilterTypeNames(listFlags))
					if err != nil {
						return err
					}
					printCollectionFilterValues(cmd, values)
					return nil
				}
				if generateListMode {
					return listGenerators(ctx, dag, generators, cmd)
				}
				return runGenerators(ctx, dag, generators, cmd)
			},
		)
	},
}

func loadGeneratorGroupInfo(ctx context.Context, dag *dagger.Client, generatorGroup *dagger.GeneratorGroup) (*GeneratorGroupInfo, error) {
	items, err := loadGroupListDetails(ctx, dag, "fetch generator information",
		func(ctx context.Context) (any, error) { return generatorGroup.ID(ctx) },
		loadGeneratorsQuery, "GeneratorGroupListDetails",
	)
	if err != nil {
		return nil, err
	}
	info := &GeneratorGroupInfo{Generators: make([]*GeneratorInfo, 0, len(items))}
	for _, item := range items {
		info.Generators = append(info.Generators, &GeneratorInfo{
			Name:        cliName(item.Name),
			Description: item.Description,
		})
	}
	return info, nil
}

type GeneratorGroupInfo struct {
	Generators []*GeneratorInfo
}

type GeneratorInfo struct {
	Name        string
	Description string
}

// 'dagger generators -l'
func listGenerators(ctx context.Context, dag *dagger.Client, generatorGroup *dagger.GeneratorGroup, cmd *cobra.Command) error {
	info, err := loadGeneratorGroupInfo(ctx, dag, generatorGroup)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	fmt.Fprintf(tw, "%s\t%s\n",
		termenv.String("Name").Bold(),
		termenv.String("Description").Bold(),
	)
	for _, generator := range info.Generators {
		firstLine := generator.Description
		if idx := strings.Index(generator.Description, "\n"); idx != -1 {
			firstLine = generator.Description[:idx]
		}
		fmt.Fprintf(tw, "%s\t%s\n", generator.Name, firstLine)
	}
	return tw.Flush()
}

// 'dagger generators' (runs by default)
func runGenerators(ctx context.Context, dag *dagger.Client, generatorGroup *dagger.GeneratorGroup, _ *cobra.Command) (rerr error) {
	ctx, zoomSpan := Tracer().Start(ctx, "generators", telemetry.Passthrough())
	defer zoomSpan.End()
	Frontend.SetPrimary(dagui.SpanID{SpanID: zoomSpan.SpanContext().SpanID()})
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
	// We don't actually use the API for rendering results
	// Instead, we rely on telemetry
	// FIXME: this feels a little weird. Can we move the relevant telemetry collection in the API?
	cs, err := generatorGroup.
		Run().
		Changes(
			dagger.GeneratorGroupChangesOpts{
				OnConflict: dagger.ChangesetsMergeConflictFailEarly,
			},
		).Sync(ctx)
	if err != nil {
		return err
	}
	return handleChangesetResponse(ctx, dag, cs, autoApply)
}
