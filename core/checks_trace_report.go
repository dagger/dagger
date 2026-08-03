package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	telemetry "github.com/dagger/otel-go"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/clientdb"
)

// traceReportBatchSize is how many telemetry rows we page in at a time when
// rebuilding the trace for rendering.
const traceReportBatchSize = 1000

// traceReportMaxLineLen is the per-line byte clamp for a rendered report.
//
// Same value (and same reasoning) as llmLogsMaxLineLen in core/mcp.go: a
// report can be fed to an LLM as a tool result, and single monster lines --
// a module trace's verbatim `schema(json: "…")` argument, or a `.contents:
// JSON!` result -- carry almost no signal past the first couple thousand
// bytes while eating the whole budget.
const traceReportMaxLineLen = llmLogsMaxLineLen

// traceReportMaxBytes is the total byte budget for a rendered report.
//
// 16 KiB is roughly 4k tokens: big enough that every report we've measured in
// practice (59 B for a single check, ~2 KB for a scoped multi-check run) is
// passed through untouched, and small enough that a pathological
// module-heavy trace (measured: 930 KB) can't blow up a tool result or crowd
// out the rest of an agent's context.
const traceReportMaxBytes = 16 * 1024

// traceReportHeadBytes is how much of traceReportMaxBytes is reserved for
// the head when a report has to be truncated. The pretty final report leads
// with the span tree and closes with the summary sections (CHECKS/TESTS
// counts, RUN LOCALLY), so both ends carry signal -- but the tree is the
// bulkier, more diagnostic half, so it gets two thirds.
const traceReportHeadBytes = traceReportMaxBytes * 2 / 3

// traceReportMaxCachedSessions bounds how many session DBs we retain.
//
// dagui.DB has no eviction API (GCThreshold is a display filter only, see
// dagql/dagui/opts.go), so a cached entry retains every span and log line of
// its session for as long as it's cached -- that's the price of incremental
// loading. We cap the number of live sessions instead: evicting an entry is
// always safe, it just means the next render for that session pays for a
// full rebuild.
const traceReportMaxCachedSessions = 8

// traceReporter is the slice of idtui's report-only frontend that we drive.
// idtui.NewASCIIReporterWithDB returns an unexported type, so name the
// behavior instead.
type traceReporter interface {
	LogExporter() sdklog.Exporter
	FinalRender(io.Writer) error
	SetReportRenderOpts(idtui.ReportRenderOpts)
}

// traceReportOpts tunes a single scoped render. The zero value shows
// completed spans expanded, prunes nothing, and leaves log lines unbounded.
type traceReportOpts struct {
	// ExpandAll unwraps the scoped subtree just far enough for the tool's own
	// output to reach the reader: the scope root and the pure wrapper spans
	// beneath it are force-expanded, stopping at the first real work span. See
	// expandedSpans, and idtui.ReportRenderOpts.ExpandSpans for why this isn't
	// done by raising verbosity to ExpandCompletedVerbosity.
	ExpandAll bool

	// HideNoise prunes the spans a tool result has no use for: a started
	// service's long-lived exec span (and the API span its log stream is
	// routed to), which streams unbounded noise into the subtree via cause
	// links, and the LLM message spans of the conversation the tool call is
	// part of. The flat capture path excludes both for the same reasons.
	HideNoise bool

	// Scoped marks the report as being about root's subtree specifically,
	// rather than about the run as a whole. Two things still depend on it,
	// both about the *frame* of the report rather than about which spans it
	// rolls up:
	//
	//  1. The TRACE verdict header is dropped: "did the RUN pass" is the
	//     enclosing run's verdict, not the subtree's, and it is rendered from
	//     db.RootSpan regardless of zoom.
	//  2. The live-tree promotions (promoteChecks/Conversation/Generators) are
	//     skipped: they MUTATE the cached, session-wide DB -- marking the host
	//     Passthrough and wiring RevealedSpans -- which would both replace the
	//     subtree's own rows and leave that reshaping behind for every later
	//     render of the same DB.
	//
	// Everything the surfacing sections themselves show (CHECKS, CONVERSATION,
	// SERVICES, GENERATORS) is now rolled up relative to the zoomed span, so
	// none of them need this flag any more.
	Scoped bool

	// NestedLogLines bounds the log lines rendered per nested row; 0 leaves
	// them unbounded.
	NestedLogLines int

	// OwnOutputOnly narrows what counts as the target's own output to the
	// records on the root span itself, excluding its direct children.
	//
	// A tool call needs those children: a module function's print lands on
	// its dagql field-call span, one hop below the tool-call span, so the
	// depth-1 rule is what makes a tool's own report survive. An explicitly
	// named target has no such indirection, and the children ARE the nested
	// work -- for a test suite they're its cases, whose logs belong to the
	// TESTS roll-up, not hoisted into (and duplicated out of) OUTPUT.
	OwnOutputOnly bool

	// HideLogSpans names spans (hex IDs) whose own logs the report must NOT
	// render inline, because the caller prints them itself, verbatim and
	// unabridged, as its own section. Without it the two would duplicate each
	// other; see MCP.spanResult.
	HideLogSpans map[string]bool

	// SuggestReadTrace re-points the report's rerun section at the ReadTrace
	// builtin instead of the `dagger check "<name>"` CLI commands. Set it on
	// every render whose reader is an LLM: an agent has tools, not a shell, so
	// a copy-paste command is noise to it -- while `ReadTrace(check: "…")` is
	// something it can actually call to see the full detail behind an abridged
	// result. Interactive CLI rendering never sets this.
	SuggestReadTrace bool
}

// readTraceRerunSuggestion is the LLM-facing replacement for the report's
// "RUN LOCALLY" section: the same failed check names, expressed as ReadTrace
// tool calls.
//
// Note this lives in core, not in dagql/idtui: ReadTrace is a core builtin
// tool (see MCP.loadBuiltins), so core legitimately owns the vocabulary, while
// the frontend keeps owning the layout.
func readTraceRerunSuggestion(checkNames []string) (string, []string) {
	body := make([]string, 0, len(checkNames))
	for _, name := range checkNames {
		body = append(body, fmt.Sprintf("ReadTrace(check: %q)", name))
	}
	return "SEE FULL TRACE", body
}

// traceReportKey identifies the session whose telemetry a cached DB holds.
type traceReportKey struct {
	sessionID string
	clientID  string
}

// traceReportSession is one session's incrementally-loaded trace.
//
// The DB and the reporter are created together and always reused together:
// feeding db.LogExporter() alone does *not* produce the rendered ┃ log lines
// -- those come from the frontend's own log buffers -- so the logs have to
// flow into this exact reporter, and the reporter has to be the one that
// renders.
type traceReportSession struct {
	db       *dagui.DB
	reporter traceReporter

	// spanCursor/logCursor are the highest clientdb row IDs already exported.
	// clientdb rows are append-only with monotonic IDs (the engine's SSE
	// subscribe handlers rely on the same property), so everything new has a
	// strictly greater ID.
	spanCursor int64
	logCursor  int64

	// defaultPrimary is the primary span dagui.DB picks on its own (the root
	// span) before any explicit scoping. Restored before each load so a
	// previous render's SetPrimarySpan doesn't change how new rows are
	// ingested.
	defaultPrimary dagui.SpanID
}

// traceReportCacheState is the process-wide cache of per-session trace DBs.
//
// dagui.DB has no internal locking, so *all* access -- exports and renders
// alike -- happens under mu. Renders are short (measured in single-digit ms
// for a scoped report) and per-session contention is nil in practice, since a
// session's tool calls are serialized anyway.
type traceReportCacheState struct {
	mu      sync.Mutex
	entries map[traceReportKey]*traceReportSession
	// lru is least-recently-used first.
	lru []traceReportKey
}

var traceReportCache = &traceReportCacheState{
	entries: map[traceReportKey]*traceReportSession{},
}

// get returns the cached session, creating it if needed, and marks it as most
// recently used (evicting the oldest entry when over capacity).
func (c *traceReportCacheState) get(key traceReportKey) *traceReportSession {
	entry, ok := c.entries[key]
	if !ok {
		db := dagui.NewDB()
		fe := idtui.NewASCIIReporterWithDB(io.Discard, db)
		// Render options are (re)applied per render, in render() -- a cached
		// reporter serves renders with different options.
		entry = &traceReportSession{db: db, reporter: fe}
		c.entries[key] = entry
	}
	c.touch(key)
	return entry
}

func (c *traceReportCacheState) touch(key traceReportKey) {
	for i, k := range c.lru {
		if k == key {
			c.lru = append(c.lru[:i], c.lru[i+1:]...)
			break
		}
	}
	c.lru = append(c.lru, key)
	for len(c.lru) > traceReportMaxCachedSessions {
		evict := c.lru[0]
		c.lru = c.lru[1:]
		delete(c.entries, evict)
	}
}

// renderTraceReport loads the session's telemetry from the client's telemetry
// store, scopes it to root (an empty root means the whole trace), and renders
// the pretty frontend's final report as plain text -- the same span tree,
// CHECKS and TESTS sections a user sees at the end of a CLI run.
//
// Loading is incremental: the session's DB (and its reporter) are cached, and
// each call only pages in the clientdb rows appended since the last one. A
// full rebuild is linear in session size (~20-25µs/span row, ~150µs/log row),
// so re-loading everything on every call would be quadratic over a session
// that keeps growing -- which is exactly what happens when a report is
// rendered per LLM tool call.
//
// The DB is loaded and only then rendered, all under the cache lock:
// dagui.DB has no internal locking, so it must not be written while the
// frontend reads it.
// The rendered report is NOT byte-guarded here: it is only ever half of what
// reaches the reader (the other half being the target's own printed output),
// and the budget has to bound the COMBINED text. guardTraceReport is applied
// by the caller that assembles the final result.
func renderTraceReport(ctx context.Context, root string, opts traceReportOpts) (string, error) {
	clientDB, key, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer clientDB.Close()

	return traceReportCache.render(ctx, key, clientDB, root, opts)
}

// traceReportClientDB opens the main client's telemetry store and returns it
// along with the cache key identifying its session. The caller closes it.
func traceReportClientDB(ctx context.Context) (*clientdb.DB, traceReportKey, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, traceReportKey{}, err
	}
	mainMeta, err := query.MainClientCallerMetadata(ctx)
	if err != nil {
		return nil, traceReportKey{}, fmt.Errorf("get main client caller metadata: %w", err)
	}
	// NB: this flushes all of the session's clients before returning, so
	// everything that has ended is visible below.
	clientDB, err := query.ClientTelemetry(ctx, mainMeta.SessionID, mainMeta.ClientID)
	if err != nil {
		return nil, traceReportKey{}, fmt.Errorf("get client telemetry: %w", err)
	}
	return clientDB, traceReportKey{sessionID: mainMeta.SessionID, clientID: mainMeta.ClientID}, nil
}

// withTraceReportDB brings the session's cached DB up to date and hands it to
// fn, under the cache lock (dagui.DB has no internal locking). fn must not
// retain the DB.
//
// This is the *same* cached DB the reports are rendered from -- there is
// deliberately only one trace-building code path -- so a lookup costs only the
// rows appended since the last render.
func withTraceReportDB(ctx context.Context, fn func(*dagui.DB) error) error {
	clientDB, key, err := traceReportClientDB(ctx)
	if err != nil {
		return err
	}
	defer clientDB.Close()

	traceReportCache.mu.Lock()
	defer traceReportCache.mu.Unlock()

	entry, err := traceReportCache.load(ctx, key, clientDB)
	if err != nil {
		return err
	}
	return fn(entry.db)
}

// load brings the cached session up to date with clientDB. Callers must hold
// c.mu.
func (c *traceReportCacheState) load(ctx context.Context, key traceReportKey, clientDB *clientdb.DB) (*traceReportSession, error) {
	entry := c.get(key)

	// Ingest as if nothing had been scoped yet, so the DB's own
	// "primary defaults to the root span" logic behaves the same as on a
	// fresh build.
	entry.db.PrimarySpan = entry.defaultPrimary

	if err := loadTraceSpans(ctx, clientDB, entry.db, &entry.spanCursor); err != nil {
		return nil, err
	}
	// Feed logs through the frontend's exporter, not the DB's: the DB only
	// tracks which spans have logs, while the rendered ┃ output lines come from
	// the frontend's own log buffers. Report mode dispatches synchronously, so
	// this is safe outside the event loop.
	if err := loadTraceLogs(ctx, clientDB, entry.reporter.LogExporter(), &entry.logCursor); err != nil {
		return nil, err
	}

	entry.defaultPrimary = entry.db.PrimarySpan
	return entry, nil
}

// render brings the cached session up to date with clientDB and renders it
// scoped to root.
func (c *traceReportCacheState) render(ctx context.Context, key traceReportKey, clientDB *clientdb.DB, root string, opt traceReportOpts) (string, error) {
	var primary dagui.SpanID
	if root != "" {
		spanID, err := trace.SpanIDFromHex(root)
		if err != nil {
			return "", fmt.Errorf("parse root span ID %q: %w", root, err)
		}
		primary = dagui.SpanID{SpanID: spanID}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, err := c.load(ctx, key, clientDB)
	if err != nil {
		return "", err
	}

	if primary.IsValid() {
		entry.db.SetPrimarySpan(primary)
	}

	// Reporters are cached (and so reused) per session, while options are
	// per-render: apply them every time, including the zero values, so a
	// previous render's options never leak into this one.
	renderOpts := idtui.ReportRenderOpts{
		// Show completed spans; without this the final render bails out
		// entirely unless something failed or a special section
		// (tests/checks/...) exists.
		Verbosity: dagui.ShowCompletedVerbosity,
		// Whether to leave completed steps expanded.
		ExpandCompleted: true,
		NestedLogLimit:  opt.NestedLogLines,
		ScopedSubtree:   opt.Scoped,
	}
	if opt.ExpandAll {
		renderOpts.ExpandSpans = expandedSpans(entry.db, primary)
	}
	if opt.HideNoise {
		renderOpts.Filter = reportNoiseFilter(entry.db, primary)
	}
	if opt.SuggestReadTrace {
		renderOpts.RerunSuggestion = readTraceRerunSuggestion
	}
	if len(opt.HideLogSpans) > 0 {
		hide := make(map[dagui.SpanID]bool, len(opt.HideLogSpans))
		for hex := range opt.HideLogSpans {
			spanID, err := trace.SpanIDFromHex(hex)
			if err != nil {
				continue
			}
			hide[dagui.SpanID{SpanID: spanID}] = true
		}
		renderOpts.HideLogSpans = hide
	}
	entry.reporter.SetReportRenderOpts(renderOpts)

	var buf bytes.Buffer
	if err := entry.reporter.FinalRender(&buf); err != nil {
		// A failing trace surfaces as an ExitError; that's expected here -- the
		// report itself is still what we want.
		var exitErr idtui.ExitError
		if !errors.As(err, &exitErr) {
			return "", fmt.Errorf("render trace report: %w", err)
		}
	}
	return buf.String(), nil
}

// loadTraceSpans pages every span appended since *cursor into db, advancing
// *cursor as it goes.
//
// NB: paging stops only on an empty page, never on a short one -- the store
// spills older rows to files and a read can return fewer rows than the limit
// while more remain (which silently truncated the trace, hiding e.g. the
// nested test spans that the TESTS section is built from).
func loadTraceSpans(ctx context.Context, clientDB *clientdb.DB, db *dagui.DB, cursor *int64) error {
	for {
		rows, err := clientDB.Read().SelectSpansSince(ctx, clientdb.SelectSpansSinceParams{
			ID:    *cursor,
			Limit: traceReportBatchSize,
		})
		if err != nil {
			return fmt.Errorf("select spans: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		spans := make([]sdktrace.ReadOnlySpan, 0, len(rows))
		last := *cursor
		for _, row := range rows {
			last = row.ID
			spans = append(spans, row.ReadOnly())
		}
		// db.ExportSpans is idempotent on re-seen rows (findOrAllocSpan), so a
		// row exported twice -- e.g. a span updated after we first saw it --
		// just updates in place.
		if err := db.ExportSpans(ctx, spans); err != nil {
			return fmt.Errorf("export spans: %w", err)
		}
		*cursor = last
	}
}

// loadTraceLogs pages every log record appended since *cursor into exporter.
// Logs matter for the report: surfaced failures and the TESTS section's
// failing-case tails render from log content, not from span attributes. Same
// short-page caveat as loadTraceSpans: only an empty page ends the loop.
func loadTraceLogs(ctx context.Context, clientDB *clientdb.DB, logExporter sdklog.Exporter, cursor *int64) error {
	for {
		rows, err := clientDB.Read().SelectLogsSince(ctx, clientdb.SelectLogsSinceParams{
			ID:    *cursor,
			Limit: traceReportBatchSize,
		})
		if err != nil {
			return fmt.Errorf("select logs: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		last := *cursor
		for _, row := range rows {
			last = row.ID
		}
		if err := telemetry.ReexportLogsFromPB(ctx, logExporter, &collogspb.ExportLogsServiceRequest{
			ResourceLogs: clientdb.LogsToPB(rows),
		}); err != nil {
			return fmt.Errorf("export logs: %w", err)
		}
		*cursor = last
	}
}

// guardTraceReport bounds a rendered report: every line is clamped to
// traceReportMaxLineLen bytes, and the whole report to traceReportMaxBytes,
// dropping the middle rather than the tail so both the span tree and the
// trailing summary sections survive.
//
// This applies to EVERY rendered report: both consumers -- LLM tool results
// and the ReadTrace builtin -- are read by an agent, and no caller wants a
// 930KB string, which is what a module-heavy trace renders to, dominated by
// verbatim call arguments. Anything needing the unbounded form should read
// the trace directly.
//
// A report that's already within both limits is returned byte-identical.
func guardTraceReport(report string) string {
	if len(report) <= traceReportMaxBytes && !anyLineTooLong(report) {
		return report
	}

	lines := strings.Split(report, "\n")
	total := 0
	for i, line := range lines {
		lines[i] = clampLineBytes(line, traceReportMaxLineLen)
		total += len(lines[i]) + 1 // +1 for the newline that rejoins it
	}
	if total > 0 {
		total-- // the last line isn't followed by a newline
	}
	if total <= traceReportMaxBytes {
		return strings.Join(lines, "\n")
	}

	// Truncate on line boundaries, keeping a generous head and tail. The
	// marker line itself counts against the budget, so reserve room for it.
	const markerReserve = 128
	headBudget := traceReportHeadBytes

	head, headBytes := 0, 0
	for head < len(lines) && headBytes+len(lines[head])+1 <= headBudget {
		headBytes += len(lines[head]) + 1
		head++
	}
	tailBudget := traceReportMaxBytes - headBytes - markerReserve
	tail, tailBytes := len(lines), 0
	for tail > head && tailBytes+len(lines[tail-1])+1 <= tailBudget {
		tailBytes += len(lines[tail-1]) + 1
		tail--
	}

	droppedLines := tail - head
	droppedBytes := total - headBytes - tailBytes
	if droppedLines <= 0 {
		return strings.Join(lines, "\n")
	}

	out := make([]string, 0, head+1+(len(lines)-tail))
	out = append(out, lines[:head]...)
	out = append(out, fmt.Sprintf("... %d lines (%d bytes) omitted from the middle of this report ...",
		droppedLines, droppedBytes))
	out = append(out, lines[tail:]...)
	return strings.Join(out, "\n")
}

// anyLineTooLong reports whether report contains a line over the per-line
// byte clamp, without allocating a split.
func anyLineTooLong(report string) bool {
	for len(report) > 0 {
		line := report
		if i := strings.IndexByte(report, '\n'); i >= 0 {
			line, report = report[:i], report[i+1:]
		} else {
			report = ""
		}
		if len(line) > traceReportMaxLineLen {
			return true
		}
	}
	return false
}

// clampLineBytes truncates line to at most max bytes, cutting on a UTF-8
// rune boundary so a multi-byte rune is never split, and marks the clamp
// inline.
func clampLineBytes(line string, max int) string {
	if len(line) <= max {
		return line
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut] + fmt.Sprintf("[... %d bytes truncated]", len(line)-cut)
}

// expandedSpans returns the expansion map for a scoped report: the scope root,
// plus the pure wrapper spans between it and the first real work span on each
// branch, plus that work span itself.
//
// The point of forcing rows open at all is narrow: the scope root is an LLM
// tool-call span (LLMTool set), and the module function beneath it may roll up
// its logs, so TraceTree.IsExpanded -- which consults this map before its own
// neverExpand rule -- would otherwise collapse the tool's own printed output to
// a bare status line. Everything past that first real span is left to the
// normal rules (ExpandCompleted already keeps ordinary completed rows open),
// which is what keeps roll-up boundaries deeper in the subtree intact.
//
// Marking the whole subtree instead (what this used to do) punched open EVERY
// roll-up in it: module-internal glob matching, long `$ .withDirectory(...)
// CACHED` runs with verbatim arguments -- measured at 713 lines over the 16 KiB
// report budget for a single check.
//
// An unset root means nothing to unwrap: without a scope there is no tool-call
// boundary to punch through.
func expandedSpans(db *dagui.DB, root dagui.SpanID) map[dagui.SpanID]bool {
	expanded := map[dagui.SpanID]bool{}
	if !root.IsValid() {
		return expanded
	}
	rootSpan, ok := db.Spans.Map[root]
	if !ok {
		return expanded
	}
	// ChildSpans already includes cause-linked children (dagui folds those
	// edges in), so this follows the same containment the report renders.
	expanded[rootSpan.ID] = true
	queue := append([]*dagui.Span{}, rootSpan.ChildSpans.Order...)
	for len(queue) > 0 {
		span := queue[0]
		queue = queue[1:]
		if expanded[span.ID] {
			continue
		}
		expanded[span.ID] = true
		if isReportWrapperSpan(span) {
			// A pure frame: keep descending toward the actual work.
			queue = append(queue, span.ChildSpans.Order...)
		}
		// Otherwise stop: this is the tool's own work, and its children are
		// nested work that the normal expansion rules govern.
	}
	return expanded
}

// isReportWrapperSpan reports whether span is a pure wrapper on the way from a
// tool call to the work it did: a span that carries no output of its own and
// exists only to frame what runs beneath it.
//
// The three shapes that show up between an LLM tool-call span and the module
// function it invoked are the passthrough/internal spans dagui already renders
// through, the `POST /query` API span, and the `<module>:<Type>.<fn>` profiling
// twin -- which has no logs and exactly one child, the rule the last clause
// generalizes. A roll-up or tool-call span is never treated as a wrapper: those
// are precisely the boundaries whose contents must stay collapsed.
func isReportWrapperSpan(span *dagui.Span) bool {
	if span.Passthrough || span.Internal {
		return true
	}
	if span.Name == "POST /query" {
		return true
	}
	if span.HasLogs || span.RollUpLogs || span.RollUpSpans || span.LLMTool != "" {
		return false
	}
	return len(span.ChildSpans.Order) == 1
}

// reportNoiseFilter prunes what a tool-call-scoped report has no use for.
//
// Services: a started service's long-lived exec span and everything beneath
// it, plus each service's *origin* -- the API call that produced the Service
// value (Container.asService and friends). The service's log stream is routed
// to that origin span (dagui keys logs by the dag digest that produced them,
// see DB.routeLog), so leaving it in would render the service's output
// verbatim even with the exec span itself pruned.
//
// Messages: the LLM conversation spans the tool call is part of.
//
// The services are surfaced relative to root -- the span the report is scoped
// to -- because that is where the report's services live: a service a tool
// started sits beneath the tool-call display span, which is a Boundary, so the
// whole-trace surfacing treats it as contained and would never mark its origin.
func reportNoiseFilter(db *dagui.DB, root dagui.SpanID) func(*dagui.Span) dagui.WalkDecision {
	origins := map[dagui.SpanID]bool{}
	var mark func(nodes []*dagui.ServiceNode)
	mark = func(nodes []*dagui.ServiceNode) {
		for _, node := range nodes {
			if origin := node.Origin(); origin != nil {
				origins[origin.ID] = true
			}
			mark(node.Children)
		}
	}
	var rootSpan *dagui.Span
	if root.IsValid() {
		rootSpan = db.Spans.Map[root]
	}
	mark(db.SurfacedServicesForSpan(rootSpan))
	return func(span *dagui.Span) dagui.WalkDecision {
		if span.Service || origins[span.ID] {
			return dagui.WalkSkip
		}
		if span.LLMRole != "" {
			// An LLM message (a prompt, a reply, a tool-call display span) is
			// the conversation, not work: render straight through to whatever
			// ran beneath it. The flat capture path drops these spans' logs for
			// the same reason.
			return dagui.WalkPassthrough
		}
		return dagui.WalkContinue
	}
}
