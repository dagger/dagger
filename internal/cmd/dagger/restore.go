package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"dagger.io/dagger"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// Trace-backed agent resume restores a past session's agents, conversations,
// lifecycle state, and TUI history into the new interactive session.
//
// Everything below the CLI is already built: internal/cloud fetches the trace,
// engine/telemetry imports it into the live frontend's own exporters,
// dagui.DB projects the restore plan, and Agent.rehydrate re-creates each
// instance's runtime entry from its committed conversation. This is the
// wiring, and the order it happens in — which is load-bearing rather than
// incidental (§3.1b, recommendation §6.2's seed race):
//
//  1. Fetch, under a span so the wait is visible, before the interactive loop
//     starts.
//  2. Rebuild EVERY entry's anchor. An anchor that will not rebuild fails the
//     command here, before anything has been re-hydrated, so a refused
//     restore leaves the engine untouched.
//  3. Re-hydrate every entry, before anything can dispatch a tool or bind an
//     LLM: the chief's recorded chain binds its workers by ID, and a dispatch
//     that resolves against a registry missing one is an error (§4.2) rather
//     than an amnesiac twin.
//  4. Then attach, adopting each restored instance as a conversation.
//  5. Then focus (§3.1c). No Replay: the imported spans ARE the scrollback
//     (§5.1.4).

// traceRestore describes one resume request. An empty traceID asks the source
// to select from its retained traces.
type traceRestore struct {
	traceID       string
	timeout       time.Duration
	agent         string
	source        traceRestoreSource
	archiveSource string
}

// traceRestoreSource is the transport boundary for trace selection and
// bootstrap. Bootstrap must not return until every restore-critical record and
// its terminal barrier have been acknowledged by the frontend. The returned
// remainder starts only after transactional restore succeeds.
type traceRestoreSource interface {
	Select(context.Context) (string, error)
	Bootstrap(context.Context, string, time.Duration) (traceRestoreRemainder, error)
	// ArchiveSource reports the source Bootstrap ultimately selected; a
	// composite source may change this while falling back on a clean miss.
	ArchiveSource() string
}

type traceRestoreRemainder interface {
	Start(context.Context)
}

type cloudTraceRestoreSource struct{}

func (cloudTraceRestoreSource) ArchiveSource() string { return "cloud" }

func (cloudTraceRestoreSource) Select(context.Context) (string, error) {
	return "", errors.New("engine archive picker unavailable; resume an explicit trace with dagger agent -r=<trace-id>")
}

func (cloudTraceRestoreSource) Bootstrap(ctx context.Context, traceID string, timeout time.Duration) (traceRestoreRemainder, error) {
	// Cloud's temporary adapter fetches the whole trace. It is deliberately
	// strict: a stall or any other stream error fails before runtime creation.
	return nil, fetchTraceIntoFrontend(ctx, traceID, timeout)
}

type archiveRestoreClient interface {
	ListAll(context.Context, archive.ListOptions) ([]archive.Manifest, error)
	Bootstrap(context.Context, string, string, func(archive.BootstrapHeader, archive.BootstrapBatch) error) (archive.BootstrapResult, error)
	Traces(context.Context, string, archive.StreamOptions, func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error)
	Logs(context.Context, string, archive.StreamOptions, func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error)
	Metrics(context.Context, string, archive.StreamOptions, func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error)
}

type archivePicker func(context.Context, []archive.Manifest) (string, error)

type archiveTraceImporter interface {
	ImportAndWait(context.Context, enginetel.ArchiveCut, enginetel.ArchiveImportBatch) error
	Wait(context.Context, enginetel.ArchiveCut) error
	CompleteRemainder(context.Context, enginetel.ArchiveCut, enginetel.ArchiveSignal, int64) error
	AbandonRemainder(context.Context, enginetel.ArchiveCut, enginetel.ArchiveSignal) error
}

type engineTraceRestoreSource struct {
	archive archiveRestoreClient
	cloud   traceRestoreSource
	picker  archivePicker

	selectedGeneration string
	archiveSource      string
	retryAttempts      int
	retryDelay         func(context.Context, int) error
	warn               func(context.Context, error)
	newImporter        func(enginetel.ArchiveCut) (archiveTraceImporter, error)
}

func newEngineTraceRestoreSource(client archiveRestoreClient) *engineTraceRestoreSource {
	return &engineTraceRestoreSource{
		archive:       client,
		cloud:         cloudTraceRestoreSource{},
		picker:        selectEngineArchive,
		retryAttempts: 3,
		retryDelay:    archiveRetryDelay,
		warn: func(ctx context.Context, err error) {
			restoreNotice(ctx, err.Error())
		},
		newImporter: func(cut enginetel.ArchiveCut) (archiveTraceImporter, error) {
			return newFrontendArchiveImporter(cut)
		},
	}
}

func (s *engineTraceRestoreSource) ArchiveSource() string { return s.archiveSource }

func (s *engineTraceRestoreSource) Select(ctx context.Context) (string, error) {
	manifests, err := s.archive.ListAll(ctx, archive.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list engine archives: %w", err)
	}
	closed := slices.DeleteFunc(manifests, func(manifest archive.Manifest) bool {
		return manifest.State != archive.StateClosed
	})
	sort.SliceStable(closed, func(i, j int) bool {
		return closed[i].StartedAt.After(closed[j].StartedAt)
	})
	if len(closed) == 0 {
		return "", errors.New("the connected engine has no closed agent archives to resume")
	}
	picker := s.picker
	if picker == nil {
		picker = selectEngineArchive
	}
	traceID, err := picker(ctx, closed)
	if err != nil || traceID == "" {
		return traceID, err
	}
	for _, manifest := range closed {
		if manifest.TraceID == traceID {
			s.selectedGeneration = manifest.Generation
			return traceID, nil
		}
	}
	return "", fmt.Errorf("archive picker selected unknown trace %q", traceID)
}

func selectEngineArchive(ctx context.Context, manifests []archive.Manifest) (string, error) {
	options := make([]huh.Option[string], 0, len(manifests))
	for _, manifest := range manifests {
		label := fmt.Sprintf("%s  %s  %s", manifest.Title,
			manifest.StartedAt.Local().Format("2006-01-02 15:04"), manifest.TraceID)
		options = append(options, huh.NewOption(label, manifest.TraceID))
	}
	var selected string
	form := idtui.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Resume an agent session").
			Height(min(len(options)+2, 12)).
			Filtering(true).
			Options(options...).
			Value(&selected),
	))
	if err := Frontend.HandleForm(ctx, form); err != nil {
		return "", err
	}
	return selected, nil
}

func (s *engineTraceRestoreSource) Bootstrap(ctx context.Context, traceID string, timeout time.Duration) (traceRestoreRemainder, error) {
	s.archiveSource = ""
	archiveClient := s.archive
	if timeout > 0 {
		if configurable, ok := archiveClient.(interface {
			WithStallTimeout(time.Duration) *archive.Client
		}); ok {
			archiveClient = configurable.WithStallTimeout(timeout)
		}
	}
	var importer archiveTraceImporter
	var cut enginetel.ArchiveCut
	result, err := archiveClient.Bootstrap(ctx, traceID, s.selectedGeneration,
		func(header archive.BootstrapHeader, batch archive.BootstrapBatch) error {
			if importer == nil {
				var err error
				cut, err = archiveCut(header)
				if err != nil {
					return err
				}
				importer, err = s.newArchiveImporter(cut)
				if err != nil {
					return err
				}
			}
			return importer.ImportAndWait(ctx, cut, enginetel.ArchiveImportBatch{
				Spans: batch.Traces,
				Logs:  batch.Logs,
			})
		})
	if err != nil {
		if !archive.IsCleanMiss(err) {
			return nil, fmt.Errorf("bootstrap engine archive %s: %w", traceID, err)
		}
		if s.cloud == nil {
			return nil, fmt.Errorf("bootstrap engine archive %s: %w", traceID, err)
		}
		remainder, cloudErr := s.cloud.Bootstrap(ctx, traceID, timeout)
		if cloudErr != nil {
			return nil, cloudErr
		}
		s.archiveSource = s.cloud.ArchiveSource()
		return remainder, nil
	}
	if importer == nil {
		cut, err = archiveCut(result.Header)
		if err != nil {
			return nil, err
		}
		importer, err = s.newArchiveImporter(cut)
		if err != nil {
			return nil, err
		}
	}
	// Bootstrap's terminal carries no OTLP batch, but it is part of the strict
	// acknowledgment contract. Do not project the restore plan until all imports
	// dispatched before it have crossed the frontend event loop.
	if err := importer.Wait(ctx, cut); err != nil {
		return nil, fmt.Errorf("acknowledge engine archive bootstrap terminal: %w", err)
	}
	s.archiveSource = "engine"
	return &engineTraceRemainder{
		archive: archiveClient, traceID: traceID, cut: cut, importer: importer,
		exclusions: result.Terminal.Exclusions,
		attempts:   s.retryAttempts, delay: s.retryDelay, warn: s.warn,
	}, nil
}

func (s *engineTraceRestoreSource) newArchiveImporter(cut enginetel.ArchiveCut) (archiveTraceImporter, error) {
	if s.newImporter == nil {
		return newFrontendArchiveImporter(cut)
	}
	return s.newImporter(cut)
}

func archiveCut(header archive.BootstrapHeader) (enginetel.ArchiveCut, error) {
	sealAt, err := time.Parse(time.RFC3339Nano, header.SealAt)
	if err != nil {
		return enginetel.ArchiveCut{}, fmt.Errorf("parse archive seal time: %w", err)
	}
	return enginetel.ArchiveCut{
		Generation: header.Generation,
		HighWater: enginetel.ArchiveHighWater{
			Spans: header.HighWater.Spans, Logs: header.HighWater.Logs, Metrics: header.HighWater.Metrics,
		},
		SealAt: sealAt,
	}, nil
}

func newFrontendArchiveImporter(cut enginetel.ArchiveCut) (*enginetel.ArchiveTraceImporter, error) {
	barrier, ok := Frontend.(enginetel.TraceImportBarrier)
	if !ok {
		return nil, fmt.Errorf("--resume needs a frontend event-loop barrier: %T cannot acknowledge archive imports", Frontend)
	}
	return enginetel.NewArchiveTraceImporter(enginetel.TraceImportSinks{
		Spans: Frontend.SpanExporter(), Logs: Frontend.LogExporter(),
		Metrics: Frontend.MetricExporter(), Barrier: barrier,
	}, cut)
}

const archiveHistoryWarning = "Previous session resumed, but some historical progress could not be loaded."

type engineTraceRemainder struct {
	archive    archiveRestoreClient
	traceID    string
	cut        enginetel.ArchiveCut
	importer   archiveTraceImporter
	exclusions archive.BootstrapExclusions
	attempts   int
	delay      func(context.Context, int) error
	warn       func(context.Context, error)
	warnOnce   sync.Once
}

func (r *engineTraceRemainder) Start(ctx context.Context) {
	for _, signal := range []enginetel.ArchiveSignal{enginetel.ArchiveSpans, enginetel.ArchiveLogs, enginetel.ArchiveMetrics} {
		go r.stream(ctx, signal)
	}
}

func (r *engineTraceRemainder) stream(ctx context.Context, signal enginetel.ArchiveSignal) {
	cursor := int64(0)
	attempts := r.attempts
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		cursor, err = r.streamOnce(ctx, signal, cursor)
		if err == nil {
			err = r.importer.CompleteRemainder(ctx, r.cut, signal, cursor)
			if err == nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		if !errors.Is(err, archive.ErrTransient) || attempt+1 == attempts {
			break
		}
		if r.delay != nil {
			if delayErr := r.delay(ctx, attempt); delayErr != nil {
				return
			}
		}
	}
	abandonErr := r.importer.AbandonRemainder(ctx, r.cut, signal)
	slog.Error("failed to load archived session history", "signal", signal, "error", err, "abandon_error", abandonErr)
	r.warnOnce.Do(func() {
		if r.warn != nil {
			r.warn(ctx, errors.New(archiveHistoryWarning))
		}
	})
}

func (r *engineTraceRemainder) streamOnce(ctx context.Context, signal enginetel.ArchiveSignal, cursor int64) (int64, error) {
	highWater := int64(0)
	opts := archive.StreamOptions{Generation: r.cut.Generation, Cursor: cursor}
	switch signal {
	case enginetel.ArchiveSpans:
		highWater = r.cut.HighWater.Spans
		opts.HighWater = highWater
		opts.ExcludeSpanIDs = r.exclusions.SpanIDs
		return r.archive.Traces(ctx, r.traceID, opts, func(_ int64, batch *coltracepb.ExportTraceServiceRequest) error {
			return r.importer.ImportAndWait(ctx, r.cut, enginetel.ArchiveImportBatch{Spans: batch})
		})
	case enginetel.ArchiveLogs:
		highWater = r.cut.HighWater.Logs
		opts.HighWater = highWater
		opts.ExcludeLogRowIDs = r.exclusions.LogRowIDs
		return r.archive.Logs(ctx, r.traceID, opts, func(_ int64, batch *collogspb.ExportLogsServiceRequest) error {
			return r.importer.ImportAndWait(ctx, r.cut, enginetel.ArchiveImportBatch{Logs: batch})
		})
	case enginetel.ArchiveMetrics:
		highWater = r.cut.HighWater.Metrics
		opts.HighWater = highWater
		return r.archive.Metrics(ctx, r.traceID, opts, func(_ int64, batch *colmetricspb.ExportMetricsServiceRequest) error {
			return r.importer.ImportAndWait(ctx, r.cut, enginetel.ArchiveImportBatch{Metrics: batch})
		})
	default:
		return cursor, fmt.Errorf("unknown archive signal %q", signal)
	}
}

func archiveRetryDelay(ctx context.Context, attempt int) error {
	delay := 100 * time.Millisecond << min(attempt, 3)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

type traceRestoreSourceContextKey struct{}

func withTraceRestoreSource(ctx context.Context, source traceRestoreSource) context.Context {
	return context.WithValue(ctx, traceRestoreSourceContextKey{}, source)
}

func traceRestoreSourceFromContext(ctx context.Context) traceRestoreSource {
	source, _ := ctx.Value(traceRestoreSourceContextKey{}).(traceRestoreSource)
	return source
}

// agentRestoreSource is the frontend seam the plan is read through
// (idtui.AgentRestorer). Named here so the executor can be driven by a fake.
type agentRestoreSource interface {
	AgentRestorePlan() []dagui.AgentRestore
	EncodedIDForCallDigest(digest string) (string, error)
}

// restoreTarget is the session half of a restore. It is an interface so the
// load-bearing order — every rehydrate attempt before any attach, focus last —
// is testable without an engine, in the style of session_agent_test.go's fake
// runtime.
type restoreTarget interface {
	// Rehydrate creates one runtime from its rebuilt conversation.
	Rehydrate(ctx context.Context, entry dagui.AgentRestore, snapshotID string) (string, error)
	// Adopt makes a re-hydrated instance a conversation of this session.
	Adopt(ctx context.Context, entry dagui.AgentRestore, agentID string) error
	// Focus points the prompt at one of the adopted conversations.
	Focus(ctx context.Context, entry dagui.AgentRestore, agentID string) error
}

// restoreFromTrace runs the whole of §5.3 against the live session.
func restoreFromTrace(ctx context.Context, handler *shellCallHandler, req traceRestore) (rerr error) {
	// Both startup resume and .resume enter here. The interactive command may
	// resume only before any agent has been prompted, spawned, or restored.
	if !handler.llmSession.Pristine() {
		return errors.New("the interactive session has already started agent work; start a new session with dagger agent -r")
	}

	source := req.source
	if source == nil {
		source = cloudTraceRestoreSource{}
	}
	if req.traceID == "" {
		traceID, err := source.Select(ctx)
		if err != nil {
			return err
		}
		if traceID == "" {
			return nil // picker aborted
		}
		req.traceID = traceID
	}

	// The plan and anchor rebuilds are reads of the frontend's DB, which the
	// frontend owns single-threaded. A frontend with no span DB cannot restore.
	restorer, ok := Frontend.(idtui.AgentRestorer)
	if !ok {
		return fmt.Errorf("--resume needs a frontend that keeps the trace: %T cannot restore from one", Frontend)
	}

	ctx, span := Tracer().Start(ctx, "resuming trace "+req.traceID, telemetry.Reveal())
	defer telemetry.EndWithCause(span, &rerr)

	remainder, err := source.Bootstrap(ctx, req.traceID, req.timeout)
	if err != nil {
		return err
	}
	req.archiveSource = source.ArchiveSource()

	// Every resume starts from a fresh unbound LLM. Restored snapshot recipes
	// carry their original workspace and composition; the destination checkout
	// must not become their reset base.
	baseID, err := freshAgentBase(ctx, handler.dag)
	if err != nil {
		return err
	}
	base := dagger.Ref[*dagger.LLM](handler.dag, dagger.ID(baseID))
	if err := handler.llmSession.Target().setInitialLLM(base); err != nil {
		return err
	}

	// Import failures leave the session pristine. Once plan execution begins it
	// may create runtimes, so reserve the session against another .resume first.
	if !handler.llmSession.BeginRestore() {
		return errors.New("the interactive session has already started agent work; start a new session with dagger agent -r")
	}
	target := &sessionRestore{
		dag:     handler.dag,
		session: handler.llmSession,
		base:    handler.llmSession.Target().initialLLM,
	}
	if err := executeRestorePlan(ctx, restorer, target, req); err != nil {
		return err
	}
	if remainder != nil {
		remainder.Start(ctx)
	}
	return nil
}

// traceFetcher is the Cloud fetch seam used to test strict startup policy
type traceFetcher func(context.Context, string, time.Duration) error

// fetchTraceForRestore keeps Cloud whole-trace startup strict. Even when an
// idle timeout is configured, a stalled stream is incomplete bootstrap data
// and must fail before any runtime is created.
func fetchTraceForRestore(ctx context.Context, req traceRestore, fetch traceFetcher) (bool, error) {
	err := fetch(ctx, req.traceID, req.timeout)
	return false, err
}

// fetchTraceIntoFrontend streams the whole trace into the LIVE frontend's own
// exporters (§5.1): one DB then holds both sessions, which is what makes the
// restored session the old session's TUI plus a live prompt.
//
// Two things the reference trace client does and this must not: Seal (the
// fetch does it internally, once the span stream has drained) and SetPrimary
// (§5.1.1 — the live CLI's root stays the primary span, and repointing it
// would take the restore plan's live-vs-imported discriminator with it).
func fetchTraceIntoFrontend(ctx context.Context, traceID string, idleTimeout time.Duration) error {
	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return fmt.Errorf("cloud auth: %w", err)
	}
	client, err := cloud.NewOTLPClient(ctx, cloudAuth)
	if err != nil {
		return fmt.Errorf("cloud client: %w", err)
	}
	if idleTimeout > 0 {
		client = client.WithStallTimeout(idleTimeout)
	}
	sink := enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans:   Frontend.SpanExporter(),
		Logs:    Frontend.LogExporter(),
		Metrics: Frontend.MetricExporter(),
	})
	if err := client.FetchTrace(ctx, traceID, sink); err != nil {
		return fmt.Errorf("fetch trace %s: %w", traceID, err)
	}
	// The largest fetch in the product, on the path where the user is
	// waiting: --debug says how much came down. Through slog rather than
	// stderr, which the interactive frontend owns.
	slog.Debug("restored trace from cloud", "trace", traceID, "stats", client.StatsSummary())
	return nil
}

// restoredAgent is one entry of the plan, with the handle its anchor rebuilt
// to and (after re-hydration) the handle on its restored runtime.
type restoredAgent struct {
	entry      dagui.AgentRestore
	snapshotID string
	agentID    string
}

func executeRestorePlan(ctx context.Context, src agentRestoreSource, dst restoreTarget, req traceRestore) error {
	plan := src.AgentRestorePlan()
	if len(plan) == 0 {
		return fmt.Errorf("trace %s carries no agents to restore: "+
			"either nothing in it published an agent loop, or its agents are already restored in this session",
			req.traceID)
	}

	// Phase 1: resolve every anchor before creating any runtime. Resolution is
	// best effort per agent; failures are visible but do not prevent independent
	// conversations from being restored.
	restoring := make([]restoredAgent, 0, len(plan))
	failures := make([]string, 0)
	for _, entry := range plan {
		snapshotID, err := resolveAnchor(src, entry)
		if err != nil {
			failure := fmt.Sprintf("agent %q (%s): %v", entry.Name, entry.ID, err)
			failures = append(failures, failure)
			restoreNotice(ctx, "skipped unrestorable "+failure)
			continue
		}
		restoring = append(restoring, restoredAgent{entry: entry, snapshotID: snapshotID})
	}
	if len(restoring) == 0 {
		return fmt.Errorf("no agent in trace %s could be restored:\n  %s",
			req.traceID, strings.Join(failures, "\n  "))
	}

	// Phase 2: attempt every runtime before attaching any conversation. Parents
	// are attempted first, and a child only retains its recorded parent when that
	// parent's runtime was successfully restored; otherwise it starts a valid
	// top-level lineage for future checkpoints.
	restoring = orderRestoringParentsFirst(restoring)
	rehydrated := make([]restoredAgent, 0, len(restoring))
	rehydratedIDs := make(map[string]struct{}, len(restoring))
	for _, restored := range restoring {
		if _, parentRestored := rehydratedIDs[restored.entry.ParentAgentID]; !parentRestored {
			restored.entry.ParentAgentID = ""
		}
		agentID, err := dst.Rehydrate(ctx, restored.entry, restored.snapshotID)
		if err != nil {
			failure := fmt.Sprintf("agent %q (%s): %v", restored.entry.Name, restored.entry.ID, err)
			failures = append(failures, failure)
			restoreNotice(ctx, "skipped agent that could not be re-hydrated: "+failure)
			continue
		}
		if agentID == "" {
			failure := fmt.Sprintf("agent %q (%s): the engine returned no handle on the restored agent",
				restored.entry.Name, restored.entry.ID)
			failures = append(failures, failure)
			restoreNotice(ctx, "skipped agent that could not be re-hydrated: "+failure)
			continue
		}
		restored.agentID = agentID
		rehydrated = append(rehydrated, restored)
		rehydratedIDs[restored.entry.ID] = struct{}{}
	}
	if len(rehydrated) == 0 {
		return fmt.Errorf("no agent in trace %s could be restored:\n  %s",
			req.traceID, strings.Join(failures, "\n  "))
	}

	// Phase 3: attach every successfully restored runtime. An attachment failure
	// skips only that conversation; unrelated restored conversations remain
	// available and become the focus pool.
	attached := make([]restoredAgent, 0, len(rehydrated))
	attachFailures := make([]string, 0)
	for _, restored := range rehydrated {
		if err := dst.Adopt(ctx, restored.entry, restored.agentID); err != nil {
			failure := fmt.Sprintf("agent %q (%s): %v", restored.entry.Name, restored.entry.ID, err)
			attachFailures = append(attachFailures, failure)
			restoreNotice(ctx, "skipped restored agent that could not be attached: "+failure)
			continue
		}
		attached = append(attached, restored)
	}
	if len(attached) == 0 {
		return fmt.Errorf("no restored agent in trace %s could be attached:\n  %s",
			req.traceID, strings.Join(attachFailures, "\n  "))
	}

	emitResumeAgentsBridge(ctx, attached, req.archiveSource)

	// Phase 4: point the prompt at an attached conversation. If --agent named a
	// skipped conversation, selection emits a notice and falls back automatically
	// rather than aborting after successful mutations.
	focus, notice, err := selectFocus(attached, req.agent)
	if err != nil {
		return err
	}
	if notice != "" {
		restoreNotice(ctx, notice)
	}
	return dst.Focus(ctx, focus.entry, focus.agentID)
}

// orderRestoringParentsFirst returns a stable parent-before-child attempt order.
// A malformed ancestry cycle is broken deterministically; whichever member is
// attempted first becomes top-level unless its parent was already restored.
func orderRestoringParentsFirst(restoring []restoredAgent) []restoredAgent {
	remaining := slices.Clone(restoring)
	known := make(map[string]struct{}, len(remaining))
	for _, restored := range remaining {
		known[restored.entry.ID] = struct{}{}
	}
	attempted := make(map[string]struct{}, len(remaining))
	ordered := make([]restoredAgent, 0, len(remaining))
	for len(remaining) > 0 {
		ready := -1
		for i, restored := range remaining {
			parent := restored.entry.ParentAgentID
			_, parentKnown := known[parent]
			_, parentAttempted := attempted[parent]
			if parent == "" || !parentKnown || parentAttempted {
				ready = i
				break
			}
		}
		if ready == -1 {
			ready = 0
		}
		restored := remaining[ready]
		ordered = append(ordered, restored)
		attempted[restored.entry.ID] = struct{}{}
		remaining = slices.Delete(remaining, ready, ready+1)
	}
	return ordered
}

// resolveAnchor turns an entry's snapshot digest into the encoded ID of the
// conversation to re-hydrate it from.
func resolveAnchor(src agentRestoreSource, entry dagui.AgentRestore) (string, error) {
	if !entry.Restorable() {
		return "", entry.Err
	}
	snapshotID, err := src.EncodedIDForCallDigest(entry.SnapshotDigest)
	if err != nil {
		return "", fmt.Errorf("agent %q (%s) cannot be restored from anchor %s: %w",
			entry.Name, entry.ID, entry.SnapshotDigest, err)
	}
	return snapshotID, nil
}

// selectFocus applies §3.1c: focus the agent with no agent above it. Several
// top-level agents means the most recently active one, and saying so;
// --agent <name|id> overrides the whole rule.
func selectFocus(restored []restoredAgent, want string) (restoredAgent, string, error) {
	if want != "" {
		focus, _, err := focusByName(restored, want)
		if err == nil {
			return focus, "", nil
		}
		fallback, notice, fallbackErr := selectFocus(restored, "")
		if fallbackErr != nil {
			return restoredAgent{}, "", fallbackErr
		}
		fallbackNotice := fmt.Sprintf("could not honor --agent: %v; focusing %s instead", err, focusLabel(fallback))
		if notice != "" && notice != focusLabel(fallback) {
			fallbackNotice += "; " + notice
		}
		return fallback, fallbackNotice, nil
	}

	// A restored agent is top-level when its parent is absent from the attached
	// set. This includes children whose parent failed resolution, rehydration, or
	// attachment.
	attachedIDs := make(map[string]struct{}, len(restored))
	for _, restored := range restored {
		attachedIDs[restored.entry.ID] = struct{}{}
	}
	toplevel := slices.DeleteFunc(slices.Clone(restored), func(r restoredAgent) bool {
		_, parentAttached := attachedIDs[r.entry.ParentAgentID]
		return r.entry.ParentAgentID != "" && parentAttached
	})
	if len(toplevel) == 0 {
		return restoredAgent{}, "", errors.New("restored trace has no focusable agent")
	}
	if len(toplevel) == 1 {
		return toplevel[0], focusLabel(toplevel[0]), nil
	}

	// Most recently active, which is NOT the plan's order: the plan is
	// ordered by when each agent first appeared, and a session's own
	// conversation is usually the first to appear and the last to speak.
	sort.SliceStable(toplevel, func(i, j int) bool {
		return toplevel[j].entry.LastActivity.Before(toplevel[i].entry.LastActivity)
	})
	focus := toplevel[0]
	return focus, fmt.Sprintf(
		"%d top-level agents restored; focusing %s, which was active most recently — "+
			"pass --agent <name|id> to focus another",
		len(toplevel), focusLabel(focus)), nil
}

func focusByName(restored []restoredAgent, want string) (restoredAgent, string, error) {
	// Instance IDs first: a name is a display label that two agents may
	// legitimately share, and an ID never is.
	for _, r := range restored {
		if r.entry.ID == want {
			return r, "", nil
		}
	}
	var matched []restoredAgent
	for _, r := range restored {
		if r.entry.Name == want {
			matched = append(matched, r)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], "", nil
	case 0:
		names := make([]string, 0, len(restored))
		for _, r := range restored {
			names = append(names, focusLabel(r))
		}
		return restoredAgent{}, "", fmt.Errorf("no restored agent named %q; the trace restored: %s",
			want, strings.Join(names, ", "))
	default:
		ids := make([]string, 0, len(matched))
		for _, r := range matched {
			ids = append(ids, r.entry.ID)
		}
		return restoredAgent{}, "", fmt.Errorf(
			"%d restored agents are named %q; name one by instance ID instead: %s",
			len(matched), want, strings.Join(ids, ", "))
	}
}

func focusLabel(r restoredAgent) string {
	return fmt.Sprintf("%s (%s)", r.entry.Name, r.entry.ID)
}

func emitResumeAgentsBridge(ctx context.Context, restored []restoredAgent, archiveSource string) {
	var (
		links         []trace.Link
		sourceTraceID string
	)
	restoredIDs := make(map[string]struct{}, len(restored))
	for _, restored := range restored {
		restoredIDs[restored.entry.ID] = struct{}{}
	}
	for _, restored := range restored {
		_, parentRestored := restoredIDs[restored.entry.ParentAgentID]
		if restored.entry.ParentAgentID != "" && parentRestored {
			continue
		}
		source := restored.entry.SourceContext
		if !source.TraceID.IsValid() || !source.SpanID.IsValid() {
			continue
		}
		if sourceTraceID == "" {
			sourceTraceID = source.TraceID.String()
		}
		links = append(links, trace.Link{
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: source.TraceID.TraceID,
				SpanID:  source.SpanID.SpanID,
				Remote:  true,
			}),
			Attributes: []attribute.KeyValue{
				attribute.String(telemetry.LinkPurposeAttr, telemetryattrs.LinkPurposeContinuation),
			},
		})
	}

	opts := []trace.SpanStartOption{telemetry.Reveal()}
	if len(links) > 0 {
		opts = append(opts,
			trace.WithLinks(links...),
			trace.WithAttributes(attribute.String(telemetryattrs.AgentResumeSourceTraceIDAttr, sourceTraceID)),
		)
	}
	if archiveSource != "" {
		opts = append(opts, trace.WithAttributes(
			attribute.String(telemetryattrs.AgentResumeArchiveSourceAttr, archiveSource)))
	}
	_, span := Tracer().Start(ctx, "resume agents", opts...)
	span.End()
}

// restoreNotice surfaces a line about the restore in the TUI. Revealed rather
// than logged: it describes a decision the user may want to override, and it
// has to survive the restore span it is emitted under.
func restoreNotice(ctx context.Context, msg string) {
	_, span := Tracer().Start(ctx, msg, telemetry.Reveal())
	span.End()
}

// sessionRestore executes a plan against the interactive session.
type sessionRestore struct {
	dag     *dagger.Client
	session *LLMSession
	// base is the composed agent group `dagger agent` started with, kept as
	// each restored conversation's reset target so .clear returns to the
	// selected agents rather than a blank workspace-bound LLM.
	base *dagger.LLM
}

var _ restoreTarget = (*sessionRestore)(nil)

// rehydrateQuery uses the public single-agent restore boundary: load the
// committed conversation, reconstruct its agent handle, and restore that one
// runtime. The returned ID is ready for LLMSession.AttachRestored.
const rehydrateQuery = `query Rehydrate($llm: ID!, $id: String!, $name: String!, $parentAgentID: String!, $state: AgentState!, $error: String!) {
  node(id: $llm) {
    ... on LLM {
      agent(id: $id, name: $name) {
        rehydrate(parentAgentID: $parentAgentID, state: $state, error: $error)
      }
    }
  }
}`

func (r *sessionRestore) Rehydrate(ctx context.Context, entry dagui.AgentRestore, snapshotID string) (string, error) {
	var res struct {
		Node struct {
			Agent struct {
				Rehydrate string
			}
		}
	}
	if err := r.dag.Do(ctx, &dagger.Request{
		Query:  rehydrateQuery,
		OpName: "Rehydrate",
		Variables: map[string]any{
			"llm":           snapshotID,
			"id":            entry.ID,
			"name":          entry.Name,
			"parentAgentID": entry.ParentAgentID,
			"state":         entry.State,
			"error":         entry.Error,
		},
	}, &dagger.Response{Data: &res}); err != nil {
		return "", err
	}
	if res.Node.Agent.Rehydrate == "" {
		return "", errors.New("the engine returned no handle on the restored agent")
	}
	return res.Node.Agent.Rehydrate, nil
}

func (r *sessionRestore) Adopt(ctx context.Context, entry dagui.AgentRestore, agentID string) error {
	conv, err := r.session.AttachRestored(ctx, entry.ID, entry.Name, agentID)
	if err != nil {
		return err
	}
	conv.initialLLM = r.base
	return nil
}

func (r *sessionRestore) Focus(ctx context.Context, entry dagui.AgentRestore, agentID string) error {
	return r.session.Focus(ctx, entry.ID, entry.Name, agentID)
}

// validateAgentResumeFlags rejects resume combinations that have no meaning
// before any engine work begins.
func validateAgentResumeFlags(
	resume bool,
	timeout time.Duration,
	timeoutSet bool,
	agentSet bool,
	args []string,
) error {
	if timeout < 0 {
		return errors.New("--resume-timeout cannot be negative")
	}
	if !resume {
		if timeoutSet {
			return errors.New("--resume-timeout requires -r/--resume")
		}
		if agentSet {
			return errors.New("--agent requires -r/--resume")
		}
		return nil
	}
	if len(args) > 0 {
		return fmt.Errorf("-r/--resume cannot be combined with agent names (%s): "+
			"a restored session's agents come from the trace, not from the workspace",
			strings.Join(args, ", "))
	}
	return nil
}
