package dagui

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

func walkTestSpanID(n int) SpanID {
	var id trace.SpanID
	binary.BigEndian.PutUint64(id[:], uint64(n))
	return SpanID{SpanID: id}
}

// randomTraceDB builds a small random trace: nested spans, some running,
// internal, passthrough, revealed, or tool calls; spans beneath parents that
// were never received; cause links; and chains of calls.
//
// Parents and causes always come earlier in the list of spans than the spans
// they hold, as they do in a real trace, where a link points at a span that
// already exists. Start times are shuffled a little, though, so causes don't
// always start first.
func randomTraceDB(rng *rand.Rand) (*DB, []SpanID) {
	db := NewDB()
	traceID := TraceID{TraceID: trace.TraceID{1}}
	start := time.Unix(100, 0)
	n := 10 + rng.Intn(40)
	ids := make([]SpanID, n)
	for i := range ids {
		ids[i] = walkTestSpanID(i + 1)
	}
	// stubs are parents that are never received
	stubs := []SpanID{walkTestSpanID(1001), walkTestSpanID(1002)}
	snaps := make([]SpanSnapshot, n)
	calls := map[string]*callpbv1.Call{}
	var digests []string
	// the last call digest beneath each parent, for chaining
	lastCallUnder := map[SpanID]string{}
	for i := range n {
		snap := SpanSnapshot{
			ID:        ids[i],
			TraceID:   traceID,
			Name:      fmt.Sprintf("s%d", i),
			StartTime: start.Add(time.Duration(i*4+rng.Intn(6)) * time.Second),
		}
		if i == 0 {
			// the root runs throughout, so nothing gets canceled; as a
			// passthrough, a view zoomed to it lists its revealed spans
			snap.Passthrough = rng.Intn(4) == 0
			snaps[i] = snap
			continue
		}
		switch r := rng.Intn(20); {
		case r == 0:
			snap.ParentID = stubs[rng.Intn(len(stubs))]
		case r < 3:
			snap.ParentID = ids[0]
		default:
			snap.ParentID = ids[rng.Intn(i)]
		}
		if rng.Intn(6) > 0 {
			snap.EndTime = snap.StartTime.Add(time.Duration(1+rng.Intn(5)) * time.Second)
		}
		switch rng.Intn(16) {
		case 0, 1:
			snap.Internal = true
		case 2, 3:
			snap.Passthrough = true
		case 4, 5:
			snap.Reveal = true
		case 6:
			snap.LLMTool = "tool"
		}
		for range 2 {
			if rng.Intn(5) == 0 {
				snap.Links = append(snap.Links, causeLink(ids[rng.Intn(i)]))
			}
		}
		if rng.Intn(20) == 0 {
			// a cause that was never received
			snap.Links = append(snap.Links, causeLink(walkTestSpanID(2001)))
		}
		if rng.Intn(2) == 0 {
			call := &callpbv1.Call{
				Digest: fmt.Sprintf("d%d", i),
				Field:  "f",
				Type:   &callpbv1.Type{NamedType: "T"},
			}
			if prev := lastCallUnder[snap.ParentID]; prev != "" && rng.Intn(3) > 0 {
				call.ReceiverDigest = prev
			} else if len(digests) > 0 && rng.Intn(2) == 0 {
				call.ReceiverDigest = digests[rng.Intn(len(digests))]
			}
			calls[call.Digest] = call
			digests = append(digests, call.Digest)
			snap.CallDigest = call.Digest
			lastCallUnder[snap.ParentID] = call.Digest
		}
		snaps[i] = snap
	}
	db.ImportSnapshots(snaps)
	for _, digest := range digests {
		db.addCall(digest, calls[digest])
	}
	db.SetPrimarySpan(ids[0])
	return db, ids
}

func randomViewOpts(rng *rand.Rand, ids []SpanID) FrontendOpts {
	opts := FrontendOpts{Verbosity: rng.Intn(5)}
	switch rng.Intn(4) {
	case 0:
		// unzoomed: every span is a candidate for the top level
	case 1, 2:
		opts.ZoomedSpan = ids[0]
	case 3:
		opts.ZoomedSpan = ids[rng.Intn(len(ids))]
	}
	if opts.ZoomedSpan.IsValid() && rng.Intn(5) == 0 {
		opts.StrictSubtree = true
	}
	if rng.Intn(5) == 0 {
		opts.RevealNoisySpans = true
	}
	if rng.Intn(6) == 0 {
		// a root filter listing a few spans, nested or not
		var roots []SpanID
		for _, id := range ids {
			if rng.Intn(4) == 0 {
				roots = append(roots, id)
			}
		}
		opts.RootFilter = func(db *DB, _ *Span) []*Span {
			spans := make([]*Span, 0, len(roots))
			for _, id := range roots {
				spans = append(spans, db.Spans.Map[id])
			}
			return spans
		}
	}
	return opts
}

func describeTraceForWalk(db *DB) string {
	var out []string
	for _, span := range db.Spans.Order {
		line := span.Name
		if !span.Received {
			line = span.ID.String()[:4] + " (stub)"
		}
		if span.ParentSpan != nil {
			line += " parent=" + span.ParentSpan.Name
		}
		for _, cause := range span.causesViaLinks.Spans() {
			line += " cause=" + cause.Name
		}
		if call := span.Call(); call != nil {
			line += " call=" + call.Digest
			if call.ReceiverDigest != "" {
				line += " base=" + call.ReceiverDigest
			}
		}
		for _, flag := range []struct {
			name string
			set  bool
		}{
			{"running", span.IsRunning()},
			{"internal", span.Internal},
			{"passthrough", span.Passthrough},
			{"reveal", span.Reveal},
			{"tool", span.LLMTool != ""},
		} {
			if flag.set {
				line += " " + flag.name
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// describeTree describes a tree and everything built beneath it.
func describeTree(tree *TraceTree, depth int, out *strings.Builder) {
	fmt.Fprintf(out, "%s%s chained=%v running=%v revealed=%v\n",
		strings.Repeat("  ", depth), tree.Span.Name,
		tree.Chained, tree.IsRunningOrChildRunning, tree.RevealedChildren)
	for _, child := range tree.Children {
		describeTree(child, depth+1, out)
	}
	if tree.Revealed != nil {
		fmt.Fprintf(out, "%s(revealed)\n", strings.Repeat("  ", depth+1))
		for _, child := range tree.Revealed {
			describeTree(child, depth+1, out)
		}
	}
}

// describeSiblings describes a sibling list without what's beneath it.
func describeSiblings(trees []*TraceTree) string {
	var out []string
	for _, tree := range trees {
		out = append(out, fmt.Sprintf("%s chained=%v", tree.Span.Name, tree.Chained))
	}
	return strings.Join(out, ", ")
}

// pathStep is a step down a view: a tree's span, and whether it's among its
// parent's revealed copies rather than its children.
type pathStep struct {
	span     *Span
	revealed bool
}

// walkPath walks the view building only the trees along path, and
// everything beneath its end. It returns the sibling list at each step, and
// the tree at the end.
func walkPath(t *testing.T, db *DB, opts FrontendOpts, path []pathStep) (lists [][]*TraceTree, target *TraceTree) {
	t.Helper()
	w, _, ok := db.newSpanWalker(opts)
	if !ok {
		t.Fatal("zoomed span not found")
	}
	w.descend = func(tree *TraceTree) bool {
		if depth := tree.Depth(); depth < len(path) {
			// a span both among its parent's children and its revealed
			// copies is built in both on the path; that's just extra work
			return tree.Span == path[depth].span
		}
		// beneath the end of the path
		return true
	}
	trees := w.walk()
	for i, step := range path {
		if step.revealed && target != nil {
			trees = target.Revealed
		}
		lists = append(lists, trees)
		target = nil
		for _, tree := range trees {
			if tree.Span == step.span {
				target = tree
				break
			}
		}
		if target == nil {
			return lists, nil
		}
		if i+1 < len(path) {
			trees = target.Children
		}
	}
	return lists, target
}

// TestWalkIsLocal holds RowsView's walk to locality: the trees built beneath
// a tree, and the sibling lists along its path from the top, are the same
// whether the walk builds the whole view or only that path. A walk that
// builds only the trees a view shows relies on this to match the full walk.
//
// It also checks that the walk places every span it reaches exactly once,
// not counting revealed copies.
func TestWalkIsLocal(t *testing.T) {
	const runs = 3000
	failures := 0
	for seed := range runs {
		rng := rand.New(rand.NewSource(int64(seed)))
		db, ids := randomTraceDB(rng)
		opts := randomViewOpts(rng, ids)
		full := db.rowsView(opts, false)

		fail := func(format string, args ...any) {
			t.Helper()
			failures++
			if failures <= 3 {
				t.Errorf("seed %d: %s\ntrace:\n%s\nopts: zoom=%v verbosity=%d strict=%v noisy=%v filter=%v",
					seed, fmt.Sprintf(format, args...), describeTraceForWalk(db),
					opts.ZoomedSpan, opts.Verbosity, opts.StrictSubtree, opts.RevealNoisySpans, opts.RootFilter != nil)
			}
		}

		// every span the walk reaches is placed once, and no other span is
		placed := map[*Span]int{}
		var count func([]*TraceTree)
		count = func(trees []*TraceTree) {
			for _, tree := range trees {
				placed[tree.Span]++
				count(tree.Children)
			}
		}
		count(full.Body)
		w, _, _ := db.newSpanWalker(opts)
		for _, span := range db.Spans.Order {
			want := 0
			if span != w.container && w.mode(span) == modeBuild && w.reachable(span, true) {
				want = 1
			}
			if placed[span] != want {
				fail("%s placed %d times, want %d", span.Name, placed[span], want)
			}
		}

		var check func(trees []*TraceTree, revealed bool, path []pathStep, lists [][]*TraceTree)
		check = func(trees []*TraceTree, revealed bool, path []pathStep, lists [][]*TraceTree) {
			lists = append(lists[:len(lists):len(lists)], trees)
			for _, tree := range trees {
				path := append(path[:len(path):len(path)], pathStep{tree.Span, revealed})
				gotLists, got := walkPath(t, db, opts, path)
				if got == nil {
					fail("walking only %s's path doesn't build it", tree.Span.Name)
					continue
				}
				for i, list := range gotLists {
					want := describeSiblings(lists[i])
					if got := describeSiblings(list); got != want {
						fail("siblings at depth %d on %s's path differ:\nfull: %s\npath: %s", i, tree.Span.Name, want, got)
					}
				}
				var want, have strings.Builder
				describeTree(tree, 0, &want)
				describeTree(got, 0, &have)
				if want.String() != have.String() {
					fail("trees beneath %s differ:\nfull:\n%s\npath:\n%s", tree.Span.Name, want.String(), have.String())
				}
				check(tree.Children, false, path, lists)
				check(tree.Revealed, true, path, lists)
			}
		}
		check(full.Body, false, nil, nil)
	}
	if failures > 0 {
		t.Logf("%d failures over %d views", failures, runs)
	}
}
