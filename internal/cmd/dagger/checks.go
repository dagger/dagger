package daggercmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
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
)

func init() {
	registerCommandArtifactFlags(checksCmd)
	checksCmd.Flags().BoolVarP(&checksListMode, "list", "l", false, "List available checks")
	checksCmd.Flags().BoolVar(&checksFailFast, "failfast", false, "Cancel remaining checks on first failure")
	checksCmd.Flags().BoolVar(&checksGenerated, "generated", true, "Include staleness checks for 'dagger generate'")
	checksCmd.Flags().StringArrayVar(&checksSkip, "skip", nil, "Exclude checks selected by this `link`")
	checksCmd.Flags().BoolVar(&checksScaleOut, "scale-out", false, "Enable scale-out to cloud engines for each check executed")
	checksCmd.Flags().Lookup("scale-out").Hidden = true
}

var checksCmd = &cobra.Command{
	Use:   "check [--failfast] [FILTERS] [OPTIONS]",
	Short: "Verify your project — tests, linters, type checks, security scans, etc.",
	Args:  cobra.ArbitraryArgs,
	RunE:  runChecksCommand,
}

func runChecksCommand(cmd *cobra.Command, args []string) error {
	params := client.Params{
		EnableCloudScaleOut:  checksScaleOut,
		SkipWorkspaceModules: true,
	}
	params, err := artifactClientParams(params, args)
	if err != nil {
		return err
	}
	return withEngine(
		cmd.Context(),
		params,
		func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			ws := dag.CurrentWorkspace()
			artifacts, err := commandArtifactsWithFlags(ctx, dag, ws, cmd, args, false)
			if err != nil {
				return err
			}
			checks, err := selectCommandChecks(dag, artifacts, cmd)
			if err != nil {
				return err
			}
			if checksListMode {
				return listArtifactSelection(ctx, dag, checks, cmd)
			}
			return runChecks(ctx, dag, checks, cmd, args)
		},
	)
}

func runChecks(ctx context.Context, dag *dagger.Client, checks *dagger.Artifacts, _ *cobra.Command, include []string) error {
	ctx, zoomSpan := Tracer().Start(ctx, "checks", telemetry.Passthrough())
	defer zoomSpan.End()
	Frontend.SetPrimary(dagui.SpanID{SpanID: zoomSpan.SpanContext().SpanID()})
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
	// We don't actually use the API for rendering results
	// Instead, we rely on telemetry
	// FIXME: this feels a little weird. Can we move the relevant telemetry collection in the API?
	results, err := evaluateArtifacts(ctx, dag, checks, checksFailFast)
	if err != nil {
		return err
	}
	if err := validateCheckSelection(include, len(results)); err != nil {
		return err
	}
	if err := artifactResultErrors(results); err != nil {
		return idtui.ExitError{OriginalCode: 1, Original: err}
	}
	return nil
}

func validateCheckSelection(include []string, selected int) error {
	if len(include) == 0 || selected > 0 {
		return nil
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

func selectCommandChecks(dag *dagger.Client, artifacts *dagger.Artifacts, cmd *cobra.Command) (*dagger.Artifacts, error) {
	checks := artifacts.FilterCheckCommand()
	if cmd.Flags().Changed("generated") {
		// Go SDK option structs omit false. Send the explicit Boolean value.
		checks = checks.WithGraphQLQuery(dag.QueryBuilder().Select("node").Arg("id", artifacts).
			InlineFragment("Artifacts").Select("filterCheckCommand").Arg("generated", checksGenerated))
	}
	for _, skip := range checksSkip {
		address, err := dagaddress.Parse(skip)
		if err != nil {
			return nil, err
		}
		address.Absolute = false
		if address.Path != "" && !strings.ContainsAny(address.Path, "*?[{") {
			address.Path += "/**"
		}
		checks = checks.WithoutURI(address.String())
	}
	return checks, nil
}
