package main

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/codes"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

var (
	checksListMode bool
	checksFailFast bool
)

//go:embed checks.graphql
var loadChecksQuery string

func init() {
	checksCmd.Flags().BoolVarP(&checksListMode, "list", "l", false, "List available checks")
	checksCmd.Flags().BoolVar(&checksFailFast, "failfast", false, "Cancel remaining checks on first failure")
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
		params := client.Params{
			EnableCloudScaleOut:  enableScaleOut,
			LoadWorkspaceModules: true,
		}
		return withEngine(
			cmd.Context(),
			params,
			func(ctx context.Context, engineClient *client.Client) error {
				dag := engineClient.Dagger()
				ws := dag.CurrentWorkspace()
				var checks *dagger.CheckGroup
				if len(args) > 0 {
					checks = ws.Checks(dagger.WorkspaceChecksOpts{Include: args})
				} else {
					checks = ws.Checks()
				}
				if checksListMode {
					return listChecks(ctx, dag, checks, cmd)
				}
				return runChecks(ctx, dag, checks, cmd)
			},
		)
	},
}

// loadGroupListDetails fetches name+description for every item in a group
// using a single batch GraphQL query.
//
// The span encapsulates its children so the per-check name/description
// resolvers don't spam the list-mode UI, but keeps them in the trace so
// module loading and query work contribute activity to this span (and can
// be revealed if it errors or with -v).
func loadGroupListDetails(
	ctx context.Context,
	dag *dagger.Client,
	spanName string,
	getID func(context.Context) (any, error),
	query string,
	opName string,
) ([]groupListItem, error) {
	ctx, span := Tracer().Start(ctx, spanName, telemetry.Encapsulate())
	defer span.End()

	id, err := getID(ctx)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	var res struct {
		Group struct {
			List []groupListItem
		}
	}

	err = dag.Do(ctx, &dagger.Request{
		Query:  query,
		OpName: opName,
		Variables: map[string]any{
			"id": id,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	return res.Group.List, nil
}

type groupListItem struct {
	Name        string
	Description string
}

func loadCheckGroupInfo(ctx context.Context, dag *dagger.Client, checkgroup *dagger.CheckGroup) (*CheckGroupInfo, error) {
	items, err := loadGroupListDetails(ctx, dag, "fetch check information",
		func(ctx context.Context) (any, error) { return checkgroup.ID(ctx) },
		loadChecksQuery, "CheckGroupListDetails",
	)
	if err != nil {
		return nil, err
	}
	info := &CheckGroupInfo{Checks: make([]*CheckInfo, 0, len(items))}
	for _, item := range items {
		info.Checks = append(info.Checks, &CheckInfo{
			Name:        cliName(item.Name),
			Description: item.Description,
		})
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
func listChecks(ctx context.Context, dag *dagger.Client, checkgroup *dagger.CheckGroup, cmd *cobra.Command) error {
	info, err := loadCheckGroupInfo(ctx, dag, checkgroup)
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
func runChecks(ctx context.Context, dag *dagger.Client, checkgroup *dagger.CheckGroup, _ *cobra.Command) error {
	ctx, zoomSpan := Tracer().Start(ctx, "checks", telemetry.Passthrough())
	defer zoomSpan.End()
	Frontend.SetPrimary(dagui.SpanID{SpanID: zoomSpan.SpanContext().SpanID()})
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
	// We don't actually use the API for rendering results
	// Instead, we rely on telemetry
	// FIXME: this feels a little weird. Can we move the relevant telemetry collection in the API?
	id, err := checkgroup.ID(ctx)
	if err != nil {
		return err
	}

	var res struct {
		CheckGroup struct {
			Run struct {
				List []struct {
					Passed bool
				}
			}
		}
	}

	opName := "CheckGroupRunStatuses"
	if checksFailFast {
		opName = "CheckGroupRunStatusesFailFast"
	}

	err = dag.Do(ctx, &dagger.Request{
		Query:  loadChecksQuery,
		OpName: opName,
		Variables: map[string]any{
			"checkGroup": id,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return err
	}

	var failed int
	for _, check := range res.CheckGroup.Run.List {
		if !check.Passed {
			failed++
		}
	}
	if failed > 0 {
		return idtui.ExitError{OriginalCode: 1, Original: fmt.Errorf("%d checks failed", failed)}
	}
	return nil
}
