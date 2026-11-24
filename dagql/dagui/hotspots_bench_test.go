package dagui

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"slices"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
)

// The benchmarks below model a remote frontend (dagger.io's per-viewer DBs)
// importing a large historical trace: 11 concurrent POST /query fan-outs of
// ~1,450 calls each, each fan-out waiting on ~480 calls of another, with
// call_exec twins, exec children carrying argv labels and lazy resumes; every
// span carrying a handful of attributes. The frontend imports the spans in
// batches of 1000 in start order, and after each batch sends what
// UpdatedSnapshots returns, asking TimeBreakdownSpans of every span it sends
// for the support spans to subscribe to. It computes the rows' waiting
// (UpdateRowWaits) after each batch, too, which UpdatedSnapshots sends.

const (
	hotspotFanOuts      = 11
	hotspotFanOutCalls  = 1447
	hotspotFanOutWaits  = 482
	hotspotImportBatch  = 1000
	hotspotTraceIDBytes = 7
)

func hotspotID(n int) SpanID {
	var id trace.SpanID
	binary.BigEndian.PutUint64(id[:], uint64(n)+1)
	return SpanID{SpanID: id}
}

// hotspotTrace builds the trace, in start order.
func hotspotTrace() []SpanSnapshot {
	traceID := TraceID{TraceID: trace.TraceID{hotspotTraceIDBytes}}
	epoch := time.Unix(1_700_000_000, 0)
	next := 0
	newID := func() SpanID {
		next++
		return hotspotID(next)
	}
	var spans []SpanSnapshot
	add := func(s SpanSnapshot) SpanSnapshot {
		s.TraceID = traceID
		for i := range s.Links {
			s.Links[i].SpanContext.TraceID = traceID
		}
		attributeHeavy(&s, len(spans))
		spans = append(spans, s)
		return s
	}
	root := add(SpanSnapshot{
		ID:        newID(),
		Name:      "dagger call",
		StartTime: epoch,
		EndTime:   epoch.Add(time.Hour),
	})

	fanOuts := make([]SpanID, hotspotFanOuts)
	calls := make([][]SpanID, hotspotFanOuts)
	for f := range fanOuts {
		fanOuts[f] = newID()
		for range hotspotFanOutCalls {
			calls[f] = append(calls[f], newID())
		}
	}
	callStart := func(f, j int) time.Time {
		return epoch.Add(time.Duration(j)*time.Second + time.Duration(f)*time.Millisecond + time.Second)
	}
	for f, id := range fanOuts {
		// the fan-out waits on calls of the next one, outside its subtree
		var links []SpanLink
		other := calls[(f+1)%hotspotFanOuts]
		for w := range hotspotFanOutWaits {
			j := w * 3
			links = append(links, waitLink(other[j], "singleflight",
				callStart(f, j), callStart(f, j).Add(2*time.Second)))
		}
		add(SpanSnapshot{
			ID:          id,
			ParentID:    root.ID,
			Name:        "POST /query",
			Passthrough: true,
			StartTime:   epoch.Add(time.Duration(f) * time.Millisecond),
			EndTime:     epoch.Add(50 * time.Minute),
			Links:       links,
		})
	}
	for j := range hotspotFanOutCalls {
		for f := range fanOuts {
			start := callStart(f, j)
			end := start.Add(3 * time.Second)
			name := "Container.withExec"
			if j%5 == 0 {
				name = "Container.from"
			}
			call := SpanSnapshot{
				ID:        calls[f][j],
				ParentID:  fanOuts[f],
				Name:      name,
				StartTime: start,
				EndTime:   end,
			}
			if j%7 == 3 {
				// calls wait on each other across fan-outs too
				other := calls[(f+2)%hotspotFanOuts][max(0, j-1)]
				call.Links = []SpanLink{waitLink(other, "call_exec", start, start.Add(time.Second))}
			}
			add(call)
			if j%2 == 1 {
				continue
			}
			twin := add(SpanSnapshot{
				ID:        newID(),
				ParentID:  call.ID,
				Name:      name,
				StartTime: start.Add(time.Millisecond),
				EndTime:   end,
			})
			if j%4 == 0 {
				run := add(SpanSnapshot{
					ID:        newID(),
					ParentID:  twin.ID,
					Name:      "exec.run",
					StartTime: start.Add(2 * time.Millisecond),
					EndTime:   end,
				})
				proc := SpanSnapshot{
					ID:        newID(),
					ParentID:  run.ID,
					Name:      "exec.processRun",
					StartTime: start.Add(3 * time.Millisecond),
					EndTime:   end,
				}
				proc.ProcessAttribute(telemetryattrs.WcprofExecArgvAttr,
					fmt.Sprintf(`["go","test","-run","TestCase%d","./..."]`, j))
				add(proc)
			} else if j%8 == 2 {
				// a lazy evaluation of a call of another fan-out
				add(SpanSnapshot{
					ID:        newID(),
					ParentID:  twin.ID,
					Name:      "resume " + name,
					StartTime: start.Add(2 * time.Millisecond),
					EndTime:   end,
					Links:     []SpanLink{evalLink(calls[(f+3)%hotspotFanOuts][j/2])},
				})
			}
		}
	}
	slices.SortStableFunc(spans, func(a, b SpanSnapshot) int {
		return a.StartTime.Compare(b.StartTime)
	})
	return spans
}

// attributeHeavy gives the span the attributes a real engine span carries:
// low-cardinality ones shared across spans, and a few of its own.
func attributeHeavy(s *SpanSnapshot, n int) {
	s.ProcessAttribute(telemetryattrs.TelemetryOriginClientIDAttr, "pjx2dkwi1b0y9pgsbq6tm4ma3")
	s.ProcessAttribute(telemetryattrs.CacheOutcomeAttr, []string{"hit", "miss"}[n%2])
	s.ProcessAttribute(telemetryattrs.WcprofOpKindAttr, "call")
	s.ProcessAttribute(telemetry.ModuleRefAttr, "github.com/dagger/dagger/toolchains/go@main")
	s.ProcessAttribute("dagger.io/dag.digest", fmt.Sprintf("xxh3:%016x", uint64(n)*0x9e3779b97f4a7c15))
	s.ProcessAttribute("wcprof.op.id", int64(n))
	s.ProcessAttribute("dagger.io/ui.labels", []string{"go", fmt.Sprintf("pkg%d", n%37)})
}

type hotspotPhase int

const (
	measureBreakdownSpans hotspotPhase = iota
	measureUpdatedSnapshots
)

// replayRemoteImport imports the trace into a tracked DB as a remote
// frontend does, timing only the phase measured.
func replayRemoteImport(b *testing.B, spans []SpanSnapshot, phase hotspotPhase) {
	b.Helper()
	b.StopTimer()
	db := NewDB(WithUpdateTracking())
	// subscribed to the root's children, as a view of the trace's top
	filter := map[SpanID]bool{spans[0].ID: true}
	var sent, supports int
	for batch := range slices.Chunk(spans, hotspotImportBatch) {
		db.ImportSnapshots(slices.Clone(batch))
		// and computes the rows' waiting for the frontend to draw
		db.UpdateRowWaits(spans[0].EndTime, 0)
		if phase == measureUpdatedSnapshots {
			b.StartTimer()
		}
		snaps := db.UpdatedSnapshots(filter)
		if phase == measureUpdatedSnapshots {
			b.StopTimer()
		}
		sent += len(snaps)
		if phase == measureBreakdownSpans {
			b.StartTimer()
		}
		for _, snap := range snaps {
			db.Spans.Map[snap.ID].TimeBreakdownSpans(func(dep *Span) {
				if !filter[dep.ID] {
					filter[dep.ID] = true
					supports++
				}
			})
		}
		if phase == measureBreakdownSpans {
			b.StopTimer()
		}
	}
	b.ReportMetric(float64(sent), "sent")
	b.ReportMetric(float64(len(filter)), "filter")
	b.StartTimer()
}

// BenchmarkRemoteTimeBreakdownSpans times the TimeBreakdownSpans calls of a
// remote frontend's batched import.
func BenchmarkRemoteTimeBreakdownSpans(b *testing.B) {
	spans := hotspotTrace()
	for b.Loop() {
		replayRemoteImport(b, spans, measureBreakdownSpans)
	}
}

// BenchmarkRemoteUpdatedSnapshots times the UpdatedSnapshots calls of a
// remote frontend's batched import, with a filter grown by the support spans.
func BenchmarkRemoteUpdatedSnapshots(b *testing.B) {
	spans := hotspotTrace()
	for b.Loop() {
		replayRemoteImport(b, spans, measureUpdatedSnapshots)
	}
}

// BenchmarkFanOutTimeBreakdownSpans times one TimeBreakdownSpans call of a
// fan-out row of the whole trace, as a frontend asks of every row it renders,
// frame after frame.
func BenchmarkFanOutTimeBreakdownSpans(b *testing.B) {
	db := NewDB()
	db.ImportSnapshots(hotspotTrace())
	var row *Span
	for _, span := range db.Spans.Order {
		if span.Name == "POST /query" {
			row = span
			break
		}
	}
	var visited int
	for b.Loop() {
		visited = 0
		row.TimeBreakdownSpans(func(*Span) { visited++ })
	}
	b.ReportMetric(float64(visited), "visited")
}

// BenchmarkSpanAttributesGob times gob-encoding and decoding the
// attributes alone of a batch of 1000 snapshots, in each form.
func BenchmarkSpanAttributesGob(b *testing.B) {
	var attrs []SpanAttributes
	for _, s := range hotspotTrace()[:hotspotImportBatch] {
		attrs = append(attrs, s.ExtraAttributes)
	}
	for _, form := range []struct {
		name   string
		binary bool
	}{
		{"json", false},
		{"binary", true},
	} {
		EncodeBinaryAttributes(form.binary)
		encoded := make([][]byte, len(attrs))
		b.Run(form.name+"/encode", func(b *testing.B) {
			for b.Loop() {
				for i, a := range attrs {
					p, err := a.GobEncode()
					if err != nil {
						b.Fatal(err)
					}
					encoded[i] = p
				}
			}
		})
		b.Run(form.name+"/decode", func(b *testing.B) {
			for b.Loop() {
				for _, p := range encoded {
					var a SpanAttributes
					if err := a.GobDecode(p); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
	EncodeBinaryAttributes(false)
}

// BenchmarkSnapshotsGob times encoding and decoding a batch of 1000
// attribute-heavy snapshots with gob, as the Cloud websocket sends them.
func BenchmarkSnapshotsGob(b *testing.B) {
	db := NewDB(WithUpdateTracking())
	db.ImportSnapshots(hotspotTrace())
	var snaps []SpanSnapshot
	for _, span := range db.Spans.Order[:hotspotImportBatch] {
		snaps = append(snaps, span.Snapshot())
	}
	encode := func() []byte {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(snaps); err != nil {
			b.Fatal(err)
		}
		return buf.Bytes()
	}
	for _, form := range []struct {
		name   string
		binary bool
	}{
		{"json", false},
		{"binary", true},
	} {
		EncodeBinaryAttributes(form.binary)
		b.Run(form.name+"/encode", func(b *testing.B) {
			var n int
			for b.Loop() {
				n = len(encode())
			}
			b.ReportMetric(float64(n), "bytes")
		})
		b.Run(form.name+"/decode", func(b *testing.B) {
			payload := encode()
			for b.Loop() {
				var decoded []SpanSnapshot
				if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&decoded); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	EncodeBinaryAttributes(false)
}
