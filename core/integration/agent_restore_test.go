package core

// The end-to-end restore (hack/designs/resume-from-trace.md §10, "End to end,
// replay provider"): a real session's agents, captured as the OTLP it really
// published, served back through a fake Cloud speaking the §5.1 endpoints, and
// restored into a SECOND session.
//
// Everything under test here is only true once telemetry has crossed a wire
// twice. A chief's conversation has to survive being published as spans and
// call payloads, fetched back as framed binary OTLP, folded into a fresh DB,
// projected into a plan, rebuilt into an ID and re-hydrated — and what proves
// it is not an assertion about records but the replay provider itself: the
// restored chief's next turn only resolves if its history is the one the
// recording expects, so "a send continues the conversation rather than opening
// an empty one" is decided by the model, not by the test.
//
// It needs its own CLI session (to point telemetry at the sink) and a second
// one to restore into, so it skips when nested, like the other trace tests in
// agent_runtime_test.go.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/engineconn"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/cloud/otlpstream"
	telemetry "github.com/dagger/otel-go"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/proto"
)

type AgentRestoreSuite struct{}

func TestAgentRestore(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(AgentRestoreSuite{})
}

// fakeCloudTrace serves one captured trace over the §5.1 endpoints:
//
//	GET /v1/traces/{id}   GET /v1/logs/{id}   GET /v1/metrics/{id}
//
// each a binary OTLP stream in the otlpstream framing Cloud deploys
// (dagger.io#5226): data frames of binary-protobuf export requests, ended by
// a terminal frame.
type fakeCloudTrace struct {
	traceID string
	traces  []*coltracepb.ExportTraceServiceRequest
	logs    []*collogspb.ExportLogsServiceRequest
}

func (f *fakeCloudTrace) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	kind, traceID, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/"), "/")
	if !ok || traceID != f.traceID {
		http.Error(w, "no such trace", http.StatusNotFound)
		return
	}

	var payloads []proto.Message
	switch kind {
	case "traces":
		for _, req := range f.traces {
			payloads = append(payloads, req)
		}
	case "logs":
		for _, req := range f.logs {
			payloads = append(payloads, req)
		}
	case "metrics":
		// The capture carries no metrics: the sink the session forwards to
		// only stands up the span and log endpoints. An empty stream — just
		// the terminal frame — is a legitimate answer.
	default:
		http.Error(w, "no such stream", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", otlpstream.ContentType)
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	fw := otlpstream.NewFrameWriter(w)
	for _, payload := range payloads {
		data, err := proto.Marshal(payload)
		if err != nil {
			return
		}
		if err := fw.WriteData(data); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	// The terminal frame is the end of trace; a connection that merely
	// closes reads as truncation, and the client fails the restore on it.
	_ = fw.WriteTerminal()
}

// serveCapture stands the fake Cloud up and returns an OTLP client pointed at
// it.
//
// The credential is built rather than read from the environment, and the URL
// is passed rather than exported as DAGGER_CLOUD_URL: both are process-wide
// mutations this suite runs too parallel to make. A Basic token renders its
// Authorization header without any network of its own.
func serveCapture(t *testctx.T, srv *fakeCloudTrace) *cloud.OTLPClient {
	t.Helper()
	cloudSrv := httptest.NewServer(srv)
	t.Cleanup(cloudSrv.Close)

	client, err := cloud.NewOTLPClient(t.Context(), &auth.Cloud{
		Token: &oauth2.Token{TokenType: "Basic", AccessToken: "restore-test-token"},
	})
	require.NoError(t, err)
	client, err = client.WithBaseURL(cloudSrv.URL)
	require.NoError(t, err)
	return client
}

// restoringDB is the DB a resuming client holds when the fetch runs: its OWN
// session's root span, and nothing else yet.
//
// That span is what makes the fetched trace foreign. RestorePlan leaves out
// every agent with a span in the LIVE trace — an agent this session already
// holds is precisely the one Agent.rehydrate refuses — and it reads "live"
// off the DB's own root, so a DB with no root of its own would treat the
// imported trace as its own and project an empty plan.
func restoringDB(t *testctx.T) *dagui.DB {
	t.Helper()
	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{{
		ID:        dagui.SpanID{SpanID: trace.SpanID{1}},
		TraceID:   dagui.TraceID{TraceID: trace.TraceID{1}},
		Name:      "dagger agent --trace",
		StartTime: time.Now(),
	}})
	return db
}

// fetchAndPlan runs the real §5.1 fetch through the real importer into db, and
// projects the restore plan from what landed.
func fetchAndPlan(ctx context.Context, t *testctx.T, client *cloud.OTLPClient, db *dagui.DB, traceID string) []dagui.AgentRestore {
	t.Helper()
	require.NoError(t, client.FetchTrace(ctx, traceID, enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans:   db,
		Logs:    db.LogExporter(),
		Metrics: db.MetricExporter(),
	})))
	return db.RestorePlan()
}

// restoreAgent executes one plan entry the way the CLI does: rebuild the
// anchor's ID from the call payloads that arrived, then re-hydrate the
// instance from it.
func restoreAgent(ctx context.Context, t *testctx.T, c *dagger.Client, db *dagui.DB, entry dagui.AgentRestore) *agentHandle {
	t.Helper()
	require.True(t, entry.Restorable(), "agent %q is unrestorable: %v", entry.Name, entry.Err)

	callID, err := db.CallIDForDigest(entry.SnapshotDigest)
	require.NoError(t, err,
		"agent %q's committed conversation did not rebuild from the trace", entry.Name)
	snapshotID, err := callID.Encode()
	require.NoError(t, err)

	h, err := rehydrateAgentWithParent(ctx, c, snapshotID, entry.ID, entry.Name, entry.State, entry.Error, entry.ParentAgentID)
	require.NoError(t, err, "re-hydrating agent %q", entry.Name)
	return h
}

func planByName(t *testctx.T, plan []dagui.AgentRestore) map[string]dagui.AgentRestore {
	t.Helper()
	byName := map[string]dagui.AgentRestore{}
	for _, entry := range plan {
		byName[entry.Name] = entry
	}
	return byName
}

// TestRestoreFromTrace is the whole feature, end to end and keyless: a chief
// and two workers run in one session, one worker is dismissed, and the OTLP
// that session published is served back through a fake Cloud and restored into
// a second session.
//
// Four claims, each of which only a round trip can settle:
//
//   - the chief comes back with the conversation it had, marker and all;
//   - a send CONTINUES that conversation — decided by the replay provider,
//     which diverges on any history but the recorded one;
//   - a live worker's state and anchor survive;
//   - a dismissed worker restores as a dormant tombstone whose real snapshot
//     can be relaunched by a message.
func (AgentRestoreSuite) TestRestoreFromTrace(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}

	// The blocking edges here hang by design when one breaks (a turn that
	// never lands leaves awaitAgents polling), so bound the whole thing into
	// a located failure.
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()

	run := identity.NewID()
	var (
		chiefPrompt1 = "chief opening " + run
		chiefReply1  = "chief remembers " + run
		chiefPrompt2 = "chief follow-up " + run
		chiefReply2  = "chief continued " + run
		scoutPrompt  = "scout task " + run
		scoutReply   = "scout reported " + run
		testsPrompt  = "tests task " + run
		testsReply   = "tests reported " + run
		testsPrompt2 = "tests follow-up " + run
		testsReply2  = "tests restarted " + run
	)

	sink := newAgentTraceSink(t)
	source := connect(ctx, t, sink.clientOpts()...)

	// The chief's recording has TWO turns, and the second one is the whole
	// point: it is only reachable from the first turn's history. A restore
	// that opened an empty conversation would hand the replayer [prompt2]
	// where it expects [prompt1] and fail the turn outright.
	chiefModel := cannedRecordingModel(ctx, t, source, source.LLM().
		WithPrompt(chiefPrompt1).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: chiefReply1},
		}).
		WithPrompt(chiefPrompt2).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: chiefReply2},
		}))
	scoutModel := cannedRecordingModel(ctx, t, source, source.LLM().
		WithPrompt(scoutPrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: scoutReply},
		}))
	testsModel := cannedRecordingModel(ctx, t, source, source.LLM().
		WithPrompt(testsPrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: testsReply},
		}).
		WithPrompt(testsPrompt2).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: testsReply2},
		}))

	chief := spawnAgent(ctx, t, source, spawnOpts{model: chiefModel, name: "chief"})
	scout := spawnAgent(ctx, t, source, spawnOpts{model: scoutModel, name: "scout"})
	// The dismissed worker gets a TOOL bound, which puts an ID literal in its
	// seed chain — llm.withTools(object: <Directory ID>) — and therefore in
	// its snapshot chain. §13.5 measured 71 of 177 call payloads on a CI trace
	// failing to rebuild, every one of them on an argument frame whose payload
	// was never published, and left "whether an agent's snapshot chain is
	// affected" as a question for this test. This is the shape that answers
	// it: a chief's own conversation has exactly this frame, since binding a
	// module object as its toolset is what makes it a chief.
	toolID, err := source.Directory().
		WithNewFile("notes.md", "restore me "+run).
		ID(ctx)
	require.NoError(t, err)
	tests := spawnAgent(ctx, t, source, spawnOpts{
		model: testsModel, name: "tests", toolIDs: []dagger.ID{toolID},
	})

	for _, turn := range []struct {
		h              *agentHandle
		prompt, expect string
	}{
		{chief, chiefPrompt1, chiefReply1},
		{scout, scoutPrompt, scoutReply},
		{tests, testsPrompt, testsReply},
	} {
		_, reply, err := turn.h.sendAndWait(ctx, t, turn.prompt)
		require.NoError(t, err)
		require.Equal(t, turn.expect, reply)
	}

	// Dismiss one worker, on purpose: the stop reason is what tells a
	// dismissal apart from the stop session teardown performs, and without it
	// the whole session would restore as tombstones or none of it would.
	require.Equal(t, "STOPPED", tests.mustVerb(ctx, t, "stop"))

	// The source session's trace, as its own client saw it. Two things have
	// to land before it is worth capturing, and each rides its own export:
	// every anchor's payload (or the plan names conversations nothing can
	// rebuild), and the dismissal's STOPPED record (or the plan puts the
	// worker back into its pre-stop state).
	rostered := sink.awaitRestorable(t, 3)
	sink.awaitAgentState(t, "tests", "STOPPED")
	require.Contains(t, rostered, "chief")
	require.NotNil(t, rostered["chief"].Control)
	sourceTraceID := rostered["chief"].Control.Trace
	traces, logs := sink.capture()
	require.NotEmpty(t, traces)
	require.NotEmpty(t, logs)

	// A SECOND session, with a sink of its own: what it publishes is what a
	// chained resume would have to read back (§8).
	restoredSink := newAgentTraceSink(t)
	target := connect(ctx, t, restoredSink.clientOpts()...)

	client := serveCapture(t, &fakeCloudTrace{traceID: sourceTraceID, traces: traces, logs: logs})
	db := restoringDB(t)
	plan := fetchAndPlan(ctx, t, client, db, sourceTraceID)

	byName := planByName(t, plan)
	require.Len(t, plan, 3, "the plan must name every agent the source session published: %+v", plan)
	require.Equal(t, "IDLE", byName["chief"].State)
	require.Equal(t, "IDLE", byName["scout"].State)
	require.Equal(t, "STOPPED", byName["tests"].State,
		"an explicitly dismissed worker restores as a tombstone, not as the state before it")

	restoredChief := restoreAgent(ctx, t, target, db, byName["chief"])
	restoredScout := restoreAgent(ctx, t, target, db, byName["scout"])
	restoredTests := restoreAgent(ctx, t, target, db, byName["tests"])

	// (1) The chief came back with the conversation it had.
	transcript, lastReply := restoredChief.snapshot(ctx, t)
	require.Contains(t, transcript, chiefPrompt1)
	require.Contains(t, transcript, chiefReply1,
		"the restored chief lost the turn only the pre-restore session produced")
	require.Equal(t, chiefReply1, lastReply)

	// (2) And a send CONTINUES it. The replay provider decides this: it
	// diverges on any history but the recorded one, so a reply at all means
	// the restored conversation really is the old one.
	_, reply, err := restoredChief.sendAndWait(ctx, t, chiefPrompt2)
	require.NoError(t, err, "the restored conversation did not continue")
	require.Equal(t, chiefReply2, reply)

	// (3) The live worker's state and snapshot survive.
	require.Equal(t, "IDLE", restoredScout.state(ctx, t))
	transcript, _ = restoredScout.snapshot(ctx, t)
	require.Contains(t, transcript, scoutReply)

	// (4) The dismissed worker is restored dormant with its REAL conversation,
	// then a send relaunches the same restored instance from that snapshot. The
	// replay provider only knows the follow-up after the source turn, so the
	// reply proves the history survived both stop and restore.
	require.Equal(t, "STOPPED", restoredTests.state(ctx, t))
	transcript, _ = restoredTests.snapshot(ctx, t)
	require.Contains(t, transcript, testsReply,
		"the tombstone restored from its seed rather than its conversation")
	delivery, reply, err := restoredTests.sendAndWait(ctx, t, testsPrompt2)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
	require.Equal(t, testsReply2, reply)
	require.Equal(t, "IDLE", restoredTests.state(ctx, t))

	// (5) Chained resume (§8): the resumed session's own trace has to carry
	// the restored chains, or resuming IT would fail. Every restored agent
	// publishes its identity and anchor into the new trace, and every anchor
	// has to rebuild from the payloads that rode with it — awaitRestorable
	// fails the test if one never does.
	chained := restoredSink.awaitRestorable(t, 3)
	require.Contains(t, chained, "chief")
}

// TestRestoreFromTraceRefusesAnUnrestorableAgent is the other half of §5.3.3,
// on real telemetry: an anchor whose conversation never reached the restoring
// client must fail loudly rather than produce a handle that looks fine.
//
// The gap has to be manufactured, and the only honest way to do it turned out
// to be blunt: withholding the call-payload LOG records is not enough, because
// a frame that got a span of its own carries its payload on the span
// (core/telemetry.go stamps dagger.io/dag.call), and in a session this small
// every frame of the chain gets one. The log channel is the fallback for the
// frames that structurally never get a span — so a capture that keeps its
// spans keeps its payloads. Stripping BOTH is what an incomplete trace
// actually looks like to a client, and it is what §9's first row describes.
func (AgentRestoreSuite) TestRestoreFromTraceRefusesAnUnrestorableAgent(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	run := identity.NewID()
	prompt, answer := "lonely prompt "+run, "lonely reply "+run

	sink := newAgentTraceSink(t)
	source := connect(ctx, t, sink.clientOpts()...)

	model := cannedRecordingModel(ctx, t, source, source.LLM().
		WithPrompt(prompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: answer},
		}))
	h := spawnAgent(ctx, t, source, spawnOpts{model: model, name: "solo"})
	_, reply, err := h.sendAndWait(ctx, t, prompt)
	require.NoError(t, err)
	require.Equal(t, answer, reply)

	// Wait for the anchor to be rebuildable BEFORE stripping, so the refusal
	// below is caused by the strip and not by a payload that had simply not
	// arrived yet.
	rostered := sink.awaitRestorable(t, 1)
	// Canonical controls can arrive before diagnostic loop spans.
	require.Contains(t, rostered, "solo")
	require.NotNil(t, rostered["solo"].Control)
	traceID := rostered["solo"].Control.Trace
	traces, logs := sink.capture()

	// Serve the spans and the agent's own state/anchor records, but no call
	// payloads on either channel: the anchor still names a conversation, and
	// nothing can rebuild it.
	client := serveCapture(t, &fakeCloudTrace{
		traceID: traceID,
		traces:  withoutSpanCallPayloads(traces),
		logs:    withoutCallPayloadRecords(logs),
	})
	db := restoringDB(t)
	plan := fetchAndPlan(ctx, t, client, db, traceID)
	require.Len(t, plan, 1)

	entry := plan[0]
	require.True(t, entry.Restorable(),
		"the PROJECTION is fine — the trace says what to restore; it is the rebuild that cannot")
	_, err = db.CallIDForDigest(entry.SnapshotDigest)
	require.ErrorContains(t, err, "never reached this client")
}

// TestRestoreDormantLifecycleGraph exercises creation-only control publication,
// pre-teardown state mapping, and preservation of failure information across two
// restores. None of the destination agents may run a model merely by restoring.
func (AgentRestoreSuite) TestRestoreDormantLifecycleGraph(ctx context.Context, t *testctx.T) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	source, sink := connectWithTrace(ctx, t)
	spawnAgent(ctx, t, source, spawnOpts{model: emptyReplayModel, name: "dormant"})
	paused := spawnAgent(ctx, t, source, spawnOpts{model: emptyReplayModel, name: "paused"})
	require.Equal(t, "PAUSED", paused.mustVerb(ctx, t, "pause"))
	failed := spawnAgent(ctx, t, source, spawnOpts{model: emptyReplayModel, name: "failed"})
	_, err := failed.sendNoWait(ctx, t, "fail once")
	require.NoError(t, err)
	failed.mustRun(ctx, t, "wait")
	require.Equal(t, "FAILED", failed.state(ctx, t))
	failure := failed.mustRun(ctx, t, "error").Get("error").String()
	require.Contains(t, failure, "no more messages")
	stopped := spawnAgent(ctx, t, source, spawnOpts{model: emptyReplayModel, name: "dismissed"})
	require.Equal(t, "STOPPED", stopped.mustVerb(ctx, t, "stop"))

	sink.awaitRestorable(t, 4)
	require.NoError(t, source.Close())
	wantStates := map[string]string{"dormant": "IDLE", "paused": "PAUSED", "failed": "FAILED", "dismissed": "STOPPED"}
	for range 2 {
		traces, logs := sink.capture()
		db := restoringDB(t)
		importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{Spans: db, Logs: db.LogExporter(), Metrics: db.MetricExporter()})
		for _, batch := range traces {
			require.NoError(t, importer.ImportSpans(ctx, batch))
		}
		for _, batch := range logs {
			require.NoError(t, importer.ImportLogs(ctx, batch))
		}
		plan := db.RestorePlan()
		require.Len(t, plan, len(wantStates))
		target, targetSink := connectWithTrace(ctx, t)
		sink = targetSink
		for _, entry := range plan {
			require.Equal(t, wantStates[entry.Name], entry.State, "%s", entry.Name)
			h := restoreAgent(ctx, t, target, db, entry)
			require.Equal(t, wantStates[entry.Name], h.state(ctx, t))
			if entry.Name == "failed" {
				require.Equal(t, failure, entry.Error)
				require.Equal(t, failure, h.mustRun(ctx, t, "error").Get("error").String())
			}
			// All providers are empty recordings: a spontaneous turn would fail
			// an idle/paused agent, or change the preserved failure text.
		}
		sink.awaitRestorable(t, 4)
		require.NoError(t, target.Close())
	}
}

// TestRestoreWorkspaceAfterSourceDisappears resolves only a traced anchor after
// the source connection is closed and its checkout has been removed. A distinct
// destination checkout intentionally disagrees with both frozen and pending data.
func (AgentRestoreSuite) TestRestoreWorkspaceAfterSourceDisappears(ctx context.Context, t *testctx.T) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	sourceDir, _ := workspaceExportCheckout(ctx, t)
	// Only remote-backed snapshots survive the source session. Keep the Git
	// base available independently of the checkout that is deleted below.
	publishCheckpointRemote(ctx, t, sourceDir)
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "base.txt"), []byte("source"), 0o644))
	source, sink := connectWithTrace(ctx, t, engineconn.Config{Workdir: sourceDir})
	frozen := snapshotWorkspace(ctx, t, source, source.CurrentWorkspace())
	wsID, err := frozen.WithNewFile("pending.txt", "unexported").ID(ctx)
	require.NoError(t, err)
	spawnAgent(ctx, t, source, spawnOpts{model: emptyReplayModel, name: "frozen", wsID: wsID})
	sink.awaitRestorable(t, 1)
	require.NoError(t, source.Close())
	require.NoError(t, os.RemoveAll(sourceDir))

	destinationDir, _ := workspaceExportCheckout(ctx, t)
	require.NoError(t, os.WriteFile(filepath.Join(destinationDir, "base.txt"), []byte("destination"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(destinationDir, "pending.txt"), []byte("not the pending edit"), 0o644))
	target, _ := connectWithTrace(ctx, t, engineconn.Config{Workdir: destinationDir})
	traces, logs := sink.capture()
	db := restoringDB(t)
	importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{Spans: db, Logs: db.LogExporter(), Metrics: db.MetricExporter()})
	for _, batch := range traces {
		require.NoError(t, importer.ImportSpans(ctx, batch))
	}
	for _, batch := range logs {
		require.NoError(t, importer.ImportLogs(ctx, batch))
	}
	plan := db.RestorePlan()
	require.Len(t, plan, 1)
	h := restoreAgent(ctx, t, target, db, plan[0])
	out := h.mustRun(ctx, t, `snapshot { workspace { source: file(path: "base.txt") { contents } pending: file(path: "pending.txt") { contents } } }`)
	require.Equal(t, "source", out.Get("snapshot.workspace.source.contents").String())
	require.Equal(t, "unexported", out.Get("snapshot.workspace.pending.contents").String())
	require.Equal(t, "IDLE", h.state(ctx, t))
	local, err := os.ReadFile(filepath.Join(destinationDir, "base.txt"))
	require.NoError(t, err)
	require.Equal(t, "destination", string(local), "restore must not implicitly export")
}

// TestRestoreNotificationGraph uses the actual published watched-to-subscriber
// graph, including a non-parent filter replacement and a removal tombstone.
// Pending source mailbox events are deliberately excluded from the guarantee.
func (AgentRestoreSuite) TestRestoreNotificationGraph(ctx context.Context, t *testctx.T) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	source, sink := connectWithTrace(ctx, t)
	workerModel := cannedRecordingModel(ctx, t, source, source.LLM().
		WithPrompt("old task").WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "old completion"}}).
		WithPrompt("new task").WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "new completion"}}))
	chiefModel := cannedRecordingModel(ctx, t, source, source.LLM().
		WithPrompt(agentIdleEventText("worker", "new completion")).
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "new completion noted"}}))
	chief := spawnAgent(ctx, t, source, spawnOpts{model: chiefModel, name: "chief"})
	chiefID := chief.mustRun(ctx, t, "handle").Get("handle").String()
	worker := spawnAgent(ctx, t, source, spawnOpts{model: workerModel, name: "worker", parentHandle: chiefID, handle: identity.NewID()})
	observer := spawnAgent(ctx, t, source, spawnOpts{model: emptyReplayModel, name: "observer"})
	removed := spawnAgent(ctx, t, source, spawnOpts{model: emptyReplayModel, name: "removed"})
	_, reply, err := worker.sendAndWait(ctx, t, "old task")
	require.NoError(t, err)
	require.Equal(t, "old completion", reply)
	for _, sub := range []struct {
		agent  *agentHandle
		states string
	}{
		{chief, "IDLE"}, {observer, "IDLE"}, {observer, "FAILED"}, {removed, "FAILED"}, {removed, ""},
	} {
		worker.mustRun(ctx, t, fmt.Sprintf(`notify(subscriber: %q, on: [%s])`, sub.agent.agentID, sub.states))
	}
	sink.awaitRestorable(t, 4)
	require.NoError(t, source.Close())
	traces, logs := sink.capture()
	db := restoringDB(t)
	importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{Spans: db, Logs: db.LogExporter(), Metrics: db.MetricExporter()})
	for _, batch := range traces {
		require.NoError(t, importer.ImportSpans(ctx, batch))
	}
	for _, batch := range logs {
		require.NoError(t, importer.ImportLogs(ctx, batch))
	}
	plan := planByName(t, db.RestorePlan())
	require.Len(t, plan, 4)
	require.Equal(t, plan["chief"].ID, plan["worker"].ParentAgentID)
	_, edges, err := db.AgentControl()
	require.NoError(t, err)
	require.Len(t, edges, 3)
	target, _ := connectWithTrace(ctx, t)
	restored := map[string]*agentHandle{}
	for _, name := range []string{"chief", "worker", "observer", "removed"} {
		entry := plan[name]
		restored[entry.ID] = restoreAgent(ctx, t, target, db, entry)
	}
	for _, edge := range edges {
		require.Equal(t, plan["worker"].Source.Namespace, edge.Namespace)
		require.Equal(t, plan["worker"].ID, edge.Watched)
		switch edge.Subscriber {
		case plan["chief"].ID:
			require.Equal(t, []string{"IDLE"}, edge.States)
		case plan["observer"].ID:
			require.Equal(t, []string{"FAILED"}, edge.States)
		case plan["removed"].ID:
			require.Empty(t, edge.States)
		default:
			t.Fatalf("unexpected subscriber %s", edge.Subscriber)
		}
		// The watched worker is restored and not yet activated, so the public
		// notify reinstalls the edge without announcing its restored state.
		restored[edge.Watched].mustRun(ctx, t, fmt.Sprintf(`notify(subscriber: %q, on: [%s])`, restored[edge.Subscriber].agentID, strings.Join(edge.States, ",")))
	}
	restoredChief := restored[plan["chief"].ID]
	restoredObserver := restored[plan["observer"].ID]
	restoredRemoved := restored[plan["removed"].ID]
	for _, h := range []*agentHandle{restoredChief, restoredObserver, restoredRemoved} {
		h.mustRun(ctx, t, "resume")
	}
	// A level check against the restored state would queue the old completion.
	// Explicitly start the subscriber loops to expose even queued stale events.
	require.Never(t, func() bool {
		for _, h := range []*agentHandle{restoredChief, restoredObserver, restoredRemoved} {
			if h.state(ctx, t) != "IDLE" || len(h.mustRun(ctx, t, "snapshot { messages { role } }").Get("snapshot.messages").Array()) != 0 {
				return true
			}
		}
		return false
	}, time.Second, 100*time.Millisecond, "restore synthesized a historical notification")
	restoredWorker := restored[plan["worker"].ID]
	_, reply, err = restoredWorker.sendAndWait(ctx, t, "new task")
	require.NoError(t, err)
	require.Equal(t, "new completion", reply)
	require.Eventually(t, func() bool { _, reply := restoredChief.snapshot(ctx, t); return reply == "new completion noted" }, time.Minute, 100*time.Millisecond)
	require.Equal(t, "IDLE", restoredObserver.state(ctx, t), "FAILED-only subscriber must not hear IDLE")
	_, err = restoredWorker.sendNoWait(ctx, t, "exhaust recording")
	require.NoError(t, err)
	restoredWorker.mustRun(ctx, t, "wait")
	require.Equal(t, "FAILED", restoredWorker.state(ctx, t))
	// The matching FAILED notification wakes the observer's empty provider;
	// its failure proves delivery without requiring unstable rendered error text.
	require.Eventually(t, func() bool { return restoredObserver.state(ctx, t) == "FAILED" }, time.Minute, 100*time.Millisecond)
	out := restoredObserver.mustRun(ctx, t, `snapshot { messages { origin { kind agentName } } }`)
	require.Equal(t, "EVENT", out.Get("snapshot.messages.0.origin.kind").String())
	require.Equal(t, "worker", out.Get("snapshot.messages.0.origin.agentName").String())
	require.Equal(t, "IDLE", restoredRemoved.state(ctx, t))
	require.Empty(t, restoredRemoved.mustRun(ctx, t, "snapshot { messages { role } }").Get("snapshot.messages").Array())
}

// TestArchiveSurvivesEngineRestart seals a real source session, stops the actual
// engine process, and starts a different process over the same state volume.
// No historical stream is downloaded before restoring and executing a new turn.
func (AgentRestoreSuite) TestArchiveSurvivesEngineRestart(ctx context.Context, t *testctx.T) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	host := connect(ctx, t)
	engine := devEngineContainer(host)
	startEngine := func(ctr *dagger.Container) (*dagger.Service, *dagger.Service, string) {
		t.Helper()
		service, err := devEngineContainerAsService(ctr).Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = service.Stop(context.WithoutCancel(ctx), dagger.ServiceStopOpts{Kill: true}) })
		tunnel, err := host.Host().Tunnel(service).Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = tunnel.Stop(context.WithoutCancel(ctx)) })
		endpoint, err := tunnel.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		return service, tunnel, endpoint
	}
	first, tunnel, endpoint := startEngine(engine)
	provider := sdktrace.NewTracerProvider()
	defer provider.Shutdown(context.WithoutCancel(ctx))
	sourceCtx, sourceSpan := provider.Tracer("archive-acceptance").Start(ctx, "archive source", trace.WithNewRoot())
	defer sourceSpan.End()
	source, sourceSink := connectWithTrace(sourceCtx, t, engineconn.Config{RunnerHost: endpoint})
	model := cannedRecordingModel(sourceCtx, t, source, source.LLM().
		WithPrompt("before restart").WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "remembered before restart"}}).
		WithPrompt("after restart").WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "continued after restart"}}))
	original := spawnAgent(sourceCtx, t, source, spawnOpts{model: model, name: "archive-worker"})
	_, reply, err := original.sendAndWait(sourceCtx, t, "before restart")
	require.NoError(t, err)
	require.Equal(t, "remembered before restart", reply)
	node := sourceSink.awaitRestorable(t, 1)["archive-worker"]
	require.NotNil(t, node.Control)
	traceID := node.Control.Trace
	require.NoError(t, source.Close(), "graceful close must finish durable finalization")
	sourceSpan.End()
	_, err = tunnel.Stop(ctx)
	require.NoError(t, err)
	_, err = first.Stop(ctx)
	require.NoError(t, err)
	// A different service identity makes this a process restart, not a second
	// connection to a still-running engine. The mounted state cache is unchanged.
	_, _, endpoint = startEngine(engine.WithEnvVariable("ARCHIVE_RESTART", identity.NewID()))
	targetCtx, targetSpan := provider.Tracer("archive-acceptance").Start(ctx, "archive destination", trace.WithNewRoot())
	defer targetSpan.End()
	target, targetSink := connectWithTrace(targetCtx, t, engineconn.Config{RunnerHost: endpoint})
	client := archive.NewClient(targetSink.conn)
	// First authenticated request: no executable GraphQL query has primed the
	// destination client. Archive access must establish authorization itself.
	manifests, err := client.ListAll(targetCtx, archive.ListOptions{})
	require.NoError(t, err)
	var manifest *archive.Manifest
	for _, candidate := range manifests {
		if candidate.TraceID == traceID {
			manifest = &candidate
		}
	}
	require.NotNil(t, manifest, "archive disappeared across engine restart")
	require.Equal(t, archive.StateClosed, manifest.State, "archive failure: %s", manifest.Failure)
	release, err := client.Acquire(targetCtx, traceID)
	require.NoError(t, err)
	defer release()
	db := restoringDB(t)
	importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{Spans: db, Logs: db.LogExporter(), Metrics: db.MetricExporter()})
	result, err := client.Bootstrap(targetCtx, traceID, func(_ archive.BootstrapHeader, batch archive.BootstrapBatch) error {
		if batch.Traces != nil {
			return importer.ImportSpans(targetCtx, batch.Traces)
		}
		return importer.ImportLogs(targetCtx, batch.Logs)
	})
	require.NoError(t, err)
	require.Equal(t, traceID, result.Header.TraceID)
	plan := db.RestorePlan()
	require.Len(t, plan, 1)
	require.Equal(t, "archive-worker", plan[0].Name)
	require.Equal(t, "IDLE", plan[0].State)
	restored := restoreAgent(targetCtx, t, target, db, plan[0])
	transcript, _ := restored.snapshot(targetCtx, t)
	require.Contains(t, transcript, "remembered before restart")
	_, reply, err = restored.sendAndWait(targetCtx, t, "after restart")
	require.NoError(t, err)
	require.Equal(t, "continued after restart", reply)
	require.NoError(t, target.Close())
}

// TestArchiveUnsealedAfterEngineCrash kills the source engine process without
// any graceful finalization, so its archive is never sealed. After a restart
// over the same state volume the archive reads as interrupted: it has no
// bootstrap, but an unsealed lease streams everything it recorded, and the
// agent restores from its latest recorded state and continues.
func (AgentRestoreSuite) TestArchiveUnsealedAfterEngineCrash(ctx context.Context, t *testctx.T) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	host := connect(ctx, t)
	engine := devEngineContainer(host)
	startEngine := func(ctr *dagger.Container) (*dagger.Service, *dagger.Service, string) {
		t.Helper()
		service, err := devEngineContainerAsService(ctr).Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = service.Stop(context.WithoutCancel(ctx), dagger.ServiceStopOpts{Kill: true}) })
		tunnel, err := host.Host().Tunnel(service).Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = tunnel.Stop(context.WithoutCancel(ctx)) })
		endpoint, err := tunnel.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		return service, tunnel, endpoint
	}
	first, tunnel, endpoint := startEngine(engine)
	provider := sdktrace.NewTracerProvider()
	defer provider.Shutdown(context.WithoutCancel(ctx))
	sourceCtx, sourceSpan := provider.Tracer("archive-acceptance").Start(ctx, "crashed source", trace.WithNewRoot())
	defer sourceSpan.End()
	source, sourceSink := connectWithTrace(sourceCtx, t, engineconn.Config{RunnerHost: endpoint})
	model := cannedRecordingModel(sourceCtx, t, source, source.LLM().
		WithPrompt("before crash").WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "remembered before crash"}}).
		WithPrompt("after crash").WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "continued after crash"}}))
	original := spawnAgent(sourceCtx, t, source, spawnOpts{model: model, name: "crash-worker"})
	_, reply, err := original.sendAndWait(sourceCtx, t, "before crash")
	require.NoError(t, err)
	require.Equal(t, "remembered before crash", reply)
	node := sourceSink.awaitRestorable(t, 1)["crash-worker"]
	require.NotNil(t, node.Control)
	traceID := node.Control.Trace
	// No client close and no graceful engine stop: SIGKILL the engine while the
	// session is still open, so finalization never runs.
	_, err = first.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
	require.NoError(t, err)
	_, _ = tunnel.Stop(ctx)
	_ = source.Close()
	sourceSpan.End()

	_, _, endpoint = startEngine(engine.WithEnvVariable("ARCHIVE_CRASH_RESTART", identity.NewID()))
	targetCtx, targetSpan := provider.Tracer("archive-acceptance").Start(ctx, "crash recovery", trace.WithNewRoot())
	defer targetSpan.End()
	target, targetSink := connectWithTrace(targetCtx, t, engineconn.Config{RunnerHost: endpoint})
	client := archive.NewClient(targetSink.conn)
	manifests, err := client.ListAll(targetCtx, archive.ListOptions{})
	require.NoError(t, err)
	var manifest *archive.Manifest
	for _, candidate := range manifests {
		if candidate.TraceID == traceID {
			manifest = &candidate
		}
	}
	require.NotNil(t, manifest, "archive disappeared across engine crash")
	require.Equal(t, archive.StateInterrupted, manifest.State)
	_, err = client.Acquire(targetCtx, traceID)
	require.ErrorIs(t, err, archive.ErrState, "an unsealed archive has no verified bootstrap")

	unsealed, err := client.AcquireUnsealed(targetCtx, traceID)
	require.NoError(t, err)
	defer unsealed.Release()
	db := restoringDB(t)
	importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{Spans: db, Logs: db.LogExporter(), Metrics: db.MetricExporter()})
	opts := func(high int64) archive.StreamOptions {
		return archive.StreamOptions{HighWater: high, Unsealed: true}
	}
	_, err = client.Traces(targetCtx, traceID, opts(unsealed.Cut.Spans), func(_ int64, batch *coltracepb.ExportTraceServiceRequest) error {
		return importer.ImportSpans(targetCtx, batch)
	})
	require.NoError(t, err)
	_, err = client.Logs(targetCtx, traceID, opts(unsealed.Cut.Logs), func(_ int64, batch *collogspb.ExportLogsServiceRequest) error {
		return importer.ImportLogs(targetCtx, batch)
	})
	require.NoError(t, err)
	plan := db.RestorePlan()
	require.Len(t, plan, 1)
	require.Equal(t, "crash-worker", plan[0].Name)
	require.Equal(t, "IDLE", plan[0].State)
	restored := restoreAgent(targetCtx, t, target, db, plan[0])
	transcript, _ := restored.snapshot(targetCtx, t)
	require.Contains(t, transcript, "remembered before crash")
	_, reply, err = restored.sendAndWait(targetCtx, t, "after crash")
	require.NoError(t, err)
	require.Equal(t, "continued after crash", reply)
	require.NoError(t, target.Close())
}

// TestCLIArchiveResumeIgnoresDestination runs the real from-source command and
// drives its headless TUI prompt, rather than calling restore orchestration seams.
func (AgentRestoreSuite) TestCLIArchiveResumeIgnoresDestination(ctx context.Context, t *testctx.T) {
	testCLITraceResume(ctx, t, false)
}

func (AgentRestoreSuite) TestCLICloudFallbackIgnoresDestination(ctx context.Context, t *testctx.T) {
	testCLITraceResume(ctx, t, true)
}

func testCLITraceResume(ctx context.Context, t *testctx.T, cloudOnly bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	provider := sdktrace.NewTracerProvider()
	defer provider.Shutdown(context.WithoutCancel(ctx))
	sourceCtx, sourceSpan := provider.Tracer("archive-cli-acceptance").Start(ctx, "source", trace.WithNewRoot())
	defer sourceSpan.End()
	source, sink := connectWithTrace(sourceCtx, t)
	model := cannedRecordingModel(sourceCtx, t, source, source.LLM().
		WithPrompt("before CLI restore").WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "source conversation retained"}}).
		WithPrompt("after CLI restore").WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "RESTORED-PROMPT-TURN-SUCCEEDED"}}))
	wsID, err := source.Directory().WithNewFile("authority.txt", "traced source").AsWorkspace().ID(sourceCtx)
	require.NoError(t, err)
	// A dismissed failure remains in the graph with its original diagnostic.
	// Restoring it as STOPPED must not pass that FAILED-only spawn argument.
	dismissed := spawnAgent(sourceCtx, t, source, spawnOpts{model: emptyReplayModel, name: "dismissed", wsID: wsID})
	_, err = dismissed.sendNoWait(sourceCtx, t, "fail before dismissal")
	require.NoError(t, err)
	dismissed.mustRun(sourceCtx, t, "wait")
	require.Equal(t, "FAILED", dismissed.state(sourceCtx, t))
	require.Equal(t, "STOPPED", dismissed.mustVerb(sourceCtx, t, "stop"))
	sink.awaitAgentState(t, "dismissed", "STOPPED")
	h := spawnAgent(sourceCtx, t, source, spawnOpts{model: model, name: "cli-restored", wsID: wsID})
	_, reply, err := h.sendAndWait(sourceCtx, t, "before CLI restore")
	require.NoError(t, err)
	require.Equal(t, "source conversation retained", reply)
	nodes := sink.awaitRestorable(t, 2)
	require.NotEmpty(t, nodes["dismissed"].Control.Failure)
	node := nodes["cli-restored"]
	require.NotNil(t, node.Control)
	traceID := node.Control.Trace
	require.NoError(t, source.Close())
	sourceSpan.End()

	cloudURL := ""
	var cloudRequests atomic.Int32
	if cloudOnly {
		// Keep the real canonical capture but serve it under a fresh trace ID:
		// this engine has no archive for that ID, so the actual CLI must miss
		// locally and fetch all three Cloud streams before restoring anything.
		_, cloudSpan := provider.Tracer("cloud-only-fixture").Start(ctx, "cloud-only", trace.WithNewRoot())
		traceID = cloudSpan.SpanContext().TraceID().String()
		cloudSpan.End()
		cloudID, err := trace.TraceIDFromHex(traceID)
		require.NoError(t, err)
		traces, logs := sink.capture()
		for i, req := range traces {
			traces[i] = proto.Clone(req).(*coltracepb.ExportTraceServiceRequest)
			for _, resource := range traces[i].ResourceSpans {
				for _, scope := range resource.ScopeSpans {
					for _, span := range scope.Spans {
						span.TraceId = slices.Clone(cloudID[:])
					}
				}
			}
		}
		for i, req := range logs {
			logs[i] = proto.Clone(req).(*collogspb.ExportLogsServiceRequest)
			for _, resource := range logs[i].ResourceLogs {
				for _, scope := range resource.ScopeLogs {
					for _, rec := range scope.LogRecords {
						rec.TraceId = slices.Clone(cloudID[:])
						for _, kv := range rec.Attributes {
							if kv.Key == agentcontrol.TraceAttr {
								kv.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: traceID}}
							}
						}
					}
				}
			}
		}
		fixture := &fakeCloudTrace{traceID: traceID, traces: traces, logs: logs}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cloudRequests.Add(1)
			fixture.ServeHTTP(w, r)
		}))
		defer server.Close()
		cloudURL = server.URL
	}

	destination := t.TempDir()
	// A destination-module load would fail before reaching the prompt.
	require.NoError(t, os.WriteFile(filepath.Join(destination, "dagger.toml"), []byte("[modules.broken]\nsource = \"definitely-missing-module\"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(destination, "authority.txt"), []byte("destination only"), 0o644))
	state := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	bin := os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN")
	require.NotEmpty(t, bin)
	commandCtx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(commandCtx, bin, "agent", "-r", traceID)
	cmd.Dir = destination
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "DAGGER_SESSION_PORT", "DAGGER_SESSION_TOKEN", "TRACEPARENT", "TRACESTATE", "DAGGER_TUI_CONSOLE", "DAGGER_PROGRESS", "XDG_STATE_HOME":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	if cloudOnly {
		cmd.Env = append(cmd.Env, "DAGGER_CLOUD_URL="+cloudURL, "DAGGER_CLOUD_TOKEN=restore-test-token")
	} else {
		// Discovery must also ignore the broken destination.
		// The listing is metadata-only and must not initialize an interactive LLM.
		// Client close flushes telemetry, but archive sealing finishes during
		// asynchronous session removal. Wait for discovery to show a final cut;
		// command errors and terminal failure states must not be retried away.
		var listing []byte
		var listErr error
		var state string
		require.Eventually(t, func() bool {
			listCmd := exec.CommandContext(ctx, bin, "agent", "-r")
			listCmd.Dir, listCmd.Env = destination, slices.Clone(cmd.Env)
			listing, listErr = listCmd.Output()
			if listErr != nil {
				return true
			}
			for _, line := range strings.Split(string(listing), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == traceID {
					state = fields[1]
					return state != string(archive.StateActive) && state != string(archive.StateFinalizing)
				}
			}
			return false
		}, time.Minute, 100*time.Millisecond, "archive discovery never reached a final state")
		require.NoError(t, listErr)
		require.Equal(t, string(archive.StateClosed), state, "archive listing: %s", listing)
	}
	cmd.Env = append(cmd.Env, "DAGGER_TUI_CONSOLE="+address, "DAGGER_PROGRESS=tty", "XDG_STATE_HOME="+state)
	logFile, err := os.CreateTemp(t.TempDir(), "cli-output")
	require.NoError(t, err)
	defer logFile.Close()
	cmd.Stdout, cmd.Stderr = logFile, logFile
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() { stop(); <-done }()
	defer func() {
		if t.Failed() {
			data, _ := os.ReadFile(logFile.Name())
			t.Logf("CLI output:\n%s", data)
		}
	}()
	client := &http.Client{Timeout: 5 * time.Second}
	request := func(method, path, body string) (int, string) {
		req, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, strings.NewReader(body))
		if err != nil {
			return 0, err.Error()
		}
		res, err := client.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			return 0, err.Error()
		}
		return res.StatusCode, string(data)
	}
	var last string
	defer func() {
		if t.Failed() {
			_, screen := request(http.MethodGet, "/screen", "")
			t.Logf("Last console response: %s\nScreen:\n%s", last, screen)
		}
	}()
	require.Eventually(t, func() bool {
		status, body := request(http.MethodGet, "/toolset", "")
		last = body
		return status == http.StatusOK
	}, time.Minute, 100*time.Millisecond, "interactive restored session never attached: %s", last)
	// The restored history is on screen before anything is sent: it hangs off
	// the imported trace, which must not wait on the live one to surface.
	require.Eventually(t, func() bool {
		_, body := request(http.MethodGet, "/screen", "")
		last = body
		return strings.Contains(body, "source conversation retained")
	}, time.Minute, 100*time.Millisecond, "restored history not shown before the first send: %s", last)
	status, body := request(http.MethodPost, "/type", "after CLI restore")
	require.Equal(t, http.StatusOK, status, "%s", body)
	status, body = request(http.MethodPost, "/key", "enter")
	require.Equal(t, http.StatusOK, status, "%s", body)
	require.Eventually(t, func() bool {
		_, body := request(http.MethodGet, "/screen", "")
		last = body
		return strings.Contains(body, "RESTORED-PROMPT-TURN-SUCCEEDED")
	}, time.Minute, 100*time.Millisecond, "restored prompt did not complete a turn: %s", last)
	if cloudOnly {
		require.GreaterOrEqual(t, cloudRequests.Load(), int32(3), "CLI must fetch the Cloud trace on a local miss")
	}

	// Ctrl+D on the empty prompt leaves the session: quietly, and pointing at
	// how to come back to it.
	status, body = request(http.MethodPost, "/key", "ctrl+d")
	require.Equal(t, http.StatusOK, status, "%s", body)
	select {
	case err := <-done:
		require.NoError(t, err, "Ctrl+D must exit cleanly")
		done <- err // for the deferred wait
	case <-time.After(time.Minute):
		t.Fatal("CLI did not exit after Ctrl+D")
	}
	output, err := os.ReadFile(logFile.Name())
	require.NoError(t, err)
	require.NotContains(t, string(output), "canceling...", "leaving the session is not an interrupt")
	require.Regexp(t, `To resume this session, run:\s+dagger agent --resume [0-9a-f]{32}\n`, string(output))

	data, err := os.ReadFile(filepath.Join(destination, "authority.txt"))
	require.NoError(t, err)
	require.Equal(t, "destination only", string(data), "restore must not implicitly export")
}

// withoutSpanCallPayloads strips the dagger.io/dag.call attribute from every
// span in a capture.
func withoutSpanCallPayloads(reqs []*coltracepb.ExportTraceServiceRequest) []*coltracepb.ExportTraceServiceRequest {
	stripped := make([]*coltracepb.ExportTraceServiceRequest, 0, len(reqs))
	for _, req := range reqs {
		clone, ok := proto.Clone(req).(*coltracepb.ExportTraceServiceRequest)
		if !ok {
			continue
		}
		for _, resource := range clone.GetResourceSpans() {
			for _, scope := range resource.GetScopeSpans() {
				for _, span := range scope.GetSpans() {
					span.Attributes = withoutAttrs(span.GetAttributes(), telemetry.DagCallAttr)
				}
			}
		}
		stripped = append(stripped, clone)
	}
	return stripped
}

// withoutCallPayloadRecords drops the call-payload records from a capture,
// leaving every other record intact.
func withoutCallPayloadRecords(reqs []*collogspb.ExportLogsServiceRequest) []*collogspb.ExportLogsServiceRequest {
	stripped := make([]*collogspb.ExportLogsServiceRequest, 0, len(reqs))
	for _, req := range reqs {
		clone, ok := proto.Clone(req).(*collogspb.ExportLogsServiceRequest)
		if !ok {
			continue
		}
		for _, resource := range clone.GetResourceLogs() {
			for _, scope := range resource.GetScopeLogs() {
				kept := scope.GetLogRecords()[:0]
				for _, record := range scope.GetLogRecords() {
					if !hasCallPayloadContentType(record.GetAttributes()) {
						kept = append(kept, record)
					}
				}
				scope.LogRecords = kept
			}
		}
		stripped = append(stripped, clone)
	}
	return stripped
}

func hasCallPayloadContentType(attrs []*commonpb.KeyValue) bool {
	for _, attr := range attrs {
		if attr.GetKey() == telemetry.ContentTypeAttr {
			return attr.GetValue().GetStringValue() == telemetryattrs.CallPayloadContentType
		}
	}
	return false
}

func withoutAttrs(attrs []*commonpb.KeyValue, key string) []*commonpb.KeyValue {
	kept := attrs[:0]
	for _, attr := range attrs {
		if attr.GetKey() != key {
			kept = append(kept, attr)
		}
	}
	return kept
}
