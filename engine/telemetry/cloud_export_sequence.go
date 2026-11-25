package telemetry

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const cloudExportHeader = "X-Dagger-Export"

// Keep uploads off the default transport's connections, which Cloud trace
// readers also use. Cloud's ingress can send duplicate HTTP/2 END_STREAM frames
// for uploads multiplexed with an active download. Reuse this private pool
// across exporters and token refreshes without disabling HTTP/2. These settings
// match the OTLP HTTP exporters' private transports and Go's DefaultTransport.
var cloudExportTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

type exportSequenceContextKey struct{}

// exportSequenceMetadata is the X-Dagger-Export header of one upload: its
// writer and that writer's sequence number. It rides the context of the
// upload's export call, so the OTLP exporter's retries of that upload, which
// reuse the context, resend the same header.
type exportSequenceMetadata struct {
	writerID string
	sequence uint64
}

func withExportSequence(ctx context.Context, metadata exportSequenceMetadata) context.Context {
	return context.WithValue(ctx, exportSequenceContextKey{}, metadata)
}

// exportSequencer numbers the uploads of one writer: 1, 2, 3, ...
type exportSequencer struct {
	writerID string
	sequence atomic.Uint64
}

func newExportSequencer() *exportSequencer {
	return &exportSequencer{writerID: uuid.NewString()}
}

func (s *exportSequencer) next() exportSequenceMetadata {
	return exportSequenceMetadata{writerID: s.writerID, sequence: s.sequence.Add(1)}
}

func (s *exportSequencer) nextContext(ctx context.Context) context.Context {
	return withExportSequence(ctx, s.next())
}

func (s *exportSequencer) httpClient() *http.Client {
	return newExportSequenceHTTPClient()
}

// newExportSequenceHTTPClient is an upload client on the private Cloud export
// transport that stamps each request with the X-Dagger-Export header on its
// context.
func newExportSequenceHTTPClient() *http.Client {
	return &http.Client{Transport: exportSequenceTransport{base: cloudExportTransport}}
}

// maxTraceWriters bounds how many traces a traceExportSequencer remembers.
// Exporters live as long as a session or the engine and see an unbounded
// number of traces; the least recently exported trace is forgotten first.
const maxTraceWriters = 4096

// traceExportSequencer numbers the uploads of one exporter per trace. Dagger
// Cloud reads each writer's sequences of a trace as a contiguous prefix,
// waiting on every gap, while one exporter carries the telemetry of many
// traces (a session forwards nested sessions' telemetry under their own
// traces). So each trace the exporter uploads gets a writer of its own,
// numbered 1, 2, 3, ... by the uploads carrying that trace's records, and an
// export spanning several traces is split into one upload per trace.
//
// A trace's writer is a UUIDv5 under the exporter's base writer, named by the
// trace ID and a generation unique within this sequencer. When a trace is
// forgotten to bound memory, a later upload of it gets a new generation, hence
// a new writer starting at 1: no (writer, trace) pair ever reuses a number.
//
// Records with no trace ID (e.g. the engine's own cache events) are no
// trace's: they upload under the base writer, with a counter of its own.
type traceExportSequencer struct {
	base      *exportSequencer
	namespace uuid.UUID
	limit     int

	mu         sync.Mutex
	generation uint64
	traces     map[trace.TraceID]*list.Element // of *traceWriter
	recent     *list.List                      // most recently used first
}

type traceWriter struct {
	traceID  trace.TraceID
	writerID string
	sequence uint64
}

func newTraceExportSequencer() *traceExportSequencer {
	return newTraceExportSequencerWithLimit(maxTraceWriters)
}

func newTraceExportSequencerWithLimit(limit int) *traceExportSequencer {
	base := newExportSequencer()
	return &traceExportSequencer{
		base:      base,
		namespace: uuid.MustParse(base.writerID),
		limit:     max(limit, 1),
		traces:    map[trace.TraceID]*list.Element{},
		recent:    list.New(),
	}
}

// next returns the header of the next upload of traceID's records.
func (s *traceExportSequencer) next(traceID trace.TraceID) exportSequenceMetadata {
	if !traceID.IsValid() {
		return s.base.next()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if elem, ok := s.traces[traceID]; ok {
		s.recent.MoveToFront(elem)
		w := elem.Value.(*traceWriter)
		w.sequence++
		return exportSequenceMetadata{writerID: w.writerID, sequence: w.sequence}
	}
	for s.recent.Len() >= s.limit {
		oldest := s.recent.Back()
		s.recent.Remove(oldest)
		delete(s.traces, oldest.Value.(*traceWriter).traceID)
	}
	s.generation++
	w := &traceWriter{
		traceID:  traceID,
		writerID: uuid.NewSHA1(s.namespace, []byte(traceID.String()+"/"+strconv.FormatUint(s.generation, 10))).String(),
		sequence: 1,
	}
	s.traces[traceID] = s.recent.PushFront(w)
	return exportSequenceMetadata{writerID: w.writerID, sequence: w.sequence}
}

// traceGroup is the records of one trace in an export, in their order.
type traceGroup[T any] struct {
	traceID trace.TraceID
	items   []T
}

// groupByTrace splits items by trace, the groups in the order their traces
// first appear. Items of one trace keep their order.
func groupByTrace[T any](items []T, traceOf func(*T) trace.TraceID) []traceGroup[T] {
	if len(items) == 0 {
		return nil
	}
	first := traceOf(&items[0])
	single := true
	for i := 1; i < len(items); i++ {
		if traceOf(&items[i]) != first {
			single = false
			break
		}
	}
	if single {
		return []traceGroup[T]{{traceID: first, items: items}}
	}
	var groups []traceGroup[T]
	index := map[trace.TraceID]int{}
	for i := range items {
		id := traceOf(&items[i])
		g, ok := index[id]
		if !ok {
			g = len(groups)
			index[id] = g
			groups = append(groups, traceGroup[T]{traceID: id})
		}
		groups[g].items = append(groups[g].items, items[i])
	}
	return groups
}

// maxConcurrentTraceUploads bounds the per-trace uploads of one export in
// flight at once, so an export of many traces costs about as many round trips
// as it has traces divided by this, not one per trace.
const maxConcurrentTraceUploads = 4

// TraceExportError is the error of a Cloud export whose records spanned
// several traces, uploaded as one request per trace, when some of those
// requests failed. Delivered names the traces whose upload succeeded: a caller
// that resends the export can leave their records out, which Cloud already
// has. Errors are the failed uploads' errors; errors.Is and errors.As see
// each of them.
type TraceExportError struct {
	Delivered []trace.TraceID
	Errors    []error
}

func (e *TraceExportError) Error() string { return errors.Join(e.Errors...).Error() }

func (e *TraceExportError) Unwrap() []error { return e.Errors }

// exportPerTrace uploads items as one export call per trace, each with the
// next sequence of that trace's writer on its context, up to
// maxConcurrentTraceUploads at once. An upload that fails does not stop the
// others: an export of several traces that fails returns a TraceExportError
// naming the traces delivered. Once ctx has ended, the remaining uploads are
// not attempted and consume no sequence numbers, which Cloud would otherwise
// wait on as gaps.
func exportPerTrace[T any](ctx context.Context, s *traceExportSequencer, items []T, traceOf func(*T) trace.TraceID, export func(context.Context, []T) error) error {
	groups := groupByTrace(items, traceOf)
	switch len(groups) {
	case 0:
		// Nothing to upload, so no request and no sequence number.
		return export(ctx, items)
	case 1:
		return export(withExportSequence(ctx, s.next(groups[0].traceID)), groups[0].items)
	}
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		errs      []error
		delivered []trace.TraceID
	)
	slots := make(chan struct{}, maxConcurrentTraceUploads)
	for _, group := range groups {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if err := context.Cause(ctx); err != nil {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
			break
		}
		// Numbered here, in the order the traces appear, before the upload
		// starts: its retries resend this header.
		uploadCtx := withExportSequence(ctx, s.next(group.traceID))
		wg.Go(func() {
			defer func() { <-slots }()
			err := export(uploadCtx, group.items)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			} else {
				delivered = append(delivered, group.traceID)
			}
		})
	}
	wg.Wait()
	if len(errs) == 0 {
		return nil
	}
	return &TraceExportError{Delivered: delivered, Errors: errs}
}

type exportSequenceTransport struct {
	base http.RoundTripper
}

func (t exportSequenceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	metadata, ok := req.Context().Value(exportSequenceContextKey{}).(exportSequenceMetadata)
	if !ok {
		return t.base.RoundTrip(req)
	}

	request := req.Clone(req.Context())
	request.Header = req.Header.Clone()
	request.Header.Set(cloudExportHeader, fmt.Sprintf("%s/%d", metadata.writerID, metadata.sequence))
	return t.base.RoundTrip(request)
}

type sequencedSpanExporter struct {
	sequencer *traceExportSequencer
	exporter  sdktrace.SpanExporter
}

var _ sdktrace.SpanExporter = (*sequencedSpanExporter)(nil)

func (e *sequencedSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return exportPerTrace(ctx, e.sequencer, spans, spanTraceID, e.exporter.ExportSpans)
}

func spanTraceID(span *sdktrace.ReadOnlySpan) trace.TraceID {
	return (*span).SpanContext().TraceID()
}

func logTraceID(rec *sdklog.Record) trace.TraceID {
	return rec.TraceID()
}

func (e *sequencedSpanExporter) Shutdown(ctx context.Context) error {
	return e.exporter.Shutdown(ctx)
}

type sequencedLogExporter struct {
	sequencer *traceExportSequencer
	exporter  sdklog.Exporter
}

var _ sdklog.Exporter = (*sequencedLogExporter)(nil)

func (e *sequencedLogExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	return exportPerTrace(ctx, e.sequencer, logs, logTraceID, e.exporter.Export)
}

func (e *sequencedLogExporter) Shutdown(ctx context.Context) error {
	return e.exporter.Shutdown(ctx)
}

func (e *sequencedLogExporter) ForceFlush(ctx context.Context) error {
	return e.exporter.ForceFlush(ctx)
}

type sequencedMetricExporter struct {
	sequencer *exportSequencer
	exporter  sdkmetric.Exporter
	shutdown  atomic.Bool
}

var _ sdkmetric.Exporter = (*sequencedMetricExporter)(nil)

func (e *sequencedMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.exporter.Temporality(kind)
}

func (e *sequencedMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.exporter.Aggregation(kind)
}

func (e *sequencedMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	// PeriodicReader performs a final collection even when this process never
	// recorded metrics. A resource with no instrumentation scopes carries no
	// metrics to deliver; avoid an otherwise empty Cloud request on shutdown.
	// Do not consume a sequence number for a request we will not send.
	if metrics != nil && len(metrics.ScopeMetrics) == 0 {
		if e.shutdown.Load() {
			return sdkmetric.ErrExporterShutdown
		}
		return ctx.Err()
	}
	return e.exporter.Export(e.sequencer.nextContext(ctx), metrics)
}

func (e *sequencedMetricExporter) Shutdown(ctx context.Context) error {
	e.shutdown.Store(true)
	return e.exporter.Shutdown(ctx)
}

func (e *sequencedMetricExporter) ForceFlush(ctx context.Context) error {
	return e.exporter.ForceFlush(ctx)
}
