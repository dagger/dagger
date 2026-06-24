package daggercmd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
	cloud "github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var traceFull bool

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
	Long: `Summarize why a trace failed: overall status, the command(s) that caused the
failure, check results, and failed tests, each with the tail of its logs. This
summary is computed server-side without loading the whole trace, and is the
default.

Pass --full to instead stream and render the entire trace -- the full call
tree, arguments, and timing.`,
	Example: `dagger trace 2f123ba77bf7bd2d4db2f70ed20613e8`,
	RunE:    traceRun,
}

func init() {
	traceCmd.Flags().BoolVar(&traceFull, "full", false, "Render the full trace (call tree, arguments, timing) instead of the failure summary")
	traceCmd.Flags().BoolVar(&cloudJSON, "json", false, "Print the summary as JSON (no logs; ignored with --full)")
	traceCmd.Flags().IntVar(&analyzeLogLines, "log-lines", 20, "Lines of log tail to show per failed span in the summary (0 to disable)")
	traceCmd.Flags().BoolVar(&analyzeNoLogs, "no-logs", false, "Skip fetching logs in the summary, just the triage")
	traceCmd.Flags().DurationVar(&analyzeLogTimeout, "log-timeout", 30*time.Second, "Max time to spend fetching each span's log tail in the summary")
}

// traceRun shows the server-computed failure summary by default, and the full
// streamed trace when --full is given. The summary path is the same one the
// 'cloud analyze' work produced; --full keeps the original render-everything
// behavior.
func traceRun(cmd *cobra.Command, args []string) error {
	if traceFull {
		return traceFullRender(cmd, args)
	}
	return cloudCLI.Analyze(cmd, args)
}

func traceFullRender(cmd *cobra.Command, args []string) error {
	traceID := args[0]

	return Frontend.Run(cmd.Context(), opts, func(ctx context.Context) (cleanups.CleanupF, error) {
		cloudAuth, err := auth.GetCloudAuth(ctx)
		if err != nil {
			return nil, fmt.Errorf("cloud auth: %w", err)
		}
		if cloudAuth == nil || cloudAuth.Token == nil {
			return nil, fmt.Errorf("not authenticated; run 'dagger login' or set DAGGER_CLOUD_TOKEN")
		}

		client, err := cloud.NewClient(ctx, cloudAuth)
		if err != nil {
			return nil, fmt.Errorf("cloud client: %w", err)
		}

		// Resolve org ID: --org flag > current org
		orgID, err := resolveOrgID(ctx, client, cloudAuth)
		if err != nil {
			return nil, err
		}

		spanExp := Frontend.SpanExporter()
		defer spanExp.Shutdown(ctx)
		logExp := Frontend.LogExporter()
		defer logExp.Shutdown(ctx)

		noop := func() error { return nil }

		// Fetch logs lazily, one span at a time, rather than a single
		// descendants=true stream of the whole trace (wasteful). The frontend
		// decides which spans need logs: lazily when the user expands a span, and
		// eagerly for the failed spans it surfaces. descendants mirrors the span's
		// RollUpLogs -- a check or test whose real output lives in a sub-operation
		// rolls that up; everything else shows just its own logs. fetchSpanLogs
		// dedups and bounds concurrency, and uses the outer ctx -- which stays
		// alive while the TUI is interactive -- so lazy expands keep working after
		// span streaming finishes (the span errgroup's ctx does not).
		var (
			logReqMu sync.Mutex
			logReq   = map[string]bool{}
			logSem   = make(chan struct{}, 8)
			logEg    errgroup.Group
		)
		fetchSpanLogs := func(spanHex string, descendants bool) {
			logReqMu.Lock()
			if spanHex == "" || logReq[spanHex] {
				logReqMu.Unlock()
				return
			}
			logReq[spanHex] = true
			logReqMu.Unlock()
			logEg.Go(func() error {
				logSem <- struct{}{}
				defer func() { <-logSem }()
				if err := client.StreamLogs(ctx, orgID, traceID, spanHex, descendants, func(logs []cloud.LogMessage) {
					records := cloud.LogMessagesToRecords(traceID, logs)
					if len(records) == 0 {
						return
					}
					if err := logExp.Export(ctx, records); err != nil {
						slog.Warn("error exporting logs", "err", err)
					}
				}); err != nil {
					slog.Warn("error streaming span logs", "span", spanHex, "err", err)
				}
				return nil
			})
		}

		// Let the TUI request a span's logs on demand (expand / surfaced failure).
		if lp, ok := Frontend.(interface {
			SetLogProvider(func(dagui.SpanID, bool))
		}); ok {
			lp.SetLogProvider(func(id dagui.SpanID, descendants bool) {
				fetchSpanLogs(id.String(), descendants)
			})
		}

		eg, spanCtx := errgroup.WithContext(ctx)
		eg.Go(func() error {
			return client.StreamSpans(spanCtx, orgID, traceID, func(spanDatas []cloud.SpanData) {
				slog.Debug("received spans from cloud", "count", len(spanDatas))

				// Convert to OTLP proto, then to OTel SDK ReadOnlySpans, and feed
				// through the frontend's exporter pipeline so rendering triggers.
				resourceSpans := cloud.SpansToPB(spanDatas)
				spans := telemetry.SpansFromPB(resourceSpans)
				if len(spans) == 0 {
					return
				}

				if err := spanExp.ExportSpans(spanCtx, spans); err != nil {
					slog.Warn("error exporting spans", "err", err)
					return
				}

				// Set the root span (no parent) as the primary span so the TUI
				// roots the tree there.
				for _, span := range spans {
					if !span.Parent().SpanID().IsValid() {
						spanID := dagui.SpanID{SpanID: span.SpanContext().SpanID()}
						slog.Debug("setting primary span", "spanID", spanID)
						Frontend.SetPrimary(spanID)
						break
					}
				}
			})
		})

		if err := eg.Wait(); err != nil {
			return noop, fmt.Errorf("stream trace: %w", err)
		}
		// Now that all spans are loaded, ask the frontend to surface its failures
		// and request their logs. This matters most for non-interactive 'report'
		// mode, which renders only once: we trigger the requests here, then drain
		// them below, so the single final render includes the failure detail.
		if r, ok := Frontend.(interface{ RequestSurfacedLogs() }); ok {
			r.RequestSurfacedLogs()
		}
		// Drain the eager failure-log fetches so the final report isn't missing
		// the detail it surfaced. Lazy expand fetches, if any, keep running.
		if err := logEg.Wait(); err != nil {
			return noop, err
		}

		return noop, nil
	})
}

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
	if cloudAuth.Org != nil && cloudAuth.Org.ID != "" {
		return cloudAuth.Org.ID, nil
	}

	return "", fmt.Errorf("no org specified; use --org or run 'dagger login' to set a default org")
}
