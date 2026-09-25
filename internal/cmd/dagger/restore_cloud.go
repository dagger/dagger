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
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

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
					return fmt.Errorf("decode Cloud call payload: %w", err)
				}
				if frame.Digest == "" {
					return errors.New("cloud call payload has no digest")
				}
				if old := capture.calls[frame.Digest]; old != nil && !proto.Equal(old, frame) {
					return fmt.Errorf("conflicting Cloud call payload %s", frame.Digest)
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
func restoreTraceSources(ctx context.Context, fe archiveFrontend, target restoreTarget, req traceRestore) (func(), error) {
	cleanup, err := restoreArchive(ctx, req.source, fe, target, req)
	if err == nil || !canFallbackToCloud(ctx, err) {
		return cleanup, err
	}
	if req.generation != "" {
		return nil, fmt.Errorf("selected engine archive generation %s is unavailable: %w; Cloud traces have no engine generation selector; omit --generation to use Cloud", req.generation, err)
	}
	source := req.cloudSource
	if source == nil {
		credentials, err := auth.GetCloudAuth(ctx)
		if err != nil {
			return nil, fmt.Errorf("restore Cloud trace %s: authenticate: %w", req.traceID, err)
		}
		source, err = cloud.NewOTLPClient(ctx, credentials)
		if err != nil {
			return nil, fmt.Errorf("restore Cloud trace %s: %w", req.traceID, err)
		}
	}
	// Unlike a local verified bootstrap, Cloud currently supplies a whole-trace
	// download, not an independent final roster witness or fixed archive cut.
	// Validate the latest observed canonical records and all required recipes;
	// never manufacture a manifest or claim that absent later records are proven.
	importer := &cloudRestoreCapture{
		TraceImporter: enginetel.NewTraceImporter(enginetel.TraceImportSinks{
			Spans: fe.SpanExporter(), Logs: fe.LogExporter(), Metrics: fe.MetricExporter(),
		}),
		calls: map[string]*callpbv1.Call{},
	}
	if err := source.FetchTrace(ctx, req.traceID, importer); err != nil {
		return nil, fmt.Errorf("fetch Cloud trace %s (no agents restored): %w", req.traceID, err)
	}
	if err := fe.WaitForEventLoop(ctx); err != nil {
		return nil, fmt.Errorf("apply Cloud trace %s: %w", req.traceID, err)
	}
	plan, edges, err := cloudRestorePlan(fe, req, importer.calls)
	if err != nil {
		return nil, fmt.Errorf("restore Cloud trace %s: %w", req.traceID, err)
	}
	if err := executeRestoreGraph(ctx, plan, target, req, edges); err != nil {
		return nil, fmt.Errorf("restore Cloud trace %s: %w", req.traceID, err)
	}
	return func() {}, nil
}

func cloudRestorePlan(fe archiveFrontend, req traceRestore, calls map[string]*callpbv1.Call) (appliedRestorePlan, []agentcontrol.Subscription, error) {
	plan := appliedRestorePlan{rebuild: func(digest string) (string, error) {
		if _, err := archive.VerifyClosure([]string{digest}, func(d string) (*callpbv1.Call, error) {
			frame := calls[d]
			if frame == nil {
				return nil, fmt.Errorf("missing Cloud call payload %s", d)
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
		if a.Removed {
			continue
		}
		entry := dagui.AgentRestore{
			Source: a.Key, ID: a.Handle, Name: a.Name, ParentAgentID: a.Parent,
			SnapshotDigest: a.Digest, LastActivity: a.Activity,
		}
		// As for archives: strict restore refuses an unmappable agent, while
		// --partial skips exactly this entry.
		state, err := a.RestoreState()
		if err != nil {
			entry.Err = fmt.Errorf("agent %q (%s) cannot be restored: %w", a.Name, a.Handle, err)
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
