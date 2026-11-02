package tracesource

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// StoreReader is the engine telemetry store's read-only signal transport (the
// archive client). It also permits reusing a restore's already verified cut
// without downloading its bootstrap again.
type StoreReader interface {
	Traces(context.Context, string, archive.StreamOptions, func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error)
	Logs(context.Context, string, archive.StreamOptions, func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error)
	Metrics(context.Context, string, archive.StreamOptions, func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error)
}

// Store is a trace in the engine's retained telemetry store, as a Source
// pinned to one fixed cut. It answers the same selections Dagger Cloud's OTLP
// endpoints do.
type Store struct {
	reader   StoreReader
	manifest archive.Manifest
	// unsealed reads an interrupted or incomplete archive best-effort, at the
	// end of its store. It has no bootstrap or seal time; its log stream
	// carries the agent control records instead.
	unsealed bool
}

var _ Source = (*Store)(nil)

func NewStore(reader StoreReader, manifest archive.Manifest) *Store {
	return &Store{reader: reader, manifest: manifest}
}

// OpenStore inspects metadata to pin the trace's cut. No bootstrap, recipe,
// agent, or telemetry record is evaluated or imported. An archive whose
// session ended without a seal is opened best-effort at the current end of its
// store, as restore does.
func OpenStore(ctx context.Context, client *archive.Client, traceID string) (*Store, error) {
	manifest, err := client.Inspect(ctx, traceID)
	if err == nil {
		return NewStore(client, manifest), nil
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
	// As `dagger agent -r` warns for the same archive.
	slog.Warn("engine archive was not sealed; showing its latest recorded state best-effort, so the most recent steps may be missing",
		"trace", traceID, "state", request.State)
	return &Store{
		reader:   client,
		manifest: archive.Manifest{TraceID: traceID, State: request.State, HighWater: unsealed.Cut},
		unsealed: true,
	}, nil
}

// FetchBootstrap imports the verified canonical agent roster and its recipe
// closure for agent inspection: a sealed archive's history omits the agent
// control records its bootstrap carries. This is display-only: no recipe is
// evaluated. Call only after source selection; a verification or import
// failure must not fall back.
func (a *Store) FetchBootstrap(ctx context.Context, sink cloud.TraceImportSink) error {
	if a.unsealed || a.manifest.TelemetryOnly {
		// No bootstrap, or one with an empty agent roster.
		return nil
	}
	reader, ok := a.reader.(interface {
		Bootstrap(context.Context, string, func(archive.BootstrapHeader, archive.BootstrapBatch) error) (archive.BootstrapResult, error)
	})
	if !ok {
		return errors.New("store reader does not support verified bootstrap")
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
func (a *Store) SealTime() time.Time {
	if a.manifest.SealAt != nil {
		return *a.manifest.SealAt
	}
	return time.Time{}
}

func (a *Store) options(traceID string, highWater int64) (archive.StreamOptions, error) {
	if traceID != a.manifest.TraceID {
		return archive.StreamOptions{}, fmt.Errorf("store source is pinned to trace %s, not %s", a.manifest.TraceID, traceID)
	}
	return archive.StreamOptions{HighWater: highWater, Unsealed: a.unsealed}, nil
}

// FetchSpans reads a span selection. Listened subtrees beyond what one store
// request accepts are read in successive requests; only the first includes
// the priority roots it asked for.
func (a *Store) FetchSpans(ctx context.Context, traceID string, sel cloud.SpanSelection, cb func(context.Context, *coltracepb.ExportTraceServiceRequest) error) error {
	opts, err := a.options(traceID, a.manifest.HighWater.Spans)
	if err != nil {
		return err
	}
	full := !sel.Incremental && !sel.NoRoot && len(sel.Listen) == 0
	listen, noRoot := sel.Listen, sel.NoRoot
	for {
		chunk := listen[:min(len(listen), archive.MaxListenSpans)]
		listen = listen[len(chunk):]
		opts.Spans = &archive.SpanSelection{Full: full, NoRoot: noRoot, Listen: chunk, DagUIView: sel.DagUIView}
		if _, err := a.reader.Traces(ctx, traceID, opts, func(_ int64, req *coltracepb.ExportTraceServiceRequest) error { return cb(ctx, req) }); err != nil {
			return err
		}
		if len(listen) == 0 {
			return nil
		}
		noRoot = true
	}
}

func (a *Store) FetchLogs(ctx context.Context, traceID string, sel cloud.LogSelection, cb func(context.Context, *collogspb.ExportLogsServiceRequest) error) error {
	opts, err := a.options(traceID, a.manifest.HighWater.Logs)
	if err != nil {
		return err
	}
	opts.Logs = &archive.LogSelection{SpanID: sel.SpanID, Descendants: sel.Descendants, After: sel.After, Records: sel.Records}
	_, err = a.reader.Logs(ctx, traceID, opts, func(_ int64, req *collogspb.ExportLogsServiceRequest) error { return cb(ctx, req) })
	return err
}

// LogSearcher searches a trace's display text where it is stored: the
// engine's telemetry store. Cloud has no search, so a Cloud trace has none.
type LogSearcher interface {
	SearchLogs(ctx context.Context, traceID string, search archive.LogSearch) (archive.LogSearchResult, error)
}

var _ LogSearcher = (*Store)(nil)

// StoreLogSearcher is the archive client's search (archive.Client.SearchLogs).
type StoreLogSearcher interface {
	SearchLogs(ctx context.Context, traceID string, unsealed bool, search archive.LogSearch) (archive.LogSearchResult, error)
}

var _ StoreLogSearcher = (*archive.Client)(nil)

// SearchLogs searches the archive's display text, up to its cut.
func (a *Store) SearchLogs(ctx context.Context, traceID string, search archive.LogSearch) (archive.LogSearchResult, error) {
	searcher, ok := a.reader.(StoreLogSearcher)
	if !ok {
		return archive.LogSearchResult{}, errors.New("store reader does not support log search")
	}
	if _, err := a.options(traceID, a.manifest.HighWater.Logs); err != nil {
		return archive.LogSearchResult{}, err
	}
	return searcher.SearchLogs(ctx, traceID, a.unsealed, search)
}

func (a *Store) FetchTrace(ctx context.Context, traceID string, sink cloud.TraceImportSink) (rerr error) {
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
