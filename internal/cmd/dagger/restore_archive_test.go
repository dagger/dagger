package daggercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/call/callpbv1"
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
	"go.opentelemetry.io/otel/trace"
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
	header       archive.BootstrapHeader
	logs         *collogspb.ExportLogsServiceRequest
	bootstrapErr error
	history      chan string

	// An unsealed archive: Unsealed returns unsealed (or unsealedErr), and
	// unsealed streams deliver unsealedLogs.
	unsealed     *archive.UnsealedArchive
	unsealedErr  error
	unsealedLogs *collogspb.ExportLogsServiceRequest
}

func (s *restoreTestArchive) Unsealed(context.Context, string) (archive.UnsealedArchive, error) {
	if s.unsealedErr != nil {
		return archive.UnsealedArchive{}, s.unsealedErr
	}
	if s.unsealed == nil {
		return archive.UnsealedArchive{}, &archive.RequestError{Kind: archive.ErrorState, State: archive.StateClosed}
	}
	return *s.unsealed, nil
}
func (s *restoreTestArchive) Bootstrap(_ context.Context, _ string, consume func(archive.BootstrapHeader, archive.BootstrapBatch) error) (archive.BootstrapResult, error) {
	if s.bootstrapErr != nil {
		return archive.BootstrapResult{}, s.bootstrapErr
	}
	err := consume(s.header, archive.BootstrapBatch{Logs: s.logs})
	return archive.BootstrapResult{Header: s.header}, err
}
func (s *restoreTestArchive) Traces(ctx context.Context, _ string, opts archive.StreamOptions, _ func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error) {
	if opts.Unsealed {
		return opts.HighWater, nil
	}
	s.history <- "spans"
	<-ctx.Done()
	return 0, ctx.Err()
}
func (s *restoreTestArchive) Logs(ctx context.Context, _ string, opts archive.StreamOptions, consume func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	if opts.Unsealed {
		if s.unsealedLogs != nil {
			if err := consume(opts.HighWater, s.unsealedLogs); err != nil {
				return 0, err
			}
		}
		return opts.HighWater, nil
	}
	s.history <- "logs"
	<-ctx.Done()
	return 0, ctx.Err()
}
func (s *restoreTestArchive) Metrics(ctx context.Context, _ string, opts archive.StreamOptions, _ func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error) {
	if opts.Unsealed {
		return opts.HighWater, nil
	}
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
		header: archive.BootstrapHeader{TraceID: ns.Trace, SealAt: time.Unix(20, 0).UTC().Format(time.RFC3339Nano), Completion: archive.Witness(want)},
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
					plan, _, err = observedRestorePlan(fe, restoreRequest(), nil)
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
		name, trace, focus       string
		listArchives, listAgents bool
		args                     []string
		invalid                  bool
	}{
		{name: "compose"},
		{name: "restore", trace: "trace"},
		{name: "restore and focus", trace: "trace", focus: "chief"},
		{name: "list all", listArchives: true},
		{name: "focus without trace", focus: "chief", invalid: true},
		{name: "list agents and trace", listAgents: true, trace: "trace", invalid: true},
		{name: "list and names", listArchives: true, args: []string{"editor"}, invalid: true},
		{name: "list and list", listArchives: true, listAgents: true, invalid: true},
		{name: "list and focus", listArchives: true, focus: "chief", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArchiveFlags(tc.trace, tc.focus, tc.listArchives, tc.listAgents, tc.args)
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
		page := archive.Page{Archives: []archive.Manifest{{TraceID: "first", State: archive.StateClosed, Title: "title\n\x1b[31m"}}, Next: "next"}
		if r.URL.Query().Get("after") == "next" {
			page = archive.Page{Archives: []archive.Manifest{
				{TraceID: "other"},
				{TraceID: "second", State: archive.StateIncomplete},
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
	require.Contains(t, out.String(), "first")
	require.Contains(t, out.String(), "second")
	require.Contains(t, out.String(), "incomplete")
	require.Contains(t, out.String(), "other", "a bare -r lists every retained archive")
	require.NotContains(t, out.String(), "\x1b")
	require.Contains(t, out.String(), `title\n\x1b[31m`)
}

type fakeArchiveLister []archive.Manifest

func (f fakeArchiveLister) ListAll(context.Context, archive.ListOptions) ([]archive.Manifest, error) {
	return slices.Clone(f), nil
}

func archiveListFixture() fakeArchiveLister {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	return fakeArchiveLister{
		{TraceID: "a0", State: archive.StateClosed, Title: "oldest", StartedAt: base},
		{TraceID: "a1", State: archive.StateActive, Title: "live", StartedAt: base.Add(4 * time.Hour)},
		{TraceID: "a2", State: archive.StateInterrupted, Title: "crashed", StartedAt: base.Add(3 * time.Hour)},
		{TraceID: "a3", State: archive.StateFinalizing, Title: "closing", StartedAt: base.Add(5 * time.Hour)},
		{TraceID: "a4", State: archive.StateClosed, Title: "middle\n\x1b[31m", StartedAt: base.Add(time.Hour)},
		{TraceID: "a5", State: archive.StateClosed, StartedAt: base.Add(2 * time.Hour)},
	}
}

func TestArchiveListIsMostRecentFirst(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, listAgentArchives(t.Context(), archiveListFixture(), &out))
	var order []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n")[1:] {
		order = append(order, strings.Fields(line)[0])
	}
	require.Equal(t, []string{"a3", "a1", "a2", "a5", "a4", "a0"}, order)
}

func TestArchivePickerOffersResumableSessionsNewestFirst(t *testing.T) {
	var offered []huh.Option[string]
	picked, err := pickAgentArchive(t.Context(), archiveListFixture(), func(_ context.Context, options []huh.Option[string]) (string, error) {
		offered = options
		return options[1].Value, nil
	})
	require.NoError(t, err)
	require.Equal(t, "a5", picked)
	var values []string
	for _, option := range offered {
		values = append(values, option.Value)
	}
	require.Equal(t, []string{"a2", "a5", "a4", "a0"}, values, "running sessions are not offered")
	require.Contains(t, offered[0].Key, "crashed [interrupted]")
	require.Contains(t, offered[1].Key, "(untitled)")
	require.Contains(t, offered[2].Key, "middle [31m")
	require.NotContains(t, offered[2].Key, "\x1b")
	require.NotContains(t, offered[2].Key, "\n")
	require.True(t, strings.HasSuffix(offered[3].Key, "  a0"), "the trace ID is filterable")
}

func TestArchivePickerDismissedOrEmpty(t *testing.T) {
	picked, err := pickAgentArchive(t.Context(), archiveListFixture(), func(context.Context, []huh.Option[string]) (string, error) {
		return "", nil
	})
	require.NoError(t, err)
	require.Empty(t, picked)

	_, err = pickAgentArchive(t.Context(), fakeArchiveLister{{TraceID: "a1", State: archive.StateActive}}, func(context.Context, []huh.Option[string]) (string, error) {
		t.Fatal("no picker without resumable sessions")
		return "", nil
	})
	require.ErrorContains(t, err, "no agent sessions to resume")
}

func TestArchiveLabelTruncatesLongTitles(t *testing.T) {
	label := archiveLabel(archive.Manifest{TraceID: "t", Title: strings.Repeat("x", 100), StartedAt: time.Date(2026, 9, 1, 12, 34, 0, 0, time.UTC)}, time.UTC)
	require.Equal(t, "2026-09-01 12:34  "+strings.Repeat("x", archiveLabelTitleMax-1)+"…  t", label)
}

func TestArchivePromptDoesNotWaitForHistory(t *testing.T) {
	source, _, _, _ := canonicalArchive() //nolint:dogsled // Only the transport fixture is needed here.
	fe, target := newRestoreTestFrontend(), newFakeRestoreTarget()
	cleanup, err := restoreArchive(t.Context(), source, fe, target, restoreRequest())
	require.NoError(t, err)
	require.Equal(t, "chief", target.focused)
	for range 3 {
		select {
		case <-source.history:
		case <-time.After(time.Second):
			t.Fatal("history did not start")
		}
	}
	cleanup()
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
	worker.Digest, worker.CaptureError = "", "no committed conversation"
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
	require.Contains(t, warnings.String(), "capture failed: no committed conversation")
	require.Contains(t, warnings.String(), "dropped subscription")
}

type lazyRestoreFrontend struct {
	*restoreTestFrontend
	idtui.TraceFrontend
	provider func(dagui.SpanID, bool)
	wait     func()
}

func (fe *lazyRestoreFrontend) SetLogProvider(f func(dagui.SpanID, bool)) { fe.provider = f }
func (fe *lazyRestoreFrontend) SetFetchWaiter(f func())                   { fe.wait = f }

type lazyRestoreArchive struct {
	*restoreTestArchive
	requests chan archive.StreamOptions
}

func (s *lazyRestoreArchive) Traces(_ context.Context, _ string, opts archive.StreamOptions, _ func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error) {
	return opts.HighWater, nil
}
func (s *lazyRestoreArchive) Metrics(_ context.Context, _ string, opts archive.StreamOptions, _ func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error) {
	return opts.HighWater, nil
}
func (s *lazyRestoreArchive) Logs(_ context.Context, _ string, opts archive.StreamOptions, _ func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	s.requests <- opts
	return opts.HighWater, nil
}

func TestArchiveRestoreDefersDisplayLogs(t *testing.T) {
	base, chief, _, _ := canonicalArchive()
	base.header.HighWater = archive.HighWater{Spans: 10, Logs: 20, Metrics: 30}
	source := &lazyRestoreArchive{restoreTestArchive: base, requests: make(chan archive.StreamOptions, 8)}
	fe := &lazyRestoreFrontend{restoreTestFrontend: newRestoreTestFrontend()}
	target := newFakeRestoreTarget()
	cleanup, err := restoreArchive(t.Context(), source, fe, target, restoreRequest())
	require.NoError(t, err)
	defer cleanup()
	require.Equal(t, chief.Handle, target.focused, "verified bootstrap still restores before history")
	var metadata archive.StreamOptions
	select {
	case metadata = <-source.requests:
	case <-time.After(time.Second):
		t.Fatal("metadata was not loaded")
	}
	require.Equal(t, archive.LogRecordsMetadata, metadata.Logs.Records)
	require.Empty(t, metadata.Logs.SpanID)
	require.EqualValues(t, 20, metadata.HighWater)
	select {
	case <-source.requests:
		t.Fatal("eager display log request")
	default:
	}

	id := dagui.SpanID{SpanID: trace.SpanID{1}}
	fe.provider(id, false)
	fe.wait()
	own := <-source.requests
	require.Equal(t, id.String(), own.Logs.SpanID)
	require.Equal(t, archive.LogRecordsAll, own.Logs.Records)
	require.EqualValues(t, 20, own.HighWater)
	fe.provider(id, false)
	fe.wait()
	select {
	case <-source.requests:
		t.Fatal("duplicate lazy fetch")
	default:
	}
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

// historyTestArchive serves a finite history whose log stream re-delivers
// rows the bootstrap already applied, as history from cursor 0 does.
type historyTestArchive struct {
	*restoreTestArchive
	historyLogs *collogspb.ExportLogsServiceRequest
}

func (s historyTestArchive) Traces(_ context.Context, _ string, opts archive.StreamOptions, _ func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error) {
	return opts.HighWater, nil
}
func (s historyTestArchive) Logs(_ context.Context, _ string, opts archive.StreamOptions, consume func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	return opts.HighWater, consume(opts.HighWater, s.historyLogs)
}
func (s historyTestArchive) Metrics(_ context.Context, _ string, opts archive.StreamOptions, _ func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error) {
	return opts.HighWater, nil
}

// TestArchiveHistoryRedeliveryIsIdempotent: history streams from cursor 0, so
// it re-delivers the bootstrap's call payloads. Re-applying them (and, though
// sealed history filters them, its control records) changes nothing the
// restore depends on.
func TestArchiveHistoryRedeliveryIsIdempotent(t *testing.T) {
	chief, worker, edge, payloads := cloudControlFixture(t)
	source, _, _, _ := canonicalArchive() //nolint:dogsled // The fixture's digests come from cloudControlFixture.
	source.logs = controlLogs(append(payloads, chief.Record(), worker.Record(), edge.Record())...)
	source.header.HighWater = archive.HighWater{Logs: int64(len(payloads) + 3)}
	fe := cloudTestFrontend{newRestoreTestFrontend()}
	cut, err := archiveCut(source.header)
	require.NoError(t, err)
	importer, err := enginetel.NewArchiveTraceImporter(enginetel.TraceImportSinks{
		Spans: fe.SpanExporter(), Logs: fe.LogExporter(), Metrics: fe.MetricExporter(), Barrier: fe,
	}, cut)
	require.NoError(t, err)
	require.NoError(t, importer.ImportAndWait(t.Context(), enginetel.ArchiveImportBatch{Logs: source.logs}))
	before, beforeEdges, err := appliedArchivePlan(fe, source.header.Completion)
	require.NoError(t, err)
	calls := map[string]*callpbv1.Call{}
	for digest, call := range fe.db.Calls {
		calls[digest] = call
	}
	mutations := fe.db.MutationCount()

	history := historyTestArchive{restoreTestArchive: source, historyLogs: source.logs}
	require.NoError(t, importArchiveRemainder(t.Context(), history, source.header.TraceID, importer, cut))

	after, afterEdges, err := appliedArchivePlan(fe, source.header.Completion)
	require.NoError(t, err)
	require.Equal(t, before.plan, after.plan)
	require.Equal(t, beforeEdges, afterEdges)
	require.Len(t, fe.db.Calls, len(calls))
	for digest, call := range calls {
		require.Same(t, call, fe.db.Calls[digest], "a re-delivered payload must not replace the applied call")
	}
	require.Equal(t, mutations, fe.db.MutationCount(), "re-delivery must not invalidate frontend views")
	for _, entry := range after.plan {
		_, err := fe.EncodedIDForCallDigest(entry.SnapshotDigest)
		require.NoError(t, err)
	}
}
