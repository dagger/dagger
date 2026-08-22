package dagui

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

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
		Field: "directory",
		Type:  &callpbv1.Type{NamedType: "Directory"},
		Args: []*callpbv1.Argument{{
			Name:  "path",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "/skills"}},
		}},
	}
	setCallDigest(dir)
	withSkills := &callpbv1.Call{
		Field: "withSkills",
		Type:  &callpbv1.Type{NamedType: "LLM"},
		Args: []*callpbv1.Argument{{
			Name:  "directory",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: dir.Digest}},
		}},
	}
	setCallDigest(withSkills)
	agent := &callpbv1.Call{
		Field:          "agent",
		Type:           &callpbv1.Type{NamedType: "Agent"},
		ReceiverDigest: withSkills.Digest,
	}
	setCallDigest(agent)
	return agent, []*callpbv1.Call{withSkills, dir}
}

func setCallDigest(callPB *callpbv1.Call) {
	dgst, err := call.CanonicalDigest(callPB)
	if err != nil {
		panic(err)
	}
	callPB.Digest = dgst.String()
}

func rawCallPayload(t *testing.T, callPB *callpbv1.Call) []byte {
	t.Helper()
	payloadCall := proto.Clone(callPB).(*callpbv1.Call)
	payloadCall.Digest = ""
	payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(payloadCall)
	if err != nil {
		t.Fatalf("encode %s: %v", callPB.Digest, err)
	}
	return payload
}

func setTestLogScope(record *sdklog.Record, name string) {
	rf := reflect.ValueOf(record).Elem()
	scope := rf.FieldByName("scope")
	scope = reflect.NewAt(scope.Type(), unsafe.Pointer(scope.UnsafeAddr())).Elem()
	scope.Set(reflect.ValueOf(&instrumentation.Scope{Name: name}))
}

// newTestCallPayloadRecord builds the record the engine emits for one frame of
// a call's transitive closure.
func newTestCallPayloadRecord(t *testing.T, span SpanID, callPB *callpbv1.Call) sdklog.Record {
	t.Helper()
	record := newTestLogRecord(trace.TraceID{1}, span.SpanID, "",
		otellog.Bool(telemetryattrs.DagCallPayloadAttr, true),
	)
	record.SetBody(otellog.BytesValue(rawCallPayload(t, callPB)))
	setTestLogScope(&record, telemetryattrs.CallPayloadInstrumentationScope)
	return record
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

func TestCallPayloadRecordReservationAndValidation(t *testing.T) {
	_, frames := callPayloadTestChain()
	callPB := frames[1]
	payload := rawCallPayload(t, callPB)

	newRecord := func(scope string, body otellog.Value, attrs ...otellog.KeyValue) sdklog.Record {
		record := newTestLogRecord(trace.TraceID{1}, trace.SpanID{1}, "", attrs...)
		record.SetBody(body)
		setTestLogScope(&record, scope)
		return record
	}
	for _, test := range []struct {
		name     string
		record   sdklog.Record
		reserved bool
		valid    bool
	}{
		{
			name: "valid marker scope and bytes body",
			record: newRecord(telemetryattrs.CallPayloadInstrumentationScope, otellog.BytesValue(payload),
				otellog.Bool(telemetryattrs.DagCallPayloadAttr, true)),
			reserved: true,
			valid:    true,
		},
		{
			name:     "scope reserves absent marker",
			record:   newRecord(telemetryattrs.CallPayloadInstrumentationScope, otellog.BytesValue(payload)),
			reserved: true,
		},
		{
			name: "false marker reserves",
			record: newRecord(telemetryattrs.CallPayloadInstrumentationScope, otellog.BytesValue(payload),
				otellog.Bool(telemetryattrs.DagCallPayloadAttr, false)),
			reserved: true,
		},
		{
			name: "wrong marker kind reserves",
			record: newRecord(telemetryattrs.CallPayloadInstrumentationScope, otellog.BytesValue(payload),
				otellog.String(telemetryattrs.DagCallPayloadAttr, "true")),
			reserved: true,
		},
		{
			name: "marker reserves wrong scope",
			record: newRecord("wrong.scope", otellog.BytesValue(payload),
				otellog.Bool(telemetryattrs.DagCallPayloadAttr, true)),
			reserved: true,
		},
		{
			name: "marker reserves wrong body kind",
			record: newRecord(telemetryattrs.CallPayloadInstrumentationScope, otellog.StringValue(string(payload)),
				otellog.Bool(telemetryattrs.DagCallPayloadAttr, true)),
			reserved: true,
		},
		{
			name:     "unmarked unrelated record",
			record:   newRecord("wrong.scope", otellog.BytesValue(payload)),
			reserved: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := NewDB()
			if got := db.ingestCallPayload(test.record); got != test.reserved {
				t.Fatalf("reserved = %v, want %v", got, test.reserved)
			}
			got := db.Calls[callPB.Digest]
			if test.valid && got == nil {
				t.Fatal("valid payload was not decoded")
			}
			if !test.valid && len(db.Calls) != 0 {
				t.Fatalf("invalid payload was accepted: %+v", db.Calls)
			}
		})
	}
}

func TestCallPayloadDecodeIntegrity(t *testing.T) {
	_, frames := callPayloadTestChain()
	original := frames[1]

	t.Run("embedded digest rejected", func(t *testing.T) {
		payload, err := proto.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		record := newTestCallPayloadRecord(t, spanID(1), original)
		record.SetBody(otellog.BytesValue(payload))
		db := NewDB()
		if !db.ingestCallPayload(record) {
			t.Fatal("reserved record was not consumed")
		}
		if len(db.Calls) != 0 {
			t.Fatal("payload with embedded self-digest was accepted")
		}
	})

	t.Run("tamper changes address", func(t *testing.T) {
		tampered := proto.Clone(original).(*callpbv1.Call)
		tampered.Digest = ""
		tampered.Args[0].Value = &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "/tampered"}}
		setCallDigest(tampered)
		db := NewDB()
		exportCallPayloads(t, db, spanID(1), original, tampered)
		if original.Digest == tampered.Digest {
			t.Fatalf("tamper retained address %s", original.Digest)
		}
		if db.Calls[original.Digest] == nil || db.Calls[tampered.Digest] == nil {
			t.Fatalf("computed addresses missing: %+v", db.Calls)
		}
	})

	t.Run("unknown fields discarded", func(t *testing.T) {
		payload := rawCallPayload(t, original)
		payload = protowire.AppendTag(payload, 1000, protowire.BytesType)
		payload = protowire.AppendBytes(payload, []byte("discard me"))
		record := newTestCallPayloadRecord(t, spanID(1), original)
		record.SetBody(otellog.BytesValue(payload))
		db := NewDB()
		db.ingestCallPayload(record)
		decoded := db.Calls[original.Digest]
		if decoded == nil {
			t.Fatal("payload with unknown field was not accepted")
		}
		if unknown := decoded.ProtoReflect().GetUnknown(); len(unknown) != 0 {
			t.Fatalf("unknown fields retained: %x", unknown)
		}
	})
}

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

func TestLateExactCallPayloadInvalidatesProvisionalSpanCaches(t *testing.T) {
	db := NewDB()
	provisional := &callpbv1.Call{
		Digest:         "xxh3:provisional-withExec",
		Field:          "withExec",
		Type:           &callpbv1.Type{NamedType: "Container"},
		ReceiverDigest: "xxh3:provisional-container",
	}
	setCallDigest(provisional)
	db.Calls[provisional.Digest] = provisional
	exact := &callpbv1.Call{
		Field:          "withExec",
		Type:           &callpbv1.Type{NamedType: "Container"},
		ReceiverDigest: "xxh3:exact-container",
		Args: []*callpbv1.Argument{{
			Name: "args",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{
				Values: []*callpbv1.Literal{
					{Value: &callpbv1.Literal_String_{String_: "sh"}},
					{Value: &callpbv1.Literal_String_{String_: "-c"}},
					{Value: &callpbv1.Literal_String_{String_: "echo exact"}},
				},
			}}},
		}},
	}
	setCallDigest(exact)
	db.ImportSnapshots([]SpanSnapshot{
		{
			ID:         spanID(1),
			Name:       "Container.withExec",
			StartTime:  time.Unix(1, 0),
			EndTime:    time.Unix(2, 0),
			CallDigest: provisional.Digest,
			Output:     exact.Digest,
		},
		{
			ID:         spanID(2),
			Name:       "Container.withExec",
			StartTime:  time.Unix(3, 0),
			EndTime:    time.Unix(4, 0),
			CallDigest: exact.Digest,
		},
	})

	span := db.Spans.Map[spanID(2)]
	if got := span.Call(); got != provisional {
		// DB.Call decodes payloads, so compare the identity that matters rather
		// than the fixture pointer.
		if got == nil || got.Digest != provisional.Digest {
			t.Fatalf("initial call did not provisionally resolve through its creator: %+v", got)
		}
	}
	if got := span.Base(); got == nil || got.Digest != provisional.ReceiverDigest {
		t.Fatalf("initial base cache = %+v, want provisional receiver", got)
	}
	if line := renderCallLine(span.Call()); strings.Contains(line, "echo exact") {
		t.Fatalf("provisional call unexpectedly contained the exact command: %s", line)
	}

	exportCallPayloads(t, db, spanID(2), exact)

	if got := span.Call(); got == nil || got.Digest != exact.Digest {
		t.Fatalf("late exact payload did not replace provisional call cache: %+v", got)
	}
	if line := renderCallLine(span.Call()); !strings.Contains(line, "echo exact") {
		t.Fatalf("late exact payload did not restore command-bearing rendering: %s", line)
	}
	if got := span.Base(); got == nil || got.Digest != exact.ReceiverDigest {
		t.Fatalf("base cache was not rebuilt from the exact call: %+v", got)
	}

	cachedCall, cachedBase := span.callCache, span.baseCache
	mutations := db.MutationCount()
	exportCallPayloads(t, db, spanID(2), exact)
	if db.MutationCount() != mutations {
		t.Fatal("duplicate payload changed the DB mutation count")
	}
	if span.callCache != cachedCall || span.baseCache != cachedBase {
		t.Fatal("duplicate payload invalidated already-exact span caches")
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

func TestCallPayloadRecordsDoNotDisturbLogOrdering(t *testing.T) {
	db := NewDB()
	span := spanID(1)
	db.PrimarySpan = span
	_, frames := callPayloadTestChain()
	before := newTestLogRecord(trace.TraceID{1}, span.SpanID, "before")
	payload := newTestCallPayloadRecord(t, span, frames[1])
	malformed := newTestCallPayloadRecord(t, span, frames[1])
	malformed.SetBody(otellog.StringValue("not protobuf bytes"))
	after := newTestLogRecord(trace.TraceID{1}, span.SpanID, "after")

	if err := db.LogExporter().Export(context.Background(), []sdklog.Record{before, payload, malformed, after}); err != nil {
		t.Fatal(err)
	}
	logs := db.PrimaryLogs[span]
	if len(logs) != 2 {
		t.Fatalf("ordinary logs = %d, want 2", len(logs))
	}
	if logs[0].Body().AsString() != "before" || logs[1].Body().AsString() != "after" {
		t.Fatalf("ordinary log order changed: %q, %q", logs[0].Body().AsString(), logs[1].Body().AsString())
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

func TestDigestedCallPayloadIsOpaqueButRebuildable(t *testing.T) {
	const canary = "DIGESTED-CANARY-dagui-raw-only"
	payloadDigest := digest.FromString("opaque-payload")
	recipe := call.New().Append(
		&ast.Type{NamedType: "OpaquePayload", NonNull: true},
		"opaquePayload",
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

	want := call.DisplayDigestedString(canary, payloadDigest)
	for name, rendered := range map[string]string{
		"error detail": frameDetail(rawCall),
		"grep line":    renderCallLine(rawCall),
		"dot label":    displayLit(rawLiteral),
	} {
		if strings.Contains(rendered, canary) {
			t.Errorf("%s exposed digested value: %q", name, rendered)
		}
		if !strings.Contains(rendered, want) {
			t.Errorf("%s = %q, want opaque label %q", name, rendered, want)
		}
	}

	if matches := db.GrepCalls(regexp.MustCompile(regexp.QuoteMeta(canary)), 10); len(matches) != 0 {
		t.Fatalf("content search exposed canary: %q", matches)
	}
	matches := db.GrepCalls(regexp.MustCompile(regexp.QuoteMeta(payloadDigest.String())), 10)
	if len(matches) != 1 || strings.Contains(matches[0], canary) || !strings.Contains(matches[0], want) {
		t.Fatalf("digest search did not return opaque call: %q", matches)
	}
}

func TestBytesCallPayloadIsOpaqueButRebuildable(t *testing.T) {
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

	want := call.DisplayBytes(contents)
	for name, rendered := range map[string]string{
		"error detail": frameDetail(rawCall),
		"grep line":    renderCallLine(rawCall),
		"dot label":    displayLit(rawLiteral),
	} {
		if strings.Contains(rendered, canary) {
			t.Errorf("%s exposed bytes: %q", name, rendered)
		}
		if !strings.Contains(rendered, want) {
			t.Errorf("%s = %q, want opaque label %q", name, rendered, want)
		}
	}

	if matches := db.GrepCalls(regexp.MustCompile(regexp.QuoteMeta(canary)), 10); len(matches) != 0 {
		t.Fatalf("content search exposed canary: %q", matches)
	}
	matches := db.GrepCalls(regexp.MustCompile(regexp.QuoteMeta(digest.FromBytes(contents).String())), 10)
	if len(matches) != 1 || strings.Contains(matches[0], canary) || !strings.Contains(matches[0], want) {
		t.Fatalf("digest search did not return opaque call: %q", matches)
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
	} else if !strings.Contains(err.Error(), unspanned[0].Digest) {
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
	root, unspanned := callPayloadTestChain()
	db := NewDB()
	db.Calls[root.Digest] = root
	db.ImportSnapshots([]SpanSnapshot{{
		ID:         spanID(1),
		Name:       "LLM.agent",
		CallDigest: root.Digest,
	}})

	if _, err := db.Spans.Map[spanID(1)].CallID(); err == nil {
		t.Fatal("expected the unspanned receiver to be reported as missing")
	} else if !strings.Contains(err.Error(), unspanned[0].Digest) {
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
