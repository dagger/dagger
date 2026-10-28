package clientdb

import (
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// Lazy evaluations make spans that each cause-link the same set of install
// spans: every part a result's evaluation demands starts its own span with
// the result's full link set. The index must hold such a fan-out at a cost
// per span, not per link, or a session that evaluates thousands of lazy parts
// of a large module closure grows by gigabytes.
//
// Not parallel: it measures the live heap, which concurrent tests would move.
func TestSpanLookupSharedCauseLinksStayCompact(t *testing.T) {
	const (
		targets = 200
		linkers = 10000
	)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	l := newSpanLookup()
	l.mu.Lock()
	for i := range linkers {
		// Fresh strings for every row, as causalLinkTargets makes them.
		row := Span{ID: int64(i + 1), TraceID: "trace", SpanID: fmt.Sprintf("%016x", 1<<32+i)}
		causal := make([]string, targets)
		for j := range causal {
			causal[j] = fmt.Sprintf("%016x", j+1)
		}
		l.addLocked(row, causal)
	}
	l.mu.Unlock()

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	runtime.KeepAlive(l)

	// The links themselves are linkers×targets = 2,000,000 edges. A cost per
	// span and per distinct target stays well under this bound; a cost per
	// edge does not.
	require.Less(t, retained, int64(16<<20), "the index retains %d bytes for %d spans sharing one set of %d cause links", retained, linkers, targets)

	kids := l.causalChildrenOf(fmt.Sprintf("%016x", 1))
	require.Len(t, kids, linkers, "every linking span is a causal child of each target")
}

// A span whose links grow across snapshots, as a reused service's live span
// does, moves to a larger set each time. The sets it leaves behind must not
// stay in the index, or growing one span link by link costs the square of
// its links.
//
// Not parallel: it measures the live heap, which concurrent tests would move.
func TestSpanLookupGrowingCauseLinksStayCompact(t *testing.T) {
	const links = 3000
	id := func(i int) string { return fmt.Sprintf("%016x", i+1) }
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	l := newSpanLookup()
	l.mu.Lock()
	// Two spans share the first set; only the first span keeps growing.
	l.addLocked(Span{ID: 1, TraceID: "trace", SpanID: "service"}, []string{id(0)})
	l.addLocked(Span{ID: 2, TraceID: "trace", SpanID: "sibling"}, []string{id(0)})
	for i := 1; i < links; i++ {
		l.addLocked(Span{ID: int64(i + 2), TraceID: "trace", SpanID: "service"}, []string{id(i)})
	}
	l.mu.Unlock()

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	runtime.KeepAlive(l)

	// One span with 3,000 links and one with one link: a cost per link
	// stays well under this bound; the square of the links does not.
	require.Less(t, retained, int64(4<<20), "the index retains %d bytes for one span grown to %d cause links", retained, links)

	require.ElementsMatch(t, []string{"service", "sibling"}, l.causalChildrenOf(id(0)))
	require.Equal(t, []string{"service"}, l.causalChildrenOf(id(links-1)))
	// Each log scope holds the span, its link targets, and the other spans
	// linking those targets.
	require.Equal(t, map[string]struct{}{"sibling": {}, id(0): {}, "service": {}}, l.logScope("sibling"))
	require.Len(t, l.logScope("service"), 1+links+1)
}

// The index answers every causal query exactly as an index of each link edge
// would: children, parents (through the log scope's seeds), descendants and
// log scopes, over spans whose links repeat across snapshot rows, grow in
// later rows, are shared, overlap, and include self-links.
func TestSpanLookupCauseLinkAnswers(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(1))
	const spans = 120
	id := func(i int) string { return fmt.Sprintf("%016x", i+1) }

	l := newSpanLookup()
	ref := newRefCauseIndex()
	// A few shared link sets, as a result's lazy parts carry, plus random ones.
	shared := [][]string{{id(0), id(1), id(2)}, {id(3)}, {id(0), id(4), id(5), id(6)}}
	for row := range 1500 {
		span := rng.Intn(spans)
		var causal []string
		switch rng.Intn(4) {
		case 0:
			causal = slices.Clone(shared[rng.Intn(len(shared))])
		case 1:
			for range rng.Intn(5) {
				causal = append(causal, id(rng.Intn(spans)))
			}
		case 2:
			causal = []string{id(span)} // a self-link
		}
		s := Span{ID: int64(row + 1), TraceID: "trace", SpanID: id(span)}
		if rng.Intn(3) == 0 {
			s.ParentSpanID = validString(id(rng.Intn(spans)))
		}
		l.mu.Lock()
		l.addLocked(s, causal)
		l.mu.Unlock()
		ref.add(s.SpanID, causal)
	}

	for i := range spans + 2 {
		spanID := id(i)
		got := l.causalChildrenOf(spanID)
		want := ref.causalChildren[spanID]
		require.ElementsMatch(t, want, got, "causal children of %s", spanID)
		require.Equal(t, refHasDescendants(l, ref, spanID), l.hasDescendants(spanID), "descendants of %s", spanID)
		require.Equal(t, refDirectChildren(l, ref, spanID), l.directChildren(spanID), "direct children of %s", spanID)
		require.Equal(t, refLogScope(l, ref, spanID), l.logScope(spanID), "log scope of %s", spanID)
	}
}

// refCauseIndex is the index of every cause-link edge in both directions, as
// the span lookup kept it before it stored shared link sets once.
type refCauseIndex struct {
	causalChildren map[string][]string
	causalParents  map[string][]string
}

func newRefCauseIndex() *refCauseIndex {
	return &refCauseIndex{causalChildren: map[string][]string{}, causalParents: map[string][]string{}}
}

func (r *refCauseIndex) add(spanID string, causal []string) {
	for _, target := range causal {
		if !slices.Contains(r.causalChildren[target], spanID) {
			r.causalChildren[target] = append(r.causalChildren[target], spanID)
		}
		if !slices.Contains(r.causalParents[spanID], target) {
			r.causalParents[spanID] = append(r.causalParents[spanID], target)
		}
	}
}

func refHasDescendants(l *spanLookup, r *refCauseIndex, root string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, kid := range l.children[root] {
		if kid != root {
			return true
		}
	}
	for _, kid := range r.causalChildren[root] {
		if kid != root {
			return true
		}
	}
	return false
}

func refDirectChildren(l *spanLookup, r *refCauseIndex, spanID string) map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	kids := map[string]struct{}{}
	for _, kid := range l.children[spanID] {
		if kid != spanID {
			kids[kid] = struct{}{}
		}
	}
	for _, kid := range r.causalChildren[spanID] {
		if kid != spanID {
			kids[kid] = struct{}{}
		}
	}
	return kids
}

func refLogScope(l *spanLookup, r *refCauseIndex, root string) map[string]struct{} {
	scope := map[string]struct{}{}
	var queue []string
	for _, seed := range append([]string{root}, r.causalParents[root]...) {
		if _, exists := scope[seed]; !exists {
			scope[seed] = struct{}{}
			queue = append(queue, seed)
		}
	}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		l.mu.RLock()
		children := append(slices.Clone(l.children[parent]), r.causalChildren[parent]...)
		l.mu.RUnlock()
		for _, child := range children {
			if _, exists := scope[child]; !exists {
				scope[child] = struct{}{}
				queue = append(queue, child)
			}
		}
	}
	return scope
}
