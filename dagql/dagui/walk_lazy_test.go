package dagui

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// describeRows describes rows as Rows renders them: every field a frontend
// reads.
func describeRows(rows *Rows) string {
	var out strings.Builder
	for _, row := range rows.Order {
		fmt.Fprintf(&out, "%s%s chained=%v running=%v children=%v showing=%v expanded=%v\n",
			strings.Repeat("  ", row.Depth), row.Span.Name, row.Chained,
			row.IsRunningOrChildRunning, row.HasChildren, row.ShowingChildren, row.Expanded)
	}
	return out.String()
}

// describeAll describes a tree and everything beneath it, as describeTree
// does, building what isn't built yet. Whether it has children is read
// before they're built, so a lazy tree has to work it out unbuilt.
func describeAll(tree *TraceTree, opts FrontendOpts, depth int, out *strings.Builder) {
	hasChildren := tree.hasVisibleChildren(opts)
	fmt.Fprintf(out, "%s%s chained=%v running=%v revealed=%v children=%v\n",
		strings.Repeat("  ", depth), tree.Span.Name,
		tree.Chained, tree.IsRunningOrChildRunning, tree.RevealedChildren, hasChildren)
	for _, child := range tree.ChildTrees() {
		describeAll(child, opts, depth+1, out)
	}
	if revealed := tree.RevealedTrees(); revealed != nil {
		fmt.Fprintf(out, "%s(revealed)\n", strings.Repeat("  ", depth+1))
		for _, child := range revealed {
			describeAll(child, opts, depth+1, out)
		}
	}
}

// randomExpansion expands and collapses spans at random, and sometimes
// changes a span's verbosity.
func randomExpansion(rng *rand.Rand, ids []SpanID, opts FrontendOpts) FrontendOpts {
	opts.SpanExpanded = map[SpanID]bool{}
	for _, id := range ids {
		switch rng.Intn(10) {
		case 0, 1, 2:
			opts.SpanExpanded[id] = true
		case 3:
			opts.SpanExpanded[id] = false
		}
	}
	if rng.Intn(4) == 0 {
		opts.SpanVerbosity = map[SpanID]int{ids[rng.Intn(len(ids))]: rng.Intn(6)}
	}
	if rng.Intn(4) == 0 {
		opts.ExpandCompleted = true
	}
	return opts
}

// TestLazyRowsMatchFullTree holds a lazy RowsView to the full one: over
// random traces (see randomTraceDB) and random views, expansions included,
//
//   - the rows a lazy view renders are the rows the full view renders, down
//     to running state, Chained and whether each row has children;
//   - so are they when the lazy view was built with fewer rows expanded, and
//     Rows builds the rest on demand;
//   - every tree, built on demand, matches the full view's tree, including
//     running state and children reported before they're built;
//   - HomeTree finds each span's home tree, at the same place.
func TestLazyRowsMatchFullTree(t *testing.T) {
	const runs = 3000
	failures := 0
	for seed := range runs {
		rng := rand.New(rand.NewSource(int64(seed)))
		db, ids := randomTraceDB(rng)
		opts := randomExpansion(rng, ids, randomViewOpts(rng, ids))

		fail := func(format string, args ...any) {
			t.Helper()
			failures++
			if failures <= 3 {
				t.Errorf("seed %d: %s\ntrace:\n%s\nopts: zoom=%v verbosity=%d strict=%v noisy=%v filter=%v",
					seed, fmt.Sprintf(format, args...), describeTraceForWalk(db),
					opts.ZoomedSpan, opts.Verbosity, opts.StrictSubtree, opts.RevealNoisySpans, opts.RootFilter != nil)
			}
		}

		full := db.rowsView(opts, false)
		want := describeRows(full.Rows(opts))
		if got := describeRows(db.rowsView(opts, true).Rows(opts)); got != want {
			fail("rows differ:\nfull:\n%s\nlazy:\n%s", want, got)
			continue
		}

		// Built with some expansions missing, as if toggled on since: Rows
		// builds what they show.
		built := opts
		built.SpanExpanded = map[SpanID]bool{}
		for id, expanded := range opts.SpanExpanded {
			if !expanded || rng.Intn(2) == 0 {
				built.SpanExpanded[id] = expanded
			}
		}
		if got := describeRows(db.rowsView(built, true).Rows(opts)); got != want {
			fail("rows built on demand differ:\nfull:\n%s\nlazy:\n%s", want, got)
			continue
		}

		lazy := db.rowsView(opts, true)
		var fullTrees, lazyTrees strings.Builder
		for _, tree := range full.Body {
			describeAll(tree, opts, 0, &fullTrees)
		}
		for _, tree := range lazy.Body {
			describeAll(tree, opts, 0, &lazyTrees)
		}
		if fullTrees.String() != lazyTrees.String() {
			fail("trees differ:\nfull:\n%s\nlazy:\n%s", fullTrees.String(), lazyTrees.String())
			continue
		}

		lazy = db.rowsView(opts, true)
		for _, span := range db.Spans.Order {
			want := full.BySpan[span.ID]
			if want != nil && want.revealedCopy {
				want = nil
			}
			got := lazy.HomeTree(span)
			if (want == nil) != (got == nil) {
				fail("HomeTree(%s) = %v, want %v", span.Name, got != nil, want != nil)
				continue
			}
			for want != nil {
				if want.Span != got.Span || want.Chained != got.Chained ||
					want.IsRunningOrChildRunning != got.IsRunningOrChildRunning {
					fail("HomeTree(%s) differs at %s", span.Name, want.Span.Name)
					break
				}
				want, got = want.Parent, got.Parent
				if (want == nil) != (got == nil) {
					fail("HomeTree(%s) has a different depth", span.Name)
					break
				}
			}
		}
	}
	if failures > 0 {
		t.Logf("%d failures over %d views", failures, runs)
	}
}
