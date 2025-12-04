package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/codes"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
)

var (
	checksListMode       bool
	checkAll             bool
	enableChecksScaleOut bool
)

func init() {
	checksCmd.Flags().BoolVarP(&checksListMode, "list", "l", false, "List available checks")
	checksCmd.Flags().BoolVarP(&checkAll, "all", "a", false, "Include dynamic checks")

	checksCmd.Flags().BoolVar(&enableChecksScaleOut, "scale-out", false, "Enable scale-out to cloud engines for each check executed")
	checksCmd.Flags().MarkHidden("scale-out")
}

var checksCmd = &cobra.Command{
	Hidden:  true,
	Aliases: []string{"checks"},
	Use:     "check [options] [pattern...]",
	Short:   "Check the state of your project by running tests, linters, etc.",
	Long: `Check the state of your project by running tests, linters, etc.

Examples:
  dagger check                    # Run all checks
  dagger check -l                 # List all available checks
  dagger check go:lint            # Run the go:lint check and any subchecks
`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(
			cmd.Context(),
			client.Params{
				EnableCloudScaleOut: enableChecksScaleOut,
			},
			func(ctx context.Context, engineClient *client.Client) error {
				dag := engineClient.Dagger()
				mod, err := loadModule(ctx, dag)
				if err != nil {
					return err
				}
				var checks *dagger.CheckGroup
				if len(args) > 0 {
					checks = mod.Checks(dagger.ModuleChecksOpts{Include: args, All: checkAll})
				} else {
					checks = mod.Checks(dagger.ModuleChecksOpts{All: checkAll})
				}
				if checksListMode {
					return listChecks(ctx, checks, cmd)
				} else {
					return runChecks(ctx, checks, cmd)
				}
			},
		)
	},
}

func loadModule(ctx context.Context, dag *dagger.Client) (*dagger.Module, error) {
	modRef, _ := getExplicitModuleSourceRef()
	if modRef == "" {
		modRef = moduleURLDefault
	}
	ctx, span := Tracer().Start(ctx, "load "+modRef)
	defer span.End()
	return dag.ModuleSource(modRef).AsModule().Sync(ctx)
}

func loadCheckGroupInfo(ctx context.Context, checkgroup *dagger.CheckGroup) (*CheckGroupInfo, error) {
	ctx, span := Tracer().Start(ctx, "fetch check information")
	defer span.End()

	checks, err := checkgroup.List(ctx)
	if err != nil {
		return nil, err
	}
	info := &CheckGroupInfo{}
	for _, check := range checks {
		checkInfo := &CheckInfo{}

		name, err := check.Name(ctx)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		checkInfo.Name = cliName(name)

		description, err := check.Description(ctx)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		checkInfo.Description = description

		info.Checks = append(info.Checks, checkInfo)
	}
	return info, nil
}

type CheckGroupInfo struct {
	Checks []*CheckInfo
}

type CheckInfo struct {
	Name        string
	Description string
}

// 'dagger checks -l'
func listChecks(ctx context.Context, checkgroup *dagger.CheckGroup, cmd *cobra.Command) error {
	info, err := loadCheckGroupInfo(ctx, checkgroup)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	fmt.Fprintf(tw, "%s\t%s\n",
		termenv.String("Name").Bold(),
		termenv.String("Description").Bold(),
	)
	for _, check := range info.Checks {
		firstLine := check.Description
		if idx := strings.Index(check.Description, "\n"); idx != -1 {
			firstLine = check.Description[:idx]
		}
		fmt.Fprintf(tw, "%s\t%s\n", check.Name, firstLine)
	}
	return tw.Flush()
}

// 'dagger checks' (runs by default)
func runChecks(ctx context.Context, checkgroup *dagger.CheckGroup, _ *cobra.Command) error {
	ctx, zoomSpan := Tracer().Start(ctx, "checks", telemetry.Passthrough())
	defer zoomSpan.End()
	Frontend.SetPrimary(dagui.SpanID{SpanID: zoomSpan.SpanContext().SpanID()})
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
	// We don't actually use the API for rendering results
	// Instead, we rely on telemetry
	// FIXME: this feels a little weird. Can we move the relevant telemetry collection in the API?
	checks, err := checkgroup.Run().List(ctx)
	if err != nil {
		return err
	}
	var failed int
	for _, check := range checks {
		passed, err := check.Passed(ctx)
		if err != nil {
			return err
		}
		if !passed {
			failed++
		}
	}
	if failed > 0 {
		return idtui.ExitError{Code: 1, Original: fmt.Errorf("%d checks failed", failed)}
	}
	return nil
}
