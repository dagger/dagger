package daggercmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"dagger.io/dagger/core"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

var (
	checksListMode  bool
	checksFailFast  bool
	checksGenerated bool
	checksSkip      []string
	checksScaleOut  bool
	checksParallel  int
)

// checksParallelEnv sets the default for 'dagger check --parallel'.
const checksParallelEnv = "DAGGER_CHECK_PARALLEL"

func init() {
	registerCommandArtifactFlags(checksCmd)
	checksCmd.Flags().BoolVarP(&checksListMode, "list", "l", false, "List available checks")
	checksCmd.Flags().BoolVar(&checksFailFast, "fail-fast", false, "Cancel remaining checks on first failure")
	checksCmd.Flags().BoolVar(&checksGenerated, "generated", true, "Include staleness checks for 'dagger generate'")
	checksCmd.Flags().StringArrayVar(&checksSkip, "skip", nil, "Exclude checks selected by this `link`")
	checksCmd.Flags().BoolVar(&checksScaleOut, "scale-out", false, "Enable scale-out to cloud engines for each check executed")
	checksCmd.Flags().Lookup("scale-out").Hidden = true
	checksCmd.Flags().IntVarP(&checksParallel, "parallel", "j", 0, "Run at most this many checks at once; 0 means no limit. Defaults to $"+checksParallelEnv)
}

var checksCmd = &cobra.Command{
	Use:   "check [--fail-fast] [FILTERS] [OPTIONS]",
	Short: "Verify your project — tests, linters, type checks, security scans, etc.",
	Args:  cobra.ArbitraryArgs,
	RunE:  runChecksCommand,
}

func runChecksCommand(cmd *cobra.Command, args []string) error {
	if !cmd.Flags().Changed("parallel") {
		if value := os.Getenv(checksParallelEnv); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("%s: %w", checksParallelEnv, err)
			}
			checksParallel = n
		}
	}
	if err := validateParallel(checksParallel); err != nil {
		return err
	}
	params, err := checksClientParams(args)
	if err != nil {
		return err
	}
	return withArtifactEngine(
		cmd.Context(),
		params,
		func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			ws := core.NewQuery(dag).CurrentWorkspace()
			artifacts, err := commandArtifactsWithFlags(ctx, dag, ws, cmd, args, false)
			if err != nil {
				return err
			}
			checks, err := commandArtifactTargets(ctx, dag, cmd, artifacts)
			if err != nil {
				return err
			}
			if checksListMode {
				return listArtifactSelection(ctx, dag, checks, cmd)
			}
			return runChecks(ctx, dag, ws, checks, cmd, args)
		},
	)
}

func checksClientParams(args []string) (client.Params, error) {
	return artifactClientParams(client.Params{
		EnableCloudScaleOut:  checksScaleOut,
		SkipWorkspaceModules: true,
		// Send the whole command line, so that Dagger Cloud can identify
		// the run and size its engine.
		ExecCmd: os.Args[1:],
	}, args)
}

func runChecks(ctx context.Context, dag *dagger.Client, ws *core.Workspace, checks *core.Artifacts, cmd *cobra.Command, include []string) error {
	ctx = artifactExecutionContext(ctx)
	ctx, zoomSpan := Tracer().Start(ctx, "checks", telemetry.Passthrough())
	defer zoomSpan.End()
	Frontend.SetPrimary(dagui.SpanID{SpanID: zoomSpan.SpanContext().SpanID()})
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
	// We don't actually use the API for rendering results
	// Instead, we rely on telemetry
	// FIXME: this feels a little weird. Can we move the relevant telemetry collection in the API?
	results, err := evaluateArtifacts(ctx, dag, checks, checksFailFast, checksParallel)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		// A check whose collection has no items runs nothing, but it was
		// still selected.
		empty, err := emptyCheckPaths(ctx, dag, ws, checks, cmd, include)
		if err != nil {
			return err
		}
		if len(empty) > 0 {
			// The checks span is primary: the frontend prints its output.
			stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
			defer stdio.Close()
			for _, path := range empty {
				fmt.Fprintf(stdio.Stderr, "check %q has no items to check\n", path)
			}
			return nil
		}
	}
	if err := validateCheckSelection(include, len(results)); err != nil {
		return err
	}
	if err := artifactResultErrors(results); err != nil {
		return idtui.ExitError{OriginalCode: 1, Original: err}
	}
	return nil
}

func validateParallel(n int) error {
	if n < 0 {
		return fmt.Errorf("--parallel must not be negative: %d", n)
	}
	return nil
}

func validateCheckSelection(include []string, selected int) error {
	if selected > 0 {
		return nil
	}
	if len(include) == 0 {
		return fmt.Errorf("no checks selected; use 'dagger check -l' to list available checks")
	}
	if len(include) == 1 {
		return fmt.Errorf("no checks matched pattern %q", include[0])
	}

	patterns := make([]string, 0, len(include))
	for _, pattern := range include {
		patterns = append(patterns, fmt.Sprintf("%q", pattern))
	}
	return fmt.Errorf("no checks matched any of the patterns: %s", strings.Join(patterns, ", "))
}

// emptyCheckPaths returns the selected check paths after a run that produced
// no results. Without item keys, the run expanded every selected collection,
// so each selected check has no items. With item keys, the keys may have left
// out every item, so it returns nothing: those still match nothing.
func emptyCheckPaths(ctx context.Context, dag *dagger.Client, ws *core.Workspace, checks *core.Artifacts, cmd *cobra.Command, addresses []string) ([]string, error) {
	keyed, err := selectsItemKeys(ctx, dag, ws, cmd, addresses)
	if err != nil || keyed {
		return nil, err
	}
	paths, err := readArtifactListPaths(ctx, dag, checks)
	if err != nil {
		return nil, err
	}
	empty := make([]string, 0, len(paths))
	for _, path := range paths {
		addr, err := dagaddress.Parse(path.URI)
		if err != nil {
			return nil, err
		}
		// A check's type key is its full field path.
		empty = append(empty, addr.Query[0].Key)
	}
	return empty, nil
}

// selectsItemKeys reports whether an address, a dimension flag or a --skip
// names a collection key, or a skip excludes items only as they are expanded.
func selectsItemKeys(ctx context.Context, dag *dagger.Client, ws *core.Workspace, cmd *cobra.Command, addresses []string) (bool, error) {
	keys := artifactKeyFlags(cmd)
	parsed, err := parseArtifactAddresses(addresses)
	if err != nil {
		return false, err
	}
	keyed := func(pair dagaddress.Pair) bool { return !pair.HasKey }
	selected := slices.Clone(keys)
	for _, address := range parsed {
		selected = append(selected, address.Query...)
	}
	selected = slices.DeleteFunc(selected, keyed)
	var skipped []dagaddress.Pair
	for _, skip := range checksSkip {
		address, err := dagaddress.Parse(skip)
		if err != nil {
			return false, err
		}
		skipped = append(skipped, address.Query...)
	}
	skipped = slices.DeleteFunc(skipped, keyed)
	if len(selected) == 0 && len(skipped) == 0 {
		return false, nil
	}
	defs, err := artifactDimensions(ctx, dag, ws.Artifacts(core.WorkspaceArtifactsOpts{Include: artifactPaths(parsed, keys...)}))
	if err != nil {
		return false, err
	}
	// A key that doesn't resolve here counts, so the run keeps its error.
	for _, pair := range selected {
		id, err := defs.Resolve(pair.Dimension)
		if err != nil || !artifact.IsStaticDimension(id) {
			return true, nil
		}
	}
	// Only a type key removes schema paths. Other keyed skips, module keys
	// included, exclude items as collections expand (Artifacts.withoutUri).
	for _, pair := range skipped {
		id, err := defs.Resolve(pair.Dimension)
		if err != nil || !strings.HasPrefix(id, "type:") {
			return true, nil
		}
	}
	return false, nil
}
