package daggercmd

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

var (
	generateListMode    bool
	generateRequireLoad bool
)

//go:embed generators.graphql
var loadGeneratorsQuery string

func init() {
	generateCmd.Flags().BoolVarP(&generateListMode, "list", "l", false, "List available generators")
	generateCmd.Flags().BoolVar(&generateRequireLoad, "require-load", false, "Fail if any workspace module cannot be loaded (default: report as a warning and generate the rest)")
}

var generateCmd = &cobra.Command{
	Use:   "generate [options] [pattern...]",
	Short: "Generate derived files for your project — code, SDKs, types, docs, etc.",
	Long: `Generate derived files for your project — code, SDKs, types, docs, etc.

Examples:
  dagger generate                            # Generate all assets
  dagger generate -l                         # List all available generators
  dagger generate go:bin                     # Generate by selecting the generator function
  dagger -W github.com/acme/ws generate go:bin  # Generate against explicit workspace
`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		params := client.Params{
			LoadWorkspaceModules: true,
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
				// Loading is best-effort: a module that fails to load is skipped
				// and surfaced in telemetry (engine-side, rendered like a check
				// that did not pass) while the rest still generate. --require-load
				// opts back into strict: fail before running or applying if any
				// workspace module could not be loaded.
				if generateRequireLoad {
					loadFailures, err := generatorGroupLoadFailures(ctx, dag, args)
					if err != nil {
						return err
					}
					if len(loadFailures) > 0 {
						return fmt.Errorf("%d workspace module(s) could not be loaded (--require-load)", len(loadFailures))
					}
				}
				if generateListMode {
					return listGenerators(ctx, dag, generators, cmd)
				}
				return runGenerators(ctx, dag, generators, cmd)
			},
		)
	},
}

// generatorGroupLoadFailures reads the best-effort per-module load failures for
// the given include patterns. It roots the query at currentWorkspace — a core
// root field the request peek recognizes as demanding no modules — so this
// fetch never re-triggers a strict entrypoint load. Resolving generators still
// performs the best-effort module load, so the returned messages cover both the
// ambient demand and any explicitly-selected module that could not load, which
// --require-load then turns fatal.
func generatorGroupLoadFailures(ctx context.Context, dag *dagger.Client, include []string) ([]string, error) {
	var res struct {
		CurrentWorkspace struct {
			Generators struct {
				LoadFailures []string
			}
		}
	}
	err := dag.Do(ctx, &dagger.Request{
		Query: `query GeneratorGroupLoadFailures($include: [String!]) {
  currentWorkspace {
    generators(include: $include) {
      loadFailures
    }
  }
}`,
		OpName:    "GeneratorGroupLoadFailures",
		Variables: map[string]any{"include": include},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, err
	}
	return res.CurrentWorkspace.Generators.LoadFailures, nil
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
