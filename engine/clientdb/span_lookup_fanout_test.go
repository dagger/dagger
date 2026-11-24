package clientdb

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpanLookupRepeatedIDsAndLateEdges(t *testing.T) {
	lookup := newSpanLookup()
	events := []struct {
		trace, span, parent string
		targets             []string
	}{
		{"a", "one", "root", []string{"cause", "cause"}},
		{"a", "two", "root", []string{"cause"}},
		// Later snapshots add cause links but do not duplicate parent edges.
		{"a", "one", "root", []string{"cause", "later", "later"}},
		// Reusing an ID across traces retains span-ID-only child uniqueness.
		{"b", "one", "root", []string{"later"}},
		{"c", "one", "other", []string{"cause"}},
		// A parent first seen after the initial snapshot keeps existing rules.
		{"a", "late", "", nil},
		{"a", "late", "root", nil},
		{"b", "late", "root", nil},
	}
	for i, event := range events {
		row := Span{ID: int64(i + 1), TraceID: event.trace, SpanID: event.span}
		if event.parent != "" {
			row.ParentSpanID = validString(event.parent)
		}
		lookup.addLocked(row, event.targets)
	}
	require.Equal(t, []string{"one", "two", "late"}, lookup.childrenLocked("root"))
	require.Equal(t, []string{"one"}, lookup.childrenLocked("other"))
	require.ElementsMatch(t, []string{"one", "two"}, lookup.causalChildrenOf("cause"))
	require.Equal(t, []string{"one"}, lookup.causalChildrenOf("later"))
	require.ElementsMatch(t, []string{"cause", "later"}, lookup.causalParentsOf("one"))
	require.Equal(t, "root", lookup.parentName("one"))
	first := func(trace, span string) int64 {
		row, _ := lookup.first(trace, span)
		return row
	}
	require.Equal(t, int64(1), first("a", "one"))
	require.Equal(t, int64(4), first("b", "one"))
	require.Equal(t, int64(5), lookup.lastRowOf("one"))
	require.Equal(t, int64(8), lookup.lastRowOf("late"))
}

// parentName is spanID's parent's ID, "" for none.
func (l *spanLookup) parentName(spanID string) string {
	n, ok := l.lookupLocked(spanID)
	if !ok {
		return ""
	}
	parent, ok := l.parentLocked(n)
	if !ok {
		return ""
	}
	return l.ids.name(parent)
}

// lastRowOf is spanID's newest row, 0 for none.
func (l *spanLookup) lastRowOf(spanID string) int64 {
	n, ok := l.lookupLocked(spanID)
	if !ok {
		return 0
	}
	return l.lastRow[n]
}

var fanoutLookupSink *spanLookup

func BenchmarkSpanLookupFanout(b *testing.B) {
	for _, kind := range []string{"parent", "cause"} {
		for _, count := range []int{1, 16, 128, 1026, 4096} {
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				rows := make([]Span, count)
				var targets []string
				if kind == "cause" {
					targets = []string{"root"}
				}
				for i := range rows {
					rows[i] = Span{ID: int64(i + 1), TraceID: "trace", SpanID: fmt.Sprintf("%016x", uint64(i+1)*0x9e3779b97f4a7c15)}
					if kind == "parent" {
						rows[i].ParentSpanID = validString("root")
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					lookup := newSpanLookup()
					for _, row := range rows {
						lookup.addLocked(row, targets)
					}
					fanoutLookupSink = lookup
				}
				b.StopTimer()
				if kind == "parent" {
					require.Len(b, fanoutLookupSink.childrenLocked("root"), count)
				} else {
					require.Len(b, fanoutLookupSink.causalChildrenOf("root"), count)
				}
			})
		}
	}
}
