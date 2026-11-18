package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/cloud/otlpstream"
	"github.com/dagger/dagger/internal/tracesource"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/proto"
)

type sealedTraceSource struct {
	tracesource.Source
	seal time.Time
}

func (s sealedTraceSource) SealTime() time.Time { return s.seal }

func TestTraceLoaderUsesArchiveSeal(t *testing.T) {
	loader, db, _ := testTraceLoader(t)
	seal := time.Unix(123, 0)
	loader.client = sealedTraceSource{seal: seal}
	span := &tracepb.Span{SpanId: []byte{1, 2, 3, 4, 5, 6, 7, 8}, StartTimeUnixNano: uint64(time.Unix(100, 0).UnixNano())}
	require.NoError(t, loader.ingest(t.Context(), spanExport(span)))
	require.NoError(t, loader.seal(t.Context()))
	for _, got := range db.Spans.Map {
		require.Equal(t, seal, got.EndTime)
	}
}

func TestTraceLoaderIgnoresRequestsAfterClose(t *testing.T) {
	loader, _, _ := testTraceLoader(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	loader.ctx = ctx
	var fg fetchGroup
	id := dagui.SpanID{}
	id.SpanID[0] = 1
	loader.fetchLogs(&fg, id, false)
	loader.listen(id)
	require.NoError(t, fg.Wait())
	require.NoError(t, loader.wait())
	require.Empty(t, loader.logReq)
	require.Empty(t, loader.pending)
}

func TestTraceUsesGlobalFrontendOpts(t *testing.T) {
	prevFrontend := Frontend
	prevOpts := opts
	t.Cleanup(func() {
		Frontend = prevFrontend
		opts = prevOpts
	})

	opts = dagui.FrontendOpts{
		Debug:             true,
		Silent:            true,
		Verbosity:         dagui.ShowSpammyVerbosity,
		RevealNoisySpans:  true,
		ExpandCompleted:   true,
		OpenWeb:           true,
		DotOutputFilePath: "trace.dot",
		DotFocusField:     "focus",
		DotShowInternal:   true,
	}

	var gotOpts dagui.FrontendOpts
	Frontend = &idtui.FrontendMock{
		RunFunc: func(ctx context.Context, runOpts dagui.FrontendOpts, f func(context.Context) (cleanups.CleanupF, error)) error {
			gotOpts = runOpts
			return nil
		},
	}

	err := traceRun(&cobra.Command{}, "2f123ba77bf7bd2d4db2f70ed20613e8", spanSelector{}, &traceViewOptions{})
	require.NoError(t, err)

	require.Equal(t, opts, gotOpts)
}

func TestFetchGroupGoDuringWait(t *testing.T) {
	var fg fetchGroup

	release := make(chan struct{})
	fg.Go(func() error {
		<-release
		return nil
	})

	waitDone := make(chan error, 1)
	go func() { waitDone <- fg.Wait() }()

	// Spawn more fetches while Wait is parked -- the TUI event loop does this
	// when the user expands spans during the run goroutine's drains. With a
	// sync.WaitGroup/errgroup this interleaving is documented misuse.
	for range 10 {
		fg.Go(func() error { return nil })
	}
	fg.Go(func() error { return errors.New("boom") })
	fg.Go(func() error { return context.Canceled }) // interrupt: not a failure

	close(release)
	err := <-waitDone
	require.ErrorContains(t, err, "boom")
	require.NotErrorIs(t, err, context.Canceled)
}

func spanExport(spans ...*tracepb.Span) *coltracepb.ExportTraceServiceRequest {
	return &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
		}},
	}
}

func boolAttr(key string, val bool) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: val}}}
}

func intAttr(key string, val int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: val}}}
}

// testTraceLoader is a loader whose importer feeds a private DB rather than
// the global frontend's exporters, with Frontend mocked for SetPrimary.
func testTraceLoader(t *testing.T) (*traceLoader, *dagui.DB, *[]dagui.SpanID) {
	t.Helper()
	prevFrontend := Frontend
	t.Cleanup(func() { Frontend = prevFrontend })

	db := dagui.NewDB()
	primaries := new([]dagui.SpanID)
	Frontend = &idtui.FrontendMock{
		SetPrimaryFunc:     func(spanID dagui.SpanID) { *primaries = append(*primaries, spanID) },
		SpanExporterFunc:   func() sdktrace.SpanExporter { return db },
		LogExporterFunc:    func() sdklog.Exporter { return db.LogExporter() },
		MetricExporterFunc: func() sdkmetric.Exporter { return db.MetricExporter() },
	}
	return newTraceLoader(context.Background(), nil, "trace-id"), db, primaries
}

// ingest reads the loader's bookkeeping off Cloud's dagui-view attributes --
// the partial flag and the newest update time, which bound later backfills --
// and zooms to the first parentless span once; later roots (a second
// parentless span in the capture) and later batches leave it alone.
func TestTraceLoaderIngestTracksViewAttrsAndPrimary(t *testing.T) {
	loader, db, primaries := testTraceLoader(t)
	ctx := context.Background()

	child := &tracepb.Span{
		SpanId: []byte{1, 1, 1, 1, 1, 1, 1, 1}, ParentSpanId: []byte{9, 9, 9, 9, 9, 9, 9, 9},
		Attributes: []*commonpb.KeyValue{
			boolAttr(telemetryattrs.UIPartialAttr, false),
			intAttr(telemetryattrs.UIUpdateTimeUnixNanoAttr, 100),
		},
	}
	require.NoError(t, loader.ingest(ctx, spanExport(child)))
	require.Empty(t, *primaries, "a child span must not become primary")
	require.False(t, loader.partial)
	require.Equal(t, time.Unix(0, 100), *loader.spanUpdateTime)

	root := &tracepb.Span{
		SpanId: []byte{2, 2, 2, 2, 2, 2, 2, 2},
		Attributes: []*commonpb.KeyValue{
			boolAttr(telemetryattrs.UIPartialAttr, true),
			intAttr(telemetryattrs.UIUpdateTimeUnixNanoAttr, 300),
			intAttr(telemetryattrs.UIChildCountAttr, 7),
		},
	}
	otherRoot := &tracepb.Span{
		SpanId: []byte{3, 3, 3, 3, 3, 3, 3, 3},
		Attributes: []*commonpb.KeyValue{
			intAttr(telemetryattrs.UIUpdateTimeUnixNanoAttr, 200),
		},
	}
	require.NoError(t, loader.ingest(ctx, spanExport(root, otherRoot)))
	require.NoError(t, loader.ingest(ctx, spanExport(root)))

	require.Len(t, *primaries, 1)
	var want dagui.SpanID
	copy(want.SpanID[:], root.SpanId)
	require.Equal(t, want, (*primaries)[0])
	require.True(t, loader.filter[want], "the root's children arrive with the initial load")
	require.True(t, loader.partial, "a partial row marks the load partial")
	require.Equal(t, time.Unix(0, 300), *loader.spanUpdateTime, "the newest update time wins")

	// The spans reached the sink, with the view's child count ingested so the
	// root stays expandable even though its children were not fetched.
	rootSpan := db.Spans.Map[want]
	require.NotNil(t, rootSpan)
	require.Equal(t, 7, rootSpan.ChildCount)
	require.Equal(t, want, db.PrimarySpan)
	require.False(t, rootSpan.Passthrough, "the trace's own root must stay a real root")
}

// A listen before the initial load completes is deferred, not decided: the
// tree's partiality isn't known yet, and latching the id as fetched would
// swallow an expand that raced the load.
func TestTraceLoaderListenDefersUntilInitialLoad(t *testing.T) {
	loader, _, _ := testTraceLoader(t)

	id := dagui.SpanID{}
	id.SpanID[0] = 5
	loader.listen(id)
	require.Equal(t, []dagui.SpanID{id}, loader.pending)
	require.False(t, loader.filter[id])

	// Once loaded and NOT partial, expanding is purely local: nothing is
	// fetched and nothing is left pending.
	loader.mu.Lock()
	loader.initialLoaded = true
	loader.pending = nil
	loader.mu.Unlock()
	loader.listen(id)
	require.True(t, loader.filter[id], "a decided listen latches its id")
	require.NoError(t, loader.wait())
}

func TestRekeyLogRecords(t *testing.T) {
	id := dagui.SpanID{}
	id.SpanID[7] = 4
	req := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{
					{SpanId: []byte{1, 1, 1, 1, 1, 1, 1, 1}},
					{SpanId: nil},
				},
			}},
		}},
	}
	rekeyLogRecords(req, id)
	for _, record := range req.ResourceLogs[0].ScopeLogs[0].LogRecords {
		require.Equal(t, id.SpanID[:], record.SpanId)
	}
}

// fetchLogs against a fake Cloud: a span's own logs are one request for
// every record; a roll-up is two -- the subtree's text output, re-keyed to
// the span so it renders though the descendants aren't loaded, then the
// subtree's call payloads, which land as-is. Each span is fetched once.
func TestTraceLoaderFetchLogs(t *testing.T) {
	var mu sync.Mutex
	var requests []url.Values
	descendant := []byte{9, 9, 9, 9, 9, 9, 9, 9}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Query())
		mu.Unlock()
		w.Header().Set("Content-Type", otlpstream.ContentType)
		fw := otlpstream.NewFrameWriter(w)
		if r.URL.Query().Get("records") != cloud.LogRecordsCallPayloads {
			payload, err := proto.Marshal(&collogspb.ExportLogsServiceRequest{
				ResourceLogs: []*logspb.ResourceLogs{{
					ScopeLogs: []*logspb.ScopeLogs{{
						LogRecords: []*logspb.LogRecord{{
							SpanId: descendant,
							Body:   &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "hello\n"}},
						}},
					}},
				}},
			})
			require.NoError(t, err)
			_ = fw.WriteData(payload)
		}
		_ = fw.WriteTerminal()
	}))
	t.Cleanup(srv.Close)

	client, err := cloud.NewOTLPClient(context.Background(), &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "token", TokenType: "Bearer"},
	})
	require.NoError(t, err)
	client, err = client.WithBaseURL(srv.URL)
	require.NoError(t, err)

	loader, db, _ := testTraceLoader(t)
	loader.client = client

	target := dagui.SpanID{}
	target.SpanID[0] = 4
	var fg fetchGroup
	loader.fetchLogs(&fg, target, true)
	loader.fetchLogs(&fg, target, true) // deduped
	require.NoError(t, fg.Wait())

	mu.Lock()
	require.Equal(t, []url.Values{
		{"span_id": {target.String()}, "descendants": {"true"}, "records": {"logs"}},
		{"span_id": {target.String()}, "descendants": {"true"}, "records": {"call_payloads"}},
	}, requests)
	requests = nil
	mu.Unlock()

	// The descendant's output was attributed to the target.
	require.True(t, db.Spans.Map[target].HasLogs, "the rolled-up output must land on the span it was fetched for")
	var orphan dagui.SpanID
	copy(orphan.SpanID[:], descendant)
	require.Nil(t, db.Spans.Map[orphan], "the unloaded descendant must not be conjured by its logs")

	own := dagui.SpanID{}
	own.SpanID[0] = 5
	loader.fetchLogs(&fg, own, false)
	require.NoError(t, fg.Wait())
	mu.Lock()
	require.Equal(t, []url.Values{{"span_id": {own.String()}}}, requests, "own logs are every record, one request")
	mu.Unlock()
}

// logSource serves each span's selected records, and reports what each
// request's callback kept of them.
type logSource struct {
	tracesource.Source
	records map[string][]*logspb.LogRecord // span_id -> records it selects
	// fail holds the errors a span's next requests fail with, before
	// importing anything.
	fail map[string][]error
	// started, when set, makes every request signal it, then block until
	// cancelled.
	started chan struct{}
	mu      sync.Mutex
	sels    []cloud.LogSelection
	kept    [][]byte // span IDs of the records the callbacks left to import
}

func (s *logSource) FetchLogs(ctx context.Context, _ string, sel cloud.LogSelection, cb func(context.Context, *collogspb.ExportLogsServiceRequest) error) error {
	if s.started != nil {
		close(s.started)
		<-ctx.Done()
		return ctx.Err()
	}
	s.mu.Lock()
	s.sels = append(s.sels, sel)
	var fail error
	if errs := s.fail[sel.SpanID]; len(errs) > 0 {
		fail, s.fail[sel.SpanID] = errs[0], errs[1:]
	}
	s.mu.Unlock()
	if fail != nil {
		return fail
	}
	req := &collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		ScopeLogs: []*logspb.ScopeLogs{{LogRecords: slices.Clone(s.records[sel.SpanID])}},
	}}}
	err := cb(ctx, req)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range req.ResourceLogs[0].ScopeLogs[0].LogRecords {
		s.kept = append(s.kept, record.SpanId)
	}
	return err
}

// With the whole span tree loaded (a restored session's history), a roll-up
// is one request for every record class, whose records keep their own span
// IDs. Spans another fetch owns are left out of it, and the spans it imported
// are not fetched again on their own.
func TestTraceLoaderFullTreeRollUp(t *testing.T) {
	rollUp, child, grandchild := []byte{1, 0, 0, 0, 0, 0, 0, 1}, []byte{2, 0, 0, 0, 0, 0, 0, 2}, []byte{3, 0, 0, 0, 0, 0, 0, 3}
	id := func(b []byte) dagui.SpanID {
		var id dagui.SpanID
		copy(id.SpanID[:], b)
		return id
	}
	text := func(span []byte) *logspb.LogRecord {
		return &logspb.LogRecord{SpanId: span, Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "out\n"}}}
	}
	source := &logSource{records: map[string][]*logspb.LogRecord{
		id(grandchild).String(): {text(grandchild)},
		id(rollUp).String():     {text(rollUp), text(child), text(grandchild)},
	}}
	loader, _, _ := testTraceLoader(t)
	loader.client = source
	loader.fullTree = true

	var fg fetchGroup
	loader.fetchLogs(&fg, id(grandchild), false)
	require.NoError(t, fg.Wait())
	loader.fetchLogs(&fg, id(rollUp), true)
	require.NoError(t, fg.Wait())
	loader.fetchLogs(&fg, id(child), false)
	loader.fetchLogs(&fg, id(grandchild), true)
	require.NoError(t, fg.Wait())

	require.Equal(t, []cloud.LogSelection{
		{SpanID: id(grandchild).String()},
		{SpanID: id(rollUp).String(), Descendants: true},
	}, source.sels, "a roll-up is one request for every record; owned spans are not fetched again")
	require.Equal(t, [][]byte{grandchild, rollUp, child}, source.kept,
		"records keep their own span IDs, without the records another fetch imported")
}

// A transient failure before anything was imported is retried. A fetch that
// fails anyway releases what it owned: an enclosing roll-up imports the
// span's records, and the span can be fetched again.
func TestTraceLoaderFailedFetchReleasesSpans(t *testing.T) {
	parent, child := []byte{1, 0, 0, 0, 0, 0, 0, 1}, []byte{2, 0, 0, 0, 0, 0, 0, 2}
	id := func(b []byte) dagui.SpanID {
		var id dagui.SpanID
		copy(id.SpanID[:], b)
		return id
	}
	text := func(span []byte) *logspb.LogRecord {
		return &logspb.LogRecord{SpanId: span, Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "out\n"}}}
	}
	transient := fmt.Errorf("%w: engine busy", archive.ErrTransient)
	source := &logSource{
		records: map[string][]*logspb.LogRecord{
			id(child).String():  {text(child)},
			id(parent).String(): {text(parent), text(child)},
		},
		fail: map[string][]error{
			id(child).String():  {transient, transient, transient, errors.New("gone")},
			id(parent).String(): {transient},
		},
	}
	loader, _, _ := testTraceLoader(t)
	loader.client = source
	loader.fullTree = true
	var fg fetchGroup

	// Three transient attempts, then a fetch of nothing more.
	loader.fetchLogs(&fg, id(child), false)
	require.ErrorIs(t, fg.Wait(), archive.ErrTransient)
	require.Len(t, source.sels, 3, "a transient failure is retried twice")

	// The roll-up is retried once, and imports the child's records too.
	var rollUpFg fetchGroup
	loader.fetchLogs(&rollUpFg, id(parent), true)
	require.NoError(t, rollUpFg.Wait())
	require.Equal(t, [][]byte{parent, child}, source.kept, "a failed fetch must not keep owning its span")

	// A span released by a permanent failure is fetched on its next request.
	loader2, _, _ := testTraceLoader(t)
	loader2.client = source
	loader2.fullTree = true
	source.sels = nil
	var fg2 fetchGroup
	loader2.fetchLogs(&fg2, id(child), false)
	require.Error(t, fg2.Wait())
	var fg3 fetchGroup
	loader2.fetchLogs(&fg3, id(child), false)
	require.NoError(t, fg3.Wait())
	require.Len(t, source.sels, 2, "a permanent failure is not retried, but the span can be requested again")
}

// Exit cancels in-flight fetches; that is not worth a warning.
func TestTraceLoaderCancelledFetchIsQuiet(t *testing.T) {
	warnings := captureRestoreWarnings(t)
	loader, _, _ := testTraceLoader(t)
	ctx, cancel := context.WithCancel(t.Context())
	loader.ctx = ctx
	source := &logSource{started: make(chan struct{})}
	loader.client = source
	var fg fetchGroup
	loader.fetchLogs(&fg, dagui.SpanID{SpanID: [8]byte{1}}, false)
	<-source.started
	cancel()
	require.NoError(t, fg.Wait())
	require.NotContains(t, warnings.String(), "error streaming span logs")
}

// resolvingFrontend is the slice of TraceFrontend resolveTraceTarget drives.
type resolvingFrontend struct {
	idtui.TraceFrontend
	resolve func(check, test string) (dagui.SpanID, bool)
}

func (f *resolvingFrontend) ResolveSpanTarget(check, test string) (dagui.SpanID, bool) {
	return f.resolve(check, test)
}

func TestResolveTraceTarget(t *testing.T) {
	resolved := dagui.SpanID{}
	resolved.SpanID[0] = 7

	t.Run("--span needs no lookup and stands alone", func(t *testing.T) {
		fe := &resolvingFrontend{resolve: func(string, string) (dagui.SpanID, bool) {
			t.Fatal("a raw span needs no lookup")
			return dagui.SpanID{}, false
		}}
		id, descendants, err := resolveTraceTarget(fe, spanSelector{span: "0102030405060708"}, "trace")
		require.NoError(t, err)
		require.Equal(t, "0102030405060708", id.String())
		require.False(t, descendants)
	})

	t.Run("--span rejects a malformed id", func(t *testing.T) {
		_, _, err := resolveTraceTarget(&resolvingFrontend{}, spanSelector{span: "nope"}, "trace")
		require.ErrorContains(t, err, `invalid span "nope"`)
	})

	t.Run("--check resolves by name and rolls up", func(t *testing.T) {
		fe := &resolvingFrontend{resolve: func(check, test string) (dagui.SpanID, bool) {
			require.Equal(t, "lint", check)
			require.Empty(t, test)
			return resolved, true
		}}
		id, descendants, err := resolveTraceTarget(fe, spanSelector{check: "lint"}, "trace")
		require.NoError(t, err)
		require.Equal(t, resolved, id)
		require.True(t, descendants)
	})

	t.Run("--test resolves by name and rolls up", func(t *testing.T) {
		fe := &resolvingFrontend{resolve: func(check, test string) (dagui.SpanID, bool) {
			require.Empty(t, check)
			require.Equal(t, "TestFoo", test)
			return resolved, true
		}}
		id, descendants, err := resolveTraceTarget(fe, spanSelector{test: "TestFoo"}, "trace")
		require.NoError(t, err)
		require.Equal(t, resolved, id)
		require.True(t, descendants)
	})

	t.Run("an unknown name fails naming the trace", func(t *testing.T) {
		fe := &resolvingFrontend{resolve: func(string, string) (dagui.SpanID, bool) {
			return dagui.SpanID{}, false
		}}
		_, _, err := resolveTraceTarget(fe, spanSelector{check: "lint"}, "abc123")
		require.EqualError(t, err, `no check named "lint" in trace abc123`)
		_, _, err = resolveTraceTarget(fe, spanSelector{test: "TestFoo"}, "abc123")
		require.EqualError(t, err, `no test named "TestFoo" in trace abc123`)
	})
}
