package daggercmd

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
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

// countingSink records what reaches the importer beneath primarySpanSink.
type countingSink struct {
	spans int
}

func (s *countingSink) ImportSpans(context.Context, *coltracepb.ExportTraceServiceRequest) error {
	s.spans++
	return nil
}
func (s *countingSink) ImportLogs(context.Context, *collogspb.ExportLogsServiceRequest) error {
	return nil
}
func (s *countingSink) ImportMetrics(context.Context, *colmetricspb.ExportMetricsServiceRequest) error {
	return nil
}
func (s *countingSink) Seal(context.Context) error { return nil }

var _ cloud.TraceImportSink = (*countingSink)(nil)

func spanExport(spans ...*tracepb.Span) *coltracepb.ExportTraceServiceRequest {
	return &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
		}},
	}
}

// The first parentless span to arrive becomes the primary span, once; later
// roots (a second parentless span in the capture) and later batches leave it
// alone, and every batch still reaches the importer.
func TestPrimarySpanSinkZoomsToTheFirstRootOnce(t *testing.T) {
	prevFrontend := Frontend
	t.Cleanup(func() { Frontend = prevFrontend })

	var primaries []dagui.SpanID
	Frontend = &idtui.FrontendMock{
		SetPrimaryFunc: func(spanID dagui.SpanID) { primaries = append(primaries, spanID) },
	}

	inner := &countingSink{}
	sink := &primarySpanSink{TraceImportSink: inner}
	ctx := context.Background()

	child := &tracepb.Span{SpanId: []byte{1, 1, 1, 1, 1, 1, 1, 1}, ParentSpanId: []byte{9, 9, 9, 9, 9, 9, 9, 9}}
	root := &tracepb.Span{SpanId: []byte{2, 2, 2, 2, 2, 2, 2, 2}}
	otherRoot := &tracepb.Span{SpanId: []byte{3, 3, 3, 3, 3, 3, 3, 3}}

	require.NoError(t, sink.ImportSpans(ctx, spanExport(child)))
	require.Empty(t, primaries, "a child span must not become primary")

	require.NoError(t, sink.ImportSpans(ctx, spanExport(root, otherRoot)))
	require.NoError(t, sink.ImportSpans(ctx, spanExport(root)))

	require.Len(t, primaries, 1)
	var want dagui.SpanID
	copy(want.SpanID[:], root.SpanId)
	require.Equal(t, want, primaries[0])
	require.Equal(t, 3, inner.spans, "every batch must reach the importer")
}

// resolvingFrontend is the slice of TraceFrontend zoomTraceView drives.
type resolvingFrontend struct {
	idtui.TraceFrontend
	resolve func(check, test string) (dagui.SpanID, bool)
	zoomed  []dagui.SpanID
}

func (f *resolvingFrontend) ResolveSpanTarget(check, test string) (dagui.SpanID, bool) {
	return f.resolve(check, test)
}

func (f *resolvingFrontend) ZoomToSpan(id dagui.SpanID) {
	f.zoomed = append(f.zoomed, id)
}

func TestZoomTraceView(t *testing.T) {
	resolved := dagui.SpanID{}
	resolved.SpanID[0] = 7

	t.Run("no selection is a no-op", func(t *testing.T) {
		fe := &resolvingFrontend{resolve: func(string, string) (dagui.SpanID, bool) {
			t.Fatal("nothing to resolve")
			return dagui.SpanID{}, false
		}}
		require.NoError(t, zoomTraceView(fe, spanSelector{}, "trace"))
		require.Empty(t, fe.zoomed)
	})

	t.Run("--span zooms without a lookup", func(t *testing.T) {
		fe := &resolvingFrontend{resolve: func(string, string) (dagui.SpanID, bool) {
			t.Fatal("a raw span needs no lookup")
			return dagui.SpanID{}, false
		}}
		require.NoError(t, zoomTraceView(fe, spanSelector{span: "0102030405060708"}, "trace"))
		require.Len(t, fe.zoomed, 1)
		require.Equal(t, "0102030405060708", fe.zoomed[0].String())
	})

	t.Run("--span rejects a malformed id", func(t *testing.T) {
		fe := &resolvingFrontend{}
		err := zoomTraceView(fe, spanSelector{span: "nope"}, "trace")
		require.ErrorContains(t, err, `invalid span "nope"`)
		require.Empty(t, fe.zoomed)
	})

	t.Run("--check resolves by name", func(t *testing.T) {
		fe := &resolvingFrontend{resolve: func(check, test string) (dagui.SpanID, bool) {
			require.Equal(t, "lint", check)
			require.Empty(t, test)
			return resolved, true
		}}
		require.NoError(t, zoomTraceView(fe, spanSelector{check: "lint"}, "trace"))
		require.Equal(t, []dagui.SpanID{resolved}, fe.zoomed)
	})

	t.Run("--test resolves by name", func(t *testing.T) {
		fe := &resolvingFrontend{resolve: func(check, test string) (dagui.SpanID, bool) {
			require.Empty(t, check)
			require.Equal(t, "TestFoo", test)
			return resolved, true
		}}
		require.NoError(t, zoomTraceView(fe, spanSelector{test: "TestFoo"}, "trace"))
		require.Equal(t, []dagui.SpanID{resolved}, fe.zoomed)
	})

	t.Run("an unknown name fails naming the trace", func(t *testing.T) {
		fe := &resolvingFrontend{resolve: func(string, string) (dagui.SpanID, bool) {
			return dagui.SpanID{}, false
		}}
		err := zoomTraceView(fe, spanSelector{check: "lint"}, "abc123")
		require.EqualError(t, err, `no check named "lint" in trace abc123`)
		err = zoomTraceView(fe, spanSelector{test: "TestFoo"}, "abc123")
		require.EqualError(t, err, `no test named "TestFoo" in trace abc123`)
		require.Empty(t, fe.zoomed)
	})
}
