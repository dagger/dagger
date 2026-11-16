package clientdb

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// Live log catch-ups: when a span starts showing logs in the live TUI, the
// CLI reads its display text up to the follow's cursor with a one-shot
// archive.LogQuery, which the engine answers through the store's row indexes
// (ReadDisplayLogs, from displayLogFetcher), or by scanning the store's log
// metadata in otlpBatchSize windows, as a follow does (SelectDisplayLogs).
// These benchmarks time both over synthetic long-session stores.
//
//	go test ./engine/clientdb -run '^$' -bench LiveLogCatchUp -benchmem
//
// -catchup-rows overrides the store sizes, e.g. "100000,1000000,5000000".

// catchUpWindow is the server's otlpBatchSize: the rows one poll scans.
const catchUpWindow = 1000

// catchUpSpansPerTurn is the shape of one agent turn: a turn span and its
// tool-call spans.
const catchUpSpansPerTurn = 50

type catchUpStore struct {
	db      *DB
	through int64
	spans   int
	// own is a tool-call span near the store's end with few rows; turn is
	// the turn span above it, with a modest subtree; digest routes a
	// service's rows from all over the session.
	own, turn, digest string
}

var (
	catchUpRowsFlag     = flag.String("catchup-rows", "100000,1000000", "comma-separated log row counts of the BenchmarkLiveLogCatchUp stores")
	catchUpChainedLinks = flag.Bool("catchup-chained-links", false, "chain BenchmarkLiveLogCatchUp cause links across turns (deep ancestry)")
)

func catchUpBenchSizes(b *testing.B) []int {
	var sizes []int
	if *catchUpRowsFlag != "" {
		for _, s := range strings.Split(*catchUpRowsFlag, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			require.NoError(b, err)
			sizes = append(sizes, n)
		}
	}
	return sizes
}

func catchUpSpanID(n int) string { return fmt.Sprintf("%016x", n) }

// newCatchUpStore builds a store shaped like a long agent session: a root,
// four long-lived spans that stream a fifth of all output (services), and
// turns of tool calls that come and go, a tenth of which cause-link a call
// of the previous turn (ancestry stays at most five hops deep, unless
// -catchup-chained-links). Rows are mostly short display text, with call
// payloads (unique digests), metadata, digest-routed service text and
// verbose text mixed in.
func newCatchUpStore(b *testing.B, rows int) *catchUpStore {
	b.Helper()
	db, err := openStore(b.Context(), b.TempDir(), "client", telemetryTailBudget)
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, db.Close()) })

	nSpans := min(max(rows/50, 10_000), 50_000)
	turns := nSpans / catchUpSpansPerTurn
	const root = 1
	heavy := []int{2, 3, 4, 5}
	turnBase := func(t int) int { return 6 + t*catchUpSpansPerTurn }

	spans := []Span{logQuerySpan(catchUpSpanID(root), "")}
	for _, h := range heavy {
		spans = append(spans, logQuerySpan(catchUpSpanID(h), catchUpSpanID(root)))
	}
	for t := range turns {
		base := turnBase(t)
		spans = append(spans, logQuerySpan(catchUpSpanID(base), catchUpSpanID(root)))
		for c := 1; c < catchUpSpansPerTurn; c++ {
			s := logQuerySpan(catchUpSpanID(base+c), catchUpSpanID(base))
			if t > 0 && c%10 == 0 {
				// By default, link a call that links nothing itself, so that
				// ancestry stays a few hops deep; -catchup-chained-links
				// chains each link to the previous turn's, across the session.
				target := turnBase(t-1) + c + 1
				if *catchUpChainedLinks {
					target = turnBase(t-1) + c
				}
				s.Links = linksJSON(b, spanLink(b, selectionTrace, catchUpSpanID(target), telemetry.LinkPurposeCause))
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
		data, err := MarshalProtoList(kvs)
		require.NoError(b, err)
		return data
	}
	resource := marshal(&resourcepb.Resource{Attributes: []*commonpb.KeyValue{
		selectionAttr("service.name", "dagger-engine"),
		selectionAttr("service.version", "v0.20.0"),
		selectionAttr("telemetry.sdk.language", "go"),
		selectionAttr("telemetry.sdk.name", "opentelemetry"),
		selectionAttr("telemetry.sdk.version", "1.38.0"),
	}})
	scope := marshal(&commonpb.InstrumentationScope{Name: "dagger.io/engine"})
	var texts [][]byte
	for i := range 16 {
		texts = append(texts, marshal(selectionText(fmt.Sprintf("#%d %s\n", i, strings.Repeat("output ", 3+i*2)))))
	}
	stdout := attrs(selectionAttr("stdio.stream", "1"))
	verbose := attrs(&commonpb.KeyValue{Key: telemetry.LogsVerboseAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}})
	spanName := attrs(selectionAttr(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName))
	callArgs := []*callpbv1.Argument{{Name: "args", Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: strings.Repeat("go test ./... ", 20)}}}}
	var services [][]byte
	for i := range 64 {
		services = append(services, attrs(selectionAttr(telemetry.DagDigestAttr, fmt.Sprintf("xxh3:svc%d", i))))
	}

	rng := rand.New(rand.NewPCG(1, 2))
	batch := make([]Log, 0, 4096)
	for i := range rows {
		t := i * turns / rows
		var span int
		switch r := rng.IntN(100); {
		case r < 20:
			span = heavy[rng.IntN(len(heavy))]
		case r < 23:
			span = turnBase(t)
		default:
			span = turnBase(t) + 1 + rng.IntN(catchUpSpansPerTurn-1)
		}
		row := Log{
			TraceID:              validString(selectionTrace),
			SpanID:               validString(catchUpSpanID(span)),
			Timestamp:            int64(i + 1),
			Body:                 texts[rng.IntN(len(texts))],
			Attributes:           stdout,
			InstrumentationScope: scope,
			Resource:             resource,
		}
		switch k := rng.IntN(100); {
		case k < 68:
		case k < 78:
			digest := fmt.Sprintf("xxh3:call%d", i)
			row.Body = marshal(&commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: marshal(&callpbv1.Call{
				ReceiverDigest: fmt.Sprintf("xxh3:call%d", i-1),
				Field:          "withExec",
				Digest:         digest,
				Type:           &callpbv1.Type{NamedType: "Container"},
				Args:           callArgs,
			})}})
			row.Attributes = attrs(
				selectionAttr(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType),
				selectionAttr(telemetry.DagDigestAttr, digest),
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
	// Spill every row, so that each run reads bodies the same way: from the
	// spill file (in the page cache), as for all but a session's newest rows.
	require.NoError(b, db.logs.flush(b.Context()))
	// The own span: a call of a turn near the end that has display rows
	// (all of them do at a million rows; a small store may leave some bare).
	tt := turns - 3
	var own string
	for c := 7; c < catchUpSpansPerTurn && own == ""; c++ {
		id := catchUpSpanID(turnBase(tt) + c)
		for _, row := range db.logIdx.rowsBySpan[id] {
			if db.logIdx.meta[row-1].flags.display() {
				own = id
				break
			}
		}
	}
	require.NotEmpty(b, own, "no call span near the end has display rows")
	return &catchUpStore{
		db:      db,
		through: db.HighWater().Logs,
		spans:   len(spans),
		own:     own,
		turn:    catchUpSpanID(turnBase(tt)),
		digest:  "xxh3:svc7",
	}
}

// scanCatchUp is the catch-up as displayLogFetcher serves it: one live scope,
// then SelectDisplayLogs over (after, through] one window at a time.
func scanCatchUp(ctx context.Context, db *DB, spans []archive.LogSpan, after, through int64) ([]int64, int, error) {
	scope := db.LiveLogScope(spans)
	var ids []int64
	windows := 0
	for after < through {
		rows, next, err := db.SelectDisplayLogs(ctx, SelectLogsRangeParams{AfterID: after, ThroughID: through, Limit: catchUpWindow}, "", scope)
		if err != nil {
			return nil, windows, err
		}
		windows++
		after = next
		ids = append(ids, logIDs(rows)...)
	}
	return ids, windows, nil
}

// indexCatchUp is the same catch-up as ReadDisplayLogs serves it (the
// displayLogFetcher's one-shot path): its rows gathered from the row indexes
// once, then paged catchUpWindow rows at a time, or scanned when they are a
// large share of the range (see displayIndexMaxShare).
func indexCatchUp(ctx context.Context, db *DB, traceID string, scope *LogQueryScope, after, through int64) ([]int64, int, error) {
	var ids []int64
	batches := 0
	for after < through {
		rows, next, _, err := db.ReadDisplayLogs(ctx, SelectLogsRangeParams{AfterID: after, ThroughID: through, Limit: catchUpWindow}, traceID, scope)
		if err != nil {
			return nil, batches, err
		}
		batches++
		after = next
		ids = append(ids, logIDs(rows)...)
	}
	return ids, batches, nil
}

// withIndexShare runs f with displayIndexMaxShare set to share.
func withIndexShare(share int64, f func()) {
	old := displayIndexMaxShare
	displayIndexMaxShare = share
	defer func() { displayIndexMaxShare = old }()
	f()
}

func BenchmarkLiveLogCatchUp(b *testing.B) {
	for _, size := range catchUpBenchSizes(b) {
		b.Run(fmt.Sprintf("rows=%d", size), func(b *testing.B) {
			store := newCatchUpStore(b, size)
			db := store.db
			b.Logf("store: %d log rows, %d spans", store.through, store.spans)
			scopes := []struct {
				name  string
				spans []archive.LogSpan
			}{
				{"own", []archive.LogSpan{{SpanID: store.own}}},
				{"own+digest", []archive.LogSpan{{SpanID: store.own, Digests: []string{store.digest}}}},
				{"tree", []archive.LogSpan{{SpanID: store.turn, Descendants: true}}},
				// Scopes holding large shares of the store, where the read
				// falls back to the scan: a service span (5% of rows), all
				// four (20%), and the root roll-up (all of them).
				{"service", []archive.LogSpan{{SpanID: catchUpSpanID(2)}}},
				{"services", []archive.LogSpan{{SpanID: catchUpSpanID(2)}, {SpanID: catchUpSpanID(3)}, {SpanID: catchUpSpanID(4)}, {SpanID: catchUpSpanID(5)}}},
				{"root", []archive.LogSpan{{SpanID: catchUpSpanID(1), Descendants: true}}},
			}
			for _, sc := range scopes {
				b.Run(sc.name, func(b *testing.B) {
					want, windows, err := scanCatchUp(b.Context(), db, sc.spans, 0, store.through)
					require.NoError(b, err)
					require.NotEmpty(b, want)
					b.Logf("%s: %d display rows over %d windows", sc.name, len(want), windows)
					b.Run("scan", func(b *testing.B) {
						b.ReportAllocs()
						for b.Loop() {
							ids, _, err := scanCatchUp(b.Context(), db, sc.spans, 0, store.through)
							if err != nil || len(ids) != len(want) {
								b.Fatal(err, len(ids))
							}
						}
						b.ReportMetric(float64(windows), "windows/op")
						b.ReportMetric(float64(len(want)), "rows/op")
					})
					read := func(b *testing.B, name string, share int64) {
						withIndexShare(share, func() {
							got, _, err := indexCatchUp(b.Context(), db, "", db.LiveLogScope(sc.spans), 0, store.through)
							require.NoError(b, err)
							require.True(b, slices.Equal(want, got), "the index reads the same rows as the scan")
							b.Run(name, func(b *testing.B) {
								b.ReportAllocs()
								var batches int
								for b.Loop() {
									var ids []int64
									ids, batches, err = indexCatchUp(b.Context(), db, "", db.LiveLogScope(sc.spans), 0, store.through)
									if err != nil || len(ids) != len(want) {
										b.Fatal(err, len(ids))
									}
								}
								b.ReportMetric(float64(batches), "batches/op")
								b.ReportMetric(float64(len(want)), "rows/op")
							})
						})
					}
					// index never falls back to the scan; read is what the
					// one-shot read does.
					read(b, "index", 0)
					read(b, "read", displayIndexMaxShare)
				})
			}
			// The archive's fixed-cut scope, as serveArchiveSignal resolves
			// it: a span view (its topology cached per cut, and built here
			// before timing), then the same windowed scan, with no live
			// placement, or the same read by index.
			tree := []archive.LogSpan{{SpanID: store.turn, Descendants: true}}
			want, _, err := scanCatchUp(b.Context(), db, tree, 0, store.through)
			require.NoError(b, err)
			cut := db.HighWater()
			_, err = db.SpanViewAt(b.Context(), selectionTrace, cut, nil)
			require.NoError(b, err)
			b.Run("archive-tree/scan", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					view, err := db.SpanViewAt(b.Context(), selectionTrace, cut, nil)
					require.NoError(b, err)
					scope := view.LogQueryScope(tree)
					n := 0
					for after := int64(0); after < store.through; {
						rows, next, err := db.SelectDisplayLogs(b.Context(), SelectLogsRangeParams{AfterID: after, ThroughID: store.through, Limit: catchUpWindow}, selectionTrace, scope)
						require.NoError(b, err)
						after = next
						n += len(rows)
					}
					if n != len(want) {
						b.Fatalf("archive read %d rows, the live scan %d", n, len(want))
					}
				}
				b.ReportMetric(float64(len(want)), "rows/op")
			})
			b.Run("archive-tree/read", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					view, err := db.SpanViewAt(b.Context(), selectionTrace, cut, nil)
					require.NoError(b, err)
					ids, _, err := indexCatchUp(b.Context(), db, selectionTrace, view.LogQueryScope(tree), 0, store.through)
					require.NoError(b, err)
					if !slices.Equal(ids, want) {
						b.Fatalf("archive read %d rows, the live scan %d", len(ids), len(want))
					}
				}
				b.ReportMetric(float64(len(want)), "rows/op")
			})
		})
	}
}
