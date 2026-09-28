package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
)

// Options select and shape a report.
type Options struct {
	View   string
	Filter Filter
	// Op is the root op for the tree and children views (0 = the slowest
	// matching op).
	Op uint64
	// Depth is how many levels below the root the tree view expands.
	Depth int
	// Top bounds rows per ranking (classes, clients, shapes, shape entries).
	Top int
	// Sort orders the classes view (self, count or dur; default self) and
	// the children view (start, dur or self; default start).
	Sort string
	// Buckets is the number of time buckets in the clients view.
	Buckets int
	// Collapse is the sibling-group size from which the tree view
	// aggregates same-class siblings into one line.
	Collapse int
	// Limit bounds output lines (0 = unlimited). Not applied to events.
	Limit int
	// Against is the baseline dump for the compare view; Name and
	// AgainstName label the two sides.
	Against           *Graph
	Name, AgainstName string
}

// Views lists the supported report views.
var Views = []string{"summary", "classes", "breakdown", "clients", "tree", "children", "compare", "events"}

// Report writes the selected view of g to w.
func Report(w io.Writer, g *Graph, opts Options) error {
	if opts.Top <= 0 {
		opts.Top = 30
	}
	if opts.Collapse <= 1 {
		opts.Collapse = 4
	}
	if opts.View == "events" {
		return events(w, g, opts.Filter)
	}
	o := &out{w: w, limit: opts.Limit}
	var err error
	switch opts.View {
	case "summary":
		summary(o, g, opts)
	case "classes":
		classes(o, g, opts)
	case "breakdown":
		err = breakdown(o, g, opts)
	case "clients":
		clients(o, g, opts)
	case "tree":
		err = tree(o, g, opts)
	case "children":
		err = childrenView(o, g, opts)
	case "compare":
		err = compare(o, g, opts)
	default:
		err = fmt.Errorf("unknown view %q (want one of %s)", opts.View, strings.Join(Views, ", "))
	}
	if err != nil {
		return err
	}
	o.finish()
	return nil
}

// out writes lines, dropping (and counting) those past the limit.
type out struct {
	w       io.Writer
	limit   int
	n       int
	dropped int
}

func (o *out) printf(format string, args ...any) {
	s := strings.TrimSuffix(fmt.Sprintf(format, args...), "\n")
	for _, line := range strings.Split(s, "\n") {
		o.n++
		if o.limit > 0 && o.n > o.limit {
			o.dropped++
			continue
		}
		fmt.Fprintln(o.w, line)
	}
}

func (o *out) finish() {
	if o.dropped > 0 {
		fmt.Fprintf(o.w, "… (%d more lines truncated; raise `limit`, or narrow with class/client/kind/top/depth) …\n", o.dropped)
	}
}

// ---- formatting ------------------------------------------------------------

func fmtDur(ns int64) string {
	neg := ""
	if ns < 0 {
		neg, ns = "-", -ns
	}
	switch {
	case ns < 1_000:
		return fmt.Sprintf("%s%dns", neg, ns)
	case ns < 1_000_000:
		return fmt.Sprintf("%s%.1fµs", neg, float64(ns)/1e3)
	case ns < 1_000_000_000:
		return fmt.Sprintf("%s%.2fms", neg, float64(ns)/1e6)
	default:
		return fmt.Sprintf("%s%.2fs", neg, float64(ns)/1e9)
	}
}

// fmtAt formats a recorder timestamp relative to the first recorded op.
func (g *Graph) fmtAt(ns int64) string {
	return "+" + fmtDur(ns-g.Start)
}

func ms(ns int64) float64 {
	return math.Round(float64(ns)/1e3) / 1e3
}

func clientName(c string) string {
	if c == "" {
		return "(none)"
	}
	return c
}

// quantile is the nearest-rank q-quantile of xs (which it sorts in place).
func quantile(xs []int64, q float64) int64 {
	if len(xs) == 0 {
		return 0
	}
	slices.Sort(xs)
	idx := int(math.Ceil(q*float64(len(xs)))) - 1
	return xs[min(max(idx, 0), len(xs)-1)]
}

// counts renders a string->count map as "a:3 b:1", largest first.
func counts(m map[string]int, top int) string {
	type kv struct {
		k string
		n int
	}
	kvs := make([]kv, 0, len(m))
	for k, n := range m {
		if k == "" {
			k = "-"
		}
		kvs = append(kvs, kv{k, n})
	}
	slices.SortFunc(kvs, func(a, b kv) int {
		return cmp.Or(cmp.Compare(b.n, a.n), cmp.Compare(a.k, b.k))
	})
	parts := make([]string, 0, len(kvs))
	for i, e := range kvs {
		if top > 0 && i == top {
			parts = append(parts, fmt.Sprintf("+%d more", len(kvs)-top))
			break
		}
		parts = append(parts, fmt.Sprintf("%s:%d", e.k, e.n))
	}
	return strings.Join(parts, " ")
}

// ---- class aggregation -----------------------------------------------------

type classAgg struct {
	Key, Kind, Class string
	Count            int
	Selfs, Durs      []int64
	SelfTotal        int64
	DurTotal         int64
	Outcomes         map[string]int
	execIdents       map[string]int
}

func (a *classAgg) add(op *Op) {
	a.Count++
	a.Selfs = append(a.Selfs, op.Self)
	a.Durs = append(a.Durs, op.Dur())
	a.SelfTotal += op.Self
	a.DurTotal += op.Dur()
	a.Outcomes[op.Outcome]++
	if op.Ident != "" && (op.Kind == "call_exec" || op.Outcome == "executed") {
		a.execIdents[op.Ident]++
	}
}

// dupExecs counts executions beyond the first per ident.
func (a *classAgg) dupExecs() int {
	n := 0
	for _, c := range a.execIdents {
		n += c - 1
	}
	return n
}

func aggregate(ops []*Op) []*classAgg {
	by := make(map[string]*classAgg)
	var list []*classAgg
	for _, op := range ops {
		a := by[op.Key()]
		if a == nil {
			a = &classAgg{
				Key: op.Key(), Kind: op.Kind, Class: op.Class,
				Outcomes: map[string]int{}, execIdents: map[string]int{},
			}
			by[op.Key()] = a
			list = append(list, a)
		}
		a.add(op)
	}
	return list
}

func sortAggs(aggs []*classAgg, by string) {
	slices.SortFunc(aggs, func(a, b *classAgg) int {
		var c int
		switch by {
		case "count":
			c = cmp.Compare(b.Count, a.Count)
		case "dur":
			c = cmp.Compare(b.DurTotal, a.DurTotal)
		default:
			c = cmp.Compare(b.SelfTotal, a.SelfTotal)
		}
		return cmp.Or(c, cmp.Compare(a.Key, b.Key))
	})
}

func (g *Graph) matching(f Filter) []*Op {
	if f.Empty() {
		return g.Ops
	}
	var ops []*Op
	for _, op := range g.Ops {
		if f.Match(op) {
			ops = append(ops, op)
		}
	}
	return ops
}

// ---- summary ---------------------------------------------------------------

func summary(o *out, g *Graph, opts Options) {
	h := g.Header
	o.printf("wcprof dump: %d ops, %d waits, %d links over %s; dropped events: %d; open ops: %d",
		len(g.Ops), len(g.Waits), len(g.Links), fmtDur(g.End-g.Start), h.DroppedEvents, len(h.OpenOps))
	if h.DroppedEvents > 0 {
		o.printf("WARNING: the recorder hit its event cap and dropped %d events; the graph is incomplete", h.DroppedEvents)
	}
	if len(g.Ops) == 0 {
		o.printf("(no ops recorded — is recording enabled, and did anything run since the last flush?)")
		return
	}
	calls := map[string]int{}
	nCalls := 0
	for _, op := range g.Ops {
		if op.Kind == "call" {
			nCalls++
			calls[op.Outcome]++
		}
	}
	o.printf("calls: %d (%s)", nCalls, counts(calls, 0))
	top := min(opts.Top, 10)

	type cl struct {
		name       string
		ops        int
		self       int64
		first, end int64
	}
	byClient := map[string]*cl{}
	var cls []*cl
	for _, op := range g.Ops {
		c := byClient[op.Client]
		if c == nil {
			c = &cl{name: op.Client, first: op.Start}
			byClient[op.Client] = c
			cls = append(cls, c)
		}
		c.ops++
		c.self += op.Self
		c.end = max(c.end, op.End)
	}
	slices.SortFunc(cls, func(a, b *cl) int { return cmp.Or(cmp.Compare(b.ops, a.ops), cmp.Compare(a.name, b.name)) })
	o.printf("top clients (of %d) by ops:", len(cls))
	o.printf("  %8s %10s  %-19s  %s", "ops", "self_tot", "active", "client")
	for i, c := range cls {
		if i == top {
			break
		}
		o.printf("  %8d %10s  %-19s  %s", c.ops, fmtDur(c.self), g.fmtAt(c.first)+".."+g.fmtAt(c.end), clientName(c.name))
	}

	ranked, internal := rankable(g.matching(opts.Filter), opts.Filter)
	aggs := aggregate(ranked)
	scope := ""
	if !opts.Filter.Empty() {
		scope = ", " + opts.Filter.String()
	}
	o.printf("top classes (of %d%s) by count:", len(aggs), scope)
	sortAggs(aggs, "count")
	o.printf("  %8s %10s  %s", "count", "self_tot", "kind class")
	for i, a := range aggs {
		if i == top {
			break
		}
		o.printf("  %8d %10s  %s", a.Count, fmtDur(a.SelfTotal), a.Key)
	}
	o.printf("top classes by self time:")
	sortAggs(aggs, "self")
	o.printf("  %8s %10s  %s", "count", "self_tot", "kind class")
	for i, a := range aggs {
		if i == top {
			break
		}
		o.printf("  %8d %10s  %s", a.Count, fmtDur(a.SelfTotal), a.Key)
	}
	internalNote(o, internal)
	o.printf("(times are relative to the first recorded op; self = duration minus child ops and waits; self_tot sums it across ops, concurrent ones included, so it can exceed wall time)")
}

// rankable splits internal-kind ops (engine bookkeeping such as
// dagql.publishResult) out of class rankings, unless the filter asks for
// that kind. They run beside the work they account for, so their self time
// summed across concurrent ops would otherwise top every self ranking.
func rankable(ops []*Op, f Filter) (kept, internal []*Op) {
	if f.Kind == "internal" {
		return ops, nil
	}
	kept = make([]*Op, 0, len(ops))
	for _, op := range ops {
		if op.Kind == "internal" {
			internal = append(internal, op)
		} else {
			kept = append(kept, op)
		}
	}
	return kept, internal
}

func internalNote(o *out, internal []*Op) {
	if len(internal) == 0 {
		return
	}
	var self int64
	for _, op := range internal {
		self += op.Self
	}
	o.printf("(left out: %d internal-kind ops, self_tot %s; kind: \"internal\" ranks them)", len(internal), fmtDur(self))
}

// ---- classes ---------------------------------------------------------------

func classes(o *out, g *Graph, opts Options) {
	ops, internal := rankable(g.matching(opts.Filter), opts.Filter)
	aggs := aggregate(ops)
	sortBy := opts.Sort
	if sortBy != "count" && sortBy != "dur" {
		sortBy = "self"
	}
	sortAggs(aggs, sortBy)
	var selfTotal int64
	for _, a := range aggs {
		selfTotal += a.SelfTotal
	}
	o.printf("classes: %d ops in %d classes (%s) over %s, self_tot %s (summed across concurrent ops); sorted by %s",
		len(ops), len(aggs), opts.Filter, fmtDur(g.End-g.Start), fmtDur(selfTotal), sortBy)
	internalNote(o, internal)
	o.printf("%8s %10s %9s %9s %10s %9s %9s %5s  %-24s %s",
		"count", "self_tot", "self_p50", "self_max", "dur_tot", "dur_p50", "dur_max", "dup", "kind class", "outcomes")
	for i, a := range aggs {
		if i == opts.Top {
			o.printf("… %d more classes (raise `top`)", len(aggs)-opts.Top)
			break
		}
		o.printf("%8d %10s %9s %9s %10s %9s %9s %5d  %-24s %s",
			a.Count, fmtDur(a.SelfTotal), fmtDur(quantile(a.Selfs, 0.5)), fmtDur(slices.Max(a.Selfs)),
			fmtDur(a.DurTotal), fmtDur(quantile(a.Durs, 0.5)), fmtDur(slices.Max(a.Durs)),
			a.dupExecs(), a.Key, counts(a.Outcomes, 0))
	}
}

// ---- breakdown -------------------------------------------------------------

type shape struct {
	sig      string
	children []childCount
	ops      []*Op
}

type childCount struct {
	key string
	n   int
}

func childShape(op *Op) (string, []childCount) {
	m := map[string]int{}
	for _, c := range op.Children {
		m[c.Key()]++
	}
	cs := make([]childCount, 0, len(m))
	for k, n := range m {
		cs = append(cs, childCount{k, n})
	}
	slices.SortFunc(cs, func(a, b childCount) int { return cmp.Compare(a.key, b.key) })
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = fmt.Sprintf("%d\x00%s", c.n, c.key)
	}
	slices.SortFunc(cs, func(a, b childCount) int { return cmp.Or(cmp.Compare(b.n, a.n), cmp.Compare(a.key, b.key)) })
	return strings.Join(parts, "\x01"), cs
}

func breakdown(o *out, g *Graph, opts Options) error {
	if opts.Filter.Class == nil {
		return fmt.Errorf("breakdown needs a class regex selecting the parent ops, e.g. -class '^Query\\.node$'")
	}
	ops := g.matching(opts.Filter)
	if len(ops) == 0 {
		o.printf("breakdown: no ops match %s", opts.Filter)
		return nil
	}
	parents := aggregate(ops)
	sortAggs(parents, "count")
	byKey := map[string][]*Op{}
	for _, op := range ops {
		byKey[op.Key()] = append(byKey[op.Key()], op)
	}
	o.printf("breakdown: %d ops in %d classes (%s), children aggregated by class", len(ops), len(parents), opts.Filter)
	for _, pa := range parents {
		group := byKey[pa.Key]
		o.printf("")
		o.printf("== %s: %d ops; dur p50 %s max %s total %s; self p50 %s total %s; outcomes %s",
			pa.Key, pa.Count, fmtDur(quantile(pa.Durs, 0.5)), fmtDur(slices.Max(pa.Durs)), fmtDur(pa.DurTotal),
			fmtDur(quantile(pa.Selfs, 0.5)), fmtDur(pa.SelfTotal), counts(pa.Outcomes, 0))

		var kids []*Op
		for _, op := range group {
			kids = append(kids, op.Children...)
		}
		if len(kids) == 0 {
			o.printf("   (no direct children)")
		} else {
			caggs := aggregate(kids)
			sortAggs(caggs, "count")
			o.printf("   direct children (%d in %d classes):", len(kids), len(caggs))
			o.printf("   %8s %8s %10s %10s %9s  %-24s %s", "count", "per_op", "self_tot", "dur_tot", "dur_p50", "kind class", "outcomes")
			for i, ca := range caggs {
				if i == opts.Top {
					o.printf("   … %d more child classes (raise `top`)", len(caggs)-opts.Top)
					break
				}
				o.printf("   %8d %8.1f %10s %10s %9s  %-24s %s", ca.Count, float64(ca.Count)/float64(len(group)),
					fmtDur(ca.SelfTotal), fmtDur(ca.DurTotal), fmtDur(quantile(ca.Durs, 0.5)), ca.Key, counts(ca.Outcomes, 0))
			}
		}

		shapes := map[string]*shape{}
		var list []*shape
		for _, op := range group {
			sig, cs := childShape(op)
			s := shapes[sig]
			if s == nil {
				s = &shape{sig: sig, children: cs}
				shapes[sig] = s
				list = append(list, s)
			}
			s.ops = append(s.ops, op)
		}
		slices.SortFunc(list, func(a, b *shape) int {
			return cmp.Or(cmp.Compare(len(b.ops), len(a.ops)), cmp.Compare(a.sig, b.sig))
		})
		o.printf("   shapes (ops with an identical child multiset): %d", len(list))
		for i, s := range list {
			if i == opts.Top {
				o.printf("   … %d more shapes (raise `top`)", len(list)-opts.Top)
				break
			}
			var durs, selfs []int64
			total := 0
			for _, op := range s.ops {
				durs = append(durs, op.Dur())
				selfs = append(selfs, op.Self)
			}
			for _, c := range s.children {
				total += c.n
			}
			o.printf("   #%d: %d ops, dur p50 %s, self p50 %s, %d children, e.g. op %d:",
				i+1, len(s.ops), fmtDur(quantile(durs, 0.5)), fmtDur(quantile(selfs, 0.5)), total, s.ops[0].ID)
			if len(s.children) == 0 {
				o.printf("       (no children)")
			}
			for j, c := range s.children {
				if j == opts.Top {
					o.printf("       … %d more child classes", len(s.children)-opts.Top)
					break
				}
				o.printf("       %6d× %s", c.n, c.key)
			}
		}
	}
	return nil
}

// ---- clients ---------------------------------------------------------------

func clients(o *out, g *Graph, opts Options) {
	ops := g.matching(opts.Filter)
	buckets := opts.Buckets
	if buckets <= 0 {
		buckets = 10
	}
	span := g.End - g.Start
	width := max(int64(math.Ceil(float64(span)/float64(buckets))), 1)

	type cl struct {
		name   string
		ops    []*Op
		self   int64
		counts []int
	}
	byClient := map[string]*cl{}
	var cls []*cl
	for _, op := range ops {
		c := byClient[op.Client]
		if c == nil {
			c = &cl{name: op.Client, counts: make([]int, buckets)}
			byClient[op.Client] = c
			cls = append(cls, c)
		}
		c.ops = append(c.ops, op)
		c.self += op.Self
		c.counts[min(int((op.Start-g.Start)/width), buckets-1)]++
	}
	slices.SortFunc(cls, func(a, b *cl) int {
		return cmp.Or(cmp.Compare(len(b.ops), len(a.ops)), cmp.Compare(a.name, b.name))
	})
	o.printf("clients: %d ops (%s) from %d clients over %s; ops started per %s bucket:",
		len(ops), opts.Filter, len(cls), fmtDur(span), fmtDur(width))
	hdr := fmt.Sprintf("%-26s %8s %10s |", "client", "ops", "self_tot")
	for i := range buckets {
		hdr += fmt.Sprintf(" %6s", "+"+fmtSecs(int64(i)*width))
	}
	o.printf("%s", hdr)
	for i, c := range cls {
		if i == opts.Top {
			o.printf("… %d more clients (raise `top`)", len(cls)-opts.Top)
			break
		}
		row := fmt.Sprintf("%-26s %8d %10s |", clientName(c.name), len(c.ops), fmtDur(c.self))
		for _, n := range c.counts {
			row += fmt.Sprintf(" %6d", n)
		}
		o.printf("%s", row)
	}
	o.printf("")
	o.printf("top classes per client:")
	for i, c := range cls {
		if i == opts.Top {
			break
		}
		aggs := aggregate(c.ops)
		sortAggs(aggs, "count")
		parts := make([]string, 0, 5)
		for j, a := range aggs {
			if j == 5 {
				parts = append(parts, fmt.Sprintf("+%d more", len(aggs)-5))
				break
			}
			parts = append(parts, fmt.Sprintf("%d× %s (self %s)", a.Count, a.Key, fmtDur(a.SelfTotal)))
		}
		o.printf("  %s [%s..%s]: %s", clientName(c.name), g.fmtAt(c.ops[0].Start), g.fmtAt(c.ops[len(c.ops)-1].Start), strings.Join(parts, ", "))
	}
}

func fmtSecs(ns int64) string {
	s := float64(ns) / 1e9
	if s < 10 {
		return fmt.Sprintf("%.1fs", s)
	}
	return fmt.Sprintf("%.0fs", s)
}

// ---- tree ------------------------------------------------------------------

// pickRoot resolves the op the tree and children views start from: opts.Op,
// or else the slowest op matching the filter (announced with the runners-up).
// A nil op with a nil error means nothing matched (already reported).
func pickRoot(o *out, g *Graph, opts Options) (*Op, error) {
	if opts.Op != 0 {
		root := g.ByID[opts.Op]
		if root == nil {
			return nil, fmt.Errorf("op %d not found in the dump", opts.Op)
		}
		return root, nil
	}
	ops := slices.Clone(g.matching(opts.Filter))
	if len(ops) == 0 {
		o.printf("%s: no ops match %s", opts.View, opts.Filter)
		return nil, nil
	}
	slices.SortStableFunc(ops, func(a, b *Op) int { return cmp.Compare(b.Dur(), a.Dur()) })
	others := make([]string, 0, 5)
	for _, op := range ops[1:min(len(ops), 6)] {
		others = append(others, fmt.Sprintf("%d (%s)", op.ID, fmtDur(op.Dur())))
	}
	line := fmt.Sprintf("%s: slowest of %d ops matching %s", opts.View, len(ops), opts.Filter)
	if len(others) > 0 {
		line += "; next slowest ops: " + strings.Join(others, ", ")
	}
	o.printf("%s", line)
	return ops[0], nil
}

func printParents(o *out, root *Op) {
	var chain []string
	for p := root.Parent; p != nil && len(chain) < 8; p = p.Parent {
		chain = append(chain, fmt.Sprintf("%d %s", p.ID, p.Key()))
	}
	if len(chain) > 0 {
		o.printf("parents: %s", strings.Join(chain, " ← "))
	}
}

// foldedExec returns the call_exec op that is a call's only real content:
// its sole child, with every wait of the call targeting it. The tree shows
// such a pair (the caller's view, then the shared execution) as one node.
// It returns nil when the call does anything besides waiting on its exec.
func foldedExec(op *Op) *Op {
	if op.Kind != "call" || len(op.Children) != 1 {
		return nil
	}
	e := op.Children[0]
	if e.Kind != "call_exec" {
		return nil
	}
	for _, w := range op.Waits {
		if w.Target != e {
			return nil
		}
	}
	return e
}

// node is one line of the tree: an op, possibly folded with its call_exec.
type node struct {
	op   *Op
	exec *Op // the folded call_exec, or nil
}

func newNode(op *Op) node { return node{op: op, exec: foldedExec(op)} }

// body is the op whose children and waits the node shows: the call_exec
// when folded, since the call itself only waits on it.
func (n node) body() *Op {
	if n.exec != nil {
		return n.exec
	}
	return n.op
}

// self is the node's own time. Folded, that is the call's interval minus
// the exec's children and waits: the exec's children (e.g. result
// publication) can run after the exec ends but while the call is still
// open, so summing the call's and the exec's self would count them twice.
func (n node) self() int64 {
	if n.exec != nil {
		return uncovered(n.op.Start, n.op.End, n.exec.Children, n.exec.Waits)
	}
	return n.op.Self
}

// expandable counts the children the tree would expand under the node,
// leaving out internal-kind bookkeeping (e.g. dagql.publishResult under
// nearly every executed call).
func (n node) expandable() int {
	k := 0
	for _, c := range n.body().Children {
		if c.Kind != "internal" {
			k++
		}
	}
	return k
}

func (n node) line() string {
	op := n.op
	s := fmt.Sprintf("%d %s", op.ID, op.Key())
	if op.Outcome != "" {
		s += " [" + op.Outcome + "]"
	}
	s += fmt.Sprintf(" dur %s self %s", fmtDur(op.Dur()), fmtDur(n.self()))
	if n.exec != nil {
		s += fmt.Sprintf(" (exec %d)", n.exec.ID)
	}
	if op.Reparented {
		s += " (nested client)"
	}
	return s
}

// fmtRel formats an offset from a parent's start.
func fmtRel(ns int64) string {
	if ns < 0 {
		return fmtDur(ns)
	}
	return "+" + fmtDur(ns)
}

func waitLine(w *Wait) string {
	target := w.Ident
	if w.Target != nil {
		target = fmt.Sprintf("%d %s", w.Target.ID, w.Target.Key())
	}
	return fmt.Sprintf("⏳ wait %s %s → %s", w.Reason, fmtDur(w.Dur()), target)
}

// item is a child op or a wait of a node, in the node's start order.
type item struct {
	start int64
	op    *Op
	wait  *Wait
}

func items(body *Op, withWaits bool) []item {
	its := make([]item, 0, len(body.Children)+len(body.Waits))
	for _, c := range body.Children {
		its = append(its, item{start: c.Start, op: c})
	}
	if withWaits {
		for _, w := range body.Waits {
			its = append(its, item{start: w.Start, wait: w})
		}
	}
	slices.SortStableFunc(its, func(a, b item) int { return cmpInt64(a.start, b.start) })
	return its
}

func tree(o *out, g *Graph, opts Options) error {
	root, err := pickRoot(o, g, opts)
	if root == nil {
		return err
	}
	depth := opts.Depth
	if depth <= 0 {
		depth = 6
	}
	printParents(o, root)
	o.printf("client %s", clientName(root.Client))
	t := &treeRenderer{o: o, g: g, collapse: opts.Collapse}
	n := newNode(root)
	o.printf("%s @%s", n.line(), g.fmtAt(root.Start))
	t.children(n, "  ", depth)
	if t.unexpanded > 0 {
		o.printf("(▸ N: N children not expanded at this depth, internal-kind ops not counted; raise `depth`, or re-root with `op`. (exec N): a call folded with its call_exec op N)")
	} else if t.folded > 0 {
		o.printf("((exec N): a call folded with its call_exec op N)")
	}
	return nil
}

type treeRenderer struct {
	o          *out
	g          *Graph
	collapse   int
	unexpanded int
	folded     int
}

// children renders n's children and waits in start order, `depth` levels
// deep. Classes with at least `collapse` siblings aggregate: consecutive
// members (between individually rendered lines) share one line.
func (t *treeRenderer) children(n node, indent string, depth int) {
	body := n.body()
	waitsInline := len(body.Waits) < t.collapse
	if !waitsInline {
		reasons := map[string]int{}
		var total int64
		for _, w := range body.Waits {
			reasons[w.Reason]++
			total += w.Dur()
		}
		t.o.printf("%s⏳ %d waits (%s), total %s", indent, len(body.Waits), counts(reasons, 0), fmtDur(total))
	}
	perKey := map[string]int{}
	for _, c := range body.Children {
		perKey[c.Key()]++
	}
	origin := n.op.Start
	var pendingKeys []string
	pending := map[string][]*Op{}
	flush := func() {
		for _, key := range pendingKeys {
			if run := pending[key]; len(run) == 1 {
				t.render(newNode(run[0]), origin, indent, depth-1)
			} else {
				t.aggregate(run, origin, indent)
			}
		}
		pendingKeys = pendingKeys[:0]
		clear(pending)
	}
	for _, it := range items(body, waitsInline) {
		if it.op != nil && perKey[it.op.Key()] >= t.collapse {
			key := it.op.Key()
			if _, ok := pending[key]; !ok {
				pendingKeys = append(pendingKeys, key)
			}
			pending[key] = append(pending[key], it.op)
			continue
		}
		flush()
		if it.wait != nil {
			t.o.printf("%s%s %s", indent, fmtRel(it.wait.Start-origin), waitLine(it.wait))
		} else {
			t.render(newNode(it.op), origin, indent, depth-1)
		}
	}
	flush()
}

func (t *treeRenderer) render(n node, origin int64, indent string, depth int) {
	if n.exec != nil {
		t.folded++
	}
	line := indent + fmtRel(n.op.Start-origin) + " " + n.line()
	if depth <= 0 {
		if k := n.expandable(); k > 0 {
			t.unexpanded++
			line += fmt.Sprintf(" ▸ %d", k)
		}
		t.o.printf("%s", line)
		return
	}
	t.o.printf("%s", line)
	t.children(n, indent+"  ", depth)
}

func (t *treeRenderer) aggregate(run []*Op, origin int64, indent string) {
	var (
		durs, selfs     []int64
		durTot, selfTot int64
		grand           int
	)
	outcomes := map[string]int{}
	for _, op := range run {
		n := newNode(op)
		durs = append(durs, op.Dur())
		selfs = append(selfs, n.self())
		durTot += op.Dur()
		selfTot += n.self()
		grand += n.expandable()
		outcomes[op.Outcome]++
	}
	slowest := slices.MaxFunc(run, func(a, b *Op) int { return cmp.Compare(a.Dur(), b.Dur()) })
	t.o.printf("%s%s..%s %d× %s [%s] dur p50 %s max %s total %s, self total %s, %d grandchildren; slowest: op %d",
		indent, fmtRel(run[0].Start-origin), fmtRel(run[len(run)-1].Start-origin), len(run), run[0].Key(),
		counts(outcomes, 0), fmtDur(quantile(durs, 0.5)), fmtDur(slices.Max(durs)),
		fmtDur(durTot), fmtDur(selfTot), grand, slowest.ID)
}

// ---- children --------------------------------------------------------------

// childrenView is a flat table of one op's direct children and waits: where
// did this op's time go?
func childrenView(o *out, g *Graph, opts Options) error {
	root, err := pickRoot(o, g, opts)
	if root == nil {
		return err
	}
	printParents(o, root)
	n := newNode(root)
	body := n.body()
	o.printf("%s @%s", n.line(), g.fmtAt(root.Start))
	if n.exec != nil {
		o.printf("(folded with its call_exec op %d: listing that op's children and waits)", n.exec.ID)
	}
	its := items(body, true)
	switch opts.Sort {
	case "dur", "self":
		key := func(it item) int64 {
			switch {
			case it.wait != nil:
				return it.wait.Dur()
			case opts.Sort == "dur":
				return it.op.Dur()
			default:
				return newNode(it.op).self()
			}
		}
		slices.SortStableFunc(its, func(a, b item) int { return cmpInt64(key(b), key(a)) })
	}
	classes := map[string]bool{}
	for _, c := range body.Children {
		classes[c.Key()] = true
	}
	var waitTot int64
	for _, w := range body.Waits {
		waitTot += w.Dur()
	}
	head := fmt.Sprintf("%d children in %d classes", len(body.Children), len(classes))
	if len(body.Waits) > 0 {
		head += fmt.Sprintf(", %d waits totaling %s", len(body.Waits), fmtDur(waitTot))
	}
	o.printf("%s; self %s; sorted by %s", head, fmtDur(n.self()), cmp.Or(opts.Sort, "start"))
	o.printf("%10s %10s %10s %10s  %-14s %s", "id", "start", "dur", "self", "outcome", "kind class")
	for _, it := range its {
		if w := it.wait; w != nil {
			target := w.Ident
			if w.Target != nil {
				target = fmt.Sprintf("%d %s", w.Target.ID, w.Target.Key())
			}
			o.printf("%10s %10s %10s %10s  %-14s wait %s → %s", "-", fmtRel(w.Start-root.Start), fmtDur(w.Dur()), "-", "-", w.Reason, target)
			continue
		}
		c := newNode(it.op)
		outcome := cmp.Or(c.op.Outcome, "-")
		class := c.op.Key()
		if c.exec != nil {
			class += fmt.Sprintf(" (exec %d)", c.exec.ID)
		}
		if nKids := c.expandable(); nKids > 0 {
			class += fmt.Sprintf(" ▸ %d", nKids)
		}
		o.printf("%10d %10s %10s %10s  %-14s %s", c.op.ID, fmtRel(c.op.Start-root.Start), fmtDur(c.op.Dur()), fmtDur(c.self()), outcome, class)
	}
	return nil
}

// ---- compare ---------------------------------------------------------------

// compare lines g's classes up against a baseline dump's: count, duration
// total and p50, and self total per side, with the ratio or delta, sorted by
// the largest absolute change in duration total.
func compare(o *out, g *Graph, opts Options) error {
	base := opts.Against
	if base == nil {
		return fmt.Errorf("compare needs a baseline dump (-against)")
	}
	nameA, nameB := cmp.Or(opts.AgainstName, "against"), cmp.Or(opts.Name, "capture")
	opsA, internalA := rankable(base.matching(opts.Filter), opts.Filter)
	opsB, internalB := rankable(g.matching(opts.Filter), opts.Filter)
	aggsA, aggsB := map[string]*classAgg{}, map[string]*classAgg{}
	var keys []string
	for _, a := range aggregate(opsA) {
		aggsA[a.Key] = a
		keys = append(keys, a.Key)
	}
	for _, b := range aggregate(opsB) {
		aggsB[b.Key] = b
		if aggsA[b.Key] == nil {
			keys = append(keys, b.Key)
		}
	}
	empty := &classAgg{}
	side := func(m map[string]*classAgg, key string) *classAgg {
		if a := m[key]; a != nil {
			return a
		}
		return empty
	}
	delta := func(key string) int64 {
		d := side(aggsB, key).DurTotal - side(aggsA, key).DurTotal
		if d < 0 {
			return -d
		}
		return d
	}
	slices.SortFunc(keys, func(x, y string) int {
		return cmp.Or(cmpInt64(delta(y), delta(x)), cmp.Compare(x, y))
	})

	o.printf("compare %q (%d ops over %s) against %q (%d ops over %s); %s; %s → %s, sorted by |Δdur|",
		nameB, len(g.Ops), fmtDur(g.End-g.Start), nameA, len(base.Ops), fmtDur(base.End-base.Start),
		opts.Filter, nameA, nameB)
	if len(internalA)+len(internalB) > 0 {
		o.printf("(left out: %d → %d internal-kind ops; kind: \"internal\" compares them)", len(internalA), len(internalB))
	}
	o.printf("%7s %7s  %9s %9s %10s %6s  %9s %9s %6s  %9s %9s %10s  %s",
		"n_a", "n_b", "dur_a", "dur_b", "Δdur", "×dur", "p50_a", "p50_b", "×p50", "self_a", "self_b", "Δself", "kind class")
	row := func(a, b *classAgg, label string) {
		p50A, p50B := quantile(a.Durs, 0.5), quantile(b.Durs, 0.5)
		o.printf("%7d %7d  %9s %9s %10s %6s  %9s %9s %6s  %9s %9s %10s  %s",
			a.Count, b.Count,
			fmtDur(a.DurTotal), fmtDur(b.DurTotal), fmtDelta(b.DurTotal-a.DurTotal), fmtRatio(a.DurTotal, b.DurTotal),
			fmtDur(p50A), fmtDur(p50B), fmtRatio(p50A, p50B),
			fmtDur(a.SelfTotal), fmtDur(b.SelfTotal), fmtDelta(b.SelfTotal-a.SelfTotal), label)
	}
	total := func(ops []*Op) *classAgg {
		t := &classAgg{Outcomes: map[string]int{}, execIdents: map[string]int{}}
		for _, op := range ops {
			t.add(op)
		}
		return t
	}
	row(total(opsA), total(opsB), "(all matching ops)")
	for i, key := range keys {
		if i == opts.Top {
			o.printf("… %d more classes (raise `top`)", len(keys)-opts.Top)
			break
		}
		row(side(aggsA, key), side(aggsB, key), key)
	}
	o.printf("(× is %s/%s; new/gone: absent from one side; totals sum across concurrent ops)", nameB, nameA)
	return nil
}

func fmtDelta(ns int64) string {
	switch {
	case ns > 0:
		return "+" + fmtDur(ns)
	case ns < 0:
		return fmtDur(ns)
	}
	return "0"
}

func fmtRatio(a, b int64) string {
	switch {
	case a == 0 && b == 0:
		return "-"
	case a == 0:
		return "new"
	case b == 0:
		return "gone"
	}
	r := float64(b) / float64(a)
	if r < 0.1 {
		return fmt.Sprintf("%.3f", r)
	}
	return fmt.Sprintf("%.2f", r)
}

// ---- events ----------------------------------------------------------------

type opEvent struct {
	Type        string  `json:"type"`
	ID          uint64  `json:"id"`
	Kind        string  `json:"kind"`
	Class       string  `json:"class"`
	Ident       string  `json:"ident,omitempty"`
	Client      string  `json:"client,omitempty"`
	Parent      uint64  `json:"parent,omitempty"`
	ParentClass string  `json:"parent_class,omitempty"`
	Outcome     string  `json:"outcome,omitempty"`
	Work        string  `json:"work,omitempty"`
	StartMS     float64 `json:"start_ms"`
	DurMS       float64 `json:"dur_ms"`
	SelfMS      float64 `json:"self_ms"`
	Children    int     `json:"children"`
	Waits       int     `json:"waits,omitempty"`
	Open        bool    `json:"open,omitempty"`
}

type waitEvent struct {
	Type        string  `json:"type"`
	Waiter      uint64  `json:"waiter,omitempty"`
	WaiterClass string  `json:"waiter_class,omitempty"`
	Target      uint64  `json:"target,omitempty"`
	TargetClass string  `json:"target_class,omitempty"`
	Reason      string  `json:"reason"`
	Ident       string  `json:"ident,omitempty"`
	StartMS     float64 `json:"start_ms"`
	DurMS       float64 `json:"dur_ms"`
}

type linkEvent struct {
	Type      string  `json:"type"`
	Link      string  `json:"link"`
	From      uint64  `json:"from,omitempty"`
	FromClass string  `json:"from_class,omitempty"`
	Target    uint64  `json:"target,omitempty"`
	Ident     string  `json:"ident,omitempty"`
	Result    uint64  `json:"result,omitempty"`
	AtMS      float64 `json:"at_ms"`
}

// events writes one JSON object per line with names resolved: every
// matching op, then the waits and links whose waiting/source op matches.
func events(w io.Writer, g *Graph, f Filter) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	match := func(op *Op) bool { return f.Empty() || f.Match(op) }
	for _, op := range g.Ops {
		if !match(op) {
			continue
		}
		ev := opEvent{
			Type: "op", ID: op.ID, Kind: op.Kind, Class: op.Class, Ident: op.Ident,
			Client: op.Client, Outcome: op.Outcome, Work: op.Work,
			StartMS: ms(op.Start - g.Start), DurMS: ms(op.Dur()), SelfMS: ms(op.Self),
			Children: len(op.Children), Waits: len(op.Waits), Open: op.Open,
		}
		if op.Parent != nil {
			ev.Parent, ev.ParentClass = op.Parent.ID, op.Parent.Class
		}
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	for _, wt := range g.Waits {
		if !f.Empty() && !f.Match(wt.Waiter) {
			continue
		}
		ev := waitEvent{
			Type: "wait", Waiter: wt.WaiterID, Target: wt.TargetID, Reason: wt.Reason, Ident: wt.Ident,
			StartMS: ms(wt.Start - g.Start), DurMS: ms(wt.Dur()),
		}
		if wt.Waiter != nil {
			ev.WaiterClass = wt.Waiter.Class
		}
		if wt.Target != nil {
			ev.Target, ev.TargetClass = wt.Target.ID, wt.Target.Class
		}
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	for _, l := range g.Links {
		from := g.ByID[l.FromID]
		if !f.Empty() && !f.Match(from) {
			continue
		}
		ev := linkEvent{
			Type: "link", Link: l.Kind, From: l.FromID, Target: l.TargetID,
			Ident: l.Ident, Result: l.ResultID, AtMS: ms(l.At - g.Start),
		}
		if from != nil {
			ev.FromClass = from.Class
		}
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return nil
}
