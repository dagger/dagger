package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
	cloud "github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/codes"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
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
}

func traceRun(cmd *cobra.Command, args []string) error {
	traceID := args[0]

	sel := spanSelector{span: traceSpan, check: traceCheck, test: traceTest}
	if err := sel.validate(); err != nil {
		return err
	}

	// statsClient hands the Cloud client to the --debug stats read below. The
	// run closure executes on the frontend's goroutine, which a force-quit
	// abandons without joining, so a plain shared variable would race.
	var statsClient atomic.Pointer[cloud.Client]
	runErr := Frontend.Run(cmd.Context(), opts, func(ctx context.Context) (cleanups.CleanupF, error) {
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
		statsClient.Store(client)

		// Resolve org ID: --org flag > current org
		orgID, err := resolveOrgID(ctx, client, cloudAuth)
		if err != nil {
			return nil, err
		}

		// Let the frontend point surfaced failure logs at 'dagger cloud logs
		// <trace> <span>' for the full, untruncated output.
		if t, ok := Frontend.(interface{ SetTraceID(string) }); ok {
			t.SetTraceID(traceID)
		}

		// Fetch the trace's source commit / CI change so the report can suggest
		// commit-scoped re-run commands. Best-effort: a missing metadata query just
		// means the report falls back to a local 'dagger check' suggestion.
		setTraceCIContext(ctx, client, orgID, traceID)

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
			logFg    fetchGroup
		)
		fetchSpanLogs := func(spanHex string, descendants bool) {
			logReqMu.Lock()
			if spanHex == "" || logReq[spanHex] {
				logReqMu.Unlock()
				return
			}
			logReq[spanHex] = true
			logReqMu.Unlock()
			logFg.Go(func() error {
				logSem <- struct{}{}
				defer func() { <-logSem }()
				if err := client.StreamLogs(ctx, orgID, traceID, spanHex, descendants, func(logs []cloud.LogMessage) {
					if descendants {
						// Incremental --full only loads priority spans, so a rolled-up
						// span's descendants aren't in the frontend's DB -- their log
						// records would route to orphan buffers nothing renders. Attribute
						// them to the span we fetched them for, like the summary's flat
						// roll-up, so e.g. a failed test shows its sub-operation's output.
						for i := range logs {
							id := spanHex
							logs[i].SpanID = &id
						}
					}
					records := cloud.LogMessagesToRecords(traceID, logs)
					if len(records) == 0 {
						return
					}
					if err := logExp.Export(ctx, records); err != nil {
						slog.Warn("error exporting logs", "err", err)
					}
				}); err != nil {
					// Warn for interactive mode, where post-drain lazy expands have no
					// one waiting on the error; the pre-report drain below also
					// collects it, failing the command rather than rendering a
					// silently incomplete report.
					slog.Warn("error streaming span logs", "span", spanHex, "err", err)
					return fmt.Errorf("stream span %s logs: %w", spanHex, err)
				}
				return nil
			})
		}

		// Let the TUI request a span's logs on demand (expand / surfaced failure).
		lazyLogs := false
		if lp, ok := Frontend.(interface {
			SetLogProvider(func(dagui.SpanID, bool))
		}); ok {
			lazyLogs = true
			lp.SetLogProvider(func(id dagui.SpanID, descendants bool) {
				fetchSpanLogs(id.String(), descendants)
			})
		}

		// Fetch spans incrementally, mirroring the Cloud web UI
		// (cloud/app_server.go): stream the priority (root) spans first, then
		// fetch a span's children on demand when the user expands it. The loader
		// uses the outer ctx so lazy expands keep working while the TUI is
		// interactive (-E).
		loader := newTraceLoader(ctx, client, orgID, traceID)
		if sp, ok := Frontend.(interface {
			SetSpanProvider(func(dagui.SpanID))
		}); ok {
			sp.SetSpanProvider(loader.listen)
		}

		// Initial load: the trace's priority spans. For a small enough trace the
		// server returns the whole thing here; for a large one it returns just the
		// priority set and marks it Partial, leaving deeper spans to be fetched
		// lazily on expand (or by --span below).
		if err := loader.loadInitial(ctx); err != nil {
			return noop, fmt.Errorf("stream trace: %w", err)
		}

		if !lazyLogs {
			// No lazy log provider (the plain/dots/logs frontends render from a
			// log stream, not on-demand expands): stream the whole trace's logs
			// up front, preserving the prior behavior.
			loader.streamAllLogs(&logFg, logExp)
		}

		// --span/--check/--test: fetch the target span's subtree and zoom the view
		// to it, mirroring the web UI's ?span= deep link. --check/--test resolve a
		// name against the priority spans just loaded.
		if err := loader.zoomToSelection(ctx, sel); err != nil {
			return noop, err
		}

		// Fetch the subtrees of surfaced failed checks so their cause and logs are
		// loaded for the report's inline detail. A failed check's cause is often a
		// deep descendant the priority window doesn't include (e.g. the withExec a
		// check links to), so neither loadInitial nor the link CTE reaches it.
		// Bounded to the failed leaf checks -- unlike the web UI, which keeps
		// fetching until the whole trace is loaded.
		if sf, ok := Frontend.(interface {
			SurfacedFailedCheckSpans() []dagui.SpanID
		}); ok {
			for _, id := range sf.SurfacedFailedCheckSpans() {
				loader.listen(id)
			}
		}

		// Drain the span fetches (--span + failed-check subtrees) before surfacing
		// logs, so the newly-loaded cause spans are present when the frontend picks
		// its failures and requests their logs. A failed backfill fails the
		// command rather than rendering a silently incomplete report.
		if err := loader.wait(); err != nil {
			return noop, fmt.Errorf("stream trace: %w", err)
		}

		// Now that the priority spans (and surfaced failures' subtrees) are loaded,
		// ask the frontend to surface its failures and request their logs. This
		// matters most for non-interactive 'report' mode, which renders only once:
		// we trigger the requests here, then drain them below, so the single final
		// render includes the failure detail.
		if r, ok := Frontend.(interface{ RequestSurfacedLogs() }); ok {
			r.RequestSurfacedLogs()
		}

		// Drain the eager log fetches, so the final report isn't missing detail it
		// surfaced -- a failed fetch fails the command instead of exiting 0 with
		// the detail quietly absent. In interactive (-E) mode further expands
		// keep fetching on the outer ctx after this returns.
		if err := logFg.Wait(); err != nil {
			return noop, fmt.Errorf("stream trace: %w", err)
		}

		// Let the console block on in-flight lazy fetches so a single HTTP
		// request reflects a zoom/expand's results instead of returning before
		// the network round-trip lands. Errors are ignored here: lazy-expand
		// failures already warn, and only the pre-report drains above turn them
		// into a command failure.
		if fw, ok := Frontend.(interface{ SetFetchWaiter(func()) }); ok {
			fw.SetFetchWaiter(func() {
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

// setTraceCIContext fetches the trace's source commit / CI change and feeds it
// to the frontend so the report can suggest commit-scoped re-run commands.
// Best-effort: a frontend that doesn't accept CI context, or a failed/empty
// metadata query, just means the report falls back to a local 'dagger check'
// suggestion.
func setTraceCIContext(ctx context.Context, client *cloud.Client, orgID, traceID string) {
	t, ok := Frontend.(interface {
		SetCIContext(commit, prNumber string, isNativeCI bool)
	})
	if !ok {
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
	var commit, prNumber string
	var isNativeCI bool
	if meta.Git != nil {
		commit = meta.Git.Ref
	}
	if meta.CI != nil {
		isNativeCI = meta.CI.IsNativeCI
		if meta.CI.Change != nil {
			prNumber = meta.CI.Change.ID
			if commit == "" {
				commit = meta.CI.Change.HeadSHA
			}
		}
	}
	t.SetCIContext(commit, prNumber, isNativeCI)
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

// traceLoader fetches a trace's spans incrementally from Dagger Cloud, mirroring
// the Cloud web UI's wsHandler (cloud/app_server.go). It streams the priority
// (root) spans first, then backfills a span's children on demand when the user
// expands it. Spans reach the frontend as snapshots (which carry ChildCount and
// Partial -- the data the lazy-expand affordance needs -- unlike the OTLP form),
// falling back to the OTLP span exporter for frontends that can't import
// snapshots.
type traceLoader struct {
	ctx            context.Context
	client         *cloud.Client
	orgID, traceID string

	// importer is the snapshot sink when the frontend supports it (the pretty
	// TUI); otherwise spanExp receives spans as OTLP, preserving the prior
	// behavior for the plain/dots/logs frontends (which don't lazily expand).
	importer interface {
		ImportSnapshots([]dagui.SpanSnapshot)
	}
	spanExp sdktrace.SpanExporter

	mu             sync.Mutex
	filter         map[dagui.SpanID]bool
	spanUpdateTime *time.Time
	partial        bool
	initialLoaded  bool
	pending        []dagui.SpanID
	primarySet     bool
	primaryHex     string

	// background backfills (lazy child loads) run on the command's ctx so they
	// keep working while the TUI is interactive (-E).
	sem chan struct{}
	fg  fetchGroup
}

func newTraceLoader(ctx context.Context, client *cloud.Client, orgID, traceID string) *traceLoader {
	l := &traceLoader{
		ctx:     ctx,
		client:  client,
		orgID:   orgID,
		traceID: traceID,
		filter:  map[dagui.SpanID]bool{{}: true}, // subscribe to roots first
		sem:     make(chan struct{}, 8),
	}
	if imp, ok := Frontend.(interface {
		ImportSnapshots([]dagui.SpanSnapshot)
	}); ok {
		l.importer = imp
	} else {
		l.spanExp = Frontend.SpanExporter()
	}
	return l
}

// loadInitial streams the trace's priority (root) spans and blocks until the
// stream completes. For a completed trace this returns once everything the
// server sends for the priority set is in; deeper spans (if the trace is marked
// Partial) are fetched lazily afterward.
func (l *traceLoader) loadInitial(ctx context.Context) error {
	l.mu.Lock()
	listen := l.listenIDsLocked()
	l.mu.Unlock()
	if err := l.client.StreamSpansWith(ctx, l.orgID, l.traceID, cloud.SpanStreamOpts{
		Root:        true,
		Listen:      listen,
		Incremental: true,
	}, l.ingest); err != nil {
		return err
	}
	// Partial is now known; fire the listens that arrived mid-load.
	l.mu.Lock()
	l.initialLoaded = true
	pending := l.pending
	l.pending = nil
	l.mu.Unlock()
	for _, id := range pending {
		l.listen(id)
	}
	return nil
}

// listen fetches a span's children on demand, mirroring the web UI's "listen"
// message. It's registered as the frontend's span provider and fired when a span
// is expanded (or zoomed via --span). When the tree is partial it backfills the
// span's historical children (root:false, before the last update we saw); when
// the whole trace is already loaded, expanding is purely local and this is a
// no-op.
func (l *traceLoader) listen(id dagui.SpanID) {
	if !id.IsValid() {
		return
	}
	l.mu.Lock()
	if l.filter[id] {
		l.mu.Unlock()
		return
	}
	if !l.initialLoaded {
		// Whether the tree is partial isn't known until the initial stream
		// completes. Deciding "fully loaded, no-op" now would permanently
		// swallow an expand racing the load (the id latches in l.filter), so
		// defer it; loadInitial replays pending listens once partial is known.
		l.pending = append(l.pending, id)
		l.mu.Unlock()
		return
	}
	l.filter[id] = true
	partial := l.partial
	before := l.spanUpdateTime
	l.mu.Unlock()

	if !partial {
		return
	}

	l.fg.Go(func() error {
		l.sem <- struct{}{}
		defer func() { <-l.sem }()
		if err := l.client.StreamSpansWith(l.ctx, l.orgID, l.traceID, cloud.SpanStreamOpts{
			Root:        false,
			Before:      before,
			Listen:      []string{id.String()},
			Incremental: true,
		}, l.ingest); err != nil {
			// Warn for interactive mode, where post-drain lazy expands have no
			// one waiting on the error; the pre-report drain also collects it.
			slog.Warn("error backfilling span children", "span", id.String(), "err", err)
			return fmt.Errorf("backfill span %s: %w", id.String(), err)
		}
		return nil
	})
}

// zoomToSelection resolves a --span/--check/--test selection, fetches the
// target's subtree, and zooms the view to it, mirroring the web UI's ?span=
// deep link. It's a no-op when no selection is set.
func (l *traceLoader) zoomToSelection(ctx context.Context, sel spanSelector) error {
	if !sel.isSet() {
		return nil
	}
	var spanID dagui.SpanID
	var descendants bool
	// Resolve --check/--test against the frontend's own loaded view first: it
	// applies the same selection rules the report renders with (boundary
	// containment, failed representative), so the drill-in commands the
	// report suggests land on the span it described -- and it skips
	// re-fetching the priority spans the loader just streamed. Fall back to
	// the raw priority-span match for names the surfaced view hides (e.g.
	// boundary-contained fixtures) and for frontends without a view.
	if sel.span == "" {
		if r, ok := Frontend.(interface {
			ResolveSpanTarget(check, test string) (dagui.SpanID, bool)
		}); ok {
			if id, found := r.ResolveSpanTarget(sel.check, sel.test); found {
				spanID = id
				descendants = true // --check/--test roll up their subtree
			}
		}
	}
	if !spanID.IsValid() {
		spanHex, desc, err := sel.resolveSpan(ctx, l.client, l.orgID, l.traceID)
		if err != nil {
			return err
		}
		sid, err := trace.SpanIDFromHex(spanHex)
		if err != nil {
			return fmt.Errorf("invalid span %q: %w", spanHex, err)
		}
		spanID = dagui.SpanID{SpanID: sid}
		descendants = desc
	}
	l.listen(spanID)
	if z, ok := Frontend.(interface {
		ZoomToSpan(dagui.SpanID)
		RequestZoomLogs(dagui.SpanID, bool)
	}); ok {
		// Request the zoom target's logs with the resolved roll-up decision
		// BEFORE zooming: setExpanded's lazy request would otherwise latch a
		// descendants=false fetch for a passing/non-leaf test (losing the
		// rolled-up subtree logs the zoomed report renders), or skip a
		// not-yet-loaded --span target entirely, with no later request in
		// report mode.
		z.RequestZoomLogs(spanID, descendants)
		z.ZoomToSpan(spanID)
	}
	return nil
}

// wait blocks for the in-flight backfills to finish. Used by report mode to
// ensure --span / surfaced-failure fetches land before the single final render.
func (l *traceLoader) wait() error {
	return l.fg.Wait()
}

// primarySpanHex returns the trace's root (parentless) span hex, once ingested.
func (l *traceLoader) primarySpanHex() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.primaryHex
}

// streamAllLogs streams the whole trace's logs (the root span's subtree) into
// exp on the given fetch group. Used for frontends without a lazy log
// provider (plain/dots/logs), which render from a log stream rather than
// on-demand expands. True span IDs are kept -- no re-key -- since these
// frontends attribute output per span.
func (l *traceLoader) streamAllLogs(fg *fetchGroup, exp sdklog.Exporter) {
	rootHex := l.primarySpanHex()
	if rootHex == "" {
		return
	}
	fg.Go(func() error {
		if err := l.client.StreamLogs(l.ctx, l.orgID, l.traceID, rootHex, true, func(logs []cloud.LogMessage) {
			records := cloud.LogMessagesToRecords(l.traceID, logs)
			if len(records) == 0 {
				return
			}
			if err := exp.Export(l.ctx, records); err != nil {
				slog.Warn("error exporting logs", "err", err)
			}
		}); err != nil {
			return fmt.Errorf("stream trace logs: %w", err)
		}
		return nil
	})
}

// ingest folds a batch of spans into the frontend and updates the loader's
// incremental-fetch bookkeeping (Partial flag, latest update time, primary
// span), mirroring wsHandler.listenForSpanUpdates.
func (l *traceLoader) ingest(spans []cloud.SpanData) {
	if len(spans) == 0 {
		return
	}

	l.mu.Lock()
	var primary dagui.SpanID
	for i := range spans {
		s := &spans[i]
		if s.Partial {
			l.partial = true
		}
		if l.spanUpdateTime == nil || s.UpdateTime.After(*l.spanUpdateTime) {
			t := s.UpdateTime
			l.spanUpdateTime = &t
		}
		if s.ParentID == nil && !l.primarySet {
			if sid, err := trace.SpanIDFromHex(s.ID); err == nil {
				primary = dagui.SpanID{SpanID: sid}
				l.primarySet = true
				l.primaryHex = s.ID
				l.filter[primary] = true
			}
		}
	}
	l.mu.Unlock()

	if primary.IsValid() {
		Frontend.SetPrimary(primary)
	}

	if l.importer != nil {
		snaps := make([]dagui.SpanSnapshot, 0, len(spans))
		for i := range spans {
			snaps = append(snaps, spanDataToSnapshot(spans[i]))
		}
		l.importer.ImportSnapshots(snaps)
		return
	}

	// Fallback for frontends that can't import snapshots: feed OTLP. ChildCount
	// is lost (so lazy expand can't surface unloaded children), but these
	// frontends don't expand interactively anyway.
	if l.spanExp != nil {
		otel := telemetry.SpansFromPB(cloud.SpansToPB(spans))
		if len(otel) > 0 {
			if err := l.spanExp.ExportSpans(l.ctx, otel); err != nil {
				slog.Warn("error exporting spans", "err", err)
			}
		}
	}
}

func (l *traceLoader) listenIDsLocked() []string {
	ids := make([]string, 0, len(l.filter))
	for id := range l.filter {
		if id.IsValid() {
			ids = append(ids, id.String())
		}
	}
	return ids
}

// spanDataToSnapshot converts a Cloud API span into a dagui snapshot. It mirrors
// snapshotAPISpan in cloud/app_server.go: the snapshot carries ChildCount and
// (via ProcessAttribute) the call payload, so the call tree and lazy-expand
// affordance render without a separate calls sync.
func spanDataToSnapshot(s cloud.SpanData) dagui.SpanSnapshot {
	var snapshot dagui.SpanSnapshot
	snapshot.ID.SpanID, _ = trace.SpanIDFromHex(s.ID)
	snapshot.TraceID.TraceID, _ = trace.TraceIDFromHex(s.TraceID)
	snapshot.Name = s.Name
	if s.ParentID != nil {
		snapshot.ParentID.SpanID, _ = trace.SpanIDFromHex(*s.ParentID)
	}
	snapshot.StartTime = s.Timestamp
	if s.EndTime != nil {
		snapshot.EndTime = *s.EndTime
	}
	switch tracepb.Status_StatusCode(tracepb.Status_StatusCode_value[s.Status.Code]) {
	case tracepb.Status_STATUS_CODE_OK:
		snapshot.Status.Code = codes.Ok
	case tracepb.Status_STATUS_CODE_ERROR:
		snapshot.Status.Code = codes.Error
	default:
		snapshot.Status.Code = codes.Unset
	}
	snapshot.Status.Description = s.Status.Message
	snapshot.Links = make([]dagui.SpanLink, len(s.Links))
	for i, link := range s.Links {
		snapshot.Links[i].SpanContext.TraceID.TraceID, _ = trace.TraceIDFromHex(link.TraceID)
		snapshot.Links[i].SpanContext.SpanID.SpanID, _ = trace.SpanIDFromHex(link.SpanID)
		if purpose, ok := link.Attributes[telemetry.LinkPurposeAttr].(string); ok {
			snapshot.Links[i].Purpose = purpose
		}
	}
	snapshot.HasLogs = s.HasLogs
	for k, v := range s.Attributes {
		snapshot.ProcessAttribute(k, v)
	}
	snapshot.ChildCount = s.ChildCount
	return snapshot
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
