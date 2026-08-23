package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/archive"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/cloud"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// The CLI policy for trace-backed `dagger agent --resume`: flag validation,
// strict restore ordering, and focus selection against fake source/target seams.
//
// The engine round trips (rehydrate, attach) and the fetch have their own
// coverage; none of them is reachable — or interesting — without an engine.

// fakeRestorePlan is the frontend seam: a plan, plus the anchors that rebuild.
type fakeRestorePlan struct {
	plan    []dagui.AgentRestore
	anchors map[string]string // snapshot digest -> encoded conversation ID
}

func (f *fakeRestorePlan) AgentRestorePlan() []dagui.AgentRestore { return f.plan }

func (f *fakeRestorePlan) EncodedIDForCallDigest(digest string) (string, error) {
	if id, ok := f.anchors[digest]; ok {
		return id, nil
	}
	// The shape DB.CallIDForDigest reports when a frame's payload never
	// reached this client (§9's first row).
	return "", fmt.Errorf("cannot rebuild ID for %q: call %s never reached this client", "llm", digest)
}

// fakeRestoreTarget records the verbs the plan was executed with, in order.
type fakeRestoreTarget struct {
	calls         []string
	focused       string
	adopted       map[string]string // instance ID -> encoded agent handle
	failOn        string            // instance ID whose Rehydrate fails
	failAdoptOn   string            // instance ID whose Adopt fails
	rehydrat      map[string]string // instance ID -> snapshot it was re-hydrated from
	restoreParent map[string]string // instance ID -> restored parent instance ID
}

var _ restoreTarget = (*fakeRestoreTarget)(nil)

func newFakeRestoreTarget() *fakeRestoreTarget {
	return &fakeRestoreTarget{
		adopted:       map[string]string{},
		rehydrat:      map[string]string{},
		restoreParent: map[string]string{},
	}
}

func (f *fakeRestoreTarget) Rehydrate(_ context.Context, entry dagui.AgentRestore, snapshotID string) (string, error) {
	f.calls = append(f.calls, "rehydrate:"+entry.ID)
	if entry.ID == f.failOn {
		return "", errors.New("already has a runtime entry in this session")
	}
	f.rehydrat[entry.ID] = snapshotID
	f.restoreParent[entry.ID] = entry.ParentAgentID
	return "handle:" + entry.ID, nil
}

func (f *fakeRestoreTarget) Adopt(_ context.Context, entry dagui.AgentRestore, agentID string) error {
	f.calls = append(f.calls, "adopt:"+entry.ID)
	if entry.ID == f.failAdoptOn {
		return errors.New("conversation attach failed")
	}
	f.adopted[entry.ID] = agentID
	return nil
}

func (f *fakeRestoreTarget) Focus(_ context.Context, entry dagui.AgentRestore, agentID string) error {
	f.calls = append(f.calls, "focus:"+entry.ID)
	f.focused = entry.ID
	return nil
}

func sourceContext(span byte) dagui.SpanContext {
	return dagui.SpanContext{
		TraceID: dagui.TraceID{TraceID: trace.TraceID{2}},
		SpanID:  dagui.SpanID{SpanID: trace.SpanID{span}},
	}
}

// chiefAndWorkers is the shape a restore is usually asked for: a top-level
// conversation with two workers spawned under it, each anchored on a
// conversation whose payloads arrived.
func chiefAndWorkers() *fakeRestorePlan {
	now := time.Unix(1_700_000_000, 0)
	return &fakeRestorePlan{
		plan: []dagui.AgentRestore{
			{
				ID: "agent-chief", Name: "interactive", State: "IDLE",
				SnapshotDigest: "xxh3:chief", LastActivity: now.Add(3 * time.Minute),
				SourceContext: sourceContext(11),
			},
			{
				ID: "agent-scout", Name: "scout", State: "IDLE",
				SnapshotDigest: "xxh3:scout", ParentAgentID: "agent-chief",
				LastActivity: now.Add(time.Minute), SourceContext: sourceContext(12),
			},
			{
				ID: "agent-tests", Name: "tests", State: "STOPPED",
				SnapshotDigest: "xxh3:tests", ParentAgentID: "agent-chief",
				LastActivity: now.Add(2 * time.Minute), SourceContext: sourceContext(13),
			},
		},
		anchors: map[string]string{
			"xxh3:chief": "llm:chief",
			"xxh3:scout": "llm:scout",
			"xxh3:tests": "llm:tests",
		},
	}
}

func restoreRequest() traceRestore {
	return traceRestore{
		traceID:       "2f123ba77bf7bd2d4db2f70ed20613e8",
		archiveSource: "cloud",
	}
}

// TestRestorePlanAttemptsEverythingBeforeAnythingIsAddressed locks the
// load-bearing phase order: every individual restore attempt completes before
// the first attachment can dispatch work.
func TestRestorePlanAttemptsEverythingBeforeAnythingIsAddressed(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	require.NoError(t, executeRestorePlan(context.Background(), src, dst, restoreRequest()))

	require.Equal(t, []string{
		"rehydrate:agent-chief", "rehydrate:agent-scout", "rehydrate:agent-tests",
		"adopt:agent-chief", "adopt:agent-scout", "adopt:agent-tests",
		"focus:agent-chief",
	}, dst.calls)

	// Each instance is re-hydrated from ITS OWN anchor, rebuilt through the
	// frontend's payloads — mixing two up would restore an agent under
	// somebody else's conversation, silently.
	require.Equal(t, map[string]string{
		"agent-chief": "llm:chief",
		"agent-scout": "llm:scout",
		"agent-tests": "llm:tests",
	}, dst.rehydrat)

	// And each conversation is adopted by the handle rehydrate returned, not
	// by the one the roster advertises: the restored runtime is the new
	// entry, not the dead one the trace describes.
	require.Equal(t, "handle:agent-scout", dst.adopted["agent-scout"])
}

func TestResumeAgentsBridgeLinksTopLevelSourceIdentities(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	src.plan[1].ParentAgentID = "" // a second independent top-level conversation
	require.NoError(t, executeRestorePlan(context.Background(), src, dst, restoreRequest()))

	var bridge sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "resume agents" {
			bridge = span
			break
		}
	}
	require.NotNil(t, bridge)
	require.Len(t, bridge.Links(), 2)
	require.Equal(t, sourceContext(11).SpanID.SpanID, bridge.Links()[0].SpanContext.SpanID())
	require.Equal(t, sourceContext(12).SpanID.SpanID, bridge.Links()[1].SpanContext.SpanID())
	for _, link := range bridge.Links() {
		require.Equal(t, telemetryattrs.LinkPurposeContinuation,
			linkAttributes(link.Attributes)[attribute.Key(telemetry.LinkPurposeAttr)].AsString())
	}
	attrs := spanAttributes(bridge.Attributes())
	require.Equal(t, sourceContext(11).TraceID.String(),
		attrs[attribute.Key(telemetryattrs.AgentResumeSourceTraceIDAttr)].AsString())
	require.Equal(t, "cloud",
		attrs[attribute.Key(telemetryattrs.AgentResumeArchiveSourceAttr)].AsString())
}

func linkAttributes(attrs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	values := make(map[attribute.Key]attribute.Value, len(attrs))
	for _, attr := range attrs {
		values[attr.Key] = attr.Value
	}
	return values
}

func spanAttributes(attrs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	return linkAttributes(attrs)
}

// TestRestoreFocusesTheAgentWithNoAgentAboveIt is §3.1c. A worker's loop span
// is started under its chief's tool-call span, so "top-level" is a fact the
// plan carries; focusing a worker would point the prompt at somebody else's
// employee.
func TestRestoreFocusesTheAgentWithNoAgentAboveIt(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	require.NoError(t, executeRestorePlan(context.Background(), src, dst, restoreRequest()))
	require.Equal(t, "agent-chief", dst.focused)
}

// TestRestoreFocusesTheMostRecentlyActiveOfSeveral: with more than one
// top-level agent there is no single right answer, so the rule is "the one
// that was doing something most recently" — NOT the plan's order, which is
// when each agent first appeared. The two disagree here on purpose: the agent
// that appeared first is the one that went quiet first.
func TestRestoreFocusesTheMostRecentlyActiveOfSeveral(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	src := &fakeRestorePlan{
		plan: []dagui.AgentRestore{
			{ID: "agent-first", Name: "interactive", State: "IDLE",
				SnapshotDigest: "xxh3:first", LastActivity: now},
			{ID: "agent-second", Name: "reviewer", State: "IDLE",
				SnapshotDigest: "xxh3:second", LastActivity: now.Add(time.Hour)},
		},
		anchors: map[string]string{"xxh3:first": "llm:first", "xxh3:second": "llm:second"},
	}
	dst := newFakeRestoreTarget()
	require.NoError(t, executeRestorePlan(context.Background(), src, dst, restoreRequest()))
	require.Equal(t, "agent-second", dst.focused)
}

// TestRestoreFocusOverride covers --agent: by display name, by instance ID,
// and the two ways of naming one that cannot be resolved. A name is a label
// two agents may legitimately share, so an ambiguous one is refused rather
// than resolved arbitrarily.
func TestRestoreFocusOverride(t *testing.T) {
	t.Run("by name", func(t *testing.T) {
		src, dst := chiefAndWorkers(), newFakeRestoreTarget()
		req := restoreRequest()
		req.agent = "scout"
		require.NoError(t, executeRestorePlan(context.Background(), src, dst, req))
		require.Equal(t, "agent-scout", dst.focused)
	})

	t.Run("by instance ID", func(t *testing.T) {
		src, dst := chiefAndWorkers(), newFakeRestoreTarget()
		req := restoreRequest()
		req.agent = "agent-tests"
		require.NoError(t, executeRestorePlan(context.Background(), src, dst, req))
		require.Equal(t, "agent-tests", dst.focused)
	})

	t.Run("unknown falls back after restore", func(t *testing.T) {
		src, dst := chiefAndWorkers(), newFakeRestoreTarget()
		req := restoreRequest()
		req.agent = "nobody"
		require.NoError(t, executeRestorePlan(context.Background(), src, dst, req))
		require.Equal(t, "agent-chief", dst.focused)
	})

	t.Run("ambiguous falls back after restore", func(t *testing.T) {
		src, dst := chiefAndWorkers(), newFakeRestoreTarget()
		src.plan[1].Name = "twin"
		src.plan[2].Name = "twin"
		req := restoreRequest()
		req.agent = "twin"
		require.NoError(t, executeRestorePlan(context.Background(), src, dst, req))
		require.Equal(t, "agent-chief", dst.focused)
	})
}

func TestRestoreSkipsUnresolvableAgents(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeRestorePlan)
	}{
		{
			name: "projection refused it",
			mutate: func(src *fakeRestorePlan) {
				src.plan[1].Err = errors.New(`agent "scout" (agent-scout) published a STOPPED record with no reason`)
			},
		},
		{
			name:   "anchor does not rebuild",
			mutate: func(src *fakeRestorePlan) { delete(src.anchors, "xxh3:scout") },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			src, dst := chiefAndWorkers(), newFakeRestoreTarget()
			test.mutate(src)
			require.NoError(t, executeRestorePlan(context.Background(), src, dst, restoreRequest()))
			require.Equal(t, []string{
				"rehydrate:agent-chief", "rehydrate:agent-tests",
				"adopt:agent-chief", "adopt:agent-tests", "focus:agent-chief",
			}, dst.calls)
		})
	}
}

// TestRestoreFailsOnAnEmptyPlan: a trace with no agents in it — a CI run, a
// typo'd ID that resolved — must say so rather than drop the user into a
// prompt that restored nothing.
func TestRestoreFailsOnAnEmptyPlan(t *testing.T) {
	dst := newFakeRestoreTarget()
	err := executeRestorePlan(context.Background(), &fakeRestorePlan{}, dst, restoreRequest())
	require.ErrorContains(t, err, "carries no agents to restore")
	require.Empty(t, dst.calls)
}

func TestRestoreContinuesPastRehydrateFailure(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	dst.failOn = "agent-chief"
	req := restoreRequest()
	req.agent = "agent-chief"

	require.NoError(t, executeRestorePlan(context.Background(), src, dst, req))
	require.Equal(t, []string{
		"rehydrate:agent-chief", "rehydrate:agent-scout", "rehydrate:agent-tests",
		"adopt:agent-scout", "adopt:agent-tests", "focus:agent-tests",
	}, dst.calls)
	require.Empty(t, dst.restoreParent["agent-scout"])
	require.Empty(t, dst.restoreParent["agent-tests"],
		"children of a failed parent must start a valid top-level checkpoint lineage")
}

func TestRestoreAttemptsParentsFirstAndPreservesAncestry(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	src.plan = []dagui.AgentRestore{src.plan[1], src.plan[2], src.plan[0]}

	require.NoError(t, executeRestorePlan(context.Background(), src, dst, restoreRequest()))
	require.Equal(t, []string{
		"rehydrate:agent-chief", "rehydrate:agent-scout", "rehydrate:agent-tests",
		"adopt:agent-chief", "adopt:agent-scout", "adopt:agent-tests", "focus:agent-chief",
	}, dst.calls)
	require.Equal(t, "agent-chief", dst.restoreParent["agent-scout"])
	require.Equal(t, "agent-chief", dst.restoreParent["agent-tests"])
}

func TestRestoreContinuesPastAttachFailure(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	dst.failAdoptOn = "agent-chief"

	require.NoError(t, executeRestorePlan(context.Background(), src, dst, restoreRequest()))
	require.Equal(t, []string{
		"rehydrate:agent-chief", "rehydrate:agent-scout", "rehydrate:agent-tests",
		"adopt:agent-chief", "adopt:agent-scout", "adopt:agent-tests", "focus:agent-tests",
	}, dst.calls)
	require.Equal(t, "agent-chief", dst.restoreParent["agent-scout"],
		"runtime ancestry remains valid when only conversation attachment fails")
}

func TestRestoreFailsWhenNoConversationAttaches(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	src.plan = src.plan[:1]
	dst.failAdoptOn = "agent-chief"

	err := executeRestorePlan(context.Background(), src, dst, restoreRequest())
	require.ErrorContains(t, err, "no restored agent")
	require.ErrorContains(t, err, "could be attached")
	require.Empty(t, dst.focused)
}

func TestRestoreFailsWhenNoAgentCanBeRestored(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	src.anchors = nil

	err := executeRestorePlan(context.Background(), src, dst, restoreRequest())
	require.ErrorContains(t, err, "no agent in trace")
	require.Empty(t, dst.calls)
}

func TestTraceFetchTimeoutIsStrict(t *testing.T) {
	const traceID = "2f123ba77bf7bd2d4db2f70ed20613e8"
	idleTimeout := 5 * time.Second
	req := traceRestore{traceID: traceID, timeout: idleTimeout}

	timedOut, err := fetchTraceForRestore(t.Context(), req,
		func(_ context.Context, gotTraceID string, gotTimeout time.Duration) error {
			require.Equal(t, traceID, gotTraceID)
			require.Equal(t, idleTimeout, gotTimeout)
			return fmt.Errorf("logs: %w", cloud.ErrStreamStalled)
		})
	require.ErrorIs(t, err, cloud.ErrStreamStalled)
	require.False(t, timedOut)
}

func TestTraceFetchErrorsRemainStrict(t *testing.T) {
	t.Run("stall without timeout", func(t *testing.T) {
		req := restoreRequest()
		_, err := fetchTraceForRestore(t.Context(), req,
			func(context.Context, string, time.Duration) error {
				return fmt.Errorf("logs: %w", cloud.ErrStreamStalled)
			})
		require.ErrorIs(t, err, cloud.ErrStreamStalled)
	})

	t.Run("non-stall with timeout", func(t *testing.T) {
		req := restoreRequest()
		req.timeout = time.Second
		want := errors.New("bad payload")
		_, err := fetchTraceForRestore(t.Context(), req,
			func(context.Context, string, time.Duration) error { return want })
		require.ErrorIs(t, err, want)
	})
}

func TestAgentResumeFlagValidation(t *testing.T) {
	require.NoError(t, validateAgentResumeFlags(true, time.Second, true, true, nil))
	require.NoError(t, validateAgentResumeFlags(false, 0, false, false, []string{"editor"}))
	require.ErrorContains(t, validateAgentResumeFlags(false, 0, true, false, nil), "--resume-timeout")
	require.ErrorContains(t, validateAgentResumeFlags(false, 0, false, true, nil), "--agent requires")
	require.ErrorContains(t, validateAgentResumeFlags(true, -time.Second, true, false, nil), "cannot be negative")

	err := validateAgentResumeFlags(true, 0, false, false, []string{"editor", "dagger-go"})
	require.ErrorContains(t, err, "editor, dagger-go")
	require.ErrorContains(t, err, "come from the trace")
}

func TestEngineArchivePickerSelectsClosedArchive(t *testing.T) {
	client := &fakeArchiveRestoreClient{manifests: []archive.Manifest{
		{TraceID: "active", State: archive.StateActive, StartedAt: time.Unix(3, 0)},
		{TraceID: "older", Generation: "old-gen", State: archive.StateClosed, StartedAt: time.Unix(1, 0)},
		{TraceID: "newer", Generation: "new-gen", State: archive.StateClosed, StartedAt: time.Unix(2, 0)},
	}}
	source := newEngineTraceRestoreSource(client)
	source.picker = func(_ context.Context, got []archive.Manifest) (string, error) {
		require.Equal(t, []string{"newer", "older"}, []string{got[0].TraceID, got[1].TraceID})
		return got[0].TraceID, nil
	}
	selected, err := source.Select(t.Context())
	require.NoError(t, err)
	require.Equal(t, "newer", selected)
	require.Equal(t, "new-gen", source.selectedGeneration)
}

func TestEngineArchiveFallbackOnlyOnCleanMiss(t *testing.T) {
	for _, test := range []struct {
		name       string
		engineErr  error
		wantCloud  bool
		wantSource string
	}{
		{name: "clean miss", engineErr: &archive.RequestError{Kind: archive.ErrorCleanMiss}, wantCloud: true, wantSource: "cloud"},
		{name: "transient", engineErr: &archive.RequestError{Kind: archive.ErrorTransient}},
		{name: "state", engineErr: &archive.RequestError{Kind: archive.ErrorState}},
		{name: "corrupt", engineErr: &archive.RequestError{Kind: archive.ErrorCorrupt}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeArchiveRestoreClient{bootstrapErr: test.engineErr}
			cloud := &fakeTraceRestoreSource{}
			source := newEngineTraceRestoreSource(client)
			source.cloud = cloud
			_, err := source.Bootstrap(t.Context(), "trace", 0)
			require.Equal(t, test.wantCloud, cloud.bootstraps.Load() == 1)
			if test.wantCloud {
				require.NoError(t, err)
				require.Equal(t, test.wantSource, source.ArchiveSource())
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestEngineBootstrapImportsAndWaitsBeforeReturning(t *testing.T) {
	header := testArchiveHeader()
	client := &fakeArchiveRestoreClient{bootstrap: func(consume func(archive.BootstrapHeader, archive.BootstrapBatch) error) (archive.BootstrapResult, error) {
		err := consume(header, archive.BootstrapBatch{Traces: &coltracepb.ExportTraceServiceRequest{}})
		return archive.BootstrapResult{Header: header}, err
	}}
	importer := new(fakeArchiveImporter)
	source := newEngineTraceRestoreSource(client)
	source.newImporter = func(enginetel.ArchiveCut) (archiveTraceImporter, error) { return importer, nil }
	remainder, err := source.Bootstrap(t.Context(), header.TraceID, 0)
	require.NoError(t, err)
	require.NotNil(t, remainder)
	require.Equal(t, []string{"import:spans", "wait"}, importer.callsSnapshot())
}

func TestEngineRemainderStartsNonblockingAndWarnsOnce(t *testing.T) {
	blocked := make(chan struct{})
	client := &fakeArchiveRestoreClient{
		traces: func(context.Context, archive.StreamOptions, func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error) {
			<-blocked
			return 0, errors.New("span history failed")
		},
		logs: func(context.Context, archive.StreamOptions, func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
			return 0, errors.New("log history failed")
		},
		metrics: func(context.Context, archive.StreamOptions, func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error) {
			return 0, errors.New("metric history failed")
		},
	}
	importer := new(fakeArchiveImporter)
	warnings := make(chan error, 3)
	remainder := &engineTraceRemainder{
		archive: client, traceID: "trace", cut: testArchiveCut(), importer: importer,
		attempts: 1, warn: func(_ context.Context, err error) { warnings <- err },
	}
	started := time.Now()
	remainder.Start(t.Context())
	require.Less(t, time.Since(started), 50*time.Millisecond, "background history must not hold the prompt")
	select {
	case warning := <-warnings:
		require.EqualError(t, warning, archiveHistoryWarning)
	case <-time.After(time.Second):
		t.Fatal("background failure did not surface a warning")
	}
	close(blocked)
	require.Eventually(t, func() bool { return len(importer.callsSnapshot()) >= 3 }, time.Second, 10*time.Millisecond)
	select {
	case extra := <-warnings:
		t.Fatalf("duplicate warning: %v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func testArchiveHeader() archive.BootstrapHeader {
	return archive.BootstrapHeader{
		Generation: "generation", TraceID: "trace", SealAt: time.Unix(10, 0).UTC().Format(time.RFC3339Nano),
		HighWater: archive.HighWater{},
	}
}

func testArchiveCut() enginetel.ArchiveCut {
	cut, err := archiveCut(testArchiveHeader())
	if err != nil {
		panic(err)
	}
	return cut
}

type fakeTraceRestoreSource struct{ bootstraps atomic.Int32 }

func (*fakeTraceRestoreSource) Select(context.Context) (string, error) { return "", nil }
func (f *fakeTraceRestoreSource) Bootstrap(context.Context, string, time.Duration) (traceRestoreRemainder, error) {
	f.bootstraps.Add(1)
	return nil, nil
}
func (*fakeTraceRestoreSource) ArchiveSource() string { return "cloud" }

type fakeArchiveRestoreClient struct {
	manifests    []archive.Manifest
	bootstrap    func(func(archive.BootstrapHeader, archive.BootstrapBatch) error) (archive.BootstrapResult, error)
	bootstrapErr error
	traces       func(context.Context, archive.StreamOptions, func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error)
	logs         func(context.Context, archive.StreamOptions, func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error)
	metrics      func(context.Context, archive.StreamOptions, func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error)
}

func (f *fakeArchiveRestoreClient) ListAll(context.Context, archive.ListOptions) ([]archive.Manifest, error) {
	return slices.Clone(f.manifests), nil
}
func (f *fakeArchiveRestoreClient) Bootstrap(_ context.Context, _, _ string, consume func(archive.BootstrapHeader, archive.BootstrapBatch) error) (archive.BootstrapResult, error) {
	if f.bootstrapErr != nil {
		return archive.BootstrapResult{}, f.bootstrapErr
	}
	if f.bootstrap != nil {
		return f.bootstrap(consume)
	}
	return archive.BootstrapResult{}, nil
}
func (f *fakeArchiveRestoreClient) Traces(ctx context.Context, _ string, opts archive.StreamOptions, consume func(int64, *coltracepb.ExportTraceServiceRequest) error) (int64, error) {
	if f.traces != nil {
		return f.traces(ctx, opts, consume)
	}
	return opts.HighWater, nil
}
func (f *fakeArchiveRestoreClient) Logs(ctx context.Context, _ string, opts archive.StreamOptions, consume func(int64, *collogspb.ExportLogsServiceRequest) error) (int64, error) {
	if f.logs != nil {
		return f.logs(ctx, opts, consume)
	}
	return opts.HighWater, nil
}
func (f *fakeArchiveRestoreClient) Metrics(ctx context.Context, _ string, opts archive.StreamOptions, consume func(int64, *colmetricspb.ExportMetricsServiceRequest) error) (int64, error) {
	if f.metrics != nil {
		return f.metrics(ctx, opts, consume)
	}
	return opts.HighWater, nil
}

type fakeArchiveImporter struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeArchiveImporter) add(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}
func (f *fakeArchiveImporter) callsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}
func (f *fakeArchiveImporter) ImportAndWait(_ context.Context, _ enginetel.ArchiveCut, batch enginetel.ArchiveImportBatch) error {
	signal := "metrics"
	if batch.Spans != nil {
		signal = "spans"
	}
	if batch.Logs != nil {
		signal = "logs"
	}
	f.add("import:" + signal)
	return nil
}
func (f *fakeArchiveImporter) Wait(context.Context, enginetel.ArchiveCut) error {
	f.add("wait")
	return nil
}
func (f *fakeArchiveImporter) CompleteRemainder(_ context.Context, _ enginetel.ArchiveCut, signal enginetel.ArchiveSignal, _ int64) error {
	f.add("complete:" + string(signal))
	return nil
}
func (f *fakeArchiveImporter) AbandonRemainder(_ context.Context, _ enginetel.ArchiveCut, signal enginetel.ArchiveSignal) error {
	f.add("abandon:" + string(signal))
	return nil
}

func TestLLMSessionPristineGate(t *testing.T) {
	session := new(LLMSession)
	require.True(t, session.Pristine())
	require.True(t, session.BeginRestore())
	require.False(t, session.Pristine())
	require.False(t, session.BeginRestore())

	prompted := new(LLMSession)
	prompted.beginPrompt()
	require.False(t, prompted.Pristine())
}

func TestAgentResumeFlagSurface(t *testing.T) {
	require.Nil(t, agentCmd.Flags().Lookup("trace"))
	require.Nil(t, agentCmd.Flags().Lookup("partial"))
	require.Nil(t, agentCmd.Flags().Lookup("trace-timeout"))
	require.NotNil(t, agentCmd.Flags().Lookup("resume-timeout"))
	resume := agentCmd.Flags().Lookup("resume")
	require.NotNil(t, resume)
	require.Equal(t, string(agentResumePicker), resume.NoOptDefVal)
}

func TestAgentResumeFlagOptionalValue(t *testing.T) {
	var flag agentResumeFlag
	require.NoError(t, flag.Set(string(agentResumePicker)))
	require.Empty(t, flag.TraceID())
	require.NoError(t, flag.Set("2f123ba77bf7bd2d4db2f70ed20613e8"))
	require.Equal(t, "2f123ba77bf7bd2d4db2f70ed20613e8", flag.TraceID())
}
