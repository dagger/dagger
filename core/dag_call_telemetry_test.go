package core

// The producer half of the call-payload side channel. What must not regress
// silently: the closure has to reach the frames buried inside an ID-literal
// ARGUMENT — those are the ones LiteralID.pb flattens to a bare digest, so
// they can never ride a span attribute — and a digest must cross the wire at
// most once per session, or an LLM loop re-sending the same chain drowns the
// log stream.

import (
	"context"
	"sync"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// payloadRecorder captures the call payloads emitted through a context,
// decoding each back into the frame a client would file under its digest.
type payloadRecorder struct {
	mu    sync.Mutex
	calls map[string]*callpbv1.Call
}

func (r *payloadRecorder) OnEmit(ctx context.Context, rec *sdklog.Record) error {
	var digest, payload string
	rec.WalkAttributes(func(kv otellog.KeyValue) bool {
		switch kv.Key {
		case telemetryattrs.DagCallPayloadDigestAttr:
			digest = kv.Value.AsString()
		case telemetryattrs.DagCallPayloadAttr:
			payload = kv.Value.AsString()
		}
		return true
	})
	if digest == "" {
		return nil
	}
	var call callpbv1.Call
	if err := call.Decode(payload); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string]*callpbv1.Call{}
	}
	r.calls[digest] = &call
	return nil
}

func (r *payloadRecorder) Shutdown(context.Context) error                         { return nil }
func (r *payloadRecorder) ForceFlush(context.Context) error                       { return nil }
func (r *payloadRecorder) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }

func (r *payloadRecorder) get(digest string) *callpbv1.Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[digest]
}

func (r *payloadRecorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

type testSeenKeys struct {
	keys sync.Map
}

func (s *testSeenKeys) LoadOrStoreTelemetrySeenKey(key string) bool {
	_, seen := s.keys.LoadOrStore(key, struct{}{})
	return seen
}

func (s *testSeenKeys) StoreTelemetrySeenKey(key string) {
	s.keys.Store(key, struct{}{})
}

// payloadRecorderCtx returns a context whose logger provider records every
// call payload emitted through it, plus the nil dagql cache the recipe walk
// needs (every ref in these fixtures is inline).
func payloadRecorderCtx(t *testing.T) (*payloadRecorder, context.Context) {
	t.Helper()
	rec := &payloadRecorder{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(rec))
	ctx := telemetry.WithLoggerProvider(context.Background(), provider)
	return rec, dagql.ContextWithCache(ctx, nil)
}

// llm.withSkills(directory: dir).agent(): the shape of the live failure. Only
// the agent frame is ever spanned; the directory frame reaches the client
// through the ID-literal argument or not at all.
func skillsChain() (agent, withSkills, dir *dagql.ResultCall) {
	llm := testResultCall("llm", &Void{}, nil)
	dir = testResultCall("directory", &Void{}, testResultCall("host", &Void{}, nil))
	withSkills = testResultCall("withSkills", &Void{}, llm)
	withSkills.Args = []*dagql.ResultCallArg{{
		Name: "directory",
		Value: &dagql.ResultCallLiteral{
			Kind:      dagql.ResultCallLiteralKindResultRef,
			ResultRef: &dagql.ResultCallRef{Call: dir},
		},
	}}
	agent = testResultCall("agent", &Void{}, withSkills)
	return agent, withSkills, dir
}

func TestRecordCallPayloadsEmitsTransitiveClosure(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	agent, withSkills, dir := skillsChain()

	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)

	recordCallPayloads(ctx, &testSeenKeys{}, rootDigest.String(), agent)

	// The frame behind the ID-literal argument is the whole point: a span
	// payload flattens it to a bare digest, so this channel is the only way
	// it can ever reach a client.
	dirDigest, err := dir.RecipeDigest(ctx)
	require.NoError(t, err)
	dirCall := rec.get(dirDigest.String())
	require.NotNil(t, dirCall, "the ID-literal argument's frame was not published")
	require.Equal(t, "directory", dirCall.Field)
	require.Equal(t, dirDigest.String(), dirCall.Digest)

	// ... and so is everything on the receiver spine below it, since any of
	// those may equally have gone unspanned.
	withSkillsDigest, err := withSkills.RecipeDigest(ctx)
	require.NoError(t, err)
	require.NotNil(t, rec.get(withSkillsDigest.String()))

	// The call's own frame rides its span as dagger.io/dag.call, so
	// publishing it here would be a duplicate on the wire.
	require.Nil(t, rec.get(rootDigest.String()), "the span's own payload was duplicated")
}

func TestRecordCallPayloadsDedupesPerSession(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	agent, _, _ := skillsChain()
	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)

	seen := &testSeenKeys{}
	recordCallPayloads(ctx, seen, rootDigest.String(), agent)
	first := rec.len()
	require.Greater(t, first, 0)

	// A second selection of the same call publishes nothing: the digest was
	// claimed, and reachability is transitive, so the first walk covered
	// everything this one would.
	recordCallPayloads(ctx, seen, rootDigest.String(), agent)
	require.Equal(t, first, rec.len())

	// A LONGER chain over the same frames publishes only what is NEW: the
	// receiver spine below it was already spent, and its own frame rides its
	// span.
	prompt := testResultCall("file", &Void{}, nil)
	longer := testResultCall("withPrompt", &Void{}, agent)
	longer.Args = []*dagql.ResultCallArg{{
		Name: "file",
		Value: &dagql.ResultCallLiteral{
			Kind:      dagql.ResultCallLiteralKindResultRef,
			ResultRef: &dagql.ResultCallRef{Call: prompt},
		},
	}}
	longerDigest, err := longer.RecipeDigest(ctx)
	require.NoError(t, err)
	recordCallPayloads(ctx, seen, longerDigest.String(), longer)
	require.Equal(t, first+1, rec.len(), "only the newly reachable frame should be published")
	promptDigest, err := prompt.RecipeDigest(ctx)
	require.NoError(t, err)
	require.NotNil(t, rec.get(promptDigest.String()), "the new frame was not published")
}

// Without a seen-key store there is no session to dedupe against, so the
// channel stays quiet rather than republishing a chain per call.
func TestRecordCallPayloadsRequiresSeenKeyStore(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	agent, _, _ := skillsChain()
	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)

	recordCallPayloads(ctx, nil, rootDigest.String(), agent)
	require.Equal(t, 0, rec.len())
}
