package dagui

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"testing"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// referenceUpdatedSnapshots is UpdatedSnapshots as it was before it kept
// track of the subscribed spans it sent and of which passthrough spans have
// a failed child: everything looked at on every call.
func referenceUpdatedSnapshots(db *DB, filter map[SpanID]bool) []SpanSnapshot {
	updated := slices.Clone(db.updatedSpans)
	slices.SortStableFunc(updated, func(a, b *Span) int {
		return a.StartTime.Compare(b.StartTime)
	})
	notable := func(span *Span) bool {
		if !span.Received {
			return false
		}
		if filter == nil || filter[span.ParentID] || filter[span.ID] {
			return true
		}
		if span.IsFailedOrCausedFailure() {
			return true
		}
		if span.Reveal || span.RevealedSpans.Len() > 0 {
			return true
		}
		if span.HasProgress() || span.ProgressSpans.Len() > 0 {
			return true
		}
		if span.Passthrough {
			for _, child := range span.ChildSpans.Spans() {
				if child.IsFailedOrCausedFailure() {
					return true
				}
			}
		}
		return false
	}
	var snapshots []SpanSnapshot
	for _, span := range updated {
		if (span.Received && db.needsAncestors(span)) || span.Matches(notable) {
			snapshots = append(snapshots, span.Snapshot())
		}
	}
	included := make(map[SpanID]bool, len(snapshots))
	for _, snapshot := range snapshots {
		included[snapshot.ID] = true
	}
	snapshots = db.appendSurfacedAncestors(snapshots, included)
	for spanID := range filter {
		span := db.Spans.Map[spanID]
		if span == nil {
			continue
		}
		if !db.hasSeen(spanID) && !included[spanID] {
			included[spanID] = true
			snapshots = append(snapshots, span.Snapshot())
		}
		for p := range span.Parents {
			if !db.hasSeen(p.ID) && !included[p.ID] {
				included[p.ID] = true
				snapshots = append(snapshots, p.Snapshot())
			}
		}
	}
	for _, snapshot := range snapshots {
		db.seen(snapshot.ID)
		if span := db.Spans.Map[snapshot.ID]; span != nil && span.Received {
			delete(db.unsentAncestors, snapshot.ID)
		}
	}
	for _, span := range db.updatedSpans {
		span.updateQueued = false
	}
	db.updatedSpans = nil
	return snapshots
}

// TestUpdatedSnapshotsMatchesReference feeds random traces to two tracked
// DBs, in random order (children before parents, spans arriving again with
// another parent or failing), subscribing to more and more spans by ID, and
// checks that UpdatedSnapshots sends what it did when it looked at every
// subscribed span and every passthrough span's children on every call.
func TestUpdatedSnapshotsMatchesReference(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			const n = 200
			id := func(i int) SpanID { return hotspotID(5000 + i) }
			snaps := make([]SpanSnapshot, n)
			for i := range n {
				s := SpanSnapshot{
					ID:          id(i),
					Name:        fmt.Sprintf("span %d", i),
					StartTime:   at(float64(i)),
					EndTime:     at(float64(i) + 1),
					Passthrough: rng.Intn(5) == 0,
				}
				if i > 0 && rng.Intn(10) > 0 {
					s.ParentID = id(rng.Intn(i))
				}
				snaps[i] = s
			}
			got, want := NewDB(WithUpdateTracking()), NewDB(WithUpdateTracking())
			filter := map[SpanID]bool{id(0): true}
			order := rng.Perm(n)
			var delivered []int
			for b, batch := range slices.Collect(slices.Chunk(order, 11)) {
				var spans []SpanSnapshot
				for _, i := range batch {
					spans = append(spans, snaps[i])
				}
				delivered = append(delivered, batch...)
				for range 2 {
					i := delivered[rng.Intn(len(delivered))]
					s := snaps[i]
					if rng.Intn(2) == 0 && i > 0 {
						s.ParentID = id(rng.Intn(i))
					} else {
						s.Status = sdktrace.Status{Code: codes.Error}
					}
					snaps[i] = s
					spans = append(spans, s)
				}
				// subscribe to spans by ID, some not received yet
				for range 4 {
					filter[id(rng.Intn(n))] = true
				}
				got.ImportSnapshots(slices.Clone(spans))
				want.ImportSnapshots(slices.Clone(spans))
				gotIDs, wantIDs := map[SpanID]bool{}, map[SpanID]bool{}
				for _, s := range got.UpdatedSnapshots(filter) {
					gotIDs[s.ID] = true
				}
				for _, s := range referenceUpdatedSnapshots(want, filter) {
					wantIDs[s.ID] = true
				}
				if !maps.Equal(gotIDs, wantIDs) {
					t.Fatalf("batch %d: sent %d spans, want %d", b, len(gotIDs), len(wantIDs))
				}
			}
		})
	}
}
