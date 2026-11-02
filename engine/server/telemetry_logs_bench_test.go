package server

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// Live log catch-ups end to end: the one-shot POST /v1/logs/query with
// Through that the CLI sends when a span starts showing logs (see
// liveLogs.catchUp), over a real HTTP server, decoded the way the client's
// consumeLogQuery decodes it. The store has the shape of clientdb's
// BenchmarkLiveLogCatchUp (engine/clientdb/logquery_bench_test.go), which
// times the store's read alone; the difference is the framing.
//
//	go test ./engine/server -run '^$' -bench LiveLogCatchUpHTTP -benchmem

var catchUpRowsFlag = flag.String("catchup-rows", "100000,1000000", "comma-separated log row counts of the BenchmarkLiveLogCatchUpHTTP stores")

const catchUpSpansPerTurn = 50

func catchUpSpanID(n int) string { return fmt.Sprintf("%016x", n) }

func catchUpKV(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

// appendCatchUpStore fills a client's store like clientdb's newCatchUpStore,
// returning its own (tool-call) and turn spans near the end, and a service
// digest whose rows span the session.
func appendCatchUpStore(b *testing.B, db *clientdb.DB, rows int) (own, turn, digest string) {
	b.Helper()
	nSpans := min(max(rows/50, 10_000), 50_000)
	turns := nSpans / catchUpSpansPerTurn
	const root = 1
	heavy := []int{2, 3, 4, 5}
	turnBase := func(t int) int { return 6 + t*catchUpSpansPerTurn }
	traceID, err := hex.DecodeString(archiveTestTrace)
	require.NoError(b, err)

	span := func(id, parent int) clientdb.Span {
		s := logQuerySpanRow(catchUpSpanID(id), "")
		if parent != 0 {
			s.ParentSpanID = sql.NullString{String: catchUpSpanID(parent), Valid: true}
		}
		return s
	}
	spans := []clientdb.Span{span(root, 0)}
	for _, h := range heavy {
		spans = append(spans, span(h, root))
	}
	for t := range turns {
		base := turnBase(t)
		spans = append(spans, span(base, root))
		for c := 1; c < catchUpSpansPerTurn; c++ {
			s := span(base+c, base)
			if t > 0 && c%10 == 0 {
				target, err := hex.DecodeString(catchUpSpanID(turnBase(t-1) + c + 1))
				require.NoError(b, err)
				s.Links, err = clientdb.MarshalProtoList([]*tracepb.Span_Link{{
					TraceId:    traceID,
					SpanId:     target,
					Attributes: []*commonpb.KeyValue{catchUpKV(telemetry.LinkPurposeAttr, telemetry.LinkPurposeCause)},
				}})
				require.NoError(b, err)
			}
			spans = append(spans, s)
		}
	}
	for chunk := range slices.Chunk(spans, 4096) {
		_, err := db.AppendSpans(chunk)
		require.NoError(b, err)
	}

	marshal := func(m proto.Message) []byte {
		data, err := proto.Marshal(m)
		require.NoError(b, err)
		return data
	}
	attrs := func(kvs ...*commonpb.KeyValue) []byte {
		data, err := clientdb.MarshalProtoList(kvs)
		require.NoError(b, err)
		return data
	}
	resource := marshal(&resourcepb.Resource{Attributes: []*commonpb.KeyValue{
		catchUpKV("service.name", "dagger-engine"),
		catchUpKV("service.version", "v0.20.0"),
		catchUpKV("telemetry.sdk.language", "go"),
		catchUpKV("telemetry.sdk.name", "opentelemetry"),
		catchUpKV("telemetry.sdk.version", "1.38.0"),
	}})
	scope := marshal(&commonpb.InstrumentationScope{Name: "dagger.io/engine"})
	var texts [][]byte
	for i := range 16 {
		texts = append(texts, marshal(&commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("#%d %s\n", i, strings.Repeat("output ", 3+i*2))}}))
	}
	stdout := attrs(catchUpKV("stdio.stream", "1"))
	verbose := attrs(&commonpb.KeyValue{Key: telemetry.LogsVerboseAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}})
	spanName := attrs(catchUpKV(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName))
	callArgs := []*callpbv1.Argument{{Name: "args", Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: strings.Repeat("go test ./... ", 20)}}}}
	var services [][]byte
	for i := range 64 {
		services = append(services, attrs(catchUpKV(telemetry.DagDigestAttr, fmt.Sprintf("xxh3:svc%d", i))))
	}

	rng := rand.New(rand.NewPCG(1, 2))
	batch := make([]clientdb.Log, 0, 4096)
	for i := range rows {
		t := i * turns / rows
		var id int
		switch r := rng.IntN(100); {
		case r < 20:
			id = heavy[rng.IntN(len(heavy))]
		case r < 23:
			id = turnBase(t)
		default:
			id = turnBase(t) + 1 + rng.IntN(catchUpSpansPerTurn-1)
		}
		row := clientdb.Log{
			TraceID:              sql.NullString{String: archiveTestTrace, Valid: true},
			SpanID:               sql.NullString{String: catchUpSpanID(id), Valid: true},
			Timestamp:            int64(i + 1),
			Body:                 texts[rng.IntN(len(texts))],
			Attributes:           stdout,
			InstrumentationScope: scope,
			Resource:             resource,
		}
		switch k := rng.IntN(100); {
		case k < 68:
		case k < 78:
			call := fmt.Sprintf("xxh3:call%d", i)
			row.Body = marshal(&commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: marshal(&callpbv1.Call{
				ReceiverDigest: fmt.Sprintf("xxh3:call%d", i-1),
				Field:          "withExec",
				Digest:         call,
				Type:           &callpbv1.Type{NamedType: "Container"},
				Args:           callArgs,
			})}})
			row.Attributes = attrs(
				catchUpKV(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType),
				catchUpKV(telemetry.DagDigestAttr, call),
			)
		case k < 83:
			row.Attributes = spanName
		case k < 93:
			row.Attributes = services[rng.IntN(len(services))]
		default:
			row.Attributes = verbose
		}
		batch = append(batch, row)
		if len(batch) == cap(batch) || i == rows-1 {
			_, err := db.AppendLogs(batch)
			require.NoError(b, err)
			batch = batch[:0]
		}
	}
	// Spill every row, as clientdb's benchmark does.
	_, err = db.Checkpoint(b.Context())
	require.NoError(b, err)
	// The own span: a call of a turn near the end that has display rows.
	tt := turns - 3
	for c := 7; c < catchUpSpansPerTurn; c++ {
		id := catchUpSpanID(turnBase(tt) + c)
		if catchUpDisplayRows(b, db, []archive.LogSpan{{SpanID: id}}) > 0 {
			return id, catchUpSpanID(turnBase(tt)), "xxh3:svc7"
		}
	}
	b.Fatal("no call span near the end has display rows")
	return "", "", ""
}

// catchUpDisplayRows counts the display rows a catch-up of spans returns,
// read in the store as the server's fetcher reads them.
func catchUpDisplayRows(b *testing.B, db *clientdb.DB, spans []archive.LogSpan) int {
	scope := db.LiveLogScope(spans)
	through := db.HighWater().Logs
	n := 0
	for after := int64(0); after < through; {
		rows, next, err := db.SelectDisplayLogs(b.Context(), clientdb.SelectLogsRangeParams{AfterID: after, ThroughID: through, Limit: otlpBatchSize}, "", scope)
		require.NoError(b, err)
		after = next
		n += len(rows)
	}
	return n
}

// catchUpOverHTTP sends one catch-up query and reads it to its terminal
// frame as consumeLogQuery does, returning the data frames and records.
func catchUpOverHTTP(b *testing.B, ts *httptest.Server, q archive.LogQuery) (frames, records int) {
	body, err := json.Marshal(q)
	if err != nil {
		b.Fatal(err)
	}
	req, err := http.NewRequestWithContext(b.Context(), http.MethodPost, ts.URL+"/v1/logs/query", bytes.NewReader(body))
	if err != nil {
		b.Fatal(err)
	}
	req.Header.Set("Content-Type", archive.LogQueryContentType)
	req.Header.Set("Accept", enginetel.LiveContentType)
	resp, err := ts.Client().Do(req)
	if err != nil {
		b.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b.Fatal(resp.Status)
	}
	for {
		kind, _, payload, err := enginetel.ReadLiveFrame(resp.Body)
		if err != nil {
			b.Fatal(err)
		}
		switch kind {
		case enginetel.LiveFrameHello:
			continue
		case enginetel.LiveFrameTerminal:
			return frames, records
		}
		frames++
		batch := &collogspb.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(payload, batch); err != nil {
			b.Fatal(err)
		}
		enginetel.NormalizeLogsRequest(batch)
		for _, rl := range batch.GetResourceLogs() {
			for _, sl := range rl.GetScopeLogs() {
				records += len(sl.GetLogRecords())
			}
		}
	}
}

func BenchmarkLiveLogCatchUpHTTP(b *testing.B) {
	for _, s := range strings.Split(*catchUpRowsFlag, ",") {
		size, err := strconv.Atoi(strings.TrimSpace(s))
		require.NoError(b, err)
		b.Run(fmt.Sprintf("rows=%d", size), func(b *testing.B) {
			f := newLogQueryFixture(b, "client")
			db, err := f.srv.clientDBs.Open(b.Context(), "client")
			require.NoError(b, err)
			b.Cleanup(func() { db.Close() })
			own, turn, digest := appendCatchUpStore(b, db, size)
			through := db.HighWater().Logs
			ts := f.serve(b, "client")
			for _, sc := range []struct {
				name  string
				spans []archive.LogSpan
			}{
				{"own", []archive.LogSpan{{SpanID: own}}},
				{"own+digest", []archive.LogSpan{{SpanID: own, Digests: []string{digest}}}},
				{"tree", []archive.LogSpan{{SpanID: turn, Descendants: true}}},
			} {
				b.Run(sc.name, func(b *testing.B) {
					want := catchUpDisplayRows(b, db, sc.spans)
					require.NotZero(b, want)
					q := archive.LogQuery{Spans: sc.spans, Through: through}
					var frames, records int
					b.ReportAllocs()
					for b.Loop() {
						frames, records = catchUpOverHTTP(b, ts, q)
						if records != want {
							b.Fatalf("read %d records, want %d", records, want)
						}
					}
					b.ReportMetric(float64(frames), "frames/op")
					b.ReportMetric(float64(records), "rows/op")
				})
			}
		})
	}
}
