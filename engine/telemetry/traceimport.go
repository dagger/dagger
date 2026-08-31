package telemetry

import (
	"context"
	"errors"
	"sync"
	"time"

	telemetry "github.com/dagger/otel-go"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// Importing a FOREIGN trace — one this process did not publish — into a live
// client's telemetry sinks (hack/designs/resume-from-trace.md §5.1).
//
// `dagger agent --trace <id>` streams a past session's whole trace into the
// LIVE frontend's own exporters, rather than into a private DB: one DB then
// holds both sessions, which is what makes a restored session "the old
// session's TUI plus a live prompt" — an agent's old and new loop spans merge
// into one roster entry, its transcript spans both lives, and every imported
// tool call keeps its logs and its call payload.
//
// Two things have to be true of the foreign half before it lands, and both are
// cheapest as edits to the PROTOBUF, before conversion:
//
//   - Its unfinished spans must be sealed. dagui.DB cancels still-running
//     spans only when ITS OWN root ends, and the imported root is not it, so a
//     crashed session's never-ended spans would render as live work forever —
//     spinners for a dead run, and AgentNode.Live() reporting true for an
//     agent that is not.
//   - Its root must be passthrough. It arrives as a second parentless span;
//     passthrough makes every walk render its children in its place instead of
//     a stale `dagger agent …` row wrapping the whole of the old session.
//
// What this deliberately does NOT do is repoint the primary span (§5.1.1) or
// contain the imported subtree with Encapsulate/Boundary (§5.1.1, §5.1.3) —
// the imported conversation has to surface, and the live session's own root
// stays the one the TUI is about.
//
// DEGRADE, NEVER PANIC. Two shapes are legal OTLP but are dereferenced blindly
// by the shared re-export path (a nil Resource in otel.ResourceFromPB, a nil
// Body in otel.LogValueFromPB): a payload carrying no resource info, and an
// attribute-only log record with no body. The second is precisely what resume
// rides on — agent state, snapshot digests and call payloads are all
// empty-bodied records — and §12 lists "does an empty-bodied record survive the
// Cloud round trip" as unverified. Each Import method fills those in rather
// than risk a nil dereference taking the CLI down mid-restore.
//
// That belongs upstream, and is fixed there by dagger/otel-go#17. These three
// guards are the stopgap until this repo's otel-go pin includes it; DELETE THEM
// WITH THAT BUMP, since a decode boundary that answers for its own optional
// fields is the real fix and duplicating it here only hides the next one.
//
// The transport is somebody else's problem: slice 5's Cloud SSE client decodes
// protojson into these three request types and feeds them here, and slice 4's
// tests feed a canned capture instead. Nothing below knows which.

// TraceImportBarrier acknowledges asynchronous frontend exporter work.
type TraceImportBarrier interface {
	// WaitForEventLoop returns after every frontend event enqueued before it has
	// been applied. Import acknowledgments use this rather than exporter return
	// values, because pretty frontend exporters enqueue their work.
	WaitForEventLoop(context.Context) error
}

// TraceImportSinks are the exporters an imported trace lands in — the live
// frontend's own (Frontend.SpanExporter, LogExporter, MetricExporter), which
// is the whole point: one DB, both sessions. A nil sink drops its stream.
type TraceImportSinks struct {
	Spans   sdktrace.SpanExporter
	Logs    sdklog.Exporter
	Metrics sdkmetric.Exporter
	Barrier TraceImportBarrier
}

// TraceImporter folds a foreign trace into a live client's sinks, applying
// §5.1's two fixes on the way through. Call ImportSpans/ImportLogs/
// ImportMetrics per export request, in any order, then Seal exactly once when
// the stream ends.
//
// Safe for concurrent use: the three streams arrive on separate connections.
type TraceImporter struct {
	sinks TraceImportSinks

	// queueMu serializes calls into the frontend exporters. In particular, it
	// prevents separate span/log/metric transports from racing asynchronous
	// frontend enqueue operations into an order different from import order.
	queueMu sync.Mutex
	mu      sync.Mutex
	// unfinished holds every span seen with no end time, keyed by span ID, so
	// a later update that DOES end it drops it back out. It is what Seal
	// works from.
	unfinished map[string]*unfinishedSpan
	// order keeps unfinished's keys in arrival order, so the seal's re-export
	// is deterministic (parents before children, as the capture had them).
	order []string
	// rootEnd is the newest end time seen on a parentless span, and newest the
	// newest timestamp seen anywhere. A session that exited cleanly gives the
	// first; a crashed one leaves only the second.
	rootEnd uint64
	newest  uint64
}

type unfinishedSpan struct {
	span *tracepb.Span
	// The groups the span arrived in, so the seal's re-export carries the same
	// resource and instrumentation scope rather than inventing empty ones.
	resource *tracepb.ResourceSpans
	scope    *tracepb.ScopeSpans
}

// NewTraceImporter returns an importer feeding sinks.
func NewTraceImporter(sinks TraceImportSinks) *TraceImporter {
	return &TraceImporter{
		sinks:      sinks,
		unfinished: map[string]*unfinishedSpan{},
	}
}

// ImportSpans folds one OTLP span export request of the foreign trace in,
// stamping its roots passthrough and noting which of its spans never ended.
func (imp *TraceImporter) ImportSpans(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) error {
	imp.queueMu.Lock()
	defer imp.queueMu.Unlock()
	return imp.importSpans(ctx, req)
}

func (imp *TraceImporter) importSpans(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) error {
	if imp.sinks.Spans == nil || req == nil {
		return nil
	}

	imp.mu.Lock()
	for _, resourceSpans := range req.GetResourceSpans() {
		if resourceSpans.GetResource() == nil {
			resourceSpans.Resource = &resourcepb.Resource{} // see "degrade, never panic"
		}
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			for _, span := range scopeSpans.GetSpans() {
				imp.noteLocked(resourceSpans, scopeSpans, span)
			}
		}
	}
	imp.mu.Unlock()

	return imp.sinks.Spans.ExportSpans(ctx, telemetry.SpansFromPB(req.GetResourceSpans()))
}

func (imp *TraceImporter) noteLocked(resource *tracepb.ResourceSpans, scope *tracepb.ScopeSpans, span *tracepb.Span) {
	parentless := len(span.GetParentSpanId()) == 0
	if parentless {
		// §5.1.1. Stamped on every parentless span rather than on "the" root:
		// a capture may hold more than one (a partial fetch, a trace whose
		// real root never reached Cloud), and each is a second root as far as
		// the live DB is concerned.
		setBoolAttr(span, telemetry.UIPassthroughAttr, true)
	}
	if start := span.GetStartTimeUnixNano(); start > imp.newest {
		imp.newest = start
	}

	key := string(span.GetSpanId())
	if pbSpanRunning(span) {
		if _, seen := imp.unfinished[key]; !seen {
			imp.order = append(imp.order, key)
		}
		imp.unfinished[key] = &unfinishedSpan{span: span, resource: resource, scope: scope}
		return
	}

	// A span that ended supersedes the running snapshot the live span
	// processor exported for it at start.
	end := span.GetEndTimeUnixNano()
	if end > imp.newest {
		imp.newest = end
	}
	if parentless && end > imp.rootEnd {
		imp.rootEnd = end
	}
	delete(imp.unfinished, key)
}

// ImportLogs folds one OTLP log export request of the foreign trace in.
func (imp *TraceImporter) ImportLogs(ctx context.Context, req *collogspb.ExportLogsServiceRequest) error {
	imp.queueMu.Lock()
	defer imp.queueMu.Unlock()
	return imp.importLogs(ctx, req)
}

func (imp *TraceImporter) importLogs(ctx context.Context, req *collogspb.ExportLogsServiceRequest) error {
	if imp.sinks.Logs == nil || req == nil {
		return nil
	}
	for _, resourceLogs := range req.GetResourceLogs() {
		if resourceLogs.GetResource() == nil {
			resourceLogs.Resource = &resourcepb.Resource{} // see "degrade, never panic"
		}
		for _, scopeLogs := range resourceLogs.GetScopeLogs() {
			for _, record := range scopeLogs.GetLogRecords() {
				if record.GetBody() == nil {
					record.Body = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{}}
				}
			}
		}
	}
	return telemetry.ReexportLogsFromPB(ctx, imp.sinks.Logs, req)
}

// ImportMetrics folds one OTLP metric export request of the foreign trace in.
// Imported metrics COUNT (§12): cost and token totals accumulate across a
// resume rather than restarting at zero.
func (imp *TraceImporter) ImportMetrics(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	imp.queueMu.Lock()
	defer imp.queueMu.Unlock()
	return imp.importMetrics(ctx, req)
}

func (imp *TraceImporter) importMetrics(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	if imp.sinks.Metrics == nil || req == nil {
		return nil
	}
	for _, resourceMetrics := range req.GetResourceMetrics() {
		if resourceMetrics.GetResource() == nil {
			resourceMetrics.Resource = &resourcepb.Resource{} // see "degrade, never panic"
		}
	}
	return ReexportMetricsFromPB(ctx, []sdkmetric.Exporter{imp.sinks.Metrics}, req)
}

// Seal ends the import: every span the capture left running is re-exported
// with an end time and the Canceled/LeftRunning marks, the same shape
// dagui.DB produces for its own root's leftovers (§5.1.2).
//
// The end time is the imported root's, or — when the source session crashed
// hard enough that even its root never ended — the newest timestamp the
// capture carried. Neither is the truth about when that work stopped, which
// nothing recorded; both say "no later than this", which is the honest
// reading and the one the DB's own sweep already takes.
//
// Idempotent: a second call has nothing left to seal.
func (imp *TraceImporter) Seal(ctx context.Context) error {
	imp.queueMu.Lock()
	defer imp.queueMu.Unlock()

	imp.mu.Lock()
	sealAt := imp.rootEnd
	if sealAt == 0 {
		sealAt = imp.newest
	}
	imp.mu.Unlock()
	return imp.sealAt(ctx, sealAt)
}

// SealAt seals unfinished imported spans at an explicit source cut. Archive
// imports use the manifest's immutable seal timestamp rather than deriving a
// bound from whichever records a partial transfer happened to deliver.
func (imp *TraceImporter) SealAt(ctx context.Context, at time.Time) error {
	if at.IsZero() {
		return errors.New("trace import seal time is zero")
	}
	imp.queueMu.Lock()
	defer imp.queueMu.Unlock()
	return imp.sealAt(ctx, uint64(at.UnixNano())) //nolint:gosec
}

func (imp *TraceImporter) sealAt(ctx context.Context, sealAt uint64) error {
	if imp.sinks.Spans == nil {
		return nil
	}

	imp.mu.Lock()
	order, unfinished := imp.order, imp.unfinished
	imp.mu.Unlock()

	if sealAt == 0 || len(unfinished) == 0 {
		imp.clearUnfinished()
		return nil
	}

	var (
		groups     []*tracepb.ResourceSpans
		byResource = map[*tracepb.ResourceSpans]*tracepb.ResourceSpans{}
		byScope    = map[*tracepb.ScopeSpans]*tracepb.ScopeSpans{}
	)
	for _, key := range order {
		entry, ok := unfinished[key]
		if !ok {
			// It ended later in the stream after all.
			continue
		}

		// Sealed on a COPY: the sink was handed the original, and a frontend
		// consumes its exports asynchronously.
		sealed := proto.Clone(entry.span).(*tracepb.Span)
		end := sealAt
		if start := sealed.GetStartTimeUnixNano(); end < start {
			// A span cannot end before it began; without this a span that
			// started after the root ended would still read as running.
			end = start
		}
		sealed.EndTimeUnixNano = end
		setBoolAttr(sealed, telemetry.CanceledAttr, true)
		setBoolAttr(sealed, telemetryattrs.DagLeftRunningAttr, true)

		scope, ok := byScope[entry.scope]
		if !ok {
			scope = &tracepb.ScopeSpans{
				Scope:     entry.scope.GetScope(),
				SchemaUrl: entry.scope.GetSchemaUrl(),
			}
			byScope[entry.scope] = scope
			resource, ok := byResource[entry.resource]
			if !ok {
				resource = &tracepb.ResourceSpans{
					Resource:  entry.resource.GetResource(),
					SchemaUrl: entry.resource.GetSchemaUrl(),
				}
				byResource[entry.resource] = resource
				groups = append(groups, resource)
			}
			resource.ScopeSpans = append(resource.ScopeSpans, scope)
		}
		scope.Spans = append(scope.Spans, sealed)
	}
	if len(groups) == 0 {
		imp.clearUnfinished()
		return nil
	}
	if err := imp.sinks.Spans.ExportSpans(ctx, telemetry.SpansFromPB(groups)); err != nil {
		return err
	}
	imp.clearUnfinished()
	return nil
}

func (imp *TraceImporter) clearUnfinished() {
	imp.mu.Lock()
	imp.order, imp.unfinished = nil, map[string]*unfinishedSpan{}
	imp.mu.Unlock()
}

// pbSpanRunning reports whether a span was still running when the capture was
// taken. A live span is exported with the ZERO end time — i.e. an end before
// its start — which is how otel.FilterLiveSpansExporter and dagui's
// Span.IsRunning both read "still running"; a missing (0) end time reads the
// same way.
func pbSpanRunning(span *tracepb.Span) bool {
	return int64(span.GetEndTimeUnixNano()) < int64(span.GetStartTimeUnixNano()) //nolint:gosec
}

func setBoolAttr(span *tracepb.Span, key string, val bool) {
	value := &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: val}}
	for _, attr := range span.GetAttributes() {
		if attr.GetKey() == key {
			attr.Value = value
			return
		}
	}
	span.Attributes = append(span.Attributes, &commonpb.KeyValue{Key: key, Value: value})
}
