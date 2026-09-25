package daggercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

type restoreTestFrontend struct {
	*idtui.FrontendMock
	db      *dagui.DB
	barrier func(context.Context) error
}

func newRestoreTestFrontend() *restoreTestFrontend {
	return &restoreTestFrontend{FrontendMock: &idtui.FrontendMock{}, db: dagui.NewDB()}
}
func (f *restoreTestFrontend) WaitForEventLoop(ctx context.Context) error {
	if f.barrier != nil {
		return f.barrier(ctx)
	}
	return ctx.Err()
}
func (f *restoreTestFrontend) AgentControl() ([]agentcontrol.Agent, []agentcontrol.Subscription, error) {
	return f.db.AgentControl()
}
func (f *restoreTestFrontend) AgentRestorePlan() []dagui.AgentRestore { return f.db.RestorePlan() }
func (f *restoreTestFrontend) EncodedIDForCallDigest(d string) (string, error) {
	return "snapshot:" + d, nil
}
func (f *restoreTestFrontend) SpanExporter() sdktrace.SpanExporter { return f.db }
func (f *restoreTestFrontend) LogExporter() sdklog.Exporter        { return f.db.LogExporter() }
func (f *restoreTestFrontend) MetricExporter() sdkmetric.Exporter  { return f.db.MetricExporter() }

type restoreTestArchive struct {
	header                   archive.BootstrapHeader
	logs                     *collogspb.ExportLogsServiceRequest
	acquireErr, bootstrapErr error
	released                 atomic.Int32
	history                  chan string
	acquiredGeneration       string
	bootstrapGeneration      string
}

func (s *restoreTestArchive) AcquireGeneration(_ context.Context, _ string, generation string) (func(), error) {
	s.acquiredGeneration = generation
	if s.acquireErr != nil {
		return nil, s.acquireErr
	}
	return func() { s.released.Add(1) }, nil
}
func (s *restoreTestArchive) Bootstrap(_ context.Context, _ string, generation string, consume func(archive.BootstrapHeader, archive.BootstrapBatch) error) (archive.BootstrapResult, error) {
	s.bootstrapGeneration = generation
	if s.bootstrapErr != nil {
		return archive.BootstrapResult{}, s.bootstrapErr
	}
	err := consume(s.header, archive.BootstrapBatch{Logs: s.logs})
	return archive.BootstrapResult{Header: s.header}, err
}
func (s *restoreTestArchive) Traces(ctx context.Context, _ string, _ archive.StreamOptions, _ func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error) {
	s.history <- "spans"
	<-ctx.Done()
	return 0, ctx.Err()
}
func (s *restoreTestArchive) Logs(ctx context.Context, _ string, _ archive.StreamOptions, _ func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	s.history <- "logs"
	<-ctx.Done()
	return 0, ctx.Err()
}
func (s *restoreTestArchive) Metrics(ctx context.Context, _ string, _ archive.StreamOptions, _ func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error) {
	s.history <- "metrics"
	<-ctx.Done()
	return 0, ctx.Err()
}

func controlLogs(records ...log.Record) *collogspb.ExportLogsServiceRequest {
	var logs []*logspb.LogRecord
	var value func(log.Value) *commonpb.AnyValue
	value = func(v log.Value) *commonpb.AnyValue {
		switch v.Kind() {
		case log.KindBytes:
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: v.AsBytes()}}
		case log.KindString:
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v.AsString()}}
		case log.KindInt64:
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v.AsInt64()}}
		case log.KindBool:
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v.AsBool()}}
		case log.KindSlice:
			var vals []*commonpb.AnyValue
			for _, item := range v.AsSlice() {
				vals = append(vals, value(item))
			}
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: vals}}}
		default:
			panic("unexpected control value")
		}
	}
	for _, rec := range records {
		entry := &logspb.LogRecord{TimeUnixNano: uint64(rec.Timestamp().UnixNano()), Body: value(rec.Body())}
		rec.WalkAttributes(func(kv log.KeyValue) bool {
			entry.Attributes = append(entry.Attributes, &commonpb.KeyValue{Key: kv.Key, Value: value(kv.Value)})
			return true
		})
		logs = append(logs, entry)
	}
	return &collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{Resource: &resourcepb.Resource{}, ScopeLogs: []*logspb.ScopeLogs{{LogRecords: logs}}}}}
}

func canonicalArchive() (*restoreTestArchive, agentcontrol.Agent, agentcontrol.Agent, agentcontrol.Subscription) {
	ns := agentcontrol.Namespace{Session: "source", Trace: restoreRequest().traceID, Incarnation: "old"}
	chief := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: ns, Handle: "chief"}, Revision: 4, Name: "chief", State: "IDLE", Digest: "xxh3:chief", Activity: time.Unix(10, 0)}
	worker := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: ns, Handle: "worker"}, Revision: 7, Name: "worker", Parent: "chief", State: "FAILED", Failure: "original failure", Digest: "xxh3:worker", Activity: time.Unix(9, 0)}
	edge := agentcontrol.Subscription{EdgeKey: agentcontrol.EdgeKey{Namespace: ns, Watched: "worker", Subscriber: "chief"}, Revision: 3, States: []string{"IDLE", "FAILED"}}
	want := agentcontrol.Expectation{Agents: map[agentcontrol.Key]int64{chief.Key: chief.Revision, worker.Key: worker.Revision}, Subscriptions: map[agentcontrol.EdgeKey]int64{edge.EdgeKey: edge.Revision}}
	return &restoreTestArchive{
		header: archive.BootstrapHeader{TraceID: ns.Trace, Generation: "fixed-cut", SealAt: time.Unix(20, 0).UTC().Format(time.RFC3339Nano), Completion: archive.Witness(want)},
		logs:   controlLogs(chief.Record(), worker.Record(), edge.Record()), history: make(chan string, 3),
	}, chief, worker, edge
}

func TestRestorePlansOnlyPassErrorsForFailedAgents(t *testing.T) {
	for _, sourceKind := range []string{"archive", "cloud"} {
		for _, tc := range []struct {
			name, state, stopReason, preTeardownState, restoredState, restoredError string
		}{
			{name: "failed", state: "FAILED", restoredState: "FAILED", restoredError: "original failure"},
			{name: "explicitly stopped failure", state: "STOPPED", stopReason: "EXPLICIT", restoredState: "STOPPED"},
			{name: "session stopped failure", state: "STOPPED", stopReason: "SESSION", preTeardownState: "FAILED", restoredState: "FAILED", restoredError: "original failure"},
		} {
			t.Run(sourceKind+"/"+tc.name, func(t *testing.T) {
				source, chief, worker, edge := canonicalArchive()
				// Stopping a failed runtime seals its tombstone without clearing
				// its recorded failure. That diagnostic is not always a spawn arg.
				worker.State, worker.StopReason, worker.PreTeardownState = tc.state, tc.stopReason, tc.preTeardownState
				fe := newRestoreTestFrontend()
				importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{Logs: fe.LogExporter()})
				require.NoError(t, importer.ImportLogs(t.Context(), controlLogs(chief.Record(), worker.Record(), edge.Record())))

				var plan appliedRestorePlan
				var err error
				if sourceKind == "archive" {
					plan, _, err = appliedArchivePlan(fe, source.header.Completion)
				} else {
					plan, _, err = cloudRestorePlan(fe, restoreRequest(), nil)
				}
				require.NoError(t, err)
				require.Len(t, plan.plan, 2)
				found := false
				for _, entry := range plan.plan {
					if entry.ID == worker.Handle {
						found = true
						require.Equal(t, tc.restoredState, entry.State)
						require.Equal(t, tc.restoredError, entry.Error, "spawn only accepts an error for FAILED")
					}
				}
				require.True(t, found, "stopped agents must still be restored")
			})
		}
	}
}

func TestArchiveSelectionFlags(t *testing.T) {
	for _, tc := range []struct {
		name, trace, source, generation, focus string
		listArchives, listAgents               bool
		args                                   []string
		invalid                                bool
	}{
		{name: "compose"},
		{name: "restore", trace: "trace"},
		{name: "selected", trace: "trace", source: "source", generation: "cut"},
		{name: "list all", listArchives: true},
		{name: "missing trace", source: "source", generation: "cut", invalid: true},
		{name: "source selection", trace: "trace", source: "source"},
		{name: "missing source", trace: "trace", generation: "cut", invalid: true},
		{name: "focus without trace", focus: "chief", invalid: true},
		{name: "list agents and trace", listAgents: true, trace: "trace", invalid: true},
		{name: "list and names", listArchives: true, args: []string{"editor"}, invalid: true},
		{name: "list and list", listArchives: true, listAgents: true, invalid: true},
		{name: "list and focus", listArchives: true, focus: "chief", invalid: true},
		{name: "list and selection", listArchives: true, trace: "trace", source: "source", generation: "cut", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArchiveFlags(tc.trace, tc.source, tc.generation, tc.focus, tc.listArchives, tc.listAgents, tc.args)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestArchiveDiscoveryIsReadOnlyAndPaginated(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/telemetry/archives", r.URL.Path)
		requests = append(requests, r.URL.Query().Get("after"))
		page := archive.Page{Archives: []archive.Manifest{{TraceID: "wanted", SourceSession: "one", Generation: "cut-one", State: archive.StateClosed, Title: "title\n\x1b[31m"}}, Next: "next"}
		if r.URL.Query().Get("after") == "next" {
			page = archive.Page{Archives: []archive.Manifest{
				{TraceID: "other", SourceSession: "hidden", Generation: "hidden"},
				{TraceID: "wanted", SourceSession: "two", Generation: "cut-two", State: archive.StateIncomplete},
			}}
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(page))
	}))
	defer server.Close()
	source, err := archive.NewClientWithURL(server.Client(), server.URL)
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, listAgentArchives(t.Context(), source, &out))
	require.Equal(t, []string{"", "next"}, requests)
	require.Contains(t, out.String(), "cut-one")
	require.Contains(t, out.String(), "cut-two")
	require.Contains(t, out.String(), "incomplete")
	require.Contains(t, out.String(), "other", "a bare -r lists every retained archive")
	require.NotContains(t, out.String(), "\x1b")
	require.Contains(t, out.String(), `title\n\x1b[31m`)
}

func TestArchiveRestorePinsSelectedGeneration(t *testing.T) {
	source, _, _, _ := canonicalArchive() //nolint:dogsled // Only the transport fixture is needed here.
	req := restoreRequest()
	req.generation = source.header.Generation
	cleanup, err := restoreArchive(t.Context(), source, newRestoreTestFrontend(), newFakeRestoreTarget(), req)
	require.NoError(t, err)
	defer cleanup()
	require.Equal(t, req.generation, source.acquiredGeneration)
	require.Equal(t, req.generation, source.bootstrapGeneration)
}

func TestArchiveAmbiguityGuidesSelection(t *testing.T) {
	cause := &archive.RequestError{Kind: archive.ErrorState, Failure: archive.FailureAmbiguous}
	err := archiveRestoreError("trace", cause)
	require.ErrorIs(t, err, cause)
	require.Contains(t, err.Error(), "dagger agent -r")
	require.Contains(t, err.Error(), "--source-session <session> --generation <generation>")
}

func TestArchivePromptDoesNotWaitForHistory(t *testing.T) {
	source, _, _, _ := canonicalArchive() //nolint:dogsled // Only the transport fixture is needed here.
	fe, target := newRestoreTestFrontend(), newFakeRestoreTarget()
	cleanup, err := restoreArchive(t.Context(), source, fe, target, restoreRequest())
	require.NoError(t, err)
	require.Equal(t, "chief", target.focused)
	require.Zero(t, source.released.Load(), "lease must span bootstrap-to-remainder gap")
	for range 3 {
		select {
		case <-source.history:
		case <-time.After(time.Second):
			t.Fatal("history did not start")
		}
	}
	cleanup()
	require.EqualValues(t, 1, source.released.Load(), "exit must cancel readers and release exactly once")
}

func TestArchiveRestoreWaitsForFrontendApplication(t *testing.T) {
	source, _, _, _ := canonicalArchive() //nolint:dogsled // Only the transport fixture is needed here.
	fe, target := newRestoreTestFrontend(), newFakeRestoreTarget()
	entered, apply := make(chan struct{}, 1), make(chan struct{})
	fe.barrier = func(ctx context.Context) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-apply:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	type result struct {
		cleanup func()
		err     error
	}
	done := make(chan result, 1)
	go func() {
		cleanup, err := restoreArchive(t.Context(), source, fe, target, restoreRequest())
		done <- result{cleanup, err}
	}()
	<-entered
	require.Empty(t, target.calls, "enqueue must not permit runtime creation")
	select {
	case <-done:
		t.Fatal("restore crossed blocked application barrier")
	default:
	}
	close(apply)
	res := <-done
	require.NoError(t, res.err)
	res.cleanup()
}

func TestArchiveBootstrapFailureNeverCreatesRuntime(t *testing.T) {
	for _, fail := range []error{archive.ErrCorrupt, archive.ErrState, archive.ErrTransient, archive.ErrCleanMiss} {
		t.Run(fail.Error(), func(t *testing.T) {
			source, _, _, _ := canonicalArchive()
			source.bootstrapErr = fail
			target := newFakeRestoreTarget()
			cleanup, err := restoreArchive(t.Context(), source, newRestoreTestFrontend(), target, restoreRequest())
			require.ErrorIs(t, err, fail)
			require.Nil(t, cleanup)
			require.Empty(t, target.calls)
			require.EqualValues(t, 1, source.released.Load())
			if errors.Is(fail, archive.ErrCleanMiss) {
				require.ErrorContains(t, err, "no retained engine archive")
			}
		})
	}
}

func TestAppliedArchivePlanUsesWitnessNamespace(t *testing.T) {
	source, chief, worker, edge := canonicalArchive()
	fe := newRestoreTestFrontend()
	importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{Logs: fe.LogExporter()})
	require.NoError(t, importer.ImportLogs(t.Context(), source.logs))
	// A destination incarnation with the same handle is a different revision
	// domain, even if its revision number is lower than historical telemetry.
	live := chief
	live.Namespace = agentcontrol.Namespace{Session: "destination", Trace: "live", Incarnation: "new"}
	live.Revision, live.Digest = 1, "xxh3:live"
	require.NoError(t, importer.ImportLogs(t.Context(), controlLogs(live.Record())))
	plan, edges, err := appliedArchivePlan(fe, source.header.Completion)
	require.NoError(t, err)
	require.Len(t, plan.plan, 2)
	require.Equal(t, chief.Key, plan.plan[0].Source)
	require.Equal(t, chief.Digest, plan.plan[0].SnapshotDigest)
	require.Equal(t, worker.Failure, plan.plan[1].Error)
	require.Len(t, edges, 1)
	require.Equal(t, edge.EdgeKey, edges[0].EdgeKey)
}

// TestArchiveRestoreSkipsCaptureFailure: an archive seals an agent whose
// final revision recorded a capture failure (it has no recipe closure to
// verify). Restore warns about it and restores the rest without it or its
// subscriptions.
func TestArchiveRestoreSkipsCaptureFailure(t *testing.T) {
	warnings := captureRestoreWarnings(t)
	source, chief, worker, edge := canonicalArchive()
	worker.Revision++
	worker.Digest, worker.CaptureError = "", "Host.directory is session-local"
	want := agentcontrol.Expectation{
		Agents:        map[agentcontrol.Key]int64{chief.Key: chief.Revision, worker.Key: worker.Revision},
		Subscriptions: map[agentcontrol.EdgeKey]int64{edge.EdgeKey: edge.Revision},
	}
	source.header.Completion = archive.Witness(want)
	source.logs = controlLogs(chief.Record(), worker.Record(), edge.Record())

	fe, target := newRestoreTestFrontend(), newFakeRestoreTarget()
	cleanup, err := restoreArchive(t.Context(), source, fe, target, restoreRequest())
	require.NoError(t, err)
	defer cleanup()
	require.Equal(t, []string{"rehydrate:chief", "adopt:chief", "focus:chief"}, target.calls,
		"restore skips the failed capture and drops its subscription")
	require.Contains(t, warnings.String(), "worker (worker)")
	require.Contains(t, warnings.String(), "capture failed: Host.directory is session-local")
	require.Contains(t, warnings.String(), "dropped subscription")
}

func TestHistoricalFailureWarnsWithoutBreakingPrompt(t *testing.T) {
	failure := errors.New("history unavailable")
	warning := make(chan error, 1)
	cleanup := startHistoricalImport(t.Context(), func(context.Context) error { return failure }, func(err error) { warning <- err })
	defer cleanup()
	select {
	case err := <-warning:
		require.ErrorIs(t, err, failure)
	case <-time.After(time.Second):
		t.Fatal("historical import failure was not surfaced")
	}
}
