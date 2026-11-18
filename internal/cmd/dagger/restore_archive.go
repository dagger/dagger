package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/tracesource"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
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
		return importer.ImportAndWait(ctx, enginetel.ArchiveImportBatch{Logs: batch.Logs})
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
	if err := importer.Wait(ctx); err != nil {
		return nil, err
	}
	plan, subscriptions, err := appliedArchivePlan(fe, result.Header.Completion)
	if err != nil {
		return nil, err
	}
	if err := executeRestoreGraph(ctx, plan, target, req, subscriptions); err != nil {
		return nil, err
	}
	if !req.backfill {
		return restoreConversationOnly(ctx, source, fe, req.traceID, importer, cut, req.offerBackfill), nil
	}

	warn := func(err error) {
		restoreNotice(ctx, fmt.Sprintf("historical telemetry import incomplete: %v; restored agents remain usable", err))
	}
	if tf, ok := fe.(idtui.TraceFrontend); ok {
		if traceID, err := trace.TraceIDFromHex(req.traceID); err == nil {
			return restoreArchiveDisplay(ctx, source, fe, tf, dagui.TraceID{TraceID: traceID}, req.traceID, importer, cut, warn), nil
		}
	}
	return startHistoricalImport(ctx, func(ctx context.Context) error {
		return importArchiveRemainder(ctx, source, req.traceID, importer, cut, false)
	}, warn), nil
}

// restoreArchiveDisplay imports history for an interactive frontend, which
// shows the history's display logs on demand, through the loader `dagger
// trace` uses, pinned to the bootstrap's cut. The background import still
// brings the whole span tree and the semantic records (names, progress, call
// payloads), so rows render without their logs. The provider serves this
// trace's spans only: the resumed session's own live spans stream their logs
// in.
func restoreArchiveDisplay(ctx context.Context, source archiveRestoreSource, fe archiveFrontend, tf idtui.TraceFrontend, traceID dagui.TraceID, traceHex string, importer *enginetel.ArchiveTraceImporter, cut enginetel.ArchiveCut, warn func(error)) func() {
	historyCtx, cancel := context.WithCancel(ctx)
	var logFg fetchGroup
	store := archiveStore(source, traceHex, cut)
	loader := newTraceLoaderFor(historyCtx, store, traceHex, fe)
	loader.fullTree = true
	tf.SetTraceLogProvider(traceID, func(id dagui.SpanID, descendants bool) {
		loader.fetchLogs(&logFg, id, descendants)
	})
	setLogCache(tf, traceID, loader, store, traceHex)
	tf.SetFetchWaiter(func() { _ = logFg.Wait() })
	stop := startHistoricalImport(historyCtx, func(ctx context.Context) error {
		return importArchiveRemainder(ctx, source, traceHex, importer, cut, true)
	}, warn)
	// Cancelling first stops queued and in-flight fetches before joining.
	return func() {
		cancel()
		stop()
		_ = logFg.Wait()
	}
}

// archiveStore is a display source pinned to a restore's verified cut.
func archiveStore(source archiveRestoreSource, traceHex string, cut enginetel.ArchiveCut) *tracesource.Store {
	return tracesource.NewStore(source, archive.Manifest{
		TraceID: traceHex,
		SealAt:  &cut.SealAt,
		HighWater: archive.HighWater{
			Spans: cut.HighWater.Spans, Logs: cut.HighWater.Logs, Metrics: cut.HighWater.Metrics,
		},
	})
}

// restoreConversationOnly is a sealed archive's history unless --backfill
// asks for all of it, so a session whose telemetry outgrew the CLI (e.g. it
// ran out of memory) can be resumed at all. Instead of importing
// every archived span, call payload and metadata record, and the metrics, it
// loads the agents' loop and message spans with their direct children, and
// each tool call's execution span, which holds its result -- enough to render
// the conversations -- through the loader `dagger trace` uses, and fetches a
// span's children when it is expanded and its own logs when shown, one level
// at a time.
//
// Of the metrics, only the LLM token counts the cost and token totals sum are
// imported (importLLMTokenMetrics), and the other metrics are lost. Call
// payloads beyond the bootstrap's recipe closure and the loaded spans' own
// load as the frontend needs them (idtui.CallFetcher): to place a rewind's
// abandoned turns, or to branch, edit, open a terminal or focus an agent.
// What a tool call ran, and its rolled-up output, shows only as its spans are
// expanded.
//
// offer, when set, receives the in-session backfill: a call that imports the
// whole history after all, in the background, as --backfill would have.
//
// A frontend that cannot load lazily gets no history at all. The returned
// cleanup cancels and joins the fetches.
func restoreConversationOnly(ctx context.Context, source archiveRestoreSource, fe archiveFrontend, traceHex string, importer *enginetel.ArchiveTraceImporter, cut enginetel.ArchiveCut, offer func(backfill func() error)) func() {
	tf, lazy := fe.(idtui.TraceFrontend)
	if !lazy {
		return func() {}
	}
	parsed, err := trace.TraceIDFromHex(traceHex)
	if err != nil {
		return func() {}
	}
	traceID := dagui.TraceID{TraceID: parsed}
	historyCtx, cancel := context.WithCancel(ctx)
	store := archiveStore(source, traceHex, cut).ConversationOnly()
	loader := newTraceLoaderFor(historyCtx, store, traceHex, fe)
	// The live session's root stays primary, and the imported one gets out
	// of its way (passthrough).
	loader.importer.KeepRoots = false
	loader.keepPrimary = true
	// A tool call rolls up its subtree's output, which is most of a large
	// session's logs: it loads as the tool call is expanded instead.
	loader.ownLogsOnly = true
	// The span provider is the frontend's, not this trace's: the live
	// session's spans arrive on their own, and the archive has none of them.
	loader.importedOnly = true
	var logFg fetchGroup
	tf.SetTraceLogProvider(traceID, func(id dagui.SpanID, descendants bool) {
		loader.fetchLogs(&logFg, id, descendants)
	})
	setLogCache(tf, traceID, loader, store, traceHex)
	tf.SetSpanProvider(loader.listen)
	tf.SetFetchWaiter(func() {
		_ = loader.wait()
		_ = logFg.Wait()
	})
	// Call payloads beyond the bootstrap's closure load as an action needs
	// them (idtui.CallFetcher).
	var callFg fetchGroup
	if cf, ok := fe.(idtui.CallFetcher); ok {
		cf.SetCallProvider(func(digests, known []string) error {
			result := make(chan error, 1)
			callFg.Go(func() error {
				result <- fetchCallClosure(historyCtx, store, traceHex, loader.importer, fe, digests, known)
				return nil
			})
			return <-result
		})
	}
	// tokensDone closes once the token metrics have been imported, or not;
	// tokensTaken says whether the engine served them, so a later /backfill
	// leaves them out rather than count them twice.
	tokensDone := make(chan struct{})
	var tokensTaken atomic.Bool
	stop := startHistoricalImport(historyCtx, func(ctx context.Context) error {
		var metricsErr error
		var metrics sync.WaitGroup
		metrics.Go(func() {
			defer close(tokensDone)
			taken, err := importLLMTokenMetrics(ctx, source, traceHex, importer, cut)
			tokensTaken.Store(taken)
			if err != nil {
				metricsErr = fmt.Errorf("cost totals: %w", err)
			}
		})
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			if err = loader.loadInitial(ctx); err == nil || ctx.Err() != nil || !errors.Is(err, archive.ErrTransient) {
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
			}
		}
		if err != nil && ctx.Err() == nil {
			// Whatever arrived must not render as still running, and stays
			// expandable.
			err = errors.Join(err, loader.seal(ctx))
			loader.abandonInitial()
		}
		metrics.Wait()
		return errors.Join(err, metricsErr)
	}, func(err error) {
		restoreNotice(ctx, fmt.Sprintf("conversation history import incomplete: %v; restored agents remain usable", err))
	})
	// A backfill runs once at a time; one that failed may be run again, one
	// that completed need not be.
	var backfillMu sync.Mutex
	var backfillLoading, backfillLoaded bool
	var stopBackfills []func()
	if offer != nil {
		offer(func() error {
			backfillMu.Lock()
			defer backfillMu.Unlock()
			switch {
			case backfillLoading:
				return errors.New("the session's whole history is already loading")
			case backfillLoaded:
				return errors.New("the session's whole history is already loaded")
			}
			if historyCtx.Err() != nil {
				return historyCtx.Err()
			}
			// A fresh importer: the restore's own already completed its
			// metric stream (the cost totals), and this one streams all of
			// it.
			full, err := enginetel.NewArchiveTraceImporter(enginetel.TraceImportSinks{
				Spans: fe.SpanExporter(), Logs: fe.LogExporter(), Metrics: fe.MetricExporter(), Barrier: fe,
			}, cut)
			if err != nil {
				return err
			}
			loader.loadWholeTree()
			backfillLoading = true
			stopBackfills = append(stopBackfills, startHistoricalImport(historyCtx, func(ctx context.Context) (rerr error) {
				defer func() {
					backfillMu.Lock()
					defer backfillMu.Unlock()
					backfillLoading, backfillLoaded = false, rerr == nil
				}()
				// The token totals sum gauge deltas per series, and a
				// replayed series starts below where the restore left it,
				// which counts as a fresh baseline: the token metrics the
				// restore took would be counted again.
				select {
				case <-tokensDone:
				case <-ctx.Done():
					return ctx.Err()
				}
				var taken []string
				if tokensTaken.Load() {
					taken = dagui.LLMTokenMetricNames
				}
				if err := importArchiveRemainder(ctx, source, traceHex, full, cut, true, taken...); err != nil {
					return err
				}
				loader.treeLoaded()
				return nil
			}, func(err error) {
				restoreNotice(ctx, fmt.Sprintf("historical telemetry import incomplete: %v; /backfill tries again", err))
			}))
			return nil
		})
	}
	return func() {
		cancel()
		stop()
		backfillMu.Lock()
		stops := stopBackfills
		backfillMu.Unlock()
		for _, stopBackfill := range stops {
			stopBackfill()
		}
		_ = loader.wait()
		_ = logFg.Wait()
		_ = callFg.Wait()
	}
}

// fetchCallClosure imports the recipe closures of digests from the archive
// into fe, except those of known calls, and waits until they are applied, for
// an action that rebuilds an ID from payloads a conversation-only history did
// not load.
func fetchCallClosure(ctx context.Context, store *tracesource.Store, traceHex string, importer *enginetel.TraceImporter, fe idtui.AgentRestorer, digests, known []string) error {
	err := store.FetchCallClosure(ctx, traceHex, digests, known, importer.ImportLogs)
	if errors.Is(err, archive.ErrCallClosureUnsupported) {
		return errors.New("the engine predates loading it on demand")
	}
	if err != nil {
		return err
	}
	return fe.WaitForEventLoop(ctx)
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
//
// With lazyLogs, the log stream carries only the semantic records, leaving
// display logs to be fetched on demand, and the span stream adds the dagui
// view's has-logs and child-count hints that lazy expansion needs. The
// metrics named in dropMetrics, already imported, are left out.
func importArchiveRemainder(ctx context.Context, source archiveRestoreSource, traceID string, importer *enginetel.ArchiveTraceImporter, cut enginetel.ArchiveCut, lazyLogs bool, dropMetrics ...string) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var resultErr error
	for _, signal := range []enginetel.ArchiveSignal{enginetel.ArchiveSpans, enginetel.ArchiveLogs, enginetel.ArchiveMetrics} {
		wg.Go(func() {
			var opts archive.StreamOptions
			switch signal {
			case enginetel.ArchiveSpans:
				if lazyLogs {
					opts.Spans = &archive.SpanSelection{Full: true, DagUIView: true}
				}
			case enginetel.ArchiveLogs:
				if lazyLogs {
					opts.Logs = &archive.LogSelection{Records: archive.LogRecordsMetadata}
				}
			}
			if err := importArchiveSignal(ctx, source, traceID, importer, cut, signal, opts, dropMetrics...); err != nil {
				mu.Lock()
				resultErr = errors.Join(resultErr, fmt.Errorf("%s: %w", signal, err))
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return resultErr
}

// importArchiveSignal streams one signal, with opts' selection, from cursor 0
// to the cut, retrying transient failures, then completes or abandons that
// signal's remainder on importer. The metrics named in dropMetrics are left
// out of a metric stream's batches.
func importArchiveSignal(ctx context.Context, source archiveRestoreSource, traceID string, importer *enginetel.ArchiveTraceImporter, cut enginetel.ArchiveCut, signal enginetel.ArchiveSignal, opts archive.StreamOptions, dropMetrics ...string) error {
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
				return importer.ImportAndWait(ctx, enginetel.ArchiveImportBatch{Spans: batch, Cursor: cursor})
			})
		case enginetel.ArchiveLogs:
			opts.Cursor, err = source.Logs(ctx, traceID, opts, func(cursor int64, batch *collogspb.ExportLogsServiceRequest) error {
				return importer.ImportAndWait(ctx, enginetel.ArchiveImportBatch{Logs: batch, Cursor: cursor})
			})
		case enginetel.ArchiveMetrics:
			opts.Cursor, err = source.Metrics(ctx, traceID, opts, func(cursor int64, batch *colmetricspb.ExportMetricsServiceRequest) error {
				dropNamedMetrics(batch, dropMetrics)
				return importer.ImportAndWait(ctx, enginetel.ArchiveImportBatch{Metrics: batch, Cursor: cursor})
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
		return importer.CompleteRemainder(ctx, signal, opts.Cursor)
	}
	if ctx.Err() == nil {
		err = errors.Join(err, importer.AbandonRemainder(ctx, signal))
	}
	return err
}

// dropNamedMetrics removes the metrics named in names from batch.
func dropNamedMetrics(batch *colmetricspb.ExportMetricsServiceRequest, names []string) {
	if len(names) == 0 {
		return
	}
	for _, rm := range batch.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			sm.Metrics = slices.DeleteFunc(sm.Metrics, func(m *metricspb.Metric) bool {
				return slices.Contains(names, m.GetName())
			})
		}
	}
}

// importLLMTokenMetrics imports only the metrics a cost and token total sums,
// for a history that otherwise skips its metrics (restoreConversationOnly): a
// session's metric stream is mostly per-exec resource metrics. An engine that
// predates metric selection would stream every metric instead; that read is
// refused and the totals restart at zero, rather than pulling in the
// whole-session volume a conversation-only restore exists to avoid. taken
// reports whether the engine served them, even if the read then failed part
// way: a later whole-metric import must leave them out, since counting some
// twice is worse than missing some.
func importLLMTokenMetrics(ctx context.Context, source archiveRestoreSource, traceID string, importer *enginetel.ArchiveTraceImporter, cut enginetel.ArchiveCut) (taken bool, err error) {
	err = importArchiveSignal(ctx, source, traceID, importer, cut, enginetel.ArchiveMetrics, archive.StreamOptions{
		Metrics: &archive.MetricSelection{Names: dagui.LLMTokenMetricNames},
	})
	if errors.Is(err, archive.ErrSelectionUnsupported) {
		slog.Debug("not restoring LLM cost totals: the engine cannot select metrics by name", "trace", traceID)
		return false, nil
	}
	return true, err
}
