package dagui

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// referenceAdd is how Activity.Add kept its completed intervals before it
// relied on them being sorted and merged: check every interval for one that
// holds the new one, insert it, and merge the whole list again.
func referenceAdd(ivals []Interval, ival Interval) []Interval {
	for _, existing := range ivals {
		if ival.Start.After(existing.Start) && ival.End.Before(existing.End) {
			return ivals
		}
	}
	idx, _ := slices.BinarySearchFunc(ivals, ival, func(a, b Interval) int {
		return a.Start.Compare(b.Start)
	})
	ivals = slices.Insert(slices.Clone(ivals), idx, ival)
	last := 0
	for i := 1; i < len(ivals); i++ {
		if ivals[i].Start.Before(ivals[last].End) {
			if ivals[i].End.After(ivals[last].End) {
				ivals[last].End = ivals[i].End
			}
		} else {
			last++
			ivals[last] = ivals[i]
		}
	}
	return ivals[:last+1]
}

// Activity.Add keeps exactly the intervals the full scan and merge kept,
// whatever order spans end in and however they overlap, touch or tie.
func TestActivityAddMatchesFullMerge(t *testing.T) {
	epoch := time.Unix(1_700_000_000, 0)
	for seed := range int64(200) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			var activity Activity
			var want []Interval
			for i := range 300 {
				var id trace.SpanID
				binary.BigEndian.PutUint64(id[:], uint64(i)+1)
				// Few distinct start times and short spans, so intervals tie,
				// touch, overlap and hold one another often.
				start := epoch.Add(time.Duration(rng.Intn(400)) * time.Second)
				end := start.Add(time.Duration(1+rng.Intn(12)) * time.Second)
				span := &Span{SpanSnapshot: SpanSnapshot{
					ID:        SpanID{SpanID: id},
					StartTime: start,
					EndTime:   end,
				}}
				activity.Add(span)
				want = referenceAdd(want, Interval{Start: start, End: end})
				if !slices.Equal(activity.CompletedIntervals, want) {
					t.Fatalf("after %d spans: intervals %v, want %v", i+1, activity.CompletedIntervals, want)
				}
			}
		})
	}
}
