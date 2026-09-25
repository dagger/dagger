package tracesource

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

type archiveReader struct {
	spans, logs, metrics []archive.StreamOptions
	failure              error
}

func (r *archiveReader) Traces(_ context.Context, _ string, opts archive.StreamOptions, cb func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error) {
	r.spans = append(r.spans, opts)
	if r.failure != nil {
		return 0, r.failure
	}
	return opts.HighWater, cb(opts.HighWater, &coltracepb.ExportTraceServiceRequest{})
}
func (r *archiveReader) Logs(_ context.Context, _ string, opts archive.StreamOptions, cb func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	r.logs = append(r.logs, opts)
	return opts.HighWater, cb(opts.HighWater, &collogspb.ExportLogsServiceRequest{})
}
func (r *archiveReader) Metrics(_ context.Context, _ string, opts archive.StreamOptions, cb func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error) {
	r.metrics = append(r.metrics, opts)
	return opts.HighWater, cb(opts.HighWater, &colmetricspb.ExportMetricsServiceRequest{})
}

type importSink struct{ seals int }

func (*importSink) ImportSpans(context.Context, *coltracepb.ExportTraceServiceRequest) error {
	return nil
}
func (*importSink) ImportLogs(context.Context, *collogspb.ExportLogsServiceRequest) error { return nil }
func (*importSink) ImportMetrics(context.Context, *colmetricspb.ExportMetricsServiceRequest) error {
	return nil
}
func (s *importSink) Seal(context.Context) error { s.seals++; return nil }

func TestArchiveSelectionsStayPinned(t *testing.T) {
	r := &archiveReader{}
	seal := time.Unix(123, 0)
	s := NewArchive(r, archive.Manifest{TraceID: "trace", Generation: "generation", SealAt: &seal, HighWater: archive.HighWater{Spans: 10, Logs: 20, Metrics: 30}})
	require.Equal(t, seal, s.SealTime())
	cb := func(context.Context, *coltracepb.ExportTraceServiceRequest) error { return nil }
	require.NoError(t, s.FetchSpans(t.Context(), "trace", cloud.SpanSelection{Incremental: true, DagUIView: true}, cb))
	require.Empty(t, r.logs, "priority load must not download log history")
	require.NoError(t, s.FetchSpans(t.Context(), "trace", cloud.SpanSelection{NoRoot: true, Listen: []string{"span"}, DagUIView: true}, cb))
	require.Equal(t, &archive.SpanSelection{NoRoot: true, Listen: []string{"span"}, DagUIView: true}, r.spans[1].Spans)
	for _, opts := range r.spans {
		require.Equal(t, "generation", opts.Generation)
		require.EqualValues(t, 10, opts.HighWater)
	}
	require.NoError(t, s.FetchLogs(t.Context(), "trace", cloud.LogSelection{SpanID: "span", Descendants: true, Records: cloud.LogRecordsLogs}, func(context.Context, *collogspb.ExportLogsServiceRequest) error { return nil }))
	require.Equal(t, &archive.LogSelection{SpanID: "span", Descendants: true, Records: archive.LogRecordsLogs}, r.logs[0].Logs)
	require.Equal(t, "generation", r.logs[0].Generation)
	require.EqualValues(t, 20, r.logs[0].HighWater)
	require.ErrorContains(t, s.FetchSpans(t.Context(), "other", cloud.SpanSelection{}, cb), "pinned")
	require.Len(t, r.spans, 2)
}

type bootstrapReader struct {
	archiveReader
	header archive.BootstrapHeader
	err    error
}

func (r *bootstrapReader) Bootstrap(_ context.Context, _ string, _ string, cb func(archive.BootstrapHeader, archive.BootstrapBatch) error) (archive.BootstrapResult, error) {
	if r.err != nil {
		return archive.BootstrapResult{}, r.err
	}
	return archive.BootstrapResult{}, cb(r.header, archive.BootstrapBatch{Logs: &collogspb.ExportLogsServiceRequest{}})
}

type bootstrapSink struct {
	importSink
	imported bool
}

func (s *bootstrapSink) ImportLogs(context.Context, *collogspb.ExportLogsServiceRequest) error {
	s.imported = true
	return nil
}

func TestArchiveBootstrapUsesSelectedCut(t *testing.T) {
	seal := time.Unix(123, 0).UTC()
	r := &bootstrapReader{header: archive.BootstrapHeader{TraceID: "trace", SourceSession: "session", Generation: "generation", SealAt: seal.Format(time.RFC3339Nano), HighWater: archive.HighWater{Logs: 42}}}
	s := NewArchive(r, archive.Manifest{TraceID: "trace", SourceSession: "session", Generation: "generation", SealAt: &seal, HighWater: r.header.HighWater})
	sink := &bootstrapSink{}
	require.NoError(t, s.FetchBootstrap(t.Context(), sink))
	require.True(t, sink.imported, "agent control must be imported for trace inspection")
	require.Empty(t, r.logs, "bootstrap must not download ordinary log history")
	sink.imported = false
	r.header.HighWater.Logs++
	require.ErrorIs(t, s.FetchBootstrap(t.Context(), sink), archive.ErrCorrupt)
	require.False(t, sink.imported)
	r.err = archive.ErrCleanMiss
	require.ErrorIs(t, s.FetchBootstrap(t.Context(), sink), archive.ErrCleanMiss)
	require.False(t, sink.imported)
}

func TestArchiveWholeTraceAndFailure(t *testing.T) {
	r := &archiveReader{}
	s := NewArchive(r, archive.Manifest{TraceID: "trace", Generation: "generation", HighWater: archive.HighWater{Spans: 10, Logs: 20, Metrics: 30}})
	sink := &importSink{}
	require.NoError(t, s.FetchTrace(t.Context(), "trace", sink))
	require.Nil(t, r.spans[0].Spans)
	require.Nil(t, r.logs[0].Logs)
	require.EqualValues(t, 30, r.metrics[0].HighWater)
	require.Equal(t, 1, sink.seals)
	r.failure = errors.New("corrupt data after source selection")
	require.ErrorIs(t, s.FetchTrace(t.Context(), "trace", sink), r.failure)
	require.Equal(t, 2, sink.seals, "failed display imports seal partial spans without hiding the error")
}
