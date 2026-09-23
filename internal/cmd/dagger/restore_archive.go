package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// archiveRestoreSource keeps source selection separate from graph restoration.
// A future Cloud source must supply the same verified finality and fixed cut.
type archiveRestoreSource interface {
	Acquire(context.Context, string) (func(), error)
	Bootstrap(context.Context, string, string, func(archive.BootstrapHeader, archive.BootstrapBatch) error) (archive.BootstrapResult, error)
	Traces(context.Context, string, archive.StreamOptions, func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error)
	Logs(context.Context, string, archive.StreamOptions, func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error)
	Metrics(context.Context, string, archive.StreamOptions, func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error)
}

type archiveFrontend interface {
	idtui.AgentRestorer
	idtui.Frontend
}

// appliedRestorePlan is a frozen, verified selection from the applied canonical
// frontend projection, not an alternate checkpoint representation.
type appliedRestorePlan struct {
	plan    []dagui.AgentRestore
	rebuild func(string) (string, error)
}

func (p appliedRestorePlan) AgentRestorePlan() []dagui.AgentRestore          { return p.plan }
func (p appliedRestorePlan) EncodedIDForCallDigest(d string) (string, error) { return p.rebuild(d) }

func appliedArchivePlan(fe idtui.AgentRestorer, completion archive.Completion) (appliedRestorePlan, []agentcontrol.Subscription, error) {
	plan := appliedRestorePlan{rebuild: fe.EncodedIDForCallDigest}
	want, err := completion.Expectation()
	if err != nil {
		return plan, nil, err
	}
	agents, edges, err := fe.AgentControl()
	if err != nil {
		return plan, nil, err
	}
	var index agentcontrol.Index
	for _, a := range agents {
		if _, ok := want.Agents[a.Key]; ok {
			if _, err := index.ApplyAgent(a); err != nil {
				return plan, nil, err
			}
		}
	}
	for _, edge := range edges {
		if _, ok := want.Subscriptions[edge.EdgeKey]; ok {
			if _, err := index.ApplySubscription(edge); err != nil {
				return plan, nil, err
			}
		}
	}
	// This second check establishes application, not just raw transport validity.
	if err := index.Verify(want); err != nil {
		return plan, nil, fmt.Errorf("applied bootstrap differs from verified roster: %w", err)
	}
	for _, a := range index.Agents() {
		if a.Removed {
			continue
		}
		state, err := a.RestoreState()
		if err != nil {
			return plan, nil, err
		}
		plan.plan = append(plan.plan, dagui.AgentRestore{
			Source: a.Key, ID: a.Handle, Name: a.Name, ParentAgentID: a.Parent,
			SnapshotDigest: a.Digest, State: state, Error: a.Failure, LastActivity: a.Activity,
		})
	}
	// Keep removal witnesses in verification above, but never install them.
	var active []agentcontrol.Subscription
	for _, edge := range index.Subscriptions() {
		if len(edge.States) != 0 {
			active = append(active, edge)
		}
	}
	return plan, active, nil
}

func archiveCut(header archive.BootstrapHeader) (enginetel.ArchiveCut, error) {
	seal, err := time.Parse(time.RFC3339Nano, header.SealAt)
	if err != nil {
		return enginetel.ArchiveCut{}, fmt.Errorf("invalid archive seal time: %w", err)
	}
	return enginetel.ArchiveCut{
		Generation: header.Generation, SealAt: seal,
		HighWater: enginetel.ArchiveHighWater{Spans: header.HighWater.Spans, Logs: header.HighWater.Logs, Metrics: header.HighWater.Metrics},
	}, nil
}

// restoreArchive keeps the reader lease from before bootstrap through remainder
// completion. The returned cleanup cancels and joins background import on exit.
func restoreArchive(ctx context.Context, source archiveRestoreSource, fe archiveFrontend, target restoreTarget, req traceRestore) (cleanup func(), rerr error) {
	release, err := source.Acquire(ctx, req.traceID)
	if err != nil {
		return nil, archiveRestoreError(req.traceID, err)
	}
	defer func() {
		if rerr != nil {
			release()
		}
	}()

	var importer *enginetel.ArchiveTraceImporter
	var cut enginetel.ArchiveCut
	result, err := source.Bootstrap(ctx, req.traceID, "", func(header archive.BootstrapHeader, batch archive.BootstrapBatch) error {
		if importer == nil {
			var err error
			cut, err = archiveCut(header)
			if err != nil {
				return err
			}
			importer, err = enginetel.NewArchiveTraceImporter(enginetel.TraceImportSinks{
				Spans: fe.SpanExporter(), Logs: fe.LogExporter(), Metrics: fe.MetricExporter(), Barrier: fe,
			}, cut)
			if err != nil {
				return err
			}
		}
		return importer.ImportAndWait(ctx, cut, enginetel.ArchiveImportBatch{Spans: batch.Traces, Logs: batch.Logs})
	})
	if err != nil {
		return nil, archiveRestoreError(req.traceID, err)
	}
	if importer == nil {
		return nil, errors.New("verified archive bootstrap contains no restore data")
	}
	if err := importer.Wait(ctx, cut); err != nil {
		return nil, err
	}
	plan, subscriptions, err := appliedArchivePlan(fe, result.Header.Completion)
	if err != nil {
		return nil, err
	}
	if err := executeRestoreGraph(ctx, plan, target, req, subscriptions); err != nil {
		return nil, err
	}

	return startHistoricalImport(ctx, func(ctx context.Context) error {
		defer release()
		return importArchiveRemainder(ctx, source, req.traceID, result, importer, cut)
	}, func(err error) {
		restoreNotice(ctx, fmt.Sprintf("historical telemetry import incomplete: %v; restored agents remain usable", err))
	}), nil
}

func archiveRestoreError(traceID string, err error) error {
	if archive.IsCleanMiss(err) {
		return fmt.Errorf("trace %s has no retained engine archive: %w; Cloud does not yet provide verified finality for strict restore; use dagger trace %s to view historical telemetry", traceID, err, traceID)
	}
	return fmt.Errorf("restore engine archive %s: %w", traceID, err)
}

// startHistoricalImport never waits for history on the prompt-ready path. Exit
// cancellation joins the reader before the engine/frontend connection is closed.
func startHistoricalImport(ctx context.Context, run func(context.Context) error, warn func(error)) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := run(ctx); err != nil && ctx.Err() == nil {
			warn(err)
		}
	}()
	return func() { cancel(); <-done }
}

func importArchiveRemainder(ctx context.Context, source archiveRestoreSource, traceID string, result archive.BootstrapResult, importer *enginetel.ArchiveTraceImporter, cut enginetel.ArchiveCut) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var resultErr error
	for _, signal := range []enginetel.ArchiveSignal{enginetel.ArchiveSpans, enginetel.ArchiveLogs, enginetel.ArchiveMetrics} {
		wg.Go(func() {
			opts := archive.StreamOptions{Generation: cut.Generation}
			switch signal {
			case enginetel.ArchiveSpans:
				opts.HighWater = cut.HighWater.Spans
				opts.ExcludeSpanIDs = result.Terminal.Exclusions.SpanIDs
			case enginetel.ArchiveLogs:
				opts.HighWater = cut.HighWater.Logs
				opts.ExcludeLogRowIDs = result.Terminal.Exclusions.LogRowIDs
			case enginetel.ArchiveMetrics:
				opts.HighWater = cut.HighWater.Metrics
			}
			var err error
			for attempt := 0; attempt < 3; attempt++ {
				switch signal {
				case enginetel.ArchiveSpans:
					opts.Cursor, err = source.Traces(ctx, traceID, opts, func(cursor int64, batch *coltracepb.ExportTraceServiceRequest) error {
						return importer.ImportAndWait(ctx, cut, enginetel.ArchiveImportBatch{Spans: batch, Cursor: cursor})
					})
				case enginetel.ArchiveLogs:
					opts.Cursor, err = source.Logs(ctx, traceID, opts, func(cursor int64, batch *collogspb.ExportLogsServiceRequest) error {
						return importer.ImportAndWait(ctx, cut, enginetel.ArchiveImportBatch{Logs: batch, Cursor: cursor})
					})
				case enginetel.ArchiveMetrics:
					opts.Cursor, err = source.Metrics(ctx, traceID, opts, func(cursor int64, batch *colmetricspb.ExportMetricsServiceRequest) error {
						return importer.ImportAndWait(ctx, cut, enginetel.ArchiveImportBatch{Metrics: batch, Cursor: cursor})
					})
				}
				if err == nil || ctx.Err() != nil || !errors.Is(err, archive.ErrTransient) {
					break
				}
				select {
				case <-ctx.Done():
				case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
				}
			}
			if err == nil {
				err = importer.CompleteRemainder(ctx, cut, signal, opts.Cursor)
			} else if ctx.Err() == nil {
				err = errors.Join(err, importer.AbandonRemainder(ctx, cut, signal))
			}
			if err != nil {
				mu.Lock()
				resultErr = errors.Join(resultErr, fmt.Errorf("%s: %w", signal, err))
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return resultErr
}
