package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// ---- labels ----------------------------------------------------------------

// decodeArgv recovers an exec op's user command from its interned meta
// string, the JSON array the recorder writes; nil when there is none or it
// isn't one.
func decodeArgv(s string) []string {
	if s == "" || s[0] != '[' {
		return nil
	}
	var argv []string
	if err := json.Unmarshal([]byte(s), &argv); err != nil {
		return nil
	}
	return argv
}

// LabelBy labels each op whose command matches re with re's first submatch
// (or the whole match), the command being its argv joined by spaces. A label
// also applies to each ancestor whose labelled descendants all carry that one
// label, so a package's request, lazy evaluation and exec ops share the label
// its compile command names, while an op above several packages has none.
// It returns how many commands matched.
func (g *Graph) LabelBy(re *regexp.Regexp) int {
	mixed := map[*Op]bool{}
	matched := 0
	for _, op := range g.Ops {
		if len(op.Argv) == 0 {
			continue
		}
		m := re.FindStringSubmatch(strings.Join(op.Argv, " "))
		if m == nil {
			continue
		}
		label := m[0]
		if len(m) > 1 {
			label = m[1]
		}
		if label == "" {
			continue
		}
		matched++
		for p := op; p != nil && !mixed[p]; p = p.Parent {
			if p.Label == "" || p.Label == label {
				p.Label = label
				continue
			}
			// Two labels meet here: this op and everything above it is mixed.
			for q := p; q != nil && !mixed[q]; q = q.Parent {
				q.Label = ""
				mixed[q] = true
			}
			break
		}
	}
	return matched
}

// groupKey is what an op aggregates under: its kind and class, or with
// by "label" its label ("" when it has none).
func groupKey(op *Op, by string) string {
	if by == "label" {
		return op.Label
	}
	return op.Key()
}

// aggregateBy is aggregate keyed by groupKey; with by "label" unlabelled ops
// are left out.
func aggregateBy(ops []*Op, by string) []*classAgg {
	if by != "label" {
		return aggregate(ops)
	}
	m := make(map[string]*classAgg)
	var list []*classAgg
	for _, op := range ops {
		k := op.Label
		if k == "" {
			continue
		}
		a := m[k]
		if a == nil {
			a = &classAgg{Key: k, Outcomes: map[string]int{}, execIdents: map[string]int{}}
			m[k] = a
			list = append(list, a)
		}
		a.add(op)
	}
	return list
}

// ---- ancestors -------------------------------------------------------------

// ancestorsView groups the matching ops by their own class and the classes
// of up to opts.Levels ancestors, so a class's ops are split by where they
// are called from.
func ancestorsView(o *out, g *Graph, opts Options) {
	levels := opts.Levels
	if levels <= 0 {
		levels = 3
	}
	type agg struct {
		chain    string
		count    int
		dur, max int64
	}
	by := map[string]*agg{}
	var list []*agg
	ops, internal := rankable(g.matching(opts.Filter), opts.Filter)
	for _, op := range ops {
		parts := []string{op.Key()}
		for p, i := op.Parent, 0; p != nil && i < levels; p, i = p.Parent, i+1 {
			parts = append(parts, p.Key())
		}
		chain := strings.Join(parts, " ← ")
		a := by[chain]
		if a == nil {
			a = &agg{chain: chain}
			by[chain] = a
			list = append(list, a)
		}
		a.count++
		a.dur += op.Dur()
		a.max = max(a.max, op.Dur())
	}
	slices.SortFunc(list, func(a, b *agg) int { return cmp.Or(cmpInt64(b.dur, a.dur), cmp.Compare(a.chain, b.chain)) })
	o.printf("ancestors: %d ops (%s) by their class and up to %d ancestor classes; sorted by summed duration", len(ops), opts.Filter, levels)
	internalNote(o, internal)
	o.printf("%8s %10s %10s  %s", "count", "dur_tot", "dur_max", "class ← parent ← …")
	for i, a := range list {
		if i == opts.Top {
			o.printf("… %d more chains (raise `top`)", len(list)-opts.Top)
			break
		}
		o.printf("%8d %10s %10s  %s", a.count, fmtDur(a.dur), fmtDur(a.max), a.chain)
	}
}

// ---- occupancy -------------------------------------------------------------

// occupancy reports, per class (or per label with -by label), how many of
// the matching ops were in progress over time: the mean over the whole
// dump, the peak, and the mean in each time bucket. A mean is the integral
// of the in-progress count over the bucket divided by its width, so long
// ops count for as long as they run, not once when they start.
func occupancy(o *out, g *Graph, opts Options) {
	buckets := opts.Buckets
	if buckets <= 0 {
		buckets = 10
	}
	span := g.End - g.Start
	if span <= 0 {
		o.printf("occupancy: the dump spans no time")
		return
	}
	width := max(int64(math.Ceil(float64(span)/float64(buckets))), 1)
	type grp struct {
		key   string
		ops   []*Op
		area  []float64
		total float64
		peak  int
	}
	by := map[string]*grp{}
	var list []*grp
	ops, internal := rankable(g.matching(opts.Filter), opts.Filter)
	for _, op := range ops {
		k := groupKey(op, opts.By)
		if k == "" {
			continue
		}
		gr := by[k]
		if gr == nil {
			gr = &grp{key: k, area: make([]float64, buckets)}
			by[k] = gr
			list = append(list, gr)
		}
		gr.ops = append(gr.ops, op)
		s, e := op.Start-g.Start, op.End-g.Start
		gr.total += float64(e - s)
		for b := max(s/width, 0); b < int64(buckets) && b*width < e; b++ {
			lo, hi := b*width, (b+1)*width
			if ov := min(e, hi) - max(s, lo); ov > 0 {
				gr.area[b] += float64(ov)
			}
		}
	}
	for _, gr := range list {
		type ev struct {
			t int64
			d int
		}
		evs := make([]ev, 0, 2*len(gr.ops))
		for _, op := range gr.ops {
			evs = append(evs, ev{op.Start, 1}, ev{op.End, -1})
		}
		// Ends before starts at the same instant, so touching ops don't overlap.
		slices.SortFunc(evs, func(a, b ev) int { return cmp.Or(cmpInt64(a.t, b.t), cmp.Compare(a.d, b.d)) })
		cur := 0
		for _, e := range evs {
			cur += e.d
			gr.peak = max(gr.peak, cur)
		}
	}
	slices.SortFunc(list, func(a, b *grp) int {
		return cmp.Or(cmp.Compare(b.total, a.total), cmp.Compare(a.key, b.key))
	})
	what := "class"
	if opts.By == "label" {
		what = "label"
	}
	o.printf("occupancy: %d ops (%s) by %s over %s; ops in progress, mean per %s bucket:",
		len(ops), opts.Filter, what, fmtDur(span), fmtDur(width))
	internalNote(o, internal)
	hdr := fmt.Sprintf("%8s %6s %6s |", "ops", "mean", "peak")
	for i := range buckets {
		hdr += fmt.Sprintf(" %6s", "+"+fmtSecs(int64(i)*width))
	}
	o.printf("%s  %s", hdr, what)
	for i, gr := range list {
		if i == opts.Top {
			o.printf("… %d more (raise `top`)", len(list)-opts.Top)
			break
		}
		row := fmt.Sprintf("%8d %6.1f %6d |", len(gr.ops), gr.total/float64(span), gr.peak)
		for b, a := range gr.area {
			w := min(width, span-int64(b)*width)
			row += fmt.Sprintf(" %6.1f", a/float64(max(w, 1)))
		}
		o.printf("%s  %s", row, gr.key)
	}
}

// ---- window ----------------------------------------------------------------

// window reports three different quantities for one op over a time window
// (the op's whole interval, or the -from/-to part of it), each summed per
// class, because "what took the time in this window" has more than one
// honest answer when work overlaps:
//
//   - on the critical chain: what the op's end actually waited on at each
//     instant, from the critpath walker; sums to the window's length;
//   - occupied wall time: for each class, the union of its ops' intervals in
//     the window, so concurrent ops of one class count once;
//   - self time: each op's own time (its interval less its children and
//     waits) in the window, summed across concurrent ops, so it can exceed
//     the window's length.
func window(o *out, g *Graph, opts Options) error {
	if opts.Op == 0 {
		return fmt.Errorf("window needs -op")
	}
	root := g.ByID[opts.Op]
	if root == nil {
		return fmt.Errorf("op %d not found in the dump", opts.Op)
	}
	from, to := root.Start, root.End
	if opts.FromMS >= 0 {
		from = max(from, g.Start+int64(opts.FromMS*1e6))
	}
	if opts.ToMS >= 0 {
		to = min(to, g.Start+int64(opts.ToMS*1e6))
	}
	if to <= from {
		return fmt.Errorf("window [%s, %s] is empty for op %d (%s..%s)", g.fmtAt(from), g.fmtAt(to), root.ID, g.fmtAt(root.Start), g.fmtAt(root.End))
	}
	o.printf("window of op %d (%s) from %s to %s (%s)", root.ID, root.Key(), g.fmtAt(from), g.fmtAt(to), fmtDur(to-from))

	w := &critWalker{onPath: map[*Op]bool{}}
	crit := map[string]*critAgg{}
	w.walk(newNode(root), nil, from, to).attribute(crit)
	critList := make([]*critAgg, 0, len(crit))
	for _, a := range crit {
		if a.own > 0 {
			critList = append(critList, a)
		}
	}
	slices.SortFunc(critList, func(a, b *critAgg) int { return cmp.Or(cmpInt64(b.own, a.own), cmp.Compare(a.key, b.key)) })
	o.printf("")
	o.printf("on the critical chain (sums to the window):")
	for i, a := range critList {
		if i == opts.Top {
			o.printf("  … %d more classes (raise `top`)", len(critList)-opts.Top)
			break
		}
		o.printf("  %10s %5.1f%%  %s", fmtDur(a.own), 100*float64(a.own)/float64(to-from), a.key)
	}

	type acc struct {
		key       string
		intervals [][2]int64
		self      int64
	}
	by := map[string]*acc{}
	var list []*acc
	var visit func(op *Op)
	visit = func(op *Op) {
		s, e := max(op.Start, from), min(op.End, to)
		if e > s {
			a := by[op.Key()]
			if a == nil {
				a = &acc{key: op.Key()}
				by[op.Key()] = a
				list = append(list, a)
			}
			a.intervals = append(a.intervals, [2]int64{s, e})
			a.self += uncovered(s, e, op.Children, op.Waits)
		}
		for _, c := range op.Children {
			if c.End > from && c.Start < to {
				visit(c)
			}
		}
	}
	visit(root)
	occupied := func(a *acc) int64 { return unionLen(a.intervals) }
	slices.SortFunc(list, func(a, b *acc) int { return cmp.Or(cmpInt64(occupied(b), occupied(a)), cmp.Compare(a.key, b.key)) })
	o.printf("")
	o.printf("occupied wall time per class (union of its ops in the window), and self time (summed across concurrent ops):")
	o.printf("  %10s %10s %6s  %s", "occupied", "self", "ops", "kind class")
	for i, a := range list {
		if i == opts.Top {
			o.printf("  … %d more classes (raise `top`)", len(list)-opts.Top)
			break
		}
		o.printf("  %10s %10s %6d  %s", fmtDur(occupied(a)), fmtDur(a.self), len(a.intervals), a.key)
	}
	return nil
}

// unionLen is the total length covered by the intervals.
func unionLen(iv [][2]int64) int64 {
	if len(iv) == 0 {
		return 0
	}
	s := slices.Clone(iv)
	slices.SortFunc(s, func(a, b [2]int64) int { return cmpInt64(a[0], b[0]) })
	var total int64
	cs, ce := s[0][0], s[0][1]
	for _, x := range s[1:] {
		if x[0] > ce {
			total += ce - cs
			cs, ce = x[0], x[1]
		} else {
			ce = max(ce, x[1])
		}
	}
	return total + ce - cs
}
