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
	Unsealed(context.Context, string) (archive.UnsealedArchive, error)
	Bootstrap(context.Context, string, func(archive.BootstrapHeader, archive.BootstrapBatch) error) (archive.BootstrapResult, error)
	Traces(context.Context, string, archive.StreamOptions, func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error)
	Logs(context.Context, string, archive.StreamOptions, func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error)
	Metrics(context.Context, string, archive.StreamOptions, func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error)
}

type archiveFrontend interface {
	idtui.AgentRestorer
	idtui.Frontend
}

// appliedRestorePlan is a frozen selection from the applied canonical frontend
// projection: the archive's sealed roster, or for Cloud the records and recipe
// closure it observed.
type appliedRestorePlan struct {
	plan    []dagui.AgentRestore
	rebuild func(string) (string, error)
}

func (p appliedRestorePlan) AgentRestorePlan() []dagui.AgentRestore          { return p.plan }
func (p appliedRestorePlan) EncodedIDForCallDigest(d string) (string, error) { return p.rebuild(d) }

// appliedArchivePlan selects the sealed roster from the applied bootstrap. The
// engine verified the roster against its producer witness when it sealed the
// archive.
func appliedArchivePlan(fe idtui.AgentRestorer, completion archive.Completion) (appliedRestorePlan, []agentcontrol.Subscription, error) {
	plan := appliedRestorePlan{rebuild: fe.EncodedIDForCallDigest}
	agents, edges, err := fe.AgentControl()
	if err != nil {
		return plan, nil, err
	}
	roster := make(map[agentcontrol.Key]bool, len(completion.Agents))
	for _, a := range completion.Agents {
		roster[a.Key] = true
	}
	subscriptions := make(map[agentcontrol.EdgeKey]bool, len(completion.Subscriptions))
	for _, s := range completion.Subscriptions {
		subscriptions[s.Key] = true
	}
	for _, a := range agents {
		if roster[a.Key] {
			plan.plan = append(plan.plan, dagui.RestoreEntryFromControl(a))
		}
	}
	// Removal tombstones are part of the roster, but are never installed.
	var active []agentcontrol.Subscription
	for _, edge := range edges {
		if subscriptions[edge.EdgeKey] && len(edge.States) != 0 {
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
		SealAt:    seal,
		HighWater: enginetel.ArchiveHighWater{Spans: header.HighWater.Spans, Logs: header.HighWater.Logs, Metrics: header.HighWater.Metrics},
	}, nil
}

// archiveUnavailableError is a local archive failure from before any of the
// bootstrap was applied, so another source may still be tried.
type archiveUnavailableError struct{ error }

func (e *archiveUnavailableError) Unwrap() error { return e.error }

// restoreArchive applies the bootstrap and restores the agent graph, then
// imports history in the background. The returned cleanup cancels and joins
// background import on exit.
func restoreArchive(ctx context.Context, source archiveRestoreSource, fe archiveFrontend, target restoreTarget, req traceRestore) (func(), error) {
	var importer *enginetel.ArchiveTraceImporter
	var cut enginetel.ArchiveCut
	result, err := source.Bootstrap(ctx, req.traceID, func(header archive.BootstrapHeader, batch archive.BootstrapBatch) error {
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
		return importer.ImportAndWait(ctx, cut, enginetel.ArchiveImportBatch{Logs: batch.Logs})
	})
	if err != nil {
		err = archiveRestoreError(req.traceID, err)
		if importer == nil {
			err = &archiveUnavailableError{err}
		}
		return nil, err
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
		return importArchiveRemainder(ctx, source, req.traceID, importer, cut)
	}, func(err error) {
		restoreNotice(ctx, fmt.Sprintf("historical telemetry import incomplete: %v; restored agents remain usable", err))
	}), nil
}

func archiveRestoreError(traceID string, err error) error {
	if archive.IsCleanMiss(err) {
		return fmt.Errorf("trace %s has no retained engine archive: %w", traceID, err)
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

// importArchiveRemainder streams each signal from cursor 0 to the cut for
// scrollback. Log history re-delivers the call payloads bootstrap already
// applied; the frontend ignores digests it already has, and a sealed archive's
// history never includes control records.
func importArchiveRemainder(ctx context.Context, source archiveRestoreSource, traceID string, importer *enginetel.ArchiveTraceImporter, cut enginetel.ArchiveCut) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var resultErr error
	for _, signal := range []enginetel.ArchiveSignal{enginetel.ArchiveSpans, enginetel.ArchiveLogs, enginetel.ArchiveMetrics} {
		wg.Go(func() {
			var opts archive.StreamOptions
			switch signal {
			case enginetel.ArchiveSpans:
				opts.HighWater = cut.HighWater.Spans
			case enginetel.ArchiveLogs:
				opts.HighWater = cut.HighWater.Logs
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
