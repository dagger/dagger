package main

import (
	"cmp"
	"fmt"
	"slices"
)

// ---- critical path ---------------------------------------------------------
//
// The critical path of an op is the chain of work its end actually waited
// for. Walking back from the op's end, at each instant the op was blocked on
// the child or wait that ran latest; before that blocker started, repeat.
// Time no blocker covers is the op's own. Each blocker is walked the same
// way over the window it was on the path, so a wait into a shared execution
// (a singleflight join, a lazy result another caller forced, an exec by
// ident) follows that execution, not the waiter's own subtree. Summed per
// class, on-path own time adds up to the root's duration: unlike summed self
// time, concurrent work that never held anything up gets nothing.

// pathNode is one segment of a critical path: an op (possibly a call folded
// with its call_exec), or a wait on an unrecorded resource.
type pathNode struct {
	n        node  // n.op is nil for a resource wait
	via      *Wait // the wait this segment was reached through, if any
	from, to int64
	// own is the time in [from, to) spent in the op itself, not in blockers.
	own   int64
	kids  []*pathNode // blockers, in time order
	cycle bool
}

func (p *pathNode) dur() int64 { return p.to - p.from }

// key is the class the segment's own time is attributed to.
func (p *pathNode) key() string {
	if p.n.op == nil {
		return "wait " + p.via.Reason + " " + cmp.Or(p.via.Ident, "(unrecorded)")
	}
	return p.n.op.Key()
}

type blocker struct {
	start, end int64
	child      *Op
	wait       *Wait
}

type critWalker struct {
	onPath map[*Op]bool
}

// walk computes the critical path of n over [from, to).
func (c *critWalker) walk(n node, via *Wait, from, to int64) *pathNode {
	p := &pathNode{n: n, via: via, from: from, to: to}
	if n.op == nil {
		p.own = to - from
		return p
	}
	// A wait can lead back into an op already on this path (e.g. an exec
	// waiting on the call that spawned it); stop there.
	if c.onPath[n.op] {
		p.own, p.cycle = to-from, true
		return p
	}
	c.onPath[n.op] = true
	defer delete(c.onPath, n.op)

	body := n.body()
	blockers := make([]blocker, 0, len(body.Children)+len(body.Waits))
	for _, ch := range body.Children {
		blockers = append(blockers, blocker{start: ch.Start, end: ch.End, child: ch})
	}
	for _, w := range body.Waits {
		// A wait on the op's own child is covered by the child.
		if w.Target != nil && w.Target.Parent == body {
			continue
		}
		blockers = append(blockers, blocker{start: w.Start, end: w.End, wait: w})
	}
	// Latest end first: walking back from t, the first blocker that started
	// before t is the one that ran latest. Blockers passed over started at
	// or after t, and t only decreases, so the scan never revisits them.
	slices.SortFunc(blockers, func(a, b blocker) int {
		return cmp.Or(cmpInt64(b.end, a.end), cmpInt64(b.start, a.start))
	})
	t := to
	i := 0
	for t > from {
		for i < len(blockers) && blockers[i].start >= t {
			i++
		}
		if i == len(blockers) || blockers[i].end <= from {
			break
		}
		b := blockers[i]
		i++
		e := min(b.end, t)
		p.own += t - e
		s := max(b.start, from)
		switch {
		case b.child != nil:
			p.kids = append(p.kids, c.walk(newNode(b.child), nil, s, e))
		case b.wait.Target != nil:
			p.kids = append(p.kids, c.walk(newNode(b.wait.Target), b.wait, s, e))
		default:
			p.kids = append(p.kids, c.walk(node{}, b.wait, s, e))
		}
		t = s
	}
	if t > from {
		p.own += t - from
	}
	slices.Reverse(p.kids)
	return p
}

// attribute adds p's on-path own time, per class, to by.
func (p *pathNode) attribute(by map[string]*critAgg) {
	a := by[p.key()]
	if a == nil {
		a = &critAgg{key: p.key()}
		by[p.key()] = a
	}
	a.own += p.own
	a.segments++
	for _, k := range p.kids {
		k.attribute(by)
	}
}

type critAgg struct {
	key      string
	own      int64
	segments int
}

// critRoots are the ops the critpath view walks: opts.Op, or else every op
// matching the filter that has no matching ancestor (so nested matches are
// not counted twice).
func critRoots(g *Graph, opts Options) ([]*Op, error) {
	if opts.Op != 0 {
		root := g.ByID[opts.Op]
		if root == nil {
			return nil, fmt.Errorf("op %d not found in the dump", opts.Op)
		}
		return []*Op{root}, nil
	}
	matched := g.matching(opts.Filter)
	set := make(map[*Op]bool, len(matched))
	for _, op := range matched {
		set[op] = true
	}
	var roots []*Op
	for _, op := range matched {
		top := true
		for p := op.Parent; p != nil; p = p.Parent {
			if set[p] {
				top = false
				break
			}
		}
		if top {
			roots = append(roots, op)
		}
	}
	return roots, nil
}

func critpath(o *out, g *Graph, opts Options) error {
	roots, err := critRoots(g, opts)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		o.printf("critpath: no ops match %s", opts.Filter)
		return nil
	}
	w := &critWalker{onPath: map[*Op]bool{}}
	by := map[string]*critAgg{}
	var total int64
	var shown *pathNode
	for _, r := range roots {
		p := w.walk(newNode(r), nil, r.Start, r.End)
		p.attribute(by)
		total += p.dur()
		if shown == nil || p.dur() > shown.dur() {
			shown = p
		}
	}
	aggs := make([]*critAgg, 0, len(by))
	for _, a := range by {
		aggs = append(aggs, a)
	}
	slices.SortFunc(aggs, func(a, b *critAgg) int { return cmp.Or(cmpInt64(b.own, a.own), cmp.Compare(a.key, b.key)) })

	if opts.Op != 0 {
		o.printf("critpath of op %d: %s", roots[0].ID, fmtDur(total))
	} else {
		o.printf("critpath of %d ops matching %s (outermost matches only): %s summed", len(roots), opts.Filter, fmtDur(total))
	}
	o.printf("on-path time by class (sums to the roots' duration; concurrent work off the path gets nothing):")
	o.printf("  %10s %6s %8s  %s", "on_path", "%", "segments", "kind class")
	for i, a := range aggs {
		if i == opts.Top {
			o.printf("  … %d more classes (raise `top`)", len(aggs)-opts.Top)
			break
		}
		o.printf("  %10s %5.1f%% %8d  %s", fmtDur(a.own), pct(a.own, total), a.segments, a.key)
	}

	depth := opts.Depth
	if depth <= 0 {
		depth = 6
	}
	o.printf("")
	o.printf("path of the longest (offsets from its start; `dur` on the path, `own` not in any blocker):")
	cp := &critPrinter{o: o, origin: shown.from, min: shown.dur() / 100}
	cp.print(shown, "", depth)
	if cp.hidden > 0 {
		o.printf("(%d segments not shown: runs of them totaling under 1%% of the path, %s)", cp.hidden, fmtDur(cp.hiddenDur))
	}
	o.printf("(runs of sub-1%% segments totaling 1%%+ show their largest members, the rest as \"… N shorter\"; ▸ N: N segments not expanded, raise `depth` or re-root with `op`; (exec N): a call folded with its call_exec op N)")
	return nil
}

func pct(part, whole int64) float64 {
	if whole == 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

// critPicks bounds how many members of a run of short segments print on
// their own lines.
const critPicks = 5

type critPrinter struct {
	o      *out
	origin int64
	// min is the segment (and run) duration worth a line: 1% of the path.
	min       int64
	hidden    int
	hiddenDur int64
}

func (cp *critPrinter) label(p *pathNode) string {
	var s string
	if p.n.op == nil {
		s = fmt.Sprintf("⏳ %s → %s", p.via.Reason, cmp.Or(p.via.Ident, "(unrecorded)"))
	} else {
		if p.via != nil {
			s = fmt.Sprintf("⏳ %s → ", p.via.Reason)
		}
		op := p.n.op
		s += fmt.Sprintf("%d %s", op.ID, op.Key())
		if op.Outcome != "" {
			s += " [" + op.Outcome + "]"
		}
		if p.n.exec != nil {
			s += fmt.Sprintf(" (exec %d)", p.n.exec.ID)
		}
	}
	if p.cycle {
		s += " (cycle: already on the path)"
	}
	return s
}

func (cp *critPrinter) print(p *pathNode, indent string, depth int) {
	line := fmt.Sprintf("%s%s dur %s own %s  %s", indent, fmtRel(p.from-cp.origin), fmtDur(p.dur()), fmtDur(p.own), cp.label(p))
	if depth <= 0 {
		if len(p.kids) > 0 {
			line += fmt.Sprintf(" ▸ %d", len(p.kids))
		}
		cp.o.printf("%s", line)
		return
	}
	cp.o.printf("%s", line)
	sub := indent + "  "
	var small []*pathNode
	for _, k := range p.kids {
		if k.dur() < cp.min {
			small = append(small, k)
			continue
		}
		cp.run(small, sub, depth-1)
		small = small[:0]
		cp.print(k, sub, depth-1)
	}
	cp.run(small, sub, depth-1)
}

// run prints a run of consecutive short segments. A run totaling under the
// threshold is hidden (and counted); otherwise its largest members print on
// their own until the rest totals under the threshold (at most critPicks),
// and the rest fold into one line.
func (cp *critPrinter) run(small []*pathNode, indent string, depth int) {
	if len(small) == 0 {
		return
	}
	var sum int64
	for _, k := range small {
		sum += k.dur()
	}
	if sum < cp.min {
		cp.hidden += len(small)
		cp.hiddenDur += sum
		return
	}
	bySize := slices.Clone(small)
	slices.SortStableFunc(bySize, func(a, b *pathNode) int { return cmpInt64(b.dur(), a.dur()) })
	picked := map[*pathNode]bool{}
	rest := sum
	for _, k := range bySize {
		if rest < cp.min || len(picked) == critPicks {
			break
		}
		picked[k] = true
		rest -= k.dur()
	}
	var folded []*pathNode
	for _, k := range small {
		if !picked[k] {
			folded = append(folded, k)
		}
	}
	// In time order; the folded line sits where its first member starts.
	for _, k := range small {
		switch {
		case picked[k] || len(folded) == 1:
			cp.print(k, indent, depth)
		case k == folded[0]:
			classes := map[string]int{}
			for _, f := range folded {
				classes[f.key()]++
			}
			cp.o.printf("%s%s..%s … %d shorter segments, %s: %s", indent, fmtRel(folded[0].from-cp.origin),
				fmtRel(folded[len(folded)-1].to-cp.origin), len(folded), fmtDur(rest), counts(classes, 3))
		}
	}
}
