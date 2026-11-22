package foldreplay

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"
	"time"

	telemetry "github.com/dagger/otel-go"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/dagui"
)

// Trace is a capture's spans, each its last snapshot, spilled to a file
// one span per record so that a replay of millions of spans holds only an
// index in memory.
type Trace struct {
	f     *os.File
	spans []span
	// end is the trace's end: the newest timestamp it holds.
	end uint64
}

type span struct {
	id, parent  [8]byte
	start, end  uint64
	off         int64
	size        uint32
	ended, kept bool
}

// Open reads the capture at path (see the package doc) into a Trace,
// spilling its spans to a temporary file, removed by Close. With root set
// (a span ID, hex), only the spans beneath it, it included, are kept.
func Open(path, root string) (*Trace, error) {
	f, err := os.CreateTemp("", "foldreplay-*.spans")
	if err != nil {
		return nil, err
	}
	_ = os.Remove(f.Name()) // gone once closed
	t := &Trace{f: f}
	w := bufio.NewWriterSize(f, 1<<20)
	var off int64
	index := map[[8]byte]int{}
	err = readCapture(path, func(req *coltracepb.ExportTraceServiceRequest) error {
		for _, rs := range req.GetResourceSpans() {
			for _, ss := range rs.GetScopeSpans() {
				for _, s := range ss.GetSpans() {
					b, err := proto.Marshal(&tracepb.ResourceSpans{
						Resource:   rs.GetResource(),
						SchemaUrl:  rs.GetSchemaUrl(),
						ScopeSpans: []*tracepb.ScopeSpans{{Scope: ss.GetScope(), SchemaUrl: ss.GetSchemaUrl(), Spans: []*tracepb.Span{s}}},
					})
					if err != nil {
						return err
					}
					if _, err := w.Write(b); err != nil {
						return err
					}
					var sp span
					copy(sp.id[:], s.GetSpanId())
					copy(sp.parent[:], s.GetParentSpanId())
					sp.start, sp.end = s.GetStartTimeUnixNano(), s.GetEndTimeUnixNano()
					sp.ended = sp.end >= sp.start && sp.end != 0
					sp.off, sp.size = off, uint32(len(b))
					off += int64(len(b))
					t.end = max(t.end, sp.start, sp.end)
					if i, ok := index[sp.id]; !ok {
						index[sp.id] = len(t.spans)
						t.spans = append(t.spans, sp)
					} else if sp.ended || !t.spans[i].ended {
						t.spans[i] = sp
					}
				}
			}
		}
		return nil
	})
	if err == nil {
		err = w.Flush()
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	if root != "" {
		var rid [8]byte
		if _, err := hex.Decode(rid[:], []byte(root)); err != nil {
			f.Close()
			return nil, fmt.Errorf("root %q: %w", root, err)
		}
		t.keepBeneath(rid, index)
	} else {
		for i := range t.spans {
			t.spans[i].kept = true
		}
	}
	t.spans = slices.DeleteFunc(t.spans, func(s span) bool { return !s.kept })
	return t, nil
}

func (t *Trace) keepBeneath(root [8]byte, index map[[8]byte]int) {
	// memoized: is span i at or beneath root?
	state := make([]int8, len(t.spans)) // 0 unknown, 1 yes, -1 no
	var beneath func(i int) bool
	beneath = func(i int) bool {
		switch state[i] {
		case 1:
			return true
		case -1:
			return false
		}
		state[i] = -1 // a cycle is not beneath
		s := t.spans[i]
		ok := s.id == root
		if !ok {
			if p, found := index[s.parent]; found {
				ok = beneath(p)
			}
		}
		if ok {
			state[i] = 1
		}
		return ok
	}
	for i := range t.spans {
		t.spans[i].kept = beneath(i)
	}
}

// Len is the number of spans.
func (t *Trace) Len() int { return len(t.spans) }

// Close removes the trace's spill file.
func (t *Trace) Close() error { return t.f.Close() }

// snapshot decodes span i as streamed: running, at its start (no links,
// status or end); else as it ended, or at the trace's end if it never did.
func (t *Trace) snapshot(i int, running bool) (sdktrace.ReadOnlySpan, error) {
	s := t.spans[i]
	b := make([]byte, s.size)
	if _, err := t.f.ReadAt(b, s.off); err != nil && err != io.EOF {
		return nil, err
	}
	var rs tracepb.ResourceSpans
	if err := proto.Unmarshal(b, &rs); err != nil {
		return nil, err
	}
	pb := rs.GetScopeSpans()[0].GetSpans()[0]
	switch {
	case running:
		pb.EndTimeUnixNano = 0
		pb.Links = nil
		pb.Status = nil
	case !s.ended:
		pb.EndTimeUnixNano = t.end
	}
	return telemetry.SpansFromPB([]*tracepb.ResourceSpans{&rs})[0], nil
}

// event is a span starting or ending.
type event struct {
	at  uint64
	i   int32
	end bool
}

// Replay streams the trace into exp as a live session does: each span's
// start snapshot at its start, then its last at its end (the trace's end
// for a span that never ended), in time order, batch events per export.
// between, if set, runs after each export, e.g. to measure, with the time
// the replay is at. read returns, for a FoldOptions.Load, the snapshots of
// ids as a store holds them at the replay's current point.
func (t *Trace) Replay(ctx context.Context, exp sdktrace.SpanExporter, batch int, between func(now time.Time)) (read func(ids []dagui.SpanID) []sdktrace.ReadOnlySpan, run func() error) {
	events := make([]event, 0, 2*len(t.spans))
	index := make(map[[8]byte]int32, len(t.spans))
	for i, s := range t.spans {
		index[s.id] = int32(i)
		end := s.end
		if !s.ended {
			end = t.end
		}
		events = append(events, event{s.start, int32(i), false}, event{max(end, s.start), int32(i), true})
	}
	slices.SortStableFunc(events, func(a, b event) int {
		switch {
		case a.at < b.at:
			return -1
		case a.at > b.at:
			return 1
		case a.end != b.end:
			// at the same instant, starts come first
			if a.end {
				return 1
			}
			return -1
		}
		return 0
	})
	ended := make([]bool, len(t.spans))
	read = func(ids []dagui.SpanID) []sdktrace.ReadOnlySpan {
		var out []sdktrace.ReadOnlySpan
		for _, id := range ids {
			i, ok := index[id.SpanID]
			if !ok {
				continue
			}
			s, err := t.snapshot(int(i), !ended[i])
			if err != nil {
				panic(err)
			}
			out = append(out, s)
		}
		return out
	}
	run = func() error {
		spans := make([]sdktrace.ReadOnlySpan, 0, batch)
		var at uint64
		flush := func() error {
			if len(spans) == 0 {
				return nil
			}
			if err := exp.ExportSpans(ctx, spans); err != nil {
				return err
			}
			spans = spans[:0]
			if between != nil {
				between(time.Unix(0, int64(at)))
			}
			return nil
		}
		for _, ev := range events {
			at = ev.at
			if ev.end {
				ended[ev.i] = true
			}
			s, err := t.snapshot(int(ev.i), !ev.end)
			if err != nil {
				return err
			}
			spans = append(spans, s)
			if len(spans) == batch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		return flush()
	}
	return read, run
}

// Result is a replay's measurement.
type Result struct {
	Spans int
	// BytesPerSpan is the heap the DB holds at the end, per span, its row
	// waits included when computed.
	BytesPerSpan float64
	// Bytes is that heap in all; RowWaitsBytes the part the final
	// UpdateRowWaits added (all of the row waits' cache unless computed
	// during the replay too).
	Bytes, RowWaitsBytes int64
	// Retained is the share of spans a folding DB holds in full.
	Retained float64
	// Loads counts the spans a folding DB read back.
	Loads int
	// Stats and Breakdown are a folding DB's (Breakdown with
	// Options.Breakdown).
	Stats     dagui.FoldStats
	Breakdown *dagui.FoldBreakdown
}

// Options configure a Measure.
type Options struct {
	// Fold folds with the live CLI's policy (dagui.RetainRendered),
	// reading folded spans back from the trace.
	Fold bool
	// NoFoldedCalls keeps no call for folded spans
	// (FoldOptions.NoFoldedCalls), to measure what keeping them costs.
	NoFoldedCalls bool
	// RowWaits computes the waiting below rows (DB.UpdateRowWaits), as the
	// live CLI does: above zero, every RowWaits exports during the replay
	// with RowWaitsBudget, then settled at the end; below zero, only at
	// the end.
	RowWaits       int
	RowWaitsBudget time.Duration
	// HeapProfile, if set, is where to write a heap profile at the end.
	HeapProfile string
	// Breakdown computes Fold.Breakdown at the end, after measuring.
	Breakdown bool
}

// Measure replays t into a DB, folding or not (see Options), and measures
// the heap it holds once the replay ends.
func Measure(ctx context.Context, t *Trace, opts Options) (Result, error) {
	var db *dagui.DB
	var read func([]dagui.SpanID) []sdktrace.ReadOnlySpan
	exp := sdktrace.SpanExporter(dbExporter{&db})
	var exports int
	var between func(time.Time)
	if opts.RowWaits > 0 {
		between = func(now time.Time) {
			if exports++; exports%opts.RowWaits == 0 {
				db.UpdateRowWaits(now, opts.RowWaitsBudget)
			}
		}
	}
	read, run := t.Replay(ctx, exp, 512, between)
	var before, replayed, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if opts.Fold {
		db = dagui.NewDB(dagui.WithFold(dagui.FoldOptions{
			Load: func(ids []dagui.SpanID) {
				if err := db.ExportSpans(dagui.FoldFill(ctx), read(ids)); err != nil {
					panic(err)
				}
			},
			NoFoldedCalls: opts.NoFoldedCalls,
		}))
	} else {
		db = dagui.NewDB()
	}
	if err := run(); err != nil {
		return Result{}, err
	}
	runtime.GC()
	runtime.ReadMemStats(&replayed)
	after = replayed
	if opts.RowWaits != 0 {
		// settle, as of the trace's end
		db.UpdateRowWaits(time.Unix(0, int64(t.end)), 0)
		runtime.GC()
		runtime.ReadMemStats(&after)
	}
	if opts.HeapProfile != "" {
		if err := writeHeapProfile(opts.HeapProfile); err != nil {
			return Result{}, err
		}
	}
	res := Result{
		Spans:         t.Len(),
		Bytes:         int64(after.HeapAlloc) - int64(before.HeapAlloc),
		RowWaitsBytes: int64(after.HeapAlloc) - int64(replayed.HeapAlloc),
	}
	res.BytesPerSpan = float64(res.Bytes) / float64(t.Len())
	if f := db.Fold(); f != nil {
		res.Stats = f.Stats()
		res.Retained = float64(res.Stats.Retained) / float64(res.Stats.Spans)
		res.Loads = f.Loads
		if opts.Breakdown {
			bd := f.Breakdown()
			res.Breakdown = &bd
		}
	}
	runtime.KeepAlive(db)
	return res, nil
}

func writeHeapProfile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	// the profile is as of the last GC, which Measure just ran
	if err := pprof.WriteHeapProfile(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// FormatBreakdown renders a Fold.Breakdown: the retained spans by reason,
// and what each variant of the policy would retain, and why.
func FormatBreakdown(bd dagui.FoldBreakdown) string {
	var sb strings.Builder
	pct := func(n int) float64 { return 100 * float64(n) / float64(max(bd.Spans, 1)) }
	fmt.Fprintf(&sb, "retained %d of %d spans (%.1f%%), %d calls kept for folded spans\n", bd.Retained, bd.Spans, pct(bd.Retained), bd.FoldedCalls)
	sb.WriteString("reasons (first that applies):\n")
	for _, r := range bd.Reasons {
		if r.Spans > 0 {
			fmt.Fprintf(&sb, "  %9d  %5.1f%%  %s\n", r.Spans, pct(r.Spans), r.Name)
		}
	}
	sb.WriteString("what-ifs:\n")
	for _, w := range bd.WhatIf {
		fmt.Fprintf(&sb, "  %9d  %5.1f%%  %s\n", w.Spans, pct(w.Spans), w.Name)
		for _, r := range bd.WhatIfReasons[w.Name] {
			fmt.Fprintf(&sb, "      %9d  %5.1f%%  %s\n", r.Spans, pct(r.Spans), r.Name)
		}
	}
	return sb.String()
}

type dbExporter struct{ db **dagui.DB }

func (e dbExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return (*e.db).ExportSpans(ctx, spans)
}

func (dbExporter) Shutdown(context.Context) error { return nil }
