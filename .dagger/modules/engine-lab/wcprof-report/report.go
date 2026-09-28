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
	// Op is the root op for the tree view (0 = the slowest matching op).
	Op uint64
	// Depth bounds the tree view's recursion.
	Depth int
	// Top bounds rows per ranking (classes, clients, shapes, shape entries).
	Top int
	// Sort orders the classes view: self, count or dur.
	Sort string
	// Buckets is the number of time buckets in the clients view.
	Buckets int
	// Collapse is the sibling-group size from which the tree view
	// aggregates same-class siblings into one line.
	Collapse int
	// Limit bounds output lines (0 = unlimited). Not applied to events.
	Limit int
}

// Views lists the supported report views.
var Views = []string{"summary", "classes", "breakdown", "clients", "tree", "events"}

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

	aggs := aggregate(g.Ops)
	o.printf("top classes (of %d) by count:", len(aggs))
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
	o.printf("(times are relative to the first recorded op; self = duration minus child ops and waits)")
}

// ---- classes ---------------------------------------------------------------

func classes(o *out, g *Graph, opts Options) {
	ops := g.matching(opts.Filter)
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
	o.printf("classes: %d ops in %d classes (%s) over %s, self total %s; sorted by %s",
		len(ops), len(aggs), opts.Filter, fmtDur(g.End-g.Start), fmtDur(selfTotal), sortBy)
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

func tree(o *out, g *Graph, opts Options) error {
	var root *Op
	if opts.Op != 0 {
		root = g.ByID[opts.Op]
		if root == nil {
			return fmt.Errorf("op %d not found in the dump", opts.Op)
		}
	} else {
		ops := slices.Clone(g.matching(opts.Filter))
		if len(ops) == 0 {
			o.printf("tree: no ops match %s", opts.Filter)
			return nil
		}
		slices.SortStableFunc(ops, func(a, b *Op) int { return cmp.Compare(b.Dur(), a.Dur()) })
		root = ops[0]
		others := make([]string, 0, 5)
		for _, op := range ops[1:min(len(ops), 6)] {
			others = append(others, fmt.Sprintf("%d (%s)", op.ID, fmtDur(op.Dur())))
		}
		line := fmt.Sprintf("tree: slowest of %d ops matching %s", len(ops), opts.Filter)
		if len(others) > 0 {
			line += "; next slowest ops: " + strings.Join(others, ", ")
		}
		o.printf("%s", line)
	}
	depth := opts.Depth
	if depth <= 0 {
		depth = 6
	}
	var chain []string
	for p := root.Parent; p != nil && len(chain) < 8; p = p.Parent {
		chain = append(chain, fmt.Sprintf("%d %s", p.ID, p.Key()))
	}
	if len(chain) > 0 {
		o.printf("parents: %s", strings.Join(chain, " ← "))
	}
	o.printf("client %s", clientName(root.Client))
	renderOp(o, g, root, "", depth, opts.Collapse)
	return nil
}

func opLine(g *Graph, op *Op) string {
	s := fmt.Sprintf("%d %s", op.ID, op.Key())
	if op.Outcome != "" {
		s += " [" + op.Outcome + "]"
	}
	s += fmt.Sprintf(" dur %s self %s @%s", fmtDur(op.Dur()), fmtDur(op.Self), g.fmtAt(op.Start))
	if op.Reparented {
		s += " (nested client)"
	}
	return s
}

func renderOp(o *out, g *Graph, op *Op, indent string, depth, collapse int) {
	o.printf("%s%s", indent, opLine(g, op))
	sub := indent + "  "
	if len(op.Waits) > 0 {
		if len(op.Waits) < collapse {
			for _, w := range op.Waits {
				target := w.Ident
				if w.Target != nil {
					target = fmt.Sprintf("%d %s", w.Target.ID, w.Target.Key())
				}
				o.printf("%s⏳ wait %s %s → %s", sub, w.Reason, fmtDur(w.Dur()), target)
			}
		} else {
			reasons := map[string]int{}
			var total int64
			for _, w := range op.Waits {
				reasons[w.Reason]++
				total += w.Dur()
			}
			o.printf("%s⏳ %d waits (%s), total %s", sub, len(op.Waits), counts(reasons, 0), fmtDur(total))
		}
	}
	if len(op.Children) == 0 {
		return
	}
	if depth <= 1 {
		o.printf("%s… %d children (raise `depth`)", sub, len(op.Children))
		return
	}
	// Group siblings by class, in first-start order; big groups collapse
	// into one aggregate line.
	groups := map[string][]*Op{}
	var order []string
	for _, c := range op.Children {
		if _, ok := groups[c.Key()]; !ok {
			order = append(order, c.Key())
		}
		groups[c.Key()] = append(groups[c.Key()], c)
	}
	for _, key := range order {
		kids := groups[key]
		if len(kids) < collapse {
			for _, c := range kids {
				renderOp(o, g, c, sub, depth-1, collapse)
			}
			continue
		}
		a := aggregate(kids)[0]
		slowest := slices.MaxFunc(kids, func(a, b *Op) int { return cmp.Compare(a.Dur(), b.Dur()) })
		grand := 0
		for _, c := range kids {
			grand += len(c.Children)
		}
		o.printf("%s%d× %s [%s] dur p50 %s max %s total %s, self total %s, %d grandchildren; slowest: op %d",
			sub, len(kids), key, counts(a.Outcomes, 0), fmtDur(quantile(a.Durs, 0.5)), fmtDur(slices.Max(a.Durs)),
			fmtDur(a.DurTotal), fmtDur(a.SelfTotal), grand, slowest.ID)
	}
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
