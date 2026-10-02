package tracesource

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/internal/cloud"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// ArchiveReader is the read-only signal transport. It also permits reusing a
// restore's already verified cut without downloading its bootstrap again.
type ArchiveReader interface {
	Traces(context.Context, string, archive.StreamOptions, func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error)
	Logs(context.Context, string, archive.StreamOptions, func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error)
	Metrics(context.Context, string, archive.StreamOptions, func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error)
}

// Archive is a display source pinned to one fixed engine cut.
type Archive struct {
	reader   ArchiveReader
	manifest archive.Manifest
	// unsealed reads an interrupted or incomplete archive best-effort, at the
	// end of its store. It has no bootstrap or seal time; its log stream
	// carries the agent control records instead.
	unsealed bool
}

func NewArchive(reader ArchiveReader, manifest archive.Manifest) *Archive {
	return &Archive{reader: reader, manifest: manifest}
}

// OpenArchive inspects metadata to pin the archive's cut. No bootstrap,
// recipe, agent, or telemetry record is evaluated or imported. An archive
// whose session ended without a seal is opened best-effort at the current end
// of its store, as restore does.
func OpenArchive(ctx context.Context, client *archive.Client, traceID string) (Source, error) {
	manifest, err := client.Inspect(ctx, traceID)
	if err == nil {
		return NewArchive(client, manifest), nil
	}
	var request *archive.RequestError
	if !errors.As(err, &request) || !errors.Is(err, archive.ErrState) {
		return nil, err
	}
	if !request.State.Unsealed() {
		// Still being captured: there is no fixed cut to display yet, so a
		// live trace is Cloud's to show.
		return nil, fmt.Errorf("%w: engine archive for trace %s is %s", archive.ErrCleanMiss, traceID, request.State)
	}
	unsealed, err := client.Unsealed(ctx, traceID)
	if err != nil {
		return nil, err
	}
	return &Archive{
		reader:   client,
		manifest: archive.Manifest{TraceID: traceID, State: request.State, HighWater: unsealed.Cut},
		unsealed: true,
	}, nil
}

// FetchBootstrap imports the verified canonical roster and its recipe closure
// for agent inspection. This is display-only: no recipe is evaluated. Call only
// after source selection; a verification or import failure must not fall back.
func (a *Archive) FetchBootstrap(ctx context.Context, sink cloud.TraceImportSink) error {
	if a.unsealed {
		return nil
	}
	reader, ok := a.reader.(interface {
		Bootstrap(context.Context, string, func(archive.BootstrapHeader, archive.BootstrapBatch) error) (archive.BootstrapResult, error)
	})
	if !ok {
		return errors.New("archive reader does not support verified bootstrap")
	}
	_, err := reader.Bootstrap(ctx, a.manifest.TraceID, func(header archive.BootstrapHeader, batch archive.BootstrapBatch) error {
		seal, err := time.Parse(time.RFC3339Nano, header.SealAt)
		if err != nil || !seal.Equal(a.SealTime()) || header.HighWater != a.manifest.HighWater || header.TraceID != a.manifest.TraceID {
			return fmt.Errorf("%w: bootstrap differs from selected display cut", archive.ErrCorrupt)
		}
		if batch.Logs != nil {
			return sink.ImportLogs(ctx, batch.Logs)
		}
		return nil
	})
	return err
}

// SealTime is the archive's independent capture boundary, unlike a Cloud
// display's best-effort seal inferred from the received records. It is zero
// for an unsealed archive.
func (a *Archive) SealTime() time.Time {
	if a.manifest.SealAt != nil {
		return *a.manifest.SealAt
	}
	return time.Time{}
}

func (a *Archive) options(traceID string, highWater int64) (archive.StreamOptions, error) {
	if traceID != a.manifest.TraceID {
		return archive.StreamOptions{}, fmt.Errorf("archive is pinned to trace %s, not %s", a.manifest.TraceID, traceID)
	}
	return archive.StreamOptions{HighWater: highWater, Unsealed: a.unsealed}, nil
}

func (a *Archive) FetchSpans(ctx context.Context, traceID string, sel cloud.SpanSelection, cb func(context.Context, *coltracepb.ExportTraceServiceRequest) error) error {
	opts, err := a.options(traceID, a.manifest.HighWater.Spans)
	if err != nil {
		return err
	}
	opts.Spans = &archive.SpanSelection{Full: !sel.Incremental && !sel.NoRoot && len(sel.Listen) == 0, NoRoot: sel.NoRoot, Listen: sel.Listen, DagUIView: sel.DagUIView}
	_, err = a.reader.Traces(ctx, traceID, opts, func(_ int64, req *coltracepb.ExportTraceServiceRequest) error { return cb(ctx, req) })
	return err
}

func (a *Archive) FetchLogs(ctx context.Context, traceID string, sel cloud.LogSelection, cb func(context.Context, *collogspb.ExportLogsServiceRequest) error) error {
	opts, err := a.options(traceID, a.manifest.HighWater.Logs)
	if err != nil {
		return err
	}
	opts.Logs = &archive.LogSelection{SpanID: sel.SpanID, Descendants: sel.Descendants, After: sel.After, Records: sel.Records}
	_, err = a.reader.Logs(ctx, traceID, opts, func(_ int64, req *collogspb.ExportLogsServiceRequest) error { return cb(ctx, req) })
	return err
}

func (a *Archive) FetchTrace(ctx context.Context, traceID string, sink cloud.TraceImportSink) (rerr error) {
	// Match Cloud's sink contract: even a failed display import seals the spans
	// it received, without claiming success or changing to another source.
	defer func() { rerr = errors.Join(rerr, sink.Seal(ctx)) }()
	// Sequential callbacks preserve the sink contract without an extra queue.
	opts, err := a.options(traceID, a.manifest.HighWater.Spans)
	if err != nil {
		return err
	}
	if _, err := a.reader.Traces(ctx, traceID, opts, func(_ int64, req *coltracepb.ExportTraceServiceRequest) error { return sink.ImportSpans(ctx, req) }); err != nil {
		return err
	}
	opts.HighWater = a.manifest.HighWater.Logs
	if _, err := a.reader.Logs(ctx, traceID, opts, func(_ int64, req *collogspb.ExportLogsServiceRequest) error { return sink.ImportLogs(ctx, req) }); err != nil {
		return err
	}
	opts.HighWater = a.manifest.HighWater.Metrics
	if _, err := a.reader.Metrics(ctx, traceID, opts, func(_ int64, req *colmetricspb.ExportMetricsServiceRequest) error {
		return sink.ImportMetrics(ctx, req)
	}); err != nil {
		return err
	}
	return nil
}
