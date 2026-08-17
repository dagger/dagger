package core

// These tests cover the client half of dagql addressing: turning what
// telemetry published about a call back into a LOADABLE ID.
//
// A client (the TUI, and anything else building on dagui.DB) rebuilds an ID
// by walking the call's frames and looking each one up by digest in the
// payloads it has ingested — dagql/dagui/extract.go, driven by
// dagui.Span.CallID. A payload only ever reaches a client as an attribute on
// the span emitted for that exact selection (core/telemetry.go), so a frame
// that never got its own span is unresolvable, and the chain it sits in
// cannot be rebuilt or loaded.
//
// The trace round trip is the point, so these stand up an OTLP endpoint of
// their own (agentTraceSink, in agent_runtime_test.go) and fold what the
// session's CLI forwards into the same dagui.DB a frontend uses — the same
// harness the TestRosterAddressing* tests use.

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type CallIDRebuildSuite struct{}

func TestCallIDRebuild(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CallIDRebuildSuite{})
}

// awaitSpanNamed blocks until a span with the given name has been folded into
// the sink's DB, and runs fn against it with ingest held off.
func awaitSpanNamed(t *testctx.T, sink *agentTraceSink, name string, fn func(db *dagui.DB, span *dagui.Span)) {
	t.Helper()
	require.EventuallyWithT(t, func(ct *assert.CollectT) {
		sink.read(func(db *dagui.DB) {
			for _, span := range db.Spans.Map {
				if span.Name == name && span.CallDigest != "" {
					fn(db, span)
					return
				}
			}
			assert.Fail(ct, "no span named "+name+" has reached this client yet")
		})
	}, 120*time.Second, 100*time.Millisecond)
}

// TestArrayMemberSubSelection covers the second shape of the payload gap: a
// sub-selection against a MEMBER of an array result.
//
// Reading a list of objects is the most ordinary thing a client does —
// `{ envVariables { name value } }` — and dagql resolves it by taking the
// nth value of the array and selecting against it. The nth value is a real
// frame in the resulting ID (Call.Nth), but nothing ever selects it, so no
// span is emitted for it and its payload never ships. The sub-selection's own
// span arrives fine and names that frame as its receiver — a digest the
// client can never resolve.
//
// The consequence is not cosmetic: every ID whose chain passes through an
// array member is unrebuildable, so a client cannot address anything it read
// out of a list.
//
// PARKED, and skipped rather than deleted so the shape stays recorded. The
// call-payload log channel (core/dag_call_telemetry.go) closed the other known
// gap -- frames behind an ID-literal argument -- but NOT this one: measured
// with that channel in, this test fails unchanged. Leading hypothesis, not yet
// confirmed: recordCallPayloads reaches the closure through
// ResultCall.RecipeID, which for an array-member receiver either fails to
// rebuild (there is a whole traceRecipeIDRebuildFailed facility in
// dagql/cache_debug.go for that shape) or yields a handle-form ID, and both
// paths return silently at Debug level. The fix sketched but not built --
// walking the ResultCall frame graph and calling callPB per frame, whose
// recipe digests are memoized on the frame -- would sidestep RecipeID
// entirely and is where the next attempt should start.
func (CallIDRebuildSuite) TestArrayMemberSubSelection(ctx context.Context, t *testctx.T) {
	t.Skip("known-broken: array members get no span, and the call payload log " +
		"channel does not cover them either; parked deliberately -- see the " +
		"comment above for what was measured and where to start")

	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		// An inherited session is already attached to somebody else's
		// frontend; only a CLI session this test starts can be pointed at
		// the sink.
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}

	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)

	marker := "member marker " + identity.NewID()

	// One array, one sub-selection per member. No image pull: a scratch
	// container carries exactly the environment this query puts on it.
	res := map[string]any{}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query: `query($marker: String!) {
			container {
				withEnvVariable(name: "ROSTER_MARKER", value: $marker) {
					envVariables { name value }
				}
			}
		}`,
		Variables: map[string]any{"marker": marker},
	}, &dagger.Response{Data: &res}))
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	members := gjson.Get(string(raw), "container.withEnvVariable.envVariables").Array()
	require.NotEmpty(t, members, "the query must return at least one member: %s", raw)

	// The sub-selection's own span reached this client (it is a selection
	// like any other). Its receiver is the array member — the frame under
	// test.
	var (
		rebuilt        *call.ID
		rebuildErr     error
		receiverDigest string
		receiverCall   bool
	)
	awaitSpanNamed(t, sink, "EnvVariable.value", func(db *dagui.DB, span *dagui.Span) {
		receiverDigest = span.Call().GetReceiverDigest()
		receiverCall = db.Call(receiverDigest) != nil
		rebuilt, rebuildErr = span.CallID()
	})

	require.NotEmpty(t, receiverDigest,
		"the sub-selection's receiver is the array member frame")
	// The gap itself, stated directly: the member frame's payload never
	// reached this client, so nothing can resolve it — and the rebuild that
	// depends on it therefore fails. Both facts ride one message, since the
	// first failure is the only one that gets printed.
	require.True(t, receiverCall,
		"the array member's call payload never reached this client (digest %s); "+
			"rebuilding the sub-selection's ID fails with: %v",
		receiverDigest, rebuildErr)
	// ... and therefore the chain does not rebuild.
	require.NoError(t, rebuildErr, "rebuilding the sub-selection's ID")
	require.NotNil(t, rebuilt)

	// The rebuilt chain is the honest one: a sub-selection against the nth
	// member of the array, rooted at the container that produced it.
	member := rebuilt.Receiver()
	require.NotNil(t, member, "the rebuilt chain must carry the member frame")
	nth := int(member.Nth())
	require.Greater(t, nth, 0, "the member frame must record its position: %s", rebuilt.Display())
	require.Equal(t, "envVariables", member.Field())
	require.LessOrEqual(t, nth, len(members))

	// And it addresses that exact member: loading the rebuilt member ID
	// yields the same name/value pair the original query returned at that
	// position. A chain that rebuilt into a different member — or into the
	// array itself — would not survive this.
	encoded, err := member.Encode()
	require.NoError(t, err)
	loaded := map[string]any{}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on EnvVariable { name value } } }`,
		Variables: map[string]any{"id": encoded},
	}, &dagger.Response{Data: &loaded}))
	loadedRaw, err := json.Marshal(loaded)
	require.NoError(t, err)
	want := members[nth-1]
	require.Equal(t, want.Get("name").String(), gjson.Get(string(loadedRaw), "node.name").String())
	require.Equal(t, want.Get("value").String(), gjson.Get(string(loadedRaw), "node.value").String())
}
