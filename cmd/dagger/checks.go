package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
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
	checksListMode     bool
	checksFailFast     bool
	checksNoGenerate   bool
	checksOnlyGenerate bool
	checksPast         bool
	checksNoPast       bool
	checksRun          bool
)

//go:embed checks.graphql
var loadChecksQuery string

func init() {
	checksCmd.Flags().BoolVarP(&checksListMode, "list", "l", false, "List available checks")
	checksCmd.Flags().BoolVar(&checksFailFast, "failfast", false, "Cancel remaining checks on first failure")
	checksCmd.Flags().BoolVar(&checksNoGenerate, "no-generate", false, "Only run annotated check functions, skip generate-as-checks")
	checksCmd.Flags().BoolVar(&checksOnlyGenerate, "generate", false, "Only run generate-as-checks, skip annotated check functions")
	checksCmd.Flags().BoolVar(&checksPast, "past", false, "Only replay past Cloud Checks results")
	checksCmd.Flags().BoolVar(&checksNoPast, "no-past", false, "Disable past Cloud Checks lookup")
	checksCmd.Flags().BoolVar(&checksRun, "run", false, "Run checks even when past Cloud Checks results are replayed")
	checksCmd.MarkFlagsMutuallyExclusive("no-generate", "generate")
	checksCmd.MarkFlagsMutuallyExclusive("past", "no-past", "run")
}

var checksCmd = &cobra.Command{
	Use:   "check [options] [pattern...]",
	Short: "Verify your project — tests, linters, type checks, security scans, etc.",
	Long: `Verify your project — tests, linters, type checks, security scans, etc.

Examples:
  dagger check                    # Run all checks
  dagger check -l                 # List all available checks
  dagger check go:lint            # Run the go:lint check and any subchecks
  dagger -W github.com/acme/ws check go:lint  # Run check(s) against explicit workspace
`,
	Args: cobra.ArbitraryArgs,
	RunE: runChecksCommand,
}

func runChecksCommand(cmd *cobra.Command, args []string) error {
	if !checksListMode {
		replayed, shouldRun, err := maybeReplayPastChecks(cmd, args)
		if !shouldRun {
			return err
		}
		if err != nil && !replayed {
			fmt.Fprintf(cmd.ErrOrStderr(), "Cloud check lookup failed: %v; running checks now.\n\n", err)
		}
	}

	return runChecksNow(cmd, args)
}

func maybeReplayPastChecks(cmd *cobra.Command, args []string) (replayed bool, shouldRun bool, err error) {
	if checksNoPast {
		return false, true, nil
	}

	address, ok, reason, err := checkPastWorkspaceAddress(cmd.Context())
	if err != nil {
		if checksPast {
			return false, false, err
		}
		return false, true, err
	}
	if !ok {
		if checksPast {
			if reason == "" {
				reason = "no remote workspace is known"
			}
			return false, false, idtui.ExitError{OriginalCode: 1, Original: fmt.Errorf("past Cloud checks unavailable: %s", reason)}
		}
		return false, true, nil
	}

	res, selectors, err := cloudCLI.loadCloudCheckRowsForWorkspaceAcrossUserOrgs(cmd.Context(), address, args, false)
	if err != nil {
		if checksPast {
			return false, false, err
		}
		if errors.Is(err, errCloudNotAuthenticated) {
			return false, true, nil
		}
		return false, true, err
	}
	if len(res.Rows) == 0 {
		if checksPast {
			return false, false, idtui.ExitError{OriginalCode: 1, Original: fmt.Errorf("no Cloud check result found for %s", address)}
		}
		if !checksRun {
			fmt.Fprintf(cmd.OutOrStdout(), "No Cloud Checks result found for %s; running checks now.\n\n", address)
		}
		return false, true, nil
	}

	err = cloudCLI.replayCloudCheckResult(cmd, res, selectors)
	if checksRun {
		return true, true, err
	}
	return true, false, err
}

func checkPastWorkspaceAddress(ctx context.Context) (string, bool, string, error) {
	address := strings.TrimSpace(workspaceRef)
	if address != "" {
		_, ok, err := parseWorkspaceRemoteAddress(ctx, address)
		if err != nil {
			return "", false, "", err
		}
		if ok {
			return address, true, "", nil
		}
	}

	_, inferred, dirty, inferErr := inferCleanLocalWorkspaceRemoteAddress(ctx, address)
	if inferErr == nil {
		if dirty {
			return "", false, "workspace has uncommitted changes", nil
		}
		if inferred == "" {
			return "", false, "no remote workspace is known", nil
		}
		return inferred, true, "", nil
	}

	return "", false, inferErr.Error(), nil
}

func runChecksNow(cmd *cobra.Command, args []string) error {
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
			checks := ws.Checks(dagger.WorkspaceChecksOpts{
				Include:      args,
				NoGenerate:   checksNoGenerate,
				OnlyGenerate: checksOnlyGenerate,
			})
			if checksListMode {
				return listChecks(ctx, dag, checks, cmd)
			}
			return runChecks(ctx, dag, checks, cmd)
		},
	)
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
	CheckType   string
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
			Type:        item.CheckType,
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
	Type        string
}

// 'dagger checks -l'
func listChecks(ctx context.Context, dag *dagger.Client, checkgroup *dagger.CheckGroup, cmd *cobra.Command) error {
	info, err := loadCheckGroupInfo(ctx, dag, checkgroup)
	if err != nil {
		return err
	}

	return writeCheckList(cmd.OutOrStdout(), info.Checks)
}

func writeCheckList(w io.Writer, checks []*CheckInfo) error {
	showType := false
	for _, c := range checks {
		if c.Type == "generate" {
			showType = true
			break
		}
	}

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	if showType {
		fmt.Fprintf(tw, "%s\t%s\t%s\n",
			termenv.String("Name").Bold(),
			termenv.String("Type").Bold(),
			termenv.String("Description").Bold(),
		)
	} else {
		fmt.Fprintf(tw, "%s\t%s\n",
			termenv.String("Name").Bold(),
			termenv.String("Description").Bold(),
		)
	}
	for _, check := range checks {
		firstLine := check.Description
		if idx := strings.Index(check.Description, "\n"); idx != -1 {
			firstLine = check.Description[:idx]
		}
		if showType {
			checkType := check.Type
			if checkType == "" {
				checkType = "check"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", check.Name, checkType, firstLine)
		} else {
			fmt.Fprintf(tw, "%s\t%s\n", check.Name, firstLine)
		}
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
