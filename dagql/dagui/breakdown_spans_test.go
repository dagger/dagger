package dagui

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"testing"
)

// TestKeptBreakdownWalksMatchFreshWalks checks that the walks the DB keeps
// for TimeBreakdownSpans, brought up to date batch after batch, visit what a
// fresh walk does: random traces arrive in random order, children before
// parents, wait targets before they're received, and spans arrive again
// with more waits, another name or another parent.
func TestKeptBreakdownWalksMatchFreshWalks(t *testing.T) {
	defer func(n int) { breakdownMemoMinEdges = n }(breakdownMemoMinEdges)
	breakdownMemoMinEdges = 0

	names := []string{"A", "B", "C", "resume A", "resume B", "open stored part (x)"}
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			const n = 150
			id := func(i int) SpanID { return hotspotID(1000 + i) }
			randomLink := func(from int) SpanLink {
				if from > 0 && rng.Intn(3) > 0 {
					// causes run before their effects (a cycle would
					// never end)
					target := id(rng.Intn(from))
					if rng.Intn(2) == 0 {
						return evalLink(target)
					}
					return causeLink(target)
				}
				start := float64(from) + rng.Float64()
				return waitLink(id(rng.Intn(n)), "test", at(start), at(start+1+rng.Float64()*5))
			}
			// some traces hide most spans, so that what an ancestry walk
			// stops at matters
			hidden := 2 + int(seed%3)*3
			snaps := make([]SpanSnapshot, n)
			for i := range n {
				s := SpanSnapshot{
					ID:          id(i),
					Name:        names[rng.Intn(len(names))],
					StartTime:   at(float64(i)),
					EndTime:     at(float64(i) + 5 + rng.Float64()*10),
					Internal:    rng.Intn(hidden) == 0,
					Passthrough: rng.Intn(hidden) == 0,
				}
				if i > 0 && rng.Intn(10) > 0 {
					p := rng.Intn(i)
					s.ParentID = id(p)
					if rng.Intn(3) == 0 {
						// an execution twin
						s.Name = snaps[p].Name
					}
				}
				for range rng.Intn(4) {
					s.Links = append(s.Links, randomLink(i))
				}
				snaps[i] = s
			}

			db := NewDB()
			check := func(when string) {
				t.Helper()
				for _, span := range db.Spans.Order {
					got := visitedBreakdownSpans(span)
					want := map[SpanID]bool{}
					newBreakdownWalker(span, func(dep *Span) { want[dep.ID] = true }, nil).run()
					if !maps.Equal(got, want) {
						t.Fatalf("%s: kept walk of %v visits %d spans, a fresh one %d", when, span.ID, len(got), len(want))
					}
				}
			}
			order := rng.Perm(n)
			var delivered []int
			for b, batch := range slices.Collect(slices.Chunk(order, 13)) {
				var spans []SpanSnapshot
				for _, i := range batch {
					spans = append(spans, snaps[i])
				}
				delivered = append(delivered, batch...)
				// some spans arrive again, changed
				for range 3 {
					i := delivered[rng.Intn(len(delivered))]
					s := snaps[i]
					switch rng.Intn(6) {
					case 0, 1:
						s.Links = append(slices.Clone(s.Links), randomLink(i))
					case 2:
						s.Name = names[rng.Intn(len(names))]
					case 3:
						if i > 0 {
							s.ParentID = id(rng.Intn(i))
						}
					case 4, 5:
						// the waiting a DB serving a remote frontend
						// computed, naming more blockers, or others
						rw := &RowWaits{AsOf: at(500)}
						if s.RowWaits != nil && rng.Intn(2) == 0 {
							rw.Segments = slices.Clone(s.RowWaits.Segments)
						}
						rw.Segments = append(rw.Segments, TimeSegment{
							Waiting:  true,
							Target:   id(rng.Intn(n)),
							Blockers: []TimeBlocker{{Target: id(rng.Intn(n))}},
						})
						s.RowWaits = rw
					}
					snaps[i] = s
					spans = append(spans, s)
				}
				db.ImportSnapshots(spans)
				if b%3 == 2 {
					db.UpdateRowWaits(at(500), 0)
				}
				check(fmt.Sprintf("batch %d", b))
			}
		})
	}
}
