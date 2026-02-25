package main

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/codes"
)

var (
	generateListMode bool
)

func init() {
	generateCmd.Flags().BoolVarP(&generateListMode, "list", "l", false, "List available generators")
}

var generateCmd = &cobra.Command{
	Hidden: true,
	Use:    "generate [options] [pattern...]",
	Short:  "Generate assets of your project",
	Long: `Generate assets of your project

Examples:
  dagger generate                            # Generate all assets
  dagger generate -l                         # List all available generators
  dagger generate go:bin                     # Generate by selecting the generator function
`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		params := client.Params{
			EnableCloudScaleOut: enableScaleOut,
		}
		if !moduleNoURL {
			if modRef, _ := getExplicitModuleSourceRef(); modRef != "" {
				params.Module = modRef
			}
		}
		return withEngine(
			cmd.Context(),
			params,
			func(ctx context.Context, engineClient *client.Client) error {
				dag := engineClient.Dagger()
				ws := dag.CurrentWorkspace()
				var generators *dagger.GeneratorGroup
				if len(args) > 0 {
					generators = ws.Generators(dagger.WorkspaceGeneratorsOpts{Include: args})
				} else {
					generators = ws.Generators()
				}
				if generateListMode {
					return listGenerators(ctx, generators, cmd)
				}
				return runGenerators(ctx, dag, generators, cmd)
			},
		)
	},
}

func loadGeneratorGroupInfo(ctx context.Context, generatorGroup *dagger.GeneratorGroup) (*GeneratorGroupInfo, error) {
	ctx, span := Tracer().Start(ctx, "fetch generator information")
	defer span.End()

	generators, err := generatorGroup.List(ctx)
	if err != nil {
		return nil, err
	}
	info := &GeneratorGroupInfo{}
	for _, generator := range generators {
		generatorInfo := &GeneratorInfo{}

		name, err := generator.Name(ctx)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		generatorInfo.Name = cliName(name)

		description, err := generator.Description(ctx)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		generatorInfo.Description = description

		info.Generators = append(info.Generators, generatorInfo)
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
func listGenerators(ctx context.Context, generatorGroup *dagger.GeneratorGroup, cmd *cobra.Command) error {
	info, err := loadGeneratorGroupInfo(ctx, generatorGroup)
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
