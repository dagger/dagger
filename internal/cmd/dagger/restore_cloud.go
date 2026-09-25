package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// cloudRestoreSource supplies a whole trace with no verified seal: a Dagger
// Cloud download, or an unsealed engine archive (unsealedArchiveFetcher).
type cloudRestoreSource interface {
	FetchTrace(context.Context, string, cloud.TraceImportSink) error
}

// cloudRestoreCapture retains exact producer payloads for integrity checks.
// Re-encoding a frontend-rebuilt ID can normalize identity annotations; validate
// the received frames, as the local archive verifier does, not a lossy round trip.
type cloudRestoreCapture struct {
	*enginetel.TraceImporter
	calls map[string]*callpbv1.Call
}

func (capture *cloudRestoreCapture) ImportLogs(ctx context.Context, req *collogspb.ExportLogsServiceRequest) error {
	for _, resource := range req.GetResourceLogs() {
		for _, scope := range resource.GetScopeLogs() {
			for _, rec := range scope.GetLogRecords() {
				payload := false
				for _, kv := range rec.GetAttributes() {
					if kv.GetKey() == telemetry.ContentTypeAttr && kv.GetValue().GetStringValue() == telemetryattrs.CallPayloadContentType {
						payload = true
					}
				}
				if !payload {
					continue
				}
				frame := new(callpbv1.Call)
				if err := proto.Unmarshal(rec.GetBody().GetBytesValue(), frame); err != nil {
					return fmt.Errorf("decode call payload: %w", err)
				}
				if frame.Digest == "" {
					return errors.New("call payload has no digest")
				}
				if old := capture.calls[frame.Digest]; old != nil && !proto.Equal(old, frame) {
					return fmt.Errorf("conflicting call payload %s", frame.Digest)
				}
				capture.calls[frame.Digest] = frame
			}
		}
	}
	return capture.TraceImporter.ImportLogs(ctx, req)
}

func canFallbackToCloud(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if archive.IsCleanMiss(err) {
		return true
	}
	var acquireErr *archiveAcquireError
	if !errors.As(err, &acquireErr) || !errors.Is(err, archive.ErrTransient) {
		return false
	}
	var requestErr *archive.RequestError
	return !errors.As(err, &requestErr) || (requestErr.StatusCode != http.StatusUnauthorized && requestErr.StatusCode != http.StatusForbidden)
}

// Prefer the persisted local cut. Missing or unavailable archive service may
// fall back before bootstrap import. Never hide ambiguity, rejected authority,
// corruption, or a partial local bootstrap behind a different source.
//
// A local archive that was never sealed (the engine stopped before finalizing
// it, or finalization failed) is restored best-effort from what it recorded,
// with a warning. If that cannot even produce a plan, Cloud is tried.
func restoreTraceSources(ctx context.Context, fe archiveFrontend, target restoreTarget, req traceRestore) (func(), error) {
	cleanup, err := restoreArchive(ctx, req.source, fe, target, req)
	if err == nil {
		return cleanup, nil
	}
	fallback := canFallbackToCloud(ctx, err)
	var unsealedErr error
	if state, ok := unsealedArchiveState(err); ok && ctx.Err() == nil {
		slog.Warn("engine archive was not sealed; restoring the latest recorded state best-effort, so the most recent steps may be missing",
			"trace", req.traceID, "state", state)
		cleanup, restored, uerr := restoreUnsealedArchive(ctx, req.source, fe, target, req)
		if uerr != nil {
			uerr = fmt.Errorf("restore unsealed engine archive %s: %w", req.traceID, uerr)
		}
		if uerr == nil || restored || ctx.Err() != nil {
			// Once graph installation has begun, runtimes may exist, and a
			// second source would collide with them.
			return cleanup, uerr
		}
		unsealedErr, fallback = uerr, true
		err = uerr
	}
	if !fallback {
		return nil, err
	}
	if req.generation != "" {
		return nil, fmt.Errorf("selected engine archive generation %s is unavailable: %w; Cloud traces have no engine generation selector; omit --generation to use Cloud", req.generation, err)
	}
	// A failed unsealed local attempt is part of the story when Cloud fails too.
	fail := func(cloudErr error) error { return errors.Join(unsealedErr, cloudErr) }
	source := req.cloudSource
	if source == nil {
		credentials, err := auth.GetCloudAuth(ctx)
		if err != nil {
			return nil, fail(fmt.Errorf("restore Cloud trace %s: authenticate: %w", req.traceID, err))
		}
		source, err = cloud.NewOTLPClient(ctx, credentials)
		if err != nil {
			return nil, fail(fmt.Errorf("restore Cloud trace %s: %w", req.traceID, err))
		}
	}
	// Unlike a local verified bootstrap, Cloud currently supplies a whole-trace
	// download, not an independent final roster witness or fixed archive cut.
	plan, edges, err := observedTracePlan(ctx, fe, req, source)
	if err != nil {
		return nil, fail(fmt.Errorf("restore Cloud trace %s (no agents restored): %w", req.traceID, err))
	}
	if err := executeRestoreGraph(ctx, plan, target, req, edges); err != nil {
		return nil, fail(fmt.Errorf("restore Cloud trace %s: %w", req.traceID, err))
	}
	return func() {}, nil
}

// unsealedArchiveState reports whether a local archive failed only because it
// was never sealed.
func unsealedArchiveState(err error) (archive.State, bool) {
	var requestErr *archive.RequestError
	if !errors.As(err, &requestErr) || !errors.Is(err, archive.ErrState) || !requestErr.State.Unsealed() {
		return "", false
	}
	return requestErr.State, true
}

// restoreUnsealedArchive restores from everything an unsealed local archive
// recorded, like a Cloud download: the latest observed control records, with
// each agent's recipe closure verified on its own. restored reports whether the
// restore got as far as installing the agent graph.
func restoreUnsealedArchive(ctx context.Context, source archiveRestoreSource, fe archiveFrontend, target restoreTarget, req traceRestore) (_ func(), restored bool, _ error) {
	unsealed, err := source.AcquireUnsealed(ctx, req.traceID, req.generation)
	if err != nil {
		return nil, false, err
	}
	defer unsealed.Release()
	plan, edges, err := observedTracePlan(ctx, fe, req, unsealedArchiveFetcher{source: source, archive: unsealed})
	if err != nil {
		return nil, false, err
	}
	if err := executeRestoreGraph(ctx, plan, target, req, edges); err != nil {
		return nil, true, err
	}
	return func() {}, true, nil
}

// unsealedArchiveFetcher streams a whole unsealed local archive through the
// same sink a Cloud download uses.
type unsealedArchiveFetcher struct {
	source  archiveRestoreSource
	archive archive.UnsealedArchive
}

func (f unsealedArchiveFetcher) FetchTrace(ctx context.Context, traceID string, sink cloud.TraceImportSink) error {
	opts := func(high int64) archive.StreamOptions {
		return archive.StreamOptions{Generation: f.archive.Generation, HighWater: high, Unsealed: true}
	}
	if _, err := f.source.Traces(ctx, traceID, opts(f.archive.Cut.Spans), func(_ int64, batch *coltracepb.ExportTraceServiceRequest) error {
		return sink.ImportSpans(ctx, batch)
	}); err != nil {
		return fmt.Errorf("stream spans: %w", err)
	}
	if _, err := f.source.Logs(ctx, traceID, opts(f.archive.Cut.Logs), func(_ int64, batch *collogspb.ExportLogsServiceRequest) error {
		return sink.ImportLogs(ctx, batch)
	}); err != nil {
		return fmt.Errorf("stream logs: %w", err)
	}
	if _, err := f.source.Metrics(ctx, traceID, opts(f.archive.Cut.Metrics), func(_ int64, batch *colmetricspb.ExportMetricsServiceRequest) error {
		return sink.ImportMetrics(ctx, batch)
	}); err != nil {
		return fmt.Errorf("stream metrics: %w", err)
	}
	return sink.Seal(ctx)
}

// observedTracePlan imports a whole trace that carries no verified seal (a
// Cloud download or an unsealed engine archive) and plans from the latest
// observed canonical records. It never manufactures a manifest or claims that
// absent later records are proven.
func observedTracePlan(ctx context.Context, fe archiveFrontend, req traceRestore, source cloudRestoreSource) (appliedRestorePlan, []agentcontrol.Subscription, error) {
	importer := &cloudRestoreCapture{
		TraceImporter: enginetel.NewTraceImporter(enginetel.TraceImportSinks{
			Spans: fe.SpanExporter(), Logs: fe.LogExporter(), Metrics: fe.MetricExporter(),
		}),
		calls: map[string]*callpbv1.Call{},
	}
	if err := source.FetchTrace(ctx, req.traceID, importer); err != nil {
		return appliedRestorePlan{}, nil, fmt.Errorf("fetch: %w", err)
	}
	if err := fe.WaitForEventLoop(ctx); err != nil {
		return appliedRestorePlan{}, nil, fmt.Errorf("apply: %w", err)
	}
	return observedRestorePlan(fe, req, importer.calls)
}

func observedRestorePlan(fe archiveFrontend, req traceRestore, calls map[string]*callpbv1.Call) (appliedRestorePlan, []agentcontrol.Subscription, error) {
	plan := appliedRestorePlan{rebuild: func(digest string) (string, error) {
		if _, err := archive.VerifyClosure([]string{digest}, func(d string) (*callpbv1.Call, error) {
			frame := calls[d]
			if frame == nil {
				return nil, fmt.Errorf("missing call payload %s", d)
			}
			return frame, nil
		}); err != nil {
			return "", err
		}
		return fe.EncodedIDForCallDigest(digest)
	}}
	agents, edges, err := fe.AgentControl()
	if err != nil {
		return plan, nil, err
	}
	namespaces := map[agentcontrol.Namespace]bool{}
	for _, a := range agents {
		if a.Trace == req.traceID && (req.sourceSession == "" || a.Session == req.sourceSession) {
			namespaces[a.Namespace] = true
		}
	}
	if len(namespaces) == 0 {
		return plan, nil, fmt.Errorf("no canonical agent records for the requested source; the trace may be missing, incomplete, or from an older producer")
	}
	if len(namespaces) > 1 {
		var choices []string
		for ns := range namespaces {
			choices = append(choices, fmt.Sprintf("source-session=%q incarnation=%q", ns.Session, ns.Incarnation))
		}
		sort.Strings(choices)
		return plan, nil, fmt.Errorf("ambiguous source namespaces (%s); select --source-session, or use a trace containing one runtime incarnation", strings.Join(choices, ", "))
	}
	for _, a := range agents {
		if !namespaces[a.Namespace] {
			continue
		}
		if err := a.Validate(); err != nil {
			return plan, nil, err
		}
		entry := dagui.AgentRestore{
			Source: a.Key, ID: a.Handle, Name: a.Name, ParentAgentID: a.Parent,
			SnapshotDigest: a.Digest, LastActivity: a.Activity,
		}
		// As for archives: restore skips an unmappable agent and warns why.
		state, err := a.RestoreState()
		if err != nil {
			entry.Err = err
		}
		entry.State = state
		// A stopped failure retains its diagnostic, but spawn only accepts an
		// error when restoring FAILED (including a session-stopped failure).
		if state == "FAILED" {
			entry.Error = a.Failure
		}
		plan.plan = append(plan.plan, entry)
	}
	var active []agentcontrol.Subscription
	for _, edge := range edges {
		if !namespaces[edge.Namespace] {
			continue
		}
		if err := edge.Validate(); err != nil {
			return plan, nil, err
		}
		if len(edge.States) > 0 {
			active = append(active, edge)
		}
	}
	return plan, active, nil
}
