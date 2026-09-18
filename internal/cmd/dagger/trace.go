package daggercmd

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	cloud "github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

var (
	traceSpan  string
	traceCheck string
	traceTest  string
)

var traceCmd = &cobra.Command{
	Use:    "trace [trace ID]",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Annotations: map[string]string{
		"experimental":       "true",
		showFinalProgressKey: "true",
	},
	Aliases: []string{"t", "analyze", "diagnose"},
	Short:   "Diagnose or view a Dagger Cloud trace.",
	Long: `Stream and render a Dagger Cloud trace: the overall pass/fail verdict, the
command(s) that caused a failure, check results, and failed tests, each with the
tail of its logs, plus the full call tree, arguments, and timing.

Use --span/--check/--test to scope and zoom the view to a single span, check, or
test by name.`,
	Example: `dagger trace 2f123ba77bf7bd2d4db2f70ed20613e8`,
	RunE:    traceRun,
}

func init() {
	traceCmd.Flags().StringVar(&traceSpan, "span", "", "Scope and zoom the view to a span ID")
	traceCmd.Flags().StringVar(&traceCheck, "check", "", "Scope and zoom the view to a check by name")
	traceCmd.Flags().StringVar(&traceTest, "test", "", "Scope and zoom the view to a test by name")
}

// traceRun streams the whole trace out of Cloud's binary OTLP endpoints
// (internal/cloud/otlp.go) into the frontend's own exporters -- the same
// path `dagger agent --trace` restores a session through -- and then zooms
// the view to any --span/--check/--test selection.
//
// Those endpoints are addressed by trace ID and token alone, which is what
// lets this command run without an org: the GraphQL-SSE subscriptions the
// previous incremental loader spoke were org-scoped, so `dagger trace` used
// to need --org (or a login with a default org) before it could show
// anything. The trade is that the fetch is whole-trace rather than lazy: a
// huge trace downloads up front instead of on expand, the cost --debug and
// high verbosity already paid.
func traceRun(cmd *cobra.Command, args []string) error {
	traceID := args[0]

	sel := spanSelector{span: traceSpan, check: traceCheck, test: traceTest}
	if err := sel.validate(); err != nil {
		return err
	}

	// The trace capabilities (name-based zoom targets, the re-run
	// suggestions' CI context) are one optional interface; tf is nil for the
	// plain/dots/logs frontends, which just render the OTLP stream.
	tf, _ := Frontend.(idtui.TraceFrontend)

	// statsClient hands the Cloud client to the --debug stats read below. The
	// run closure executes on the frontend's goroutine, which a force-quit
	// abandons without joining, so a plain shared variable would race.
	var statsClient atomic.Pointer[cloud.OTLPClient]
	runErr := Frontend.Run(cmd.Context(), opts, func(ctx context.Context) (cleanups.CleanupF, error) {
		noop := func() error { return nil }

		cloudAuth, err := auth.GetCloudAuth(ctx)
		if err != nil {
			return nil, fmt.Errorf("cloud auth: %w", err)
		}
		client, err := cloud.NewOTLPClient(ctx, cloudAuth)
		if err != nil {
			return nil, fmt.Errorf("cloud client: %w", err)
		}
		statsClient.Store(client)

		// Let the frontend point surfaced failure logs at 'dagger cloud logs
		// <trace> <span>' for the full, untruncated output.
		if tf != nil {
			tf.SetTraceID(traceID)
		}

		// Fetch the trace's source commit / CI change so the report can
		// suggest commit-scoped re-run commands. Runs beside the fetch; only
		// the final report reads the result.
		ciDone := make(chan struct{})
		go func() {
			defer close(ciDone)
			setTraceCIContext(ctx, tf, cloudAuth, traceID)
		}()

		importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{
			Spans:   Frontend.SpanExporter(),
			Logs:    Frontend.LogExporter(),
			Metrics: Frontend.MetricExporter(),
		})
		// This trace is the whole session: its root is the primary span,
		// not a second root to render through.
		importer.KeepRoots = true
		if err := client.FetchTrace(ctx, traceID, &primarySpanSink{TraceImportSink: importer}); err != nil {
			return noop, fmt.Errorf("fetch trace %s: %w", traceID, err)
		}
		<-ciDone

		// --span/--check/--test: zoom the view to the target, mirroring the
		// web UI's ?span= deep link. Everything is loaded, so --check/--test
		// resolve against the frontend's own view.
		if tf != nil {
			if err := zoomTraceView(tf, sel, traceID); err != nil {
				return noop, err
			}
		}
		return noop, nil
	})

	// With --debug, report how much data the run pulled from Cloud so expensive
	// fetches are visible.
	if opts.Debug {
		if client := statsClient.Load(); client != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), client.StatsSummary())
		}
	}
	return runErr
}

// primarySpanSink zooms the frontend to the trace's root as soon as it
// arrives, so the interactive view is scoped while the rest of a large trace
// is still streaming rather than after it has all landed. The first
// parentless span is the root; a capture can hold more than one (a trace
// whose real root never reached Cloud), and the first is the one the old
// incremental loader picked too.
type primarySpanSink struct {
	cloud.TraceImportSink
	once sync.Once
}

func (s *primarySpanSink) ImportSpans(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) error {
	for _, rs := range req.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				if len(span.GetParentSpanId()) != 0 {
					continue
				}
				s.once.Do(func() {
					var id dagui.SpanID
					copy(id.SpanID[:], span.GetSpanId())
					if id.IsValid() {
						Frontend.SetPrimary(id)
					}
				})
			}
		}
	}
	return s.TraceImportSink.ImportSpans(ctx, req)
}

// zoomTraceView scopes the view to the --span/--check/--test selection. A raw
// --span needs no lookup; --check/--test resolve by name against the loaded
// trace. It's a no-op when no selection is set.
func zoomTraceView(tf idtui.TraceFrontend, sel spanSelector, traceID string) error {
	if !sel.isSet() {
		return nil
	}
	var id dagui.SpanID
	if sel.span != "" {
		sid, err := trace.SpanIDFromHex(sel.span)
		if err != nil {
			return fmt.Errorf("invalid span %q: %w", sel.span, err)
		}
		id = dagui.SpanID{SpanID: sid}
	} else {
		var found bool
		id, found = tf.ResolveSpanTarget(sel.check, sel.test)
		if !found {
			if sel.check != "" {
				return fmt.Errorf("no check named %q in trace %s", sel.check, traceID)
			}
			return fmt.Errorf("no test named %q in trace %s", sel.test, traceID)
		}
	}
	tf.ZoomToSpan(id)
	return nil
}

// setTraceCIContext fetches the trace's source commit / CI change and feeds it
// to the frontend so the report can suggest commit-scoped re-run commands
// ('dagger cloud rerun').
//
// Best-effort, and the one org-scoped call left in this command: the metadata
// query is GraphQL and takes an org, so it only runs when one is at hand --
// --org, or the login's default org -- and a missing org, a frontend that
// doesn't accept CI context, or a failed/empty query all just mean the report
// falls back to a local 'dagger check' suggestion. It never gates the trace
// itself, which the OTLP fetch pulls by ID alone.
func setTraceCIContext(ctx context.Context, tf idtui.TraceFrontend, cloudAuth *auth.Cloud, traceID string) {
	if tf == nil {
		return
	}
	client, err := cloud.NewClient(ctx, cloudAuth)
	if err != nil {
		slog.Debug("skipping re-run suggestions", "err", err)
		return
	}
	orgID, err := resolveOrgID(ctx, client, cloudAuth)
	if err != nil {
		slog.Debug("skipping re-run suggestions", "err", err)
		return
	}
	meta, err := client.TraceMetadata(ctx, orgID, traceID)
	if err != nil {
		slog.Warn("failed to fetch trace metadata for re-run suggestions", "err", err)
		return
	}
	if meta == nil {
		return
	}
	var commit string
	var isNativeCI bool
	if meta.Git != nil {
		commit = meta.Git.Ref
	}
	if meta.CI != nil {
		isNativeCI = meta.CI.IsNativeCI
		if commit == "" && meta.CI.Change != nil {
			commit = meta.CI.Change.HeadSHA
		}
	}
	tf.SetCIContext(commit, isNativeCI)
}

// resolveOrgID picks the org for Cloud's org-scoped GraphQL calls: --org,
// else the login's current org.
func resolveOrgID(ctx context.Context, client *cloud.Client, cloudAuth *auth.Cloud) (string, error) {
	orgName := cloudOrgFlag
	if orgName != "" {
		// Resolve org name to ID via GraphQL
		org, err := client.OrgByName(ctx, orgName)
		if err != nil {
			return "", fmt.Errorf("resolve org %q: %w", orgName, err)
		}
		return org.ID, nil
	}

	// Fall back to current org from auth
	if cloudAuth != nil && cloudAuth.Org != nil && cloudAuth.Org.ID != "" {
		return cloudAuth.Org.ID, nil
	}

	return "", fmt.Errorf("no org specified; use --org or run 'dagger login' to set a default org")
}
