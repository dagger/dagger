package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/proto"
)

var (
	traceA = trace.TraceID{0xa}
	traceB = trace.TraceID{0xb}
	traceC = trace.TraceID{0xc}
)

func tracedTestRecord(traceID trace.TraceID, body string) sdklog.Record {
	var rec sdklog.Record
	rec.SetTraceID(traceID)
	rec.SetBody(log.StringValue(body))
	return rec
}

func tracedTestSpan(traceID trace.TraceID, spanID byte) sdktrace.ReadOnlySpan {
	return tracetest.SpanStub{
		Name: fmt.Sprint(spanID),
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID, SpanID: trace.SpanID{spanID}, TraceFlags: trace.FlagsSampled,
		}),
	}.Snapshot()
}

func parseExportHeader(t *testing.T, header string) (string, uint64) {
	t.Helper()
	writer, seq, ok := strings.Cut(header, "/")
	require.True(t, ok, "header %q is writer/seq", header)
	parsed, err := uuid.Parse(writer)
	require.NoError(t, err)
	require.Equal(t, parsed.String(), writer, "the writer is a canonical UUID")
	n, err := strconv.ParseUint(seq, 10, 64)
	require.NoError(t, err)
	require.Positive(t, n)
	return writer, n
}

// recordingLogExporter records each export call's header metadata and the
// trace IDs and bodies of its records. fail, when set, fails a call.
type recordingLogExporter struct {
	mu    sync.Mutex
	calls []recordedExport
	fail  func(traceID trace.TraceID) error
}

type recordedExport struct {
	metadata exportSequenceMetadata
	stamped  bool
	traces   []trace.TraceID
	bodies   []string
}

func (e *recordingLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	metadata, ok := ctx.Value(exportSequenceContextKey{}).(exportSequenceMetadata)
	call := recordedExport{metadata: metadata, stamped: ok}
	for _, rec := range records {
		call.traces = append(call.traces, rec.TraceID())
		call.bodies = append(call.bodies, rec.Body().AsString())
	}
	e.mu.Lock()
	e.calls = append(e.calls, call)
	e.mu.Unlock()
	if e.fail != nil && len(records) > 0 {
		return e.fail(records[0].TraceID())
	}
	return nil
}

func (*recordingLogExporter) Shutdown(context.Context) error   { return nil }
func (*recordingLogExporter) ForceFlush(context.Context) error { return nil }

// Each trace gets a writer of its own, numbered from 1 independently of the
// others; records with no trace use the base writer and its own counter.
func TestTraceExportSequencerNumbersPerTrace(t *testing.T) {
	s := newTraceExportSequencer()
	a1, b1, a2 := s.next(traceA), s.next(traceB), s.next(traceA)
	none1, none2 := s.next(trace.TraceID{}), s.next(trace.TraceID{})

	require.Equal(t, uint64(1), a1.sequence)
	require.Equal(t, uint64(1), b1.sequence)
	require.Equal(t, uint64(2), a2.sequence)
	require.Equal(t, a1.writerID, a2.writerID)
	require.NotEqual(t, a1.writerID, b1.writerID)
	for _, m := range []exportSequenceMetadata{a1, b1} {
		require.NotEqual(t, s.base.writerID, m.writerID)
		parsed, err := uuid.Parse(m.writerID)
		require.NoError(t, err)
		require.Equal(t, uuid.Version(5), parsed.Version())
	}
	require.Equal(t, exportSequenceMetadata{writerID: s.base.writerID, sequence: 1}, none1)
	require.Equal(t, exportSequenceMetadata{writerID: s.base.writerID, sequence: 2}, none2)

	// Another exporter's writers for the same traces are its own.
	other := newTraceExportSequencer()
	require.NotEqual(t, a1.writerID, other.next(traceA).writerID)
}

// A forgotten trace never restarts its writer's numbering: its next upload
// uses a new writer, starting at 1.
func TestTraceExportSequencerEvictionUsesNewWriter(t *testing.T) {
	s := newTraceExportSequencerWithLimit(2)
	a1 := s.next(traceA)
	a2 := s.next(traceA)
	b1 := s.next(traceB)
	require.Equal(t, uint64(2), a2.sequence)

	s.next(traceA)       // A is now the most recently used
	c1 := s.next(traceC) // evicts B, the least recently used
	b2 := s.next(traceB) // evicts A
	require.Len(t, s.traces, 2, "per-trace state is bounded")
	require.NotEqual(t, b1.writerID, b2.writerID, "an evicted trace gets a new writer")
	require.Equal(t, uint64(1), b2.sequence)
	require.Equal(t, uint64(1), c1.sequence)

	a4 := s.next(traceA)
	require.NotEqual(t, a1.writerID, a4.writerID)
	require.Equal(t, uint64(1), a4.sequence)
	require.Equal(t, uint64(2), s.next(traceB).sequence, "B's new writer keeps counting")
}

// An export whose records span several traces is uploaded as one export call
// per trace, in the order the traces first appear, each with that trace's
// next sequence, and the records of each trace in their order.
func TestSequencedLogExporterSplitsMixedTraces(t *testing.T) {
	probe := &recordingLogExporter{}
	exp := &sequencedLogExporter{sequencer: newTraceExportSequencer(), exporter: probe}
	require.NoError(t, exp.Export(t.Context(), []sdklog.Record{
		tracedTestRecord(traceA, "a1"),
		tracedTestRecord(traceB, "b1"),
		tracedTestRecord(trace.TraceID{}, "none1"),
		tracedTestRecord(traceA, "a2"),
		tracedTestRecord(traceB, "b2"),
	}))
	require.NoError(t, exp.Export(t.Context(), []sdklog.Record{
		tracedTestRecord(traceB, "b3"),
		tracedTestRecord(traceA, "a3"),
	}))
	require.NoError(t, exp.Export(t.Context(), []sdklog.Record{tracedTestRecord(traceA, "a4")}))

	type upload struct {
		trace  trace.TraceID
		seq    uint64
		bodies string
	}
	var got []upload
	writers := map[trace.TraceID]string{}
	for _, call := range probe.calls {
		require.True(t, call.stamped)
		for _, id := range call.traces {
			require.Equal(t, call.traces[0], id, "an upload carries one trace")
		}
		id := call.traces[0]
		if w, ok := writers[id]; ok {
			require.Equal(t, w, call.metadata.writerID, "one writer per trace")
		}
		writers[id] = call.metadata.writerID
		got = append(got, upload{id, call.metadata.sequence, strings.Join(call.bodies, ",")})
	}
	// One export's uploads run concurrently, in no particular order.
	require.ElementsMatch(t, []upload{
		{traceA, 1, "a1,a2"},
		{traceB, 1, "b1,b2"},
		{trace.TraceID{}, 1, "none1"},
		{traceB, 2, "b3"},
		{traceA, 2, "a3"},
		{traceA, 3, "a4"},
	}, got)
	require.Equal(t, exp.sequencer.base.writerID, writers[trace.TraceID{}])
	require.Len(t, map[string]bool{writers[traceA]: true, writers[traceB]: true, writers[trace.TraceID{}]: true}, 3)
}

// Spans are split the same way.
func TestSequencedSpanExporterSplitsMixedTraces(t *testing.T) {
	var (
		mu    sync.Mutex
		calls []recordedExport
	)
	inner := spanExporterFunc(func(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
		metadata, ok := ctx.Value(exportSequenceContextKey{}).(exportSequenceMetadata)
		call := recordedExport{metadata: metadata, stamped: ok}
		for _, span := range spans {
			call.traces = append(call.traces, span.SpanContext().TraceID())
			call.bodies = append(call.bodies, span.Name())
		}
		mu.Lock()
		calls = append(calls, call)
		mu.Unlock()
		return nil
	})
	exp := &sequencedSpanExporter{sequencer: newTraceExportSequencer(), exporter: inner}
	require.NoError(t, exp.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{
		tracedTestSpan(traceA, 1), tracedTestSpan(traceB, 2), tracedTestSpan(traceA, 3),
	}))
	require.NoError(t, exp.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{tracedTestSpan(traceB, 4)}))
	require.Len(t, calls, 3)
	byBodies := map[string]exportSequenceMetadata{}
	for _, call := range calls {
		require.True(t, call.stamped)
		byBodies[strings.Join(call.bodies, ",")] = call.metadata
	}
	a, b1, b2 := byBodies["1,3"], byBodies["2"], byBodies["4"]
	require.Equal(t, uint64(1), a.sequence)
	require.Equal(t, uint64(1), b1.sequence)
	require.Equal(t, uint64(2), b2.sequence)
	require.Equal(t, b1.writerID, b2.writerID)
	require.NotEqual(t, a.writerID, b1.writerID)
}

type spanExporterFunc func(context.Context, []sdktrace.ReadOnlySpan) error

func (f spanExporterFunc) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return f(ctx, spans)
}
func (spanExporterFunc) Shutdown(context.Context) error { return nil }

// One trace's failed upload does not keep the others from being attempted;
// the export's TraceExportError names the traces delivered and carries the
// failures.
func TestSequencedLogExporterPartialFailure(t *testing.T) {
	errB := errors.New("trace B refused")
	probe := &recordingLogExporter{fail: func(id trace.TraceID) error {
		if id == traceB {
			return errB
		}
		return nil
	}}
	exp := &sequencedLogExporter{sequencer: newTraceExportSequencer(), exporter: probe}
	err := exp.Export(t.Context(), []sdklog.Record{
		tracedTestRecord(traceA, "a"), tracedTestRecord(traceB, "b"), tracedTestRecord(traceC, "c"),
	})
	require.ErrorIs(t, err, errB)
	var traceErr *TraceExportError
	require.ErrorAs(t, err, &traceErr)
	require.ElementsMatch(t, []trace.TraceID{traceA, traceC}, traceErr.Delivered)
	require.Equal(t, []error{errB}, traceErr.Errors)
	require.Equal(t, errB.Error(), err.Error())
	var bodies []string
	for _, call := range probe.calls {
		bodies = append(bodies, call.bodies...)
	}
	require.ElementsMatch(t, []string{"a", "b", "c"}, bodies, "every trace's upload is attempted")

	// An export of one trace returns the exporter's error as it is.
	err = exp.Export(t.Context(), []sdklog.Record{tracedTestRecord(traceB, "b")})
	require.Same(t, errB, err)
}

// An export whose context has ended attempts no upload, and none consumes a
// sequence number Cloud would wait on.
func TestSequencedLogExporterStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	probe := &recordingLogExporter{}
	exp := &sequencedLogExporter{sequencer: newTraceExportSequencer(), exporter: probe}
	err := exp.Export(ctx, []sdklog.Record{tracedTestRecord(traceA, "a"), tracedTestRecord(traceB, "b")})
	require.ErrorIs(t, err, context.Canceled)
	var traceErr *TraceExportError
	require.ErrorAs(t, err, &traceErr)
	require.Empty(t, traceErr.Delivered)
	require.Empty(t, probe.calls)

	require.NoError(t, exp.Export(t.Context(), []sdklog.Record{tracedTestRecord(traceB, "b")}))
	require.Equal(t, uint64(1), probe.calls[0].metadata.sequence, "the abandoned upload used no number")
}

// One export's per-trace uploads run concurrently, a bounded number at once.
func TestSequencedLogExporterBoundsConcurrentUploads(t *testing.T) {
	var inFlight, peak atomic.Int32
	slow := logExporterFunc(func(context.Context, []sdklog.Record) error {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		return nil
	})
	exp := &sequencedLogExporter{sequencer: newTraceExportSequencer(), exporter: slow}
	var records []sdklog.Record
	for i := range 3 * maxConcurrentTraceUploads {
		records = append(records, tracedTestRecord(trace.TraceID{1, byte(i)}, fmt.Sprint(i)))
	}
	require.NoError(t, exp.Export(t.Context(), records))
	require.Greater(t, peak.Load(), int32(1), "uploads overlap")
	require.LessOrEqual(t, peak.Load(), int32(maxConcurrentTraceUploads))
}

// An empty export makes no upload and uses no number.
func TestSequencedLogExporterEmptyExport(t *testing.T) {
	probe := &recordingLogExporter{}
	exp := &sequencedLogExporter{sequencer: newTraceExportSequencer(), exporter: probe}
	require.NoError(t, exp.Export(t.Context(), nil))
	require.Len(t, probe.calls, 1)
	require.False(t, probe.calls[0].stamped)
	require.Zero(t, exp.sequencer.base.sequence.Load())
	require.Empty(t, exp.sequencer.traces)
}

// Retries of one per-trace upload, which reuse its export call's context as
// the OTLP exporters' do, resend the same header.
func TestSequencedLogExporterRetriesResendHeader(t *testing.T) {
	var (
		mu      sync.Mutex
		headers []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Get(cloudExportHeader))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	client := newExportSequenceHTTPClient()
	send := func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.Body.Close()
	}
	retrying := logExporterFunc(func(ctx context.Context, _ []sdklog.Record) error {
		for range 3 { // the first attempt and two retries
			if err := send(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	exp := &sequencedLogExporter{sequencer: newTraceExportSequencer(), exporter: retrying}
	require.NoError(t, exp.Export(t.Context(), []sdklog.Record{
		tracedTestRecord(traceA, "a1"), tracedTestRecord(traceB, "b1"),
	}))
	require.NoError(t, exp.Export(t.Context(), []sdklog.Record{tracedTestRecord(traceA, "a2")}))

	require.Len(t, headers, 9)
	counts := map[string]int{}
	for _, h := range headers {
		counts[h]++
	}
	require.Len(t, counts, 3, "three uploads")
	seqs := map[string][]uint64{}
	for h, n := range counts {
		require.Equal(t, 3, n, "each attempt of %s resends its header", h)
		writer, seq := parseExportHeader(t, h)
		seqs[writer] = append(seqs[writer], seq)
	}
	require.Len(t, seqs, 2, "a writer per trace")
	for writer, got := range seqs {
		slices.Sort(got)
		if len(got) == 2 {
			require.Equal(t, []uint64{1, 2}, got, "trace A's writer %s", writer)
		} else {
			require.Equal(t, []uint64{1}, got, "trace B's writer %s", writer)
		}
	}
}

type logExporterFunc func(context.Context, []sdklog.Record) error

func (f logExporterFunc) Export(ctx context.Context, records []sdklog.Record) error {
	return f(ctx, records)
}
func (logExporterFunc) Shutdown(context.Context) error   { return nil }
func (logExporterFunc) ForceFlush(context.Context) error { return nil }

// Through the real Cloud log exporter, a mixed-trace export reaches Cloud as
// one request per trace, and for each trace the sequences Cloud sees from its
// writer are contiguous from 1.
func TestCloudLogExportSequencesPerTrace(t *testing.T) {
	t.Parallel()
	type received struct {
		header string
		traces map[string]bool
		bodies []string
	}
	var (
		mu   sync.Mutex
		reqs []received
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req collogspb.ExportLogsServiceRequest
		require.NoError(t, proto.Unmarshal(body, &req))
		got := received{header: r.Header.Get(cloudExportHeader), traces: map[string]bool{}}
		for _, rl := range req.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				for _, rec := range sl.LogRecords {
					got.traces[fmt.Sprintf("%x", rec.TraceId)] = true
					got.bodies = append(got.bodies, rec.Body.GetStringValue())
				}
			}
		}
		mu.Lock()
		reqs = append(reqs, got)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	ctx := t.Context()
	_, logs, _, err := NewCloudExporters(ctx, &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "engine-token", TokenType: "Basic"},
	}, nil, srv.URL)
	require.NoError(t, err)
	defer logs.Shutdown(ctx) //nolint:errcheck

	traces := []trace.TraceID{traceA, traceB, traceC}
	for round := range 3 {
		var batch []sdklog.Record
		for i := range 6 {
			id := traces[(i+round)%len(traces)]
			batch = append(batch, tracedTestRecord(id, fmt.Sprintf("%s-%d-%d", id, round, i)))
		}
		require.NoError(t, logs.Export(ctx, batch))
	}

	require.Len(t, reqs, 9, "one request per trace per export")
	seqs := map[string][]uint64{}
	writerOf := map[string]string{}
	for _, req := range reqs {
		require.Len(t, req.traces, 1, "a request carries one trace's records")
		require.Len(t, req.bodies, 2)
		var id string
		for k := range req.traces {
			id = k
		}
		writer, seq := parseExportHeader(t, req.header)
		if w, ok := writerOf[id]; ok {
			require.Equal(t, w, writer)
		}
		writerOf[id] = writer
		seqs[id] = append(seqs[id], seq)
	}
	require.Len(t, seqs, 3)
	for id, got := range seqs {
		require.Equal(t, []uint64{1, 2, 3}, got, "trace %s", id)
	}
	require.Len(t, map[string]bool{writerOf[traceA.String()]: true, writerOf[traceB.String()]: true, writerOf[traceC.String()]: true}, 3)
}
