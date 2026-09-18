package daggercmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/telemetryattrs"
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
)

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

	err := traceRun(&cobra.Command{}, []string{"2f123ba77bf7bd2d4db2f70ed20613e8"})
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
