package core

// These tests cover the client half of dagql addressing: turning what
// telemetry published about a call back into a LOADABLE ID.
//
// A client (the TUI, and anything else building on dagui.DB) rebuilds an ID
// by walking the call's frames and looking each one up by digest in the
// payloads it has ingested. Engines emit the root and transitive closure
// through raw call-payload log records.
//
// The trace round trip is the point, so these stand up an OTLP endpoint of
// their own (agentTraceSink, in tracesink_test.go) and fold what the
// session's CLI forwards into the same dagui.DB a frontend uses.

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
// the sink's DB and fn's assertions pass, running fn with ingest held off.
func awaitSpanNamed(t *testctx.T, sink *agentTraceSink, name string, fn func(ct *assert.CollectT, db *dagui.DB, span *dagui.Span)) {
	t.Helper()
	require.EventuallyWithT(t, func(ct *assert.CollectT) {
		sink.read(func(db *dagui.DB) {
			for _, span := range db.Spans.Map {
				if span.Name == name && span.CallDigest != "" {
					fn(ct, db, span)
					return
				}
			}
			assert.Fail(ct, "no span named "+name+" has reached this client yet")
		})
	}, 120*time.Second, 100*time.Millisecond)
}

// TestArrayMemberSubSelection covers a sub-selection against a member of an
// array result.
//
// Reading a list of objects is the most ordinary thing a client does —
// `{ envVariables { name value } }` — and dagql resolves it by taking the
// nth value of the array and selecting against it. The nth value is a real
// frame in the resulting ID (Call.Nth), but nothing ever selects it, so it has
// no span of its own. Its payload must instead arrive in the transitive closure
// published through call-payload logs.
//
// Spans and logs are batched independently, so seeing the sub-selection span
// does not imply its receiver payload has arrived. The assertion deliberately
// waits for both before rebuilding the ID.
func (CallIDRebuildSuite) TestArrayMemberSubSelection(ctx context.Context, t *testctx.T) {
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
		receiverDigest string
	)
	awaitSpanNamed(t, sink, "EnvVariable.value", func(ct *assert.CollectT, db *dagui.DB, span *dagui.Span) {
		spanCall := span.Call()
		if !assert.NotNil(ct, spanCall, "the sub-selection's payload has not reached this client yet") {
			return
		}
		digest := spanCall.GetReceiverDigest()
		if !assert.NotEmpty(ct, digest, "the sub-selection's receiver is the array member frame") {
			return
		}
		if !assert.NotNil(ct, db.Call(digest), "the array member's payload has not reached this client yet") {
			return
		}
		id, err := span.CallID()
		if !assert.NoError(ct, err, "rebuilding the sub-selection's ID") ||
			!assert.NotNil(ct, id, "the rebuilt sub-selection ID") {
			return
		}
		receiverDigest = digest
		rebuilt = id
	})

	require.NotEmpty(t, receiverDigest)
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
