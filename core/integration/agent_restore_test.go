package core

// The end-to-end restore (hack/designs/resume-from-trace.md §10, "End to end,
// replay provider"): a real session's agents, captured as the OTLP it really
// published, served back through a fake Cloud speaking the §5.1 endpoints, and
// restored into a SECOND session.
//
// Everything under test here is only true once telemetry has crossed a wire
// twice. A chief's conversation has to survive being published as spans and
// call payloads, fetched back as protojson over SSE, folded into a fresh DB,
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
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/go-sse/sse"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/encoding/protojson"
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
// each an SSE stream of protojson-encoded OTLP export requests. The wire shape
// is the one measured against api.dagger.cloud in slice 5 (dagql/idtui/
// trace_live_test.go): a named, data-less preamble event, then payloads as
// unnamed events.
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
		// only stands up the span and log endpoints. An empty stream is a
		// legitimate answer, and one Cloud gave for real on the trace §13.5
		// probed.
	default:
		http.Error(w, "no such stream", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	_ = sse.Event{Name: "connected"}.Write(w)
	if flusher != nil {
		flusher.Flush()
	}
	for _, payload := range payloads {
		data, err := protojson.Marshal(payload)
		if err != nil {
			return
		}
		if err := (sse.Event{Data: data}).Write(w); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
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

	h, err := rehydrateAgent(ctx, c, snapshotID, entry.ID, entry.Name, entry.State, entry.Error)
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
	chiefModel := cannedReplayModel(ctx, t, source, source.LLM().
		WithPrompt(chiefPrompt1).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: chiefReply1},
		}).
		WithPrompt(chiefPrompt2).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: chiefReply2},
		}))
	scoutModel := cannedReplayModel(ctx, t, source, source.LLM().
		WithPrompt(scoutPrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: scoutReply},
		}))
	testsModel := cannedReplayModel(ctx, t, source, source.LLM().
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

	// The source session's trace, as its own client saw it.
	rostered := sink.awaitAgents(t, 3)
	require.Contains(t, rostered, "chief")
	sourceTraceID := rostered["chief"].Span().TraceID.String()
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
	// has to rebuild from the payloads that rode with it.
	chained := restoredSink.awaitAgents(t, 3)
	restoredSink.read(func(db *dagui.DB) {
		for name, node := range chained {
			_, err := db.CallIDForDigest(node.SnapshotDigest)
			require.NoError(t, err,
				"agent %q's anchor does not rebuild from the RESUMED session's own trace: "+
					"resuming that trace in turn would fail", name)
		}
	})
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

	model := cannedReplayModel(ctx, t, source, source.LLM().
		WithPrompt(prompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: answer},
		}))
	h := spawnAgent(ctx, t, source, spawnOpts{model: model, name: "solo"})
	_, reply, err := h.sendAndWait(ctx, t, prompt)
	require.NoError(t, err)
	require.Equal(t, answer, reply)

	rostered := sink.awaitAgents(t, 1)
	traceID := rostered["solo"].Span().TraceID.String()
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

func hasAttr(attrs []*commonpb.KeyValue, key string) bool {
	for _, attr := range attrs {
		if attr.GetKey() == key {
			return true
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
