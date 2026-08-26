package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// TestAssembleLines covers log-line assembly from raw stdio segments: log
// records aren't guaranteed to be line-aligned, so a line that straddles two
// records takes the provenance of the record that started it.
func TestAssembleLines(t *testing.T) {
	for _, tc := range []struct {
		name     string
		segments []capturedLine
		want     []capturedLine
	}{
		{
			name: "whole lines keep their provenance",
			segments: []capturedLine{
				{text: "nested one\nnested two\n", direct: false},
				{text: "printed\n", direct: true},
			},
			want: []capturedLine{
				{text: "nested one", direct: false},
				{text: "nested two", direct: false},
				{text: "printed", direct: true},
			},
		},
		{
			name: "line split across records is assembled once",
			segments: []capturedLine{
				{text: "hello, ", direct: true},
				{text: "world\n", direct: true},
			},
			want: []capturedLine{{text: "hello, world", direct: true}},
		},
		{
			name: "straddling line takes the starting record's provenance",
			segments: []capturedLine{
				{text: "start", direct: true},
				{text: "-end\nnested\n", direct: false},
			},
			want: []capturedLine{
				{text: "start-end", direct: true},
				{text: "nested", direct: false},
			},
		},
		{
			name:     "trailing newlines don't contribute lines",
			segments: []capturedLine{{text: "only\n\n\n", direct: true}},
			want:     []capturedLine{{text: "only", direct: true}},
		},
		{
			name:     "unterminated final line is kept",
			segments: []capturedLine{{text: "no trailing newline", direct: false}},
			want:     []capturedLine{{text: "no trailing newline", direct: false}},
		},
		{
			name:     "empty input",
			segments: nil,
			want:     nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := assembleLines(tc.segments)
			if len(got) != len(tc.want) {
				t.Fatalf("assembleLines() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

type logCaptureTestServer struct {
	*mockServer
	dbs *clientdb.DBs
}

func (srv *logCaptureTestServer) ClientTelemetry(ctx context.Context, _, _ string) (*clientdb.DB, error) {
	return srv.dbs.Open(ctx, "capture-test")
}

// TestCallPayloadRecordsExcludedFromLLMLogs covers both LLM-facing consumers
// of captureLogLines: the explicit ReadLogs builtin and automatic tool-result
// log capture. A marker key reserves its record regardless of value or type;
// the dedicated scope independently reserves malformed records with no marker.
func TestCallPayloadRecordsExcludedFromLLMLogs(t *testing.T) {
	const (
		traceID = "000102030405060708090a0b0c0d0e0f"
		spanID  = "0000000000000001"
	)

	dbs := clientdb.NewDBs(t.TempDir())
	store, err := dbs.Open(t.Context(), "capture-test")
	require.NoError(t, err)
	_, err = store.AppendSpans([]clientdb.Span{{
		TraceID:    traceID,
		SpanID:     spanID,
		Attributes: marshalSpanAttrs(t),
	}})
	require.NoError(t, err)

	ordinaryScope := "ordinary.logs"
	callContentType := &otlpcommonv1.KeyValue{
		Key: telemetry.ContentTypeAttr,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{
			StringValue: telemetryattrs.CallPayloadContentType,
		}},
	}
	logs := []clientdb.Log{
		persistedCaptureLog(t, traceID, spanID, ordinaryScope, stringLogBody("before\n")),
		// A well-formed payload record: reserved by content type.
		persistedCaptureLog(t, traceID, spanID, ordinaryScope, bytesLogBody([]byte("CALL-PAYLOAD-BYTES")), callContentType),
		// Malformed: the payload content type over a text body is still
		// reserved, never rendered.
		persistedCaptureLog(t, traceID, spanID, ordinaryScope, stringLogBody("CONTENT-TYPE-WRONG-BODY\n"), callContentType),
		// No content type at all: not a call payload, but binary data all the
		// same. Only string bodies may become LLM-visible text.
		persistedCaptureLog(t, traceID, spanID, ordinaryScope, bytesLogBody([]byte("UNTYPED-BYTES"))),
		persistedCaptureLog(t, traceID, spanID, ordinaryScope, stringLogBody("after\n")),
	}
	_, err = store.AppendLogs(logs)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	ctx := ContextWithQuery(t.Context(), &Query{Server: &logCaptureTestServer{
		mockServer: &mockServer{},
		dbs:        dbs,
	}})
	traceIDValue, err := trace.TraceIDFromHex(traceID)
	require.NoError(t, err)
	spanIDValue, err := trace.SpanIDFromHex(spanID)
	require.NoError(t, err)
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceIDValue,
		SpanID:  spanIDValue,
	}))
	m := newMCP()

	t.Run("ReadLogs", func(t *testing.T) {
		got, err := m.readLogsTool(&dagql.Server{})(ctx, map[string]any{"span": spanID})
		require.NoError(t, err)
		require.Equal(t, "     1→before\n     2→after", got)
	})

	t.Run("automatic tool result", func(t *testing.T) {
		require.Equal(t, "before\nafter", m.toolLogs(ctx))
	})
}

func persistedCaptureLog(t *testing.T, traceID, spanID, scope string, body *otlpcommonv1.AnyValue, attrs ...*otlpcommonv1.KeyValue) clientdb.Log {
	t.Helper()
	bodyBytes, err := proto.Marshal(body)
	require.NoError(t, err)
	attrBytes, err := clientdb.MarshalProtoJSONs(attrs)
	require.NoError(t, err)
	scopeBytes, err := protojson.Marshal(&otlpcommonv1.InstrumentationScope{Name: scope})
	require.NoError(t, err)
	return clientdb.Log{
		TraceID:              sql.NullString{String: traceID, Valid: true},
		SpanID:               sql.NullString{String: spanID, Valid: true},
		Body:                 bodyBytes,
		Attributes:           attrBytes,
		InstrumentationScope: scopeBytes,
	}
}

func stringLogBody(value string) *otlpcommonv1.AnyValue {
	return &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: value}}
}

func bytesLogBody(value []byte) *otlpcommonv1.AnyValue {
	return &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: value}}
}

// TestLimitIndirectLines locks in the tool-result abridging rule: whatever the
// tool printed itself survives in full — a sub-agent's report is the point of
// the call — while logs from nested work beneath it are cut down to a tail,
// with the dropped runs counted so the model knows to reach for ReadLogs.
func TestLimitIndirectLines(t *testing.T) {
	direct := func(texts ...string) []capturedLine {
		lines := make([]capturedLine, len(texts))
		for i, text := range texts {
			lines[i] = capturedLine{text: text, direct: true}
		}
		return lines
	}
	nested := func(texts ...string) []capturedLine {
		lines := make([]capturedLine, len(texts))
		for i, text := range texts {
			lines[i] = capturedLine{text: text}
		}
		return lines
	}

	t.Run("a long report survives noisy nested work", func(t *testing.T) {
		// The shape that motivated this: a sub-agent runs a build (lots of
		// nested output), then the tool prints its report. Tail-only
		// truncation evicted the report; now only the build output is cut.
		var lines []capturedLine
		for i := 1; i <= 50; i++ {
			lines = append(lines, capturedLine{text: fmt.Sprintf("NESTED-%02d", i)})
		}
		for i := 1; i <= 12; i++ {
			lines = append(lines, capturedLine{text: fmt.Sprintf("REPORT-%02d", i), direct: true})
		}

		got := limitIndirectLines("abc123", lines, 3, 1000)
		want := []string{
			"... 47 lines omitted (use ReadLogs(span: abc123) to read more) ...",
			"NESTED-48", "NESTED-49", "NESTED-50",
		}
		for i := 1; i <= 12; i++ {
			want = append(want, fmt.Sprintf("REPORT-%02d", i))
		}
		requireLines(t, got, want)
	})

	t.Run("direct lines are never dropped even past the limit", func(t *testing.T) {
		got := limitIndirectLines("s", direct("a", "b", "c", "d", "e"), 2, 1000)
		requireLines(t, got, []string{"a", "b", "c", "d", "e"})
	})

	t.Run("interleaved runs each report their own count", func(t *testing.T) {
		var lines []capturedLine
		lines = append(lines, nested("n1", "n2", "n3")...)
		lines = append(lines, direct("report A")...)
		lines = append(lines, nested("n4", "n5")...)
		lines = append(lines, direct("report B")...)

		// limit 1 keeps only the last indirect line (n5).
		got := limitIndirectLines("s", lines, 1, 1000)
		requireLines(t, got, []string{
			"... 3 lines omitted (use ReadLogs(span: s) to read more) ...",
			"report A",
			"... 1 lines omitted (use ReadLogs(span: s) to read more) ...",
			"n5",
			"report B",
		})
	})

	t.Run("nothing is abridged when nested output fits", func(t *testing.T) {
		lines := append(nested("n1", "n2"), direct("done")...)
		got := limitIndirectLines("s", lines, 8, 1000)
		requireLines(t, got, []string{"n1", "n2", "done"})
	})

	t.Run("zero limit keeps everything", func(t *testing.T) {
		lines := append(nested("n1", "n2", "n3"), direct("done")...)
		got := limitIndirectLines("s", lines, 0, 1000)
		requireLines(t, got, []string{"n1", "n2", "n3", "done"})
	})

	t.Run("long lines are still character-capped", func(t *testing.T) {
		long := strings.Repeat("x", 20)
		got := limitIndirectLines("s", direct(long), 8, 10)
		requireLines(t, got, []string{strings.Repeat("x", 10) + "[... 10 chars truncated]"})
	})

	t.Run("a runaway direct print hits the byte cap", func(t *testing.T) {
		// Direct lines are never dropped by the line limit, so the byte cap is
		// the only bound on a tool that prints far more than any report: the
		// head and tail survive around a counted marker, and the total stays
		// within budget.
		var lines []capturedLine
		for i := 1; i <= 40; i++ {
			lines = append(lines, capturedLine{text: fmt.Sprintf("LINE-%02d-%s", i, strings.Repeat("x", 995)), direct: true})
		}
		got := limitIndirectLines("s", lines, 8, 2000)
		if len(got) >= 40 {
			t.Fatalf("got %d lines, want the byte cap to drop some", len(got))
		}
		joined := strings.Join(got, "\n")
		if len(joined) > llmToolLogsMaxBytes+100 {
			t.Errorf("joined output is %d bytes, want within ~%d", len(joined), llmToolLogsMaxBytes)
		}
		if !strings.HasPrefix(got[0], "LINE-01") {
			t.Errorf("first line = %.20q, want the head to survive", got[0])
		}
		if !strings.HasPrefix(got[len(got)-1], "LINE-40") {
			t.Errorf("last line = %.20q, want the tail to survive", got[len(got)-1])
		}
		if !strings.Contains(joined, "lines omitted (use ReadLogs(span: s)") {
			t.Errorf("output lacks the counted ReadLogs marker:\n%s", joined)
		}
	})
}

// TestCapLinesBytes covers the last-resort byte cap on captured tool logs:
// under budget the lines pass through untouched; over budget the middle is
// dropped behind a counted marker, with the head keeping the larger share
// and at least one line surviving on each side.
func TestCapLinesBytes(t *testing.T) {
	t.Run("under budget passes through", func(t *testing.T) {
		lines := []string{"one", "two", "three"}
		requireLines(t, capLinesBytes("s", lines, 1000), lines)
	})

	t.Run("zero budget disables the cap", func(t *testing.T) {
		lines := []string{strings.Repeat("x", 100)}
		requireLines(t, capLinesBytes("s", lines, 0), lines)
	})

	t.Run("over budget drops the middle behind a marker", func(t *testing.T) {
		// 10 lines of 10 bytes (11 with newline); budget 66 → head budget 44
		// keeps 4 lines, tail budget 22 keeps 2, marker counts the 4 dropped.
		var lines []string
		for i := 1; i <= 10; i++ {
			lines = append(lines, fmt.Sprintf("line-%02dxxx", i))
		}
		got := capLinesBytes("s", lines, 66)
		requireLines(t, got, []string{
			"line-01xxx", "line-02xxx", "line-03xxx", "line-04xxx",
			"... 4 lines omitted (use ReadLogs(span: s) to read more) ...",
			"line-09xxx", "line-10xxx",
		})
	})

	t.Run("an oversized boundary line still survives", func(t *testing.T) {
		// The first and last lines are always kept even when either alone
		// exceeds its share of the budget.
		lines := []string{strings.Repeat("a", 50), "mid", strings.Repeat("z", 50)}
		got := capLinesBytes("s", lines, 40)
		requireLines(t, got, []string{
			strings.Repeat("a", 50),
			"... 1 lines omitted (use ReadLogs(span: s) to read more) ...",
			strings.Repeat("z", 50),
		})
	})
}

func requireLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines:\n%s\nwant %d lines:\n%s",
			len(got), strings.Join(got, "\n"), len(want), strings.Join(want, "\n"))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestGuardText covers the shared bounding helper behind guardTraceReport and
// guardToolResult: a per-line byte clamp on a rune boundary, then a total
// budget enforced by dropping whole lines out of the MIDDLE behind the
// guard's own marker.
func TestGuardText(t *testing.T) {
	guard := textGuard{
		maxBytes:   400,
		maxLineLen: 10,
		headBytes:  240,
		marker: func(lines, bytes int) string {
			return fmt.Sprintf("<%d lines, %d bytes dropped>", lines, bytes)
		},
	}

	// 60 lines of exactly 10 bytes: 659 bytes joined, well over the 400-byte
	// budget.
	var manyLines []string
	for i := 1; i <= 60; i++ {
		manyLines = append(manyLines, fmt.Sprintf("line-%02dxxx", i))
	}
	overBudget := strings.Join(manyLines, "\n")

	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "within both limits passes through byte-identical",
			input: "one\ntwo\nthree\n",
			want:  "one\ntwo\nthree\n",
		},
		{
			name:  "a long line is clamped inline",
			input: "head\n" + strings.Repeat("x", 30) + "\ntail",
			want:  "head\n" + strings.Repeat("x", 10) + "[... 20 bytes truncated]\ntail",
		},
		{
			name:  "the clamp cuts on a rune boundary",
			input: strings.Repeat("┃", 5), // 3 bytes each, so 10 lands mid-rune
			want:  "┃┃┃[... 6 bytes truncated]",
		},
		{
			name: "over budget drops the middle behind the guard's marker",
			// head budget 240 keeps 21 lines (231 bytes), the 128-byte marker
			// reserve leaves 41 for the tail, which keeps 3 (33 bytes), so 36
			// lines (395 bytes) go.
			input: overBudget,
			want: strings.Join(append(append([]string{}, manyLines[:21]...),
				"<36 lines, 395 bytes dropped>",
				manyLines[57], manyLines[58], manyLines[59]), "\n"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := guardText(tc.input, guard)
			if got != tc.want {
				t.Fatalf("guardText() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestGuardToolResult covers the last-resort bound on a tool's return value.
// The motivating case: editor_read on a 6-line JSON file whose second line is
// a ~400 KB base64 blob (a saved session's llm_id). The call's own `limit: 15`
// counted LINES, so the module's limit never fired and the blob landed
// verbatim in the model's context.
// TestGuardToolResult: a result within both limits is byte-identical.
func TestGuardToolResultInBudgetIsIdentical(t *testing.T) {
	for _, res := range []string{
		"",
		"(done)",
		"{\"ok\":true}",
		strings.Repeat("a line of ordinary tool output\n", 500),
		strings.Repeat("x", llmLogsMaxLineLen), // exactly at the line clamp
	} {
		if got := guardToolResult(res); got != res {
			t.Fatalf("guardToolResult modified an in-budget result of %d bytes:\ngot  %.80q\nwant %.80q",
				len(res), got, res)
		}
	}
}

// TestGuardToolResult: a single monster line is clamped, the rest survives.
func TestGuardToolResultClampsMonsterLine(t *testing.T) {
	blob := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=", 12000) // ~420 KB
	res := "{\n" +
		"  \"llm_id\": \"" + blob + "\",\n" +
		"  \"model\": \"claude\",\n" +
		"  \"messages\": 3,\n" +
		"  \"tokens\": 1234\n" +
		"}"

	got := guardToolResult(res)
	if len(got) > llmToolResultMaxBytes {
		t.Fatalf("guarded result is %d bytes, over the %d-byte budget", len(got), llmToolResultMaxBytes)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 6 {
		t.Fatalf("expected the 6 lines to survive as lines, got %d", len(lines))
	}
	for i, want := range map[int]string{
		0: "{",
		2: "  \"model\": \"claude\",",
		3: "  \"messages\": 3,",
		4: "  \"tokens\": 1234",
		5: "}",
	} {
		if lines[i] != want {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
	if !strings.HasPrefix(lines[1], "  \"llm_id\": \""+blob[:100]) {
		t.Errorf("clamped line lost its head: %.60q", lines[1])
	}
	if !strings.HasSuffix(lines[1], " bytes truncated]") {
		t.Errorf("clamped line lacks its inline marker: %.60q", lines[1][max(0, len(lines[1])-60):])
	}
	if n := len(lines[1]); n > llmLogsMaxLineLen+64 {
		t.Errorf("clamped line is %d bytes, want ~%d", n, llmLogsMaxLineLen)
	}
	// The whole point: the blob does not reach the model.
	if len(got) > 4*1024 {
		t.Errorf("guarded result is %d bytes (from %d), want the blob gone", len(got), len(res))
	}
}

// TestGuardToolResult: a total-budget blowout drops the middle.
func TestGuardToolResultDropsMiddleOverBudget(t *testing.T) {
	var lines []string
	lines = append(lines, "FIRST LINE OF THE RESULT")
	for i := range 20000 {
		lines = append(lines, fmt.Sprintf("%6d: some ordinary line of file content", i))
	}
	lines = append(lines, "LAST LINE OF THE RESULT")
	res := strings.Join(lines, "\n")

	got := guardToolResult(res)
	if len(got) > llmToolResultMaxBytes {
		t.Fatalf("guarded result is %d bytes, over the %d-byte budget", len(got), llmToolResultMaxBytes)
	}
	if !strings.HasPrefix(got, "FIRST LINE OF THE RESULT\n") {
		t.Errorf("head did not survive: %.60q", got)
	}
	if !strings.HasSuffix(got, "\nLAST LINE OF THE RESULT") {
		t.Errorf("tail did not survive: %.60q", got[max(0, len(got)-60):])
	}

	// Exactly one marker, and every other line is a whole input line.
	input := map[string]bool{}
	for _, line := range lines {
		input[line] = true
	}
	var markers int
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "omitted from the middle of this result") {
			markers++
			if !strings.Contains(line, "re-run the call more narrowly") {
				t.Errorf("marker doesn't say what to do about it: %q", line)
			}
			continue
		}
		if !input[line] {
			t.Errorf("kept line is not a whole input line: %q", line)
		}
	}
	if markers != 1 {
		t.Fatalf("expected exactly one marker, got %d", markers)
	}

	// Both ends are generous: the head keeps its two-thirds share, and the
	// tail is not a token gesture.
	head, tail, ok := strings.Cut(got, "... ")
	if !ok {
		t.Fatal("expected to find the marker")
	}
	if len(head) < llmToolResultMaxBytes/2 {
		t.Errorf("head is only %d bytes of a %d-byte budget", len(head), llmToolResultMaxBytes)
	}
	if len(tail) < 1024 {
		t.Errorf("tail is only %d bytes", len(tail))
	}
}

// TestGuardToolResult: multi-byte runes are never split.
func TestGuardToolResultNeverSplitsRunes(t *testing.T) {
	// A tree-drawing report: 3-byte runes straddling both the per-line
	// clamp and the total budget.
	res := strings.Repeat("┃", 5000) + "\n" +
		strings.Repeat(strings.Repeat("◼ ┃ a nested span row\n", 1), 5000)

	got := guardToolResult(res)
	if !utf8.ValidString(got) {
		t.Fatal("guard split a rune: result is not valid UTF-8")
	}
	if len(got) > llmToolResultMaxBytes {
		t.Fatalf("guarded result is %d bytes, over the %d-byte budget", len(got), llmToolResultMaxBytes)
	}
}

// TestGuardToolResult: degenerate sizes.
func TestGuardToolResultDegenerateSizes(t *testing.T) {
	// One line, longer than the whole budget: the per-line clamp alone
	// brings it back inside.
	single := strings.Repeat("y", llmToolResultMaxBytes+1)
	got := guardToolResult(single)
	if len(got) > llmToolResultMaxBytes {
		t.Errorf("single-line result is %d bytes, over the %d-byte budget", len(got), llmToolResultMaxBytes)
	}
	if !strings.HasPrefix(got, strings.Repeat("y", llmLogsMaxLineLen)) {
		t.Errorf("single-line result lost its head: %.40q", got)
	}

	// A result exactly at the budget passes through; one line more does
	// not, and still comes back within budget.
	line := strings.Repeat("z", 63) // 64 bytes with its newline
	atBudget := strings.TrimSuffix(strings.Repeat(line+"\n", llmToolResultMaxBytes/64), "\n")
	if len(atBudget) != llmToolResultMaxBytes-1 {
		t.Fatalf("test setup: %d bytes, want %d", len(atBudget), llmToolResultMaxBytes-1)
	}
	if got := guardToolResult(atBudget); got != atBudget {
		t.Errorf("a result at the budget was modified (%d -> %d bytes)", len(atBudget), len(got))
	}
	overBudget := atBudget + "\n" + line
	got = guardToolResult(overBudget)
	if got == overBudget {
		t.Error("a result over the budget passed through untouched")
	}
	if len(got) > llmToolResultMaxBytes {
		t.Errorf("guarded result is %d bytes, over the %d-byte budget", len(got), llmToolResultMaxBytes)
	}
}

// TestInternalSpanFilterSubtreeBounds covers beneathInternal's containment
// rule: a cause-linked span's parent chain leaves the captured subtree
// without passing through the capture root (a service exec span is parented
// under whatever call triggered the start), so internal spans on that
// unrelated chain must not hide the capture's logs. Internal-ness within the
// subtree still hides, service filtering still applies to the cause-linked
// exec span itself, and refresh picks up spans that joined the subtree after
// the filter's snapshot.
func TestInternalSpanFilterSubtreeBounds(t *testing.T) {
	ctx := context.Background()
	store, err := clientdb.NewDBs(t.TempDir()).Open(ctx, "filter-test")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Link targets round-trip through OTLP span IDs, so use valid hex IDs.
	const (
		traceID     = "000102030405060708090a0b0c0d0e0f"
		rootSpan    = "0000000000000001" // trace root
		hiddenSpan  = "0000000000000002" // internal span OUTSIDE the capture
		triggerSpan = "0000000000000003" // triggered the service start, beneath hiddenSpan
		installSpan = "0000000000000004" // Container.asService: the capture root
		execSpan    = "0000000000000005" // service exec span, cause-links to installSpan
		execChild   = "0000000000000006" // e.g. the exec's process span
		innerHidden = "0000000000000007" // internal span WITHIN the capture
		innerLeaf   = "0000000000000008" // beneath innerHidden; appended late
	)

	internalAttrs := marshalSpanAttrs(t, boolAttr(telemetry.UIInternalAttr))
	serviceAttrs := marshalSpanAttrs(t, boolAttr(telemetryattrs.ServiceAttr))
	noAttrs := marshalSpanAttrs(t)

	_, err = store.AppendSpans([]clientdb.Span{
		{TraceID: traceID, SpanID: rootSpan, Attributes: noAttrs},
		{TraceID: traceID, SpanID: hiddenSpan, ParentSpanID: validSpanID(rootSpan), Attributes: internalAttrs},
		{TraceID: traceID, SpanID: triggerSpan, ParentSpanID: validSpanID(hiddenSpan), Attributes: noAttrs},
		{TraceID: traceID, SpanID: installSpan, ParentSpanID: validSpanID(rootSpan), Attributes: noAttrs},
		{TraceID: traceID, SpanID: execSpan, ParentSpanID: validSpanID(triggerSpan),
			Attributes: serviceAttrs, Links: marshalCauseLink(t, traceID, installSpan)},
		{TraceID: traceID, SpanID: execChild, ParentSpanID: validSpanID(execSpan), Attributes: noAttrs},
		{TraceID: traceID, SpanID: innerHidden, ParentSpanID: validSpanID(installSpan), Attributes: internalAttrs},
	})
	if err != nil {
		t.Fatal(err)
	}

	f := newInternalSpanFilter(store, installSpan, false)

	// The exec child's chain (execSpan → triggerSpan → hiddenSpan) leaves the
	// subtree at triggerSpan; the internal span above is not between these
	// logs and the capture root and must not hide them.
	if beneathInternalOrFatal(t, f, traceID, execChild) {
		t.Error("execChild hidden by an internal span outside the captured subtree")
	}

	// innerLeaf hasn't been appended: it's outside the snapshot, unhidden.
	if beneathInternalOrFatal(t, f, traceID, innerLeaf) {
		t.Error("innerLeaf hidden before joining the subtree")
	}
	_, err = store.AppendSpans([]clientdb.Span{
		{TraceID: traceID, SpanID: innerLeaf, ParentSpanID: validSpanID(innerHidden), Attributes: noAttrs},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.refresh()
	// Now within the subtree, beneath an internal span: hidden.
	if !beneathInternalOrFatal(t, f, traceID, innerLeaf) {
		t.Error("innerLeaf not hidden by an internal ancestor within the subtree")
	}

	// Service filtering still applies to the cause-linked exec span itself:
	// it IS within the capture — that's how its logs got here.
	sf := newInternalSpanFilter(store, installSpan, true)
	if !beneathInternalOrFatal(t, sf, traceID, execChild) {
		t.Error("execChild not filtered as service logs with skipServices set")
	}
}

func beneathInternalOrFatal(t *testing.T, f *internalSpanFilter, traceID, spanID string) bool {
	t.Helper()
	hidden, err := f.beneathInternal(context.Background(), traceID, spanID)
	if err != nil {
		t.Fatal(err)
	}
	return hidden
}

func boolAttr(key string) *otlpcommonv1.KeyValue {
	return &otlpcommonv1.KeyValue{
		Key:   key,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BoolValue{BoolValue: true}},
	}
}

func marshalSpanAttrs(t *testing.T, attrs ...*otlpcommonv1.KeyValue) []byte {
	t.Helper()
	data, err := clientdb.MarshalProtoJSONs(attrs)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func marshalCauseLink(t *testing.T, traceID, spanID string) []byte {
	t.Helper()
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex(spanID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := clientdb.MarshalProtoJSONs(telemetry.SpanLinksToPB([]sdktrace.Link{{
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid}),
		Attributes:  []attribute.KeyValue{attribute.String(telemetry.LinkPurposeAttr, telemetry.LinkPurposeCause)},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validSpanID(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

// TestNormalizeSpanArg covers span-argument normalization: agents paste span
// IDs in whatever form they last saw them — bare hex, the "span=<hex>" report
// rendering, or a traceparent marker from an error — and all of them should
// resolve to the bare span ID.
func TestNormalizeSpanArg(t *testing.T) {
	const (
		traceID = "000102030405060708090a0b0c0d0e0f"
		spanID  = "00000000000000cc"
	)
	for _, tc := range []struct {
		name string
		arg  string
		want string
	}{
		{name: "bare span ID", arg: spanID, want: spanID},
		{name: "surrounding whitespace", arg: "  " + spanID + "\n", want: spanID},
		{name: "span= report rendering", arg: "span=" + spanID, want: spanID},
		{name: "error-origin marker", arg: "[traceparent:" + traceID + "-" + spanID + "]", want: spanID},
		{name: "traceparent prefix", arg: "traceparent:" + traceID + "-" + spanID, want: spanID},
		{name: "w3c traceparent", arg: "00-" + traceID + "-" + spanID + "-01", want: spanID},
		{name: "unrecognized input passes through", arg: "not-a-span", want: "not-a-span"},
		{name: "16 chars but not hex passes through", arg: "zzzzzzzzzzzzzzzz", want: "zzzzzzzzzzzzzzzz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSpanArg(tc.arg); got != tc.want {
				t.Errorf("normalizeSpanArg(%q) = %q, want %q", tc.arg, got, tc.want)
			}
		})
	}
}

// TestRenderReadLogs covers ReadLogs result shaping: offset/grep/limit
// handling, and — for agent recovery — that the failure and empty cases
// report how many lines actually exist.
func TestRenderReadLogs(t *testing.T) {
	logLines := func() []string { return []string{"alpha", "beta", "gamma"} }

	t.Run("numbers the lines", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), 0, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		want := "     1→alpha\n     2→beta\n     3→gamma"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("offset trims from the end", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), 1, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		want := "     1→alpha\n     2→beta"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("negative offset reads the tail", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), -5, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "gamma") {
			t.Errorf("got %q, want the full tail", got)
		}
	})

	t.Run("offset past the start reports the total", func(t *testing.T) {
		_, err := renderReadLogs("s", logLines(), 3, 100, "")
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), "3 available lines") {
			t.Errorf("error %q should report the available line count", err)
		}
	})

	t.Run("grep filters and keeps original numbering", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), 0, 100, "ta$")
		if err != nil {
			t.Fatal(err)
		}
		want := "     2→beta"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("grep with no matches reports the searched count", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), 0, 100, "nope")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, `"nope"`) || !strings.Contains(got, "3 lines") {
			t.Errorf("got %q, want a no-matches message with the searched count", got)
		}
	})

	t.Run("invalid grep pattern errors", func(t *testing.T) {
		_, err := renderReadLogs("s", logLines(), 0, 100, "(")
		if err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("limit keeps the tail with a counted marker", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), 0, 2, "")
		if err != nil {
			t.Fatal(err)
		}
		want := "... 1 lines omitted (use ReadLogs(span: s) to read more) ...\n     2→beta\n     3→gamma"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
