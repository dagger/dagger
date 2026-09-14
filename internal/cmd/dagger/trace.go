package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	cloud "github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

var (
	traceSpan  string
	traceCheck string
	traceTest  string
)

var traceCmd = &cobra.Command{
	Use:  "traces [trace ID]",
	Args: cobra.ExactArgs(1),
	Annotations: map[string]string{
		"experimental":       "true",
		showFinalProgressKey: "true",
	},
	Aliases: []string{"trace", "t", "analyze", "diagnose"},
	Short:   "Diagnose or view a Dagger Cloud trace.",
	Long: `Stream and render a Dagger Cloud trace: the overall pass/fail verdict, the
command(s) that caused a failure, check results, and failed tests, each with the
tail of its logs, plus the full call tree, arguments, and timing. Spans and logs
are fetched incrementally, so the whole trace doesn't have to load up front.

Use --span/--check/--test to scope and zoom the view to a single span, check, or
test by name.`,
	Example: `dagger trace 2f123ba77bf7bd2d4db2f70ed20613e8`,
	RunE:    traceRun,
}

func init() {
	traceCmd.Flags().StringVar(&traceSpan, "span", "", "Scope and zoom the view to a span ID (fetches its subtree and logs)")
	traceCmd.Flags().StringVar(&traceCheck, "check", "", "Scope and zoom the view to a check by name")
	traceCmd.Flags().StringVar(&traceTest, "test", "", "Scope and zoom the view to a test by name")

	// Lives under `dagger cloud` as `dagger cloud traces` (with a `trace` alias).
	cloudCmd.AddCommand(traceCmd)
}

// traceRun streams a trace out of Cloud's binary OTLP endpoints
// (internal/cloud/otlp.go) into the frontend's own exporters -- the transport
// `dagger agent --trace` restores a session through -- and zooms the view to
// any --span/--check/--test selection.
//
// Those endpoints are addressed by trace ID and token alone, which is what
// lets this command run without an org: the GraphQL-SSE subscriptions it used
// to speak were org-scoped, so `dagger trace` needed --org (or a login with a
// default org) before it could show anything. They take the same selection
// the subscriptions did, so loading stays incremental: the priority spans
// first, a span's children when it's expanded, a span's logs when they're
// shown.
func traceRun(cmd *cobra.Command, args []string) error {
	traceID := args[0]

	sel := spanSelector{span: traceSpan, check: traceCheck, test: traceTest}
	if err := sel.validate(); err != nil {
		return err
	}

	// The trace capabilities (lazy loading, zooming, surfaced-failure
	// prefetch) are one optional interface; tf is nil for the plain/dots/logs
	// frontends, which get the whole trace as an OTLP span/log stream instead.
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
		// the final report reads the result, and the pre-report drain below
		// orders it.
		var logFg fetchGroup
		logFg.Go(func() error {
			setTraceCIContext(ctx, tf, cloudAuth, traceID)
			return nil
		})

		// Fetch spans incrementally, mirroring the Cloud web UI: stream the
		// priority (root) spans first, then fetch a span's children on demand
		// when the user expands it, and its logs when they're shown. The
		// loader uses the outer ctx so lazy fetches keep working while the
		// TUI is interactive (-E).
		//
		// ...unless the whole tree is going to be shown and expanded anyway
		// (--debug, --expand, or a verbosity that expands completed spans), or
		// the frontend can't expand lazily at all (plain/dots/logs): then
		// fetch the entire trace up front. Incremental loading only pays off
		// when most of the tree stays collapsed; once everything expands it
		// just leaves spans unfetched (report mode renders once, with no lazy
		// expand) or costs a round-trip per expand. This mirrors IsExpanded's
		// own always-expand gate (dagql/dagui/types.go).
		full := tf == nil || opts.Debug || opts.ExpandCompleted ||
			opts.Verbosity >= dagui.ExpandCompletedVerbosity
		loader := newTraceLoader(ctx, client, traceID)

		if full {
			if err := client.FetchTrace(ctx, traceID, loader.sink()); err != nil {
				return noop, fmt.Errorf("fetch trace %s: %w", traceID, err)
			}
		} else {
			tf.SetLogProvider(func(id dagui.SpanID, descendants bool) {
				loader.fetchLogs(&logFg, id, descendants)
			})
			tf.SetSpanProvider(loader.listen)

			// Initial load: the trace's priority spans. For a small enough
			// trace the server returns the whole thing here; for a large one
			// it returns just the priority set and marks it partial, leaving
			// deeper spans to be fetched lazily on expand (or by --span
			// below).
			if err := loader.loadInitial(ctx); err != nil {
				return noop, fmt.Errorf("stream trace: %w", err)
			}
		}

		// --span/--check/--test: fetch the target span's subtree and zoom the
		// view to it, mirroring the web UI's ?span= deep link. --check/--test
		// resolve a name against the spans loaded so far -- checks and tests
		// are priority spans, so they're present without the whole trace.
		if tf != nil && sel.isSet() {
			id, descendants, err := resolveTraceTarget(tf, sel, traceID)
			if err != nil {
				return noop, err
			}
			loader.listen(id)
			// Request the zoom target's logs with the resolved roll-up
			// decision BEFORE zooming: setExpanded's lazy request would
			// otherwise latch a descendants=false fetch for a passing/non-leaf
			// test (losing the rolled-up subtree logs the zoomed report
			// renders), or skip a not-yet-loaded --span target entirely, with
			// no later request in report mode.
			tf.RequestZoomLogs(id, descendants)
			tf.ZoomToSpan(id)
		}

		// Fetch the subtrees of surfaced failed checks so their cause and
		// logs are loaded for the report's inline detail. A failed check's
		// cause is often a deep descendant the priority window doesn't
		// include (e.g. the withExec a check links to), so neither the
		// initial load nor the link CTE reaches it. Bounded to the failed
		// leaf checks -- unlike the web UI, which keeps fetching until the
		// whole trace is loaded.
		if tf != nil {
			loader.listenAll(tf.SurfacedFailedCheckSpans())
		}

		// Drain the span fetches (--span + failed-check subtrees) before
		// surfacing logs, so the newly-loaded cause spans are present when
		// the frontend picks its failures and requests their logs. A failed
		// backfill fails the command rather than rendering a silently
		// incomplete report.
		if err := loader.wait(); err != nil {
			return noop, fmt.Errorf("stream trace: %w", err)
		}

		// Now that the priority spans (and surfaced failures' subtrees) are
		// loaded, ask the frontend to surface its failures and request their
		// logs. This matters most for non-interactive 'report' mode, which
		// renders only once: we trigger the requests here, then drain them
		// below, so the single final render includes the failure detail.
		if tf != nil {
			tf.RequestSurfacedLogs()
		}

		// Drain the eager log fetches, so the final report isn't missing
		// detail it surfaced -- a failed fetch fails the command instead of
		// exiting 0 with the detail quietly absent. In interactive (-E) mode
		// further expands keep fetching on the outer ctx after this returns.
		if err := logFg.Wait(); err != nil {
			return noop, fmt.Errorf("stream trace: %w", err)
		}

		// Let the console block on in-flight lazy fetches so a single HTTP
		// request reflects a zoom/expand's results instead of returning
		// before the network round-trip lands. Errors are ignored here:
		// lazy-expand failures already warn, and only the pre-report drains
		// above turn them into a command failure.
		if tf != nil {
			tf.SetFetchWaiter(func() {
				_ = loader.wait()
				_ = logFg.Wait()
			})
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

// resolveTraceTarget turns a --span/--check/--test selection into the span to
// zoom to, plus whether the zoomed report rolls up its descendants' logs. A
// raw --span needs no lookup and stands alone (just that span, which may not
// be loaded yet); --check/--test resolve by name against the loaded trace and
// roll up their subtree.
func resolveTraceTarget(tf idtui.TraceFrontend, sel spanSelector, traceID string) (dagui.SpanID, bool, error) {
	if sel.span != "" {
		sid, err := trace.SpanIDFromHex(sel.span)
		if err != nil {
			return dagui.SpanID{}, false, fmt.Errorf("invalid span %q: %w", sel.span, err)
		}
		return dagui.SpanID{SpanID: sid}, false, nil
	}
	id, found := tf.ResolveSpanTarget(sel.check, sel.test)
	if !found {
		if sel.check != "" {
			return dagui.SpanID{}, false, fmt.Errorf("no check named %q in trace %s", sel.check, traceID)
		}
		return dagui.SpanID{}, false, fmt.Errorf("no test named %q in trace %s", sel.test, traceID)
	}
	return id, true, nil
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

// fetchGroup tracks in-flight background fetches. Unlike errgroup.Group /
// sync.WaitGroup it tolerates Go racing Wait -- the TUI event loop spawns
// fetches (expand, surfaced failures) while the run goroutine drains them,
// which for a WaitGroup is documented misuse (Add concurrent with Wait when
// the counter may be zero) -- and it collects every error rather than just
// the first. Context cancellation (the user interrupting) is not treated as
// a fetch failure.
type fetchGroup struct {
	mu   sync.Mutex
	n    int
	done chan struct{} // non-nil while n > 0; closed when n hits 0
	errs []error
}

func (g *fetchGroup) Go(f func() error) {
	g.mu.Lock()
	g.n++
	if g.done == nil {
		g.done = make(chan struct{})
	}
	g.mu.Unlock()
	go func() {
		err := f()
		g.mu.Lock()
		if err != nil && !errors.Is(err, context.Canceled) {
			g.errs = append(g.errs, err)
		}
		g.n--
		if g.n == 0 {
			close(g.done)
			g.done = nil
		}
		g.mu.Unlock()
	}()
}

// Wait blocks until no fetches are in flight and returns the errors collected
// so far. Fetches started after Wait observes an idle group are not waited
// for; callers drain at known quiesce points.
func (g *fetchGroup) Wait() error {
	for {
		g.mu.Lock()
		if g.n == 0 {
			err := errors.Join(g.errs...)
			g.mu.Unlock()
			return err
		}
		done := g.done
		g.mu.Unlock()
		<-done
	}
}

// traceLoader fetches a trace's spans and logs from Dagger Cloud's OTLP
// stream endpoints, incrementally, mirroring the Cloud web UI: it streams the
// priority (root) spans first, then backfills a span's children on demand when
// the user expands it, and a span's logs when they're shown. Spans reach the
// frontend through a TraceImporter (which applies the "degrade, never panic"
// guards and seals whatever the capture left running), with the
// dagger.io/ui.* attributes Cloud's dagui view stamps on them carrying the
// child count and has-logs flag the lazy-expand affordance needs.
type traceLoader struct {
	ctx     context.Context
	client  *cloud.OTLPClient
	traceID string

	// importer folds spans and logs into the frontend's exporters. The
	// imported trace is the whole session, so its root stays a real root.
	importer *enginetel.TraceImporter

	mu sync.Mutex
	// filter holds the spans whose subtrees have been requested (or whose
	// children arrived with the initial load), so each is fetched once.
	filter map[dagui.SpanID]bool
	// spanUpdateTime is the newest Cloud-side update time seen, sent back as
	// the `before` bound of a backfill so it's bounded rather than live.
	spanUpdateTime *time.Time
	// partial is whether the initial load was priority-only, i.e. whether
	// there is anything left to backfill; when the whole trace came down
	// expanding is purely local.
	partial       bool
	initialLoaded bool
	pending       []dagui.SpanID
	primarySet    bool

	// logReq dedups per-span log fetches across the log provider and the
	// zoom request.
	logReq map[string]bool
	logSem chan struct{}

	// background backfills (lazy child loads) run on the command's ctx so
	// they keep working while the TUI is interactive (-E).
	sem chan struct{}
	fg  fetchGroup
}

func newTraceLoader(ctx context.Context, client *cloud.OTLPClient, traceID string) *traceLoader {
	importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans:   Frontend.SpanExporter(),
		Logs:    Frontend.LogExporter(),
		Metrics: Frontend.MetricExporter(),
	})
	// This trace is the whole session: its root is the primary span, not a
	// second root to render through.
	importer.KeepRoots = true
	return &traceLoader{
		ctx:      ctx,
		client:   client,
		traceID:  traceID,
		importer: importer,
		filter:   map[dagui.SpanID]bool{{}: true}, // subscribe to roots first
		logReq:   map[string]bool{},
		logSem:   make(chan struct{}, 8),
		sem:      make(chan struct{}, 8),
	}
}

// sink is the loader as a whole-trace import sink, for the full fetch: spans
// pass through ingest (which zooms to the root as it arrives), and logs,
// metrics and the seal go straight to the importer.
func (l *traceLoader) sink() cloud.TraceImportSink {
	return loaderSink{l}
}

type loaderSink struct{ *traceLoader }

func (s loaderSink) ImportSpans(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) error {
	return s.ingest(ctx, req)
}

func (s loaderSink) ImportLogs(ctx context.Context, req *collogspb.ExportLogsServiceRequest) error {
	return s.importer.ImportLogs(ctx, req)
}

func (s loaderSink) ImportMetrics(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	return s.importer.ImportMetrics(ctx, req)
}

func (s loaderSink) Seal(ctx context.Context) error {
	return s.importer.Seal(ctx)
}

// loadInitial streams the trace's priority (root) spans and blocks until the
// stream completes. For a completed trace this returns once everything the
// server sends for the priority set is in; deeper spans (if the trace is
// marked partial) are fetched lazily afterward.
func (l *traceLoader) loadInitial(ctx context.Context) error {
	if err := l.client.FetchSpans(ctx, l.traceID, cloud.SpanSelection{
		Incremental: true,
		DagUIView:   true,
	}, l.ingest); err != nil {
		return err
	}
	// The stream ended, so the trace is over: whatever it shows still
	// running never ended.
	if err := l.importer.Seal(ctx); err != nil {
		return fmt.Errorf("seal trace: %w", err)
	}
	// Partial is now known; fire the listens that arrived mid-load.
	l.mu.Lock()
	l.initialLoaded = true
	pending := l.pending
	l.pending = nil
	l.mu.Unlock()
	l.listenAll(pending)
	return nil
}

// listen fetches a span's children on demand, mirroring the web UI's "listen"
// message. It's registered as the frontend's span provider and fired when a
// span is expanded (or zoomed via --span). When the tree is partial it
// backfills the span's historical children (root:false, before the last
// update we saw); when the whole trace is already loaded, expanding is purely
// local and this is a no-op.
func (l *traceLoader) listen(id dagui.SpanID) {
	l.listenAll([]dagui.SpanID{id})
}

// listenAll backfills several spans' subtrees through one request -- the
// server's listen argument takes a list, so a batch (e.g. the
// surfaced-failure prefetch: each failed check plus its error origins and
// links) costs one round trip instead of one per span.
func (l *traceLoader) listenAll(ids []dagui.SpanID) {
	l.mu.Lock()
	fetch := make([]string, 0, len(ids))
	for _, id := range ids {
		if !id.IsValid() || l.filter[id] {
			continue
		}
		if !l.initialLoaded {
			// Whether the tree is partial isn't known until the initial
			// stream completes. Deciding "fully loaded, no-op" now would
			// permanently swallow an expand racing the load (the id latches
			// in l.filter), so defer it; loadInitial replays pending listens
			// once partial is known.
			l.pending = append(l.pending, id)
			continue
		}
		l.filter[id] = true
		fetch = append(fetch, id.String())
	}
	partial := l.partial
	before := l.spanUpdateTime
	l.mu.Unlock()

	if !partial || len(fetch) == 0 {
		return
	}

	l.fg.Go(func() error {
		l.sem <- struct{}{}
		defer func() { <-l.sem }()
		if err := l.client.FetchSpans(l.ctx, l.traceID, cloud.SpanSelection{
			NoRoot:      true,
			Listen:      fetch,
			Incremental: true,
			Before:      before,
			DagUIView:   true,
		}, l.ingest); err != nil {
			// Warn for interactive mode, where post-drain lazy expands have
			// no one waiting on the error; the pre-report drain also
			// collects it.
			slog.Warn("error backfilling span children", "spans", strings.Join(fetch, ","), "err", err)
			return fmt.Errorf("backfill %d span(s): %w", len(fetch), err)
		}
		// The trace is over (the initial load ended); a backfilled span the
		// capture shows running never ended either.
		if err := l.importer.Seal(l.ctx); err != nil {
			return fmt.Errorf("seal backfilled span(s): %w", err)
		}
		return nil
	})
}

// fetchLogs fetches one span's logs, once, on the given fetch group. It's the
// frontend's log provider: fired lazily when the user expands a span, and
// eagerly for the failed spans it surfaces. descendants mirrors the span's
// RollUpLogs -- a check or test whose real output lives in a sub-operation
// rolls that up; everything else shows just its own logs.
func (l *traceLoader) fetchLogs(fg *fetchGroup, id dagui.SpanID, descendants bool) {
	spanHex := id.String()
	l.mu.Lock()
	if !id.IsValid() || l.logReq[spanHex] {
		l.mu.Unlock()
		return
	}
	l.logReq[spanHex] = true
	l.mu.Unlock()
	fg.Go(func() error {
		l.logSem <- struct{}{}
		defer func() { <-l.logSem }()
		sel := cloud.LogSelection{SpanID: spanHex, Descendants: descendants}
		if descendants {
			// A rolled-up span's descendants aren't loaded (the priority
			// window doesn't include them), so their records would route to
			// orphan buffers nothing renders. Ask for text output only --
			// the semantic records riding the log channel (span names,
			// progress, agent state) are per-span facts that must not be
			// re-attributed -- and attribute it to the span we fetched it
			// for, like the summary's flat roll-up, so e.g. a failed test
			// shows its sub-operation's output.
			sel.Records = cloud.LogRecordsLogs
		}
		if err := l.client.FetchLogs(l.ctx, l.traceID, sel, func(ctx context.Context, req *collogspb.ExportLogsServiceRequest) error {
			if descendants {
				rekeyLogRecords(req, id)
			}
			return l.importer.ImportLogs(ctx, req)
		}); err != nil {
			// Warn for interactive mode, where post-drain lazy expands have
			// no one waiting on the error; the pre-report drain also
			// collects it, failing the command rather than rendering a
			// silently incomplete report.
			slog.Warn("error streaming span logs", "span", spanHex, "err", err)
			return fmt.Errorf("stream span %s logs: %w", spanHex, err)
		}
		if !descendants {
			return nil
		}
		// The text-only class above left out the subtree's call payloads,
		// which the loaded spans beneath the roll-up (its listened children)
		// need to render their calls. Payloads are keyed by digest, not
		// span, so they land as-is: no re-keying, no orphaning.
		if err := l.client.FetchLogs(l.ctx, l.traceID, cloud.LogSelection{
			SpanID:      spanHex,
			Descendants: true,
			Records:     cloud.LogRecordsCallPayloads,
		}, l.importer.ImportLogs); err != nil {
			slog.Warn("error streaming span call payloads", "span", spanHex, "err", err)
			return fmt.Errorf("stream span %s call payloads: %w", spanHex, err)
		}
		return nil
	})
}

// rekeyLogRecords attributes every record in req to id.
func rekeyLogRecords(req *collogspb.ExportLogsServiceRequest, id dagui.SpanID) {
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, record := range sl.GetLogRecords() {
				record.SpanId = id.SpanID[:]
			}
		}
	}
}

// wait blocks for the in-flight backfills to finish. Used by report mode to
// ensure --span / surfaced-failure fetches land before the single final
// render.
func (l *traceLoader) wait() error {
	return l.fg.Wait()
}

// ingest folds a batch of spans into the frontend and updates the loader's
// incremental-fetch bookkeeping from the dagger.io/ui.* attributes Cloud's
// dagui view stamps on them (partial flag, latest update time), zooming the
// frontend to the root as soon as it arrives so the interactive view is
// scoped while the rest of a large trace is still streaming. The first
// parentless span is the root; a capture can hold more than one (a trace
// whose real root never reached Cloud).
func (l *traceLoader) ingest(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) error {
	l.mu.Lock()
	var primary dagui.SpanID
	for _, rs := range req.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				partial, updated := traceViewAttrs(span)
				if partial {
					l.partial = true
				}
				if updated > 0 {
					t := time.Unix(0, updated)
					if l.spanUpdateTime == nil || t.After(*l.spanUpdateTime) {
						l.spanUpdateTime = &t
					}
				}
				if len(span.GetParentSpanId()) == 0 && !l.primarySet {
					var id dagui.SpanID
					copy(id.SpanID[:], span.GetSpanId())
					if id.IsValid() {
						primary = id
						l.primarySet = true
						l.filter[id] = true
					}
				}
			}
		}
	}
	l.mu.Unlock()

	if primary.IsValid() {
		Frontend.SetPrimary(primary)
	}
	return l.importer.ImportSpans(ctx, req)
}

// traceViewAttrs reads the loader's bookkeeping off a span from Cloud's dagui
// view: whether it came from a partial (priority-only) selection, and its
// Cloud-side update time in Unix nanoseconds (0 when absent).
func traceViewAttrs(span *tracepb.Span) (partial bool, updatedUnixNano int64) {
	for _, attr := range span.GetAttributes() {
		switch attr.GetKey() {
		case telemetryattrs.UIPartialAttr:
			partial = attr.GetValue().GetBoolValue()
		case telemetryattrs.UIUpdateTimeUnixNanoAttr:
			updatedUnixNano = anyValueInt(attr.GetValue())
		}
	}
	return partial, updatedUnixNano
}

// anyValueInt reads an integer attribute, tolerating the encodings a
// timestamp survives a round trip in.
func anyValueInt(v *commonpb.AnyValue) int64 {
	switch val := v.GetValue().(type) {
	case *commonpb.AnyValue_IntValue:
		return val.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return int64(val.DoubleValue)
	case *commonpb.AnyValue_StringValue:
		n, _ := strconv.ParseInt(val.StringValue, 10, 64)
		return n
	}
	return 0
}
