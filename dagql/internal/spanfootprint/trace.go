// Package spanfootprint generates a large synthetic trace shaped like a long
// `dagger agent` session, for measuring how much memory frontends hold per
// span (see dagui's BenchmarkSpanFootprint and idtui's
// BenchmarkFrontendSpanFootprint).
package spanfootprint

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// Trace generates a synthetic trace shaped like a long `dagger agent`
// session.
//
// Spans are generated on demand, one export batch at a time, and every string
// is freshly allocated per span, the way OTLP decoding hands them over:
// whatever a frontend retains is charged to it, and whatever it doesn't is
// garbage by the time the heap is measured.
//
// Most spans are dagql calls (digest, base64 call payload, output, cache
// evidence, origin client), many of them internal; the rest are execs, LLM
// messages, tool calls, tests and checks. Spans sit in deep, narrow chains
// under a few wide agent-loop parents; some carry causal links, some fail
// with error-origin links.
type Trace struct {
	N       int
	traceID trace.TraceID
	epoch   time.Time
	parents []int32
}

// LongLived spans (the root and the agent loops) run for the whole session.
const LongLived = 9

// New returns an n-span trace.
func New(n int) *Trace {
	tr := &Trace{
		N:       n,
		traceID: trace.TraceID{1},
		epoch:   time.Unix(1_700_000_000, 0),
		parents: make([]int32, n),
	}
	// Spans come in call trees, like the work of one tool call: a tree's
	// first span hangs off an agent loop, the rest off a recent span of the
	// same tree. Bounding a tree's size bounds its depth, as in a real trace
	// (chains that ran through the whole trace would make walks over a
	// span's ancestors cost O(n) apiece).
	var tree, treeEnd int
	for i := 1; i < n; i++ {
		h := hash(i, 0)
		var p int
		switch r := h % 100; {
		case i < LongLived:
			p = 0 // agent loops under the root
		case r < 2:
			p = 0
		case i >= treeEnd || r < 10:
			p = 1 + int(h>>8)%(LongLived-1) // a wide agent loop
			if i >= treeEnd {
				tree, treeEnd = i, i+maxTreeSize/16+int(h>>32)%maxTreeSize
			}
		default:
			p = max(tree, i-1-int(h>>8)%48) // deep, narrow call chains
		}
		tr.parents[i] = int32(p)
	}
	return tr
}

// maxTreeSize bounds the size of a call tree (see New).
const maxTreeSize = 2048

// hash is a splitmix64 hash of a span index and a salt, so every span's
// random choices are reproducible without keeping generator state.
func hash(i, salt int) uint64 {
	z := uint64(i)*0x9e3779b97f4a7c15 + uint64(salt)*0xbf58476d1ce4e5b9
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// SpanID returns span i's ID.
func (tr *Trace) SpanID(i int) trace.SpanID {
	var id trace.SpanID
	binary.BigEndian.PutUint64(id[:], uint64(i)+1)
	return id
}

func (tr *Trace) spanContext(i int) trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tr.traceID,
		SpanID:  tr.SpanID(i),
	})
}

func digest(kind string, i int) string {
	return fmt.Sprintf("xxh3:%016x", hash(i, len(kind)))
}

var calls = []struct{ typ, field string }{
	{"Container", "withExec"},
	{"Container", "withDirectory"},
	{"Container", "withMountedCache"},
	{"Directory", "file"},
	{"Directory", "withNewFile"},
	{"Query", "container"},
	{"Query", "host"},
	{"File", "contents"},
	{"Workspace", "directory"},
	{"Env", "withStringInput"},
}

// str clones key and value so the span owns them, like a decoded OTLP span.
func str(key, val string) attribute.KeyValue {
	return attribute.String(string([]byte(key)), string([]byte(val)))
}

// flag sets a boolean attribute to true, the only value the trace uses.
func flag(key string) attribute.KeyValue {
	return attribute.Bool(string([]byte(key)), true)
}

// Stub returns span i as exported when it starts (done=false) or ends.
func (tr *Trace) Stub(i int, done bool) sdktrace.ReadOnlySpan {
	h := hash(i, 1)
	start := tr.epoch.Add(time.Duration(i) * time.Millisecond)
	stub := tracetest.SpanStub{
		SpanContext: tr.spanContext(i),
		StartTime:   start,
	}
	if i > 0 {
		stub.Parent = tr.spanContext(int(tr.parents[i]))
	}
	if done {
		stub.EndTime = start.Add(time.Duration(1+h%5000) * time.Millisecond)
	}
	attrs := []attribute.KeyValue{
		str(telemetryattrs.TelemetryOriginClientIDAttr, "pbhbtxrpvpl1sxuvaqm5skyrl"),
	}
	switch kind := h % 100; {
	case i == 0:
		stub.Name = "dagger agent"
	case i < LongLived:
		stub.Name = fmt.Sprintf("agent loop %d", i)
		attrs = append(attrs,
			flag(telemetryattrs.AgentAttr),
			str(telemetryattrs.AgentIDAttr, fmt.Sprintf("agent-%d", i)),
			str(telemetryattrs.AgentNameAttr, fmt.Sprintf("worker-%d", i)),
			str(telemetryattrs.AgentCallDigestAttr, digest("agent", i)),
		)
	case kind < 6:
		// LLM message
		stub.Name = "LLM response"
		attrs = append(attrs,
			str(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
			str(telemetry.UIActorEmojiAttr, "🤖"),
			str(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
			str(telemetryattrs.LLMCallDigestAttr, digest("llm", i)),
		)
	case kind < 10:
		// tool call
		stub.Name = "read"
		attrs = append(attrs,
			str(telemetry.LLMToolAttr, "read"),
			flag(telemetry.UIBoundaryAttr),
			attribute.StringSlice(telemetry.LLMToolArgNamesAttr, []string{"filePath"}),
			attribute.StringSlice(telemetry.LLMToolArgValuesAttr, []string{fmt.Sprintf("dagql/file%d.go", i)}),
		)
	case kind < 12:
		// test case
		stub.Name = fmt.Sprintf("TestThing/case_%d", i)
		attrs = append(attrs,
			str(string(semconv.TestCaseNameKey), stub.Name),
			str(string(semconv.TestSuiteNameKey), "TestThing"),
		)
		if done {
			attrs = append(attrs, str(string(semconv.TestCaseResultStatusKey), "pass"))
		}
	case kind < 13:
		stub.Name = fmt.Sprintf("check-%d", i%40)
		attrs = append(attrs, str(telemetry.CheckNameAttr, stub.Name))
		if done {
			attrs = append(attrs, flag(telemetry.CheckPassedAttr))
		}
	case kind < 25:
		// exec
		stub.Name = fmt.Sprintf("exec go test ./pkg%d", i%300)
		attrs = append(attrs,
			str(telemetryattrs.WcprofOpKindAttr, "exec"),
			str(telemetry.DagDigestAttr, digest("exec", i)),
			str(telemetryattrs.ExecutionContentPreferredDigestAttr, digest("content", i)),
		)
	default:
		// dagql call
		call := calls[h>>8%uint64(len(calls))]
		dig := digest("call", i)
		stub.Name = call.typ + "." + call.field
		payload, err := (&callpbv1.Call{
			ReceiverDigest: digest("call", int(tr.parents[i])),
			Type:           &callpbv1.Type{NamedType: call.typ},
			Field:          call.field,
			Args: []*callpbv1.Argument{{
				Name: "path",
				Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{
					String_: fmt.Sprintf("/src/path/%d", i),
				}},
			}},
			Digest: dig,
		}).Encode()
		if err != nil {
			panic(err)
		}
		attrs = append(attrs,
			str(telemetry.DagDigestAttr, dig),
			str(telemetry.DagCallAttr, payload),
			str(telemetryattrs.DagContentPreferredDigestAttr, digest("content", i)),
		)
		if kind < 70 {
			attrs = append(attrs, flag(telemetry.UIInternalAttr))
		}
		if done {
			attrs = append(attrs,
				str(telemetry.DagOutputAttr, digest("output", i)),
				str(telemetryattrs.CacheContractAttr, "v1"),
				str(telemetryattrs.CacheOutcomeAttr, "hit"),
				str(telemetryattrs.CacheResultIDAttr, strconv.Itoa(i)),
				str(telemetryattrs.CacheTypeAttr, call.typ),
			)
			if h>>16%4 == 0 {
				attrs = append(attrs, flag(telemetry.CachedAttr))
			}
		}
	}
	if h>>20%100 < 5 && i > LongLived {
		// a causal link to an earlier span
		stub.Links = append(stub.Links, sdktrace.Link{
			SpanContext: tr.spanContext(int(h>>28) % i),
		})
	}
	if done && h>>24%100 == 0 && i > LongLived {
		// a failure, with an error origin
		stub.Status = sdktrace.Status{Code: codes.Error, Description: "exit code 1"}
		stub.Links = append(stub.Links, sdktrace.Link{
			SpanContext: tr.spanContext(i - 1),
			Attributes: []attribute.KeyValue{
				attribute.String(telemetry.LinkPurposeAttr, telemetry.LinkPurposeErrorOrigin),
			},
		})
	}
	stub.Attributes = attrs
	return stub.Snapshot()
}

// Export feeds the whole trace to exp the way a live session does: each
// batch exports the spans that just started (still running) and completes
// the previous batch's spans. The root and agent loops end last. Between
// batches it calls between, if given (e.g. to render a frame).
func (tr *Trace) Export(ctx context.Context, exp sdktrace.SpanExporter, batch int, between func()) error {
	for lo := 0; lo < tr.N+batch; lo += batch {
		spans := make([]sdktrace.ReadOnlySpan, 0, 2*batch)
		for i := lo; i < min(lo+batch, tr.N); i++ {
			spans = append(spans, tr.Stub(i, false))
		}
		for i := max(lo-batch, LongLived); i < min(lo, tr.N); i++ {
			spans = append(spans, tr.Stub(i, true))
		}
		if len(spans) == 0 {
			continue
		}
		if err := exp.ExportSpans(ctx, spans); err != nil {
			return err
		}
		if between != nil {
			between()
		}
	}
	final := make([]sdktrace.ReadOnlySpan, 0, LongLived)
	for i := LongLived - 1; i >= 0; i-- {
		final = append(final, tr.Stub(i, true))
	}
	return exp.ExportSpans(ctx, final)
}

// SizesFlag is the usage of the test flag footprint benchmarks take their
// sizes from (see Sizes).
const SizesFlag = "comma-separated trace sizes for the span footprint benchmarks (default 100000, or $DAGGER_SPAN_FOOTPRINT_SIZES)"

// Sizes returns the trace sizes a footprint benchmark should measure: the
// comma-separated sizes in spec (e.g. "100000,1000000"), else in
// $DAGGER_SPAN_FOOTPRINT_SIZES, else 100k spans.
func Sizes(spec string) ([]int, error) {
	if spec == "" {
		spec = os.Getenv("DAGGER_SPAN_FOOTPRINT_SIZES")
	}
	if spec == "" {
		return []int{100_000}, nil
	}
	var sizes []int
	for _, s := range strings.Split(spec, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("span footprint sizes: %w", err)
		}
		sizes = append(sizes, n)
	}
	return sizes, nil
}
