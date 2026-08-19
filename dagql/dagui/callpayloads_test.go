package dagui

import (
	"context"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// The chain from the live failure this side channel exists for: focusing an
// agent needs llm.withSkills(directory: <dir>).agent(), where the withSkills
// frame is synthesized (LLM.recipeSelectors) and its directory argument is an
// ID literal whose own frame was never independently spanned. Only the agent
// frame gets a span; without the log channel the other two can never reach a
// client, and the chain is unrebuildable forever.
func callPayloadTestChain() (root *callpbv1.Call, unspanned []*callpbv1.Call) {
	dir := &callpbv1.Call{
		Digest: "xxh3:dir",
		Field:  "directory",
		Type:   &callpbv1.Type{NamedType: "Directory"},
		Args: []*callpbv1.Argument{{
			Name:  "path",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "/skills"}},
		}},
	}
	withSkills := &callpbv1.Call{
		Digest: "xxh3:withSkills",
		Field:  "withSkills",
		Type:   &callpbv1.Type{NamedType: "LLM"},
		Args: []*callpbv1.Argument{{
			Name:  "directory",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: dir.Digest}},
		}},
	}
	agent := &callpbv1.Call{
		Digest:         "xxh3:agent",
		Field:          "agent",
		Type:           &callpbv1.Type{NamedType: "Agent"},
		ReceiverDigest: withSkills.Digest,
	}
	return agent, []*callpbv1.Call{withSkills, dir}
}

// newTestCallPayloadRecord builds the record the engine emits for one frame of
// a call's transitive closure.
func newTestCallPayloadRecord(t *testing.T, span SpanID, call *callpbv1.Call) sdklog.Record {
	t.Helper()
	payload, err := call.Encode()
	if err != nil {
		t.Fatalf("encode %s: %v", call.Digest, err)
	}
	return newTestLogRecord(trace.TraceID{1}, span.SpanID, "",
		otellog.String(telemetryattrs.DagCallPayloadDigestAttr, call.Digest),
		otellog.String(telemetryattrs.DagCallPayloadAttr, payload),
	)
}

func exportCallPayloads(t *testing.T, db *DB, span SpanID, calls ...*callpbv1.Call) {
	t.Helper()
	records := make([]sdklog.Record, 0, len(calls))
	for _, call := range calls {
		records = append(records, newTestCallPayloadRecord(t, span, call))
	}
	if err := db.LogExporter().Export(context.Background(), records); err != nil {
		t.Fatalf("export payload records: %v", err)
	}
}

// A payload that arrives ONLY over the log channel must resolve exactly like
// one that rode a span attribute: DB.Call is the single lookup both feed.
func TestIngestCallPayloadResolvesCallWithNoSpan(t *testing.T) {
	db := NewDB()
	_, unspanned := callPayloadTestChain()
	dir := unspanned[1]

	before := db.MutationCount()
	exportCallPayloads(t, db, spanID(1), dir)

	call := db.Call(dir.Digest)
	if call == nil {
		t.Fatal("payload arrived over the log channel but DB.Call cannot resolve it")
	}
	if call.Field != "directory" || call.GetType().GetNamedType() != "Directory" {
		t.Fatalf("decoded the wrong call: %+v", call)
	}
	// Memoized views (the agent roster among them) are derived from this
	// data, so a payload that leaves the mutation counter alone would be
	// invisible until some unrelated span happened to bump it.
	if db.MutationCount() == before {
		t.Error("ingesting a payload did not bump the mutation counter")
	}
}

// The record is call data, not output: it must be consumed before the log-text
// path, or every payload turns into a phantom log line on its span.
func TestIngestCallPayloadIsNotLogText(t *testing.T) {
	db := NewDB()
	_, unspanned := callPayloadTestChain()
	exportCallPayloads(t, db, spanID(1), unspanned...)

	if span := db.Spans.Map[spanID(1)]; span != nil && span.HasLogs {
		t.Error("payload record was treated as log text")
	}
	if got := len(db.PrimaryLogs); got != 0 {
		t.Errorf("payload record was buffered as a primary log: %d", got)
	}
}

// The acceptance criterion: a chain rebuilds through extractIntoDAG with no
// missing frames when only ONE of its frames ever got a span — and it does so
// whichever way the two pipelines happen to interleave. Spans and logs are
// separately batched, so neither order can be assumed.
func TestCallIDRebuildsFromLogOnlyRootAndClosure(t *testing.T) {
	root, unspanned := callPayloadTestChain()
	allPayloads := append([]*callpbv1.Call{root}, unspanned...)
	// The span carries only the root digest; every payload is log-only.
	spanned := []SpanSnapshot{{
		ID:         spanID(1),
		Name:       "LLM.agent",
		CallDigest: root.Digest,
	}}

	for _, tc := range []struct {
		name         string
		payloadFirst bool
	}{
		{"payload before span", true},
		{"payload after span", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := NewDB()
			if tc.payloadFirst {
				exportCallPayloads(t, db, spanID(1), allPayloads...)
				db.ImportSnapshots(spanned)
			} else {
				db.ImportSnapshots(spanned)
				exportCallPayloads(t, db, spanID(1), allPayloads...)
			}

			span := db.Spans.Map[spanID(1)]
			if span == nil {
				t.Fatal("span not ingested")
			}
			if span.CallPayload != "" {
				t.Fatal("fixture span unexpectedly carried a call payload")
			}
			id, err := span.CallID()
			if err != nil {
				t.Fatalf("chain not rebuildable: %v", err)
			}
			if got := id.Digest().String(); got != root.Digest {
				t.Errorf("rebuilt ID digest = %q, want %q", got, root.Digest)
			}
			// The frame behind the ID-literal argument is the one the span
			// channel structurally cannot deliver, so it is the one worth
			// asserting made it into the rebuilt chain.
			if display := id.Display(); !strings.Contains(display, "directory") {
				t.Errorf("rebuilt ID lost the ID-literal argument's frame: %s", display)
			}
		})
	}
}

func TestLegacySpanCarriedCallPayloadStillIngests(t *testing.T) {
	_, unspanned := callPayloadTestChain()
	legacyCall := unspanned[1]
	payload, err := legacyCall.Encode()
	if err != nil {
		t.Fatal(err)
	}

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{{
		ID:          spanID(1),
		Name:        "Query.directory",
		CallDigest:  legacyCall.Digest,
		CallPayload: payload,
	}})
	if got := db.Call(legacyCall.Digest); got == nil || got.GetField() != legacyCall.GetField() {
		t.Fatalf("legacy span payload was not ingested: %+v", got)
	}
	id, err := db.Spans.Map[spanID(1)].CallID()
	if err != nil {
		t.Fatalf("legacy span payload did not rebuild: %v", err)
	}
	if id.Digest().String() != legacyCall.Digest {
		t.Fatalf("rebuilt digest = %s, want %s", id.Digest(), legacyCall.Digest)
	}
}

// The two raw-payload tests below check that a digested or binary literal
// survives the log channel byte-for-byte. Whether the frontend then keeps
// that value OUT of what it renders and searches is asserted alongside the
// opaque rendering and call search themselves, which live further up the
// stack.
func TestDigestedCallPayloadIsRebuildable(t *testing.T) {
	const canary = "CHECKPOINT-CANARY-dagui-raw-only"
	payloadDigest := digest.FromString("checkpoint-chunk")
	recipe := call.New().Append(
		&ast.Type{NamedType: "WorkspaceCheckpointChunk", NonNull: true},
		"workspaceCheckpointChunk",
		call.WithArgs(call.NewArgument(
			"data",
			call.NewLiteralDigestedString(canary, payloadDigest),
			false,
		)),
	)
	rawCall := recipe.Call()
	rawLiteral := rawCall.GetArgs()[0].GetValue()
	if got := rawLiteral.GetDigestedString().GetValue(); got != canary {
		t.Fatalf("raw call payload = %q, want canary", got)
	}

	db := NewDB()
	exportCallPayloads(t, db, spanID(1), rawCall)
	rebuilt, err := db.CallIDForDigest(recipe.Digest().String())
	if err != nil {
		t.Fatalf("rebuild call ID from payload: %v", err)
	}
	rebuiltLiteral, ok := rebuilt.Arg("data").Value().(*call.LiteralDigestedString)
	if !ok {
		t.Fatalf("rebuilt data literal has type %T", rebuilt.Arg("data").Value())
	}
	if got := rebuiltLiteral.Value(); got != canary {
		t.Fatalf("rebuilt data literal = %q, want canary", got)
	}
}

func TestBytesCallPayloadIsRebuildable(t *testing.T) {
	const canary = "BINARY-CANARY-dagui-raw-only"
	contents := append([]byte{0x00, 0xff, 0xfe}, []byte(canary)...)
	recipe := call.New().Append(
		&ast.Type{NamedType: "File", NonNull: true},
		"blob",
		call.WithArgs(call.NewArgument("contents", call.NewLiteralBytes(contents), false)),
	)
	rawCall := recipe.Call()
	rawLiteral := rawCall.GetArgs()[0].GetValue()
	if got := rawLiteral.GetBytes(); string(got) != string(contents) {
		t.Fatalf("raw call payload = %x, want %x", got, contents)
	}

	db := NewDB()
	exportCallPayloads(t, db, spanID(1), rawCall)
	rebuilt, err := db.CallIDForDigest(recipe.Digest().String())
	if err != nil {
		t.Fatalf("rebuild call ID from payload: %v", err)
	}
	rebuiltLiteral, ok := rebuilt.Arg("contents").Value().(*call.LiteralBytes)
	if !ok {
		t.Fatalf("rebuilt contents literal has type %T", rebuilt.Arg("contents").Value())
	}
	if got := rebuiltLiteral.Value(); string(got) != string(contents) {
		t.Fatalf("rebuilt contents literal = %x, want %x", got, contents)
	}
}

// A digest that arrived ONLY over the log channel, for a call that never got
// a span at all, must still rebuild. This is design §3.2's failure mode 2:
// span emission dedupes per session by call digest (ShouldEmitTelemetry), so
// an identical chain — two agents with identical seeds and identical replies
// — suppresses the second span while its payload still rides the log channel.
// A rebuild that needs a span carrying the digest cannot serve that case at
// all, which is why the walk lives on the DB rather than on Span.
func TestCallIDForDigestResolvesAChainWithNoSpans(t *testing.T) {
	db := NewDB()
	root, unspanned := callPayloadTestChain()
	exportCallPayloads(t, db, spanID(1), append([]*callpbv1.Call{root}, unspanned...)...)

	if got := len(db.Spans.Map); got != 0 {
		t.Fatalf("fixture: no span should exist at all, got %d", got)
	}

	id, err := db.CallIDForDigest(root.Digest)
	if err != nil {
		t.Fatalf("chain not rebuildable from payloads alone: %v", err)
	}
	if got := id.Digest().String(); got != root.Digest {
		t.Errorf("rebuilt ID digest = %q, want %q", got, root.Digest)
	}
	if display := id.Display(); !strings.Contains(display, "directory") {
		t.Errorf("rebuilt ID lost the ID-literal argument's frame: %s", display)
	}
}

// The gap report is the same one Span.CallID gives, since both are now the
// same walk: a digest whose payload never reached this client fails loudly
// rather than producing a truncated chain.
func TestCallIDForDigestReportsAMissingPayload(t *testing.T) {
	db := NewDB()
	root, unspanned := callPayloadTestChain()
	// The root and the ID-literal's frame arrive; the receiver in between
	// never does.
	exportCallPayloads(t, db, spanID(1), root, unspanned[1])

	if _, err := db.CallIDForDigest(root.Digest); err == nil {
		t.Fatal("expected the unspanned receiver to be reported as missing")
	} else if !strings.Contains(err.Error(), "xxh3:withSkills") {
		t.Fatalf("gap report does not name the missing frame: %v", err)
	}

	// And a digest nothing at all was published for names itself, rather than
	// rebuilding an empty chain.
	if _, err := db.CallIDForDigest("xxh3:never-published"); err == nil {
		t.Fatal("expected an unknown digest to be reported")
	} else if !strings.Contains(err.Error(), "xxh3:never-published") {
		t.Fatalf("error does not name the digest: %v", err)
	}
}

// Control: the same chain WITHOUT the log channel must fail, and name the gap.
// Without this, the test above could pass for reasons unrelated to the payloads
// it exports.
func TestCallIDWithoutLogPayloadsStillReportsTheGap(t *testing.T) {
	root, _ := callPayloadTestChain()
	rootPayload, err := root.Encode()
	if err != nil {
		t.Fatal(err)
	}
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{{
		ID:          spanID(1),
		Name:        "LLM.agent",
		CallDigest:  root.Digest,
		CallPayload: rootPayload,
	}})

	if _, err := db.Spans.Map[spanID(1)].CallID(); err == nil {
		t.Fatal("expected the unspanned receiver to be reported as missing")
	} else if !strings.Contains(err.Error(), "xxh3:withSkills") {
		t.Fatalf("gap report does not name the missing frame: %v", err)
	}
}

// A span that is its OWN creator — Output == CallDigest, which an ID-returning
// call routinely produces — must not send the creator walk round in circles
// when the payload it is looking for never arrived.
//
// The walk is the last resort in DB.Call, reached only when no payload answers
// the digest, and that is precisely the case a resume has to REPORT rather
// than survive: design §9's first row, "call <digest> never reached this
// client". Before the visited set it recurred on the digest it started from
// and blew the stack — found by the end-to-end restore test
// (core/integration/agent_restore_test.go), which serves a capture with its
// call payloads withheld.
func TestCallDoesNotLoopThroughASelfCreatingSpan(t *testing.T) {
	const dig = "xxh3:selfcreated"
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{{
		ID:         spanID(1),
		Name:       "LLM.withPrompt",
		CallDigest: dig,
		// The span records itself as the creator of its own output, and no
		// payload for it ever arrived.
		Output: dig,
	}})

	if call := db.Call(dig); call != nil {
		t.Fatalf("expected no call for a digest nothing published a payload for, got %v", call)
	}
	if _, err := db.CallIDForDigest(dig); err == nil {
		t.Fatal("expected the missing payload to be reported")
	} else if !strings.Contains(err.Error(), dig) {
		t.Fatalf("error does not name the digest: %v", err)
	}
}
