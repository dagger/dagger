package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	telemetry "github.com/dagger/otel-go"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/clientdb"
)

// traceReportBatchSize is how many spans we feed into the throwaway DB per
// ExportSpans call when materializing a report's scope, bounding the
// transient conversion slice.
const traceReportBatchSize = 1000

// traceReportMaxLogRowsPerSpan bounds how many log rows a scoped load fetches
// per span — each span's NEWEST rows, since every consumer of a rendered
// report reads bounded tails (NestedLogLines clamps nested rows, the byte
// guard clamps the whole report, and the tool's own verbatim OUTPUT section
// is captured separately from the store). Without it a single pathological
// span in scope — a service that streamed millions of lines — would balloon
// a render's transient memory; ReadLogs remains the path to the full stream.
const traceReportMaxLogRowsPerSpan = 512

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

// traceReportOpts tunes a single scoped render. The zero value shows
// completed spans expanded, prunes nothing, and leaves log lines unbounded.
type traceReportOpts struct {
	// ExpandWrappers unwraps the scoped subtree just far enough for the tool's
	// own output to reach the reader: the scope root and the pure wrapper spans
	// beneath it are force-expanded, stopping at the first real work span. See
	// expandedSpans, and idtui.ReportRenderOpts.ExpandSpans for why this isn't
	// done by raising verbosity to ExpandCompletedVerbosity.
	ExpandWrappers bool

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
	//     skipped: they reshape the report to be about the whole run -- marking
	//     the host Passthrough and wiring RevealedSpans -- which would replace
	//     the subtree's own rows with the run-wide view.
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

	// HideSpanTree drops the span tree from the report, leaving only what the
	// run SURFACED beneath the root -- CHECKS, TESTS, SERVICES, the
	// conversation, the generators -- alongside the caller's own OUTPUT
	// section.
	//
	// Set for a successful tool call's own result: the agent asked what its
	// tool did, and a transcription of every dagql field call beneath it is
	// noise it did not ask for (and, at 16 KiB of budget, noise that crowds
	// out the answer). ReadTrace, whose entire purpose is "show me the shape
	// of what ran", never sets it.
	HideSpanTree bool
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

// renderTraceReport materializes root's telemetry scope from the client's
// telemetry store and renders the pretty frontend's final report as plain
// text -- the same span tree, CHECKS and TESTS sections a user sees at the
// end of a CLI run.
//
// Loading is scoped, not session-wide: the store's span index answers "which
// spans belong to a report rooted here" (the containment closure over child
// and cause-link edges, plus every member's ancestor chain), and only those
// spans' newest snapshots -- and the scope's log tails -- are fetched, into a
// throwaway DB that dies with the render. Nothing here caches or retains
// telemetry between renders: the session-lifetime state is the store's own
// indexes, which is what keeps an engine hosting long agent sessions at a
// flat memory profile no matter how large a session's trace grows (the
// previous design retained every span and log line of the session in a
// cached DB, measured at ~7GB in a heap profile taken just before an OOM
// kill). The cost moves to the render: a load linear in the SCOPE's size --
// single-digit ms for a typical tool call's subtree -- instead of retained
// memory linear in the session's.
func renderTraceReport(ctx context.Context, root string, opts traceReportOpts) (string, error) {
	if root == "" {
		return "", fmt.Errorf("render trace report: no root span")
	}
	clientDB, err := traceReportClientDB(ctx)
	if err != nil {
		return "", err
	}
	defer clientDB.Close()

	session, err := loadTraceReportSession(ctx, clientDB, root)
	if err != nil {
		return "", err
	}
	return renderTraceReportSession(session, root, opts)
}

// traceReportClientDB opens the main client's telemetry store. The caller
// closes it.
func traceReportClientDB(ctx context.Context) (*clientdb.DB, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	mainMeta, err := query.MainClientCallerMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("get main client caller metadata: %w", err)
	}
	// NB: this flushes all of the session's clients before returning, so
	// everything that has ended is visible below.
	clientDB, err := query.ClientTelemetry(ctx, mainMeta.SessionID, mainMeta.ClientID)
	if err != nil {
		return nil, fmt.Errorf("get client telemetry: %w", err)
	}
	return clientDB, nil
}

// loadTraceReportSession materializes the scope of a report rooted at root
// into a fresh report session: the containment closure's spans (newest
// snapshots) and their log tails.
//
// The scope is the store's log-scope walk -- the same containment dagui
// renders, child edges plus cause-purpose link edges, seeded from both ends
// of root's cause links -- closed over every member's ancestor chain, so no
// loaded span's parent pointer resolves to an unreceived placeholder. Logs
// are fetched for the walk only: ancestors above root frame the tree but
// render no output of their own in a scoped report.
func loadTraceReportSession(ctx context.Context, clientDB *clientdb.DB, root string) (*idtui.ReportSession, error) {
	read := clientDB.Read()
	walk := read.SpanLogScope(root)
	scope := read.AncestorClosure(walk)

	session := idtui.NewReportSession(dagui.NewDB())
	if err := ingestSpanScope(ctx, read, session.DB(), scope); err != nil {
		return nil, err
	}

	// Second pass: error origins. A failed span's origin reference (an
	// error_origin link, or a [traceparent:...] marker in its status) may
	// point at a span outside the containment scope, for which dagui
	// allocated an unreceived placeholder; the report would render an empty
	// reference for it. Resolve exactly those. One pass suffices: an
	// origin's own origins render nowhere in this report.
	if missing := unreceivedErrorOrigins(session.DB()); len(missing) > 0 {
		if err := ingestSpanScope(ctx, read, session.DB(), read.AncestorClosure(missing)); err != nil {
			return nil, err
		}
	}

	logs, err := read.SelectLogsForSpans(ctx, walk, traceReportMaxLogRowsPerSpan)
	if err != nil {
		return nil, fmt.Errorf("select logs: %w", err)
	}
	// Feed logs through the report session's exporter, not the DB's: the DB
	// only tracks which spans have logs, while the rendered ┃ output lines
	// come from the session's own log buffers.
	for start := 0; start < len(logs); start += traceReportBatchSize {
		batch := logs[start:min(start+traceReportBatchSize, len(logs))]
		if err := telemetry.ReexportLogsFromPB(ctx, session.LogExporter(), &collogspb.ExportLogsServiceRequest{
			ResourceLogs: clientdb.LogsToPB(batch),
		}); err != nil {
			return nil, fmt.Errorf("export logs: %w", err)
		}
	}
	return session, nil
}

// ingestSpanScope feeds the newest snapshot of every span in scope into db,
// in append order -- the order sequential processing of the stream would have
// delivered them in. A span's snapshots are cumulative, so its newest row
// alone reproduces the state full sequential processing would end with; still-running
// spans naturally ingest as running.
func ingestSpanScope(ctx context.Context, read *clientdb.DB, db *dagui.DB, scope map[string]struct{}) error {
	rows, err := read.SelectSpansLatest(ctx, scope)
	if err != nil {
		return fmt.Errorf("select spans: %w", err)
	}
	for start := 0; start < len(rows); start += traceReportBatchSize {
		batch := rows[start:min(start+traceReportBatchSize, len(rows))]
		spans := make([]sdktrace.ReadOnlySpan, len(batch))
		for i := range batch {
			spans[i] = batch[i].ReadOnly()
		}
		if err := db.ExportSpans(ctx, spans); err != nil {
			return fmt.Errorf("export spans: %w", err)
		}
	}
	return nil
}

// unreceivedErrorOrigins collects the span IDs referenced as error origins by
// any loaded span but not themselves loaded -- the placeholders dagui
// allocated for references pointing outside the loaded scope.
func unreceivedErrorOrigins(db *dagui.DB) map[string]struct{} {
	missing := map[string]struct{}{}
	for span := range db.Spans.Iter() {
		for _, origin := range span.ErrorOrigins.Order {
			if !origin.Received {
				missing[origin.ID.String()] = struct{}{}
			}
		}
	}
	return missing
}

// renderTraceReportSession renders session's DB scoped to root.
//
// Scoping is a per-render option (idtui.ReportRenderOpts.Root), and every
// render owns its session outright -- the DB was loaded for this render and
// is discarded with it -- so no render can observe another render's scope,
// expansion, claims, or promotions.
func renderTraceReportSession(session *idtui.ReportSession, root string, opt traceReportOpts) (string, error) {
	spanID, err := trace.SpanIDFromHex(root)
	if err != nil {
		return "", fmt.Errorf("parse root span ID %q: %w", root, err)
	}
	primary := dagui.SpanID{SpanID: spanID}
	db := session.DB()

	renderOpts := idtui.ReportRenderOpts{
		// Show completed spans; without this the final render bails out
		// entirely unless something failed or a special section
		// (tests/checks/...) exists.
		Verbosity: dagui.ShowCompletedVerbosity,
		// Whether to leave completed steps expanded.
		ExpandCompleted: true,
		NestedLogLimit:  opt.NestedLogLines,
		ScopedSubtree:   opt.Scoped,
		Root:            primary,
		HideSpanTree:    opt.HideSpanTree,
		// Every report rendered here is assembled for an LLM: a tool call's own
		// result, or the ReadTrace builtin. Inside the engine there is no agent
		// env var to sniff (it's a daemon), so say so explicitly, per render.
		// Section headings then render as greppable "== TITLE ==" markers
		// instead of bold-TTY text. (The color profile needs no opt: the report
		// session pins termenv.Ascii, so no escape codes reach the model.)
		AgentStyle: true,
	}
	if opt.ExpandWrappers {
		renderOpts.ExpandSpans = expandedSpans(db, primary)
	}
	if opt.HideNoise {
		renderOpts.Filter = reportNoiseFilter(db, primary)
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

	var buf bytes.Buffer
	if err := session.Render(&buf, renderOpts); err != nil {
		// A failing trace surfaces as an ExitError; that's expected here -- the
		// report itself is still what we want.
		var exitErr idtui.ExitError
		if !errors.As(err, &exitErr) {
			return "", fmt.Errorf("render trace report: %w", err)
		}
	}
	return buf.String(), nil
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
	return guardText(report, textGuard{
		maxBytes:   traceReportMaxBytes,
		maxLineLen: traceReportMaxLineLen,
		headBytes:  traceReportHeadBytes,
		marker: func(lines, bytes int) string {
			return fmt.Sprintf("... %d lines (%d bytes) omitted from the middle of this report ...",
				lines, bytes)
		},
	})
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
