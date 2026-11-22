package dagui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	telemetry "github.com/dagger/otel-go"
)

// CauseAnatomy classifies the cause links of the spans the fold retains, as
// their stubs place them: by carrier kind and purpose, where the target
// sits, where the ends meet and whether that span would be retained without
// the links, the names involved, and how many carriers share a target. A
// diagnostic, for sizing rules that fold cause links (see
// simulateContainedCauses).
func (f *Fold) CauseAnatomy() []FoldCount {
	targets, links, realWaits := f.retainedLinks()
	_, _, bare := f.simulate(foldVariant{waitSidecar: true, causeSidecar: true}, links, realWaits)
	_, _, contained := f.simulateContainedCauses(links, realWaits, targets)
	dims := map[string]map[string]int{}
	note := func(dim, val string) {
		if dims[dim] == nil {
			dims[dim] = map[string]int{}
		}
		dims[dim][val]++
	}
	fanIn := map[uint32]int{}
	var maxEnd int64
	for _, e := range f.end {
		maxEnd = max(maxEnd, e)
	}
	for i, st := range f.state {
		if st&(foldReceived|foldRetained) != foldReceived|foldRetained {
			continue
		}
		span := f.db.Spans.Map[f.spans[i]]
		if span == nil {
			continue
		}
		kind := "plain"
		switch {
		case strings.HasPrefix(span.Name, "resume "):
			kind = "resume"
		case span.isLazyEvaluation():
			kind = "stored part"
		}
		var nlinks, nfolded, ncontained int
		for _, link := range span.Links {
			if link.Purpose != telemetry.LinkPurposeCause && link.Purpose != "" {
				continue
			}
			purpose := "evaluates"
			if link.Purpose == telemetry.LinkPurposeCause {
				purpose = "install"
			}
			k := kind + "/" + purpose
			nlinks++
			note("links", k)
			t, ok := f.lookup(link.SpanContext.SpanID)
			if !ok || f.state[t]&foldReceived == 0 {
				note("target", k+": not received")
				continue
			}
			fanIn[t]++
			c := uint32(i)
			lca := f.commonAncestor(c, t)
			var rel string
			switch {
			case lca == noSpan:
				rel = "no common ancestor"
			case lca == t:
				rel = "target an ancestor"
			case lca == c:
				rel = "target beneath"
			case f.parent[c] == f.parent[t]:
				rel = "target a sibling"
			default:
				rel = "elsewhere"
			}
			note("target", k+": "+rel)
			note("target name", k+": "+nameClass(f.stubName(t)))
			if lca == noSpan {
				continue
			}
			dc, _ := f.depthTop(c)
			dt, _ := f.depthTop(t)
			dl, _ := f.depthTop(lca)
			note("lca depth", fmt.Sprintf("%s: %s", k, bucket(dl)))
			note("carrier below lca", fmt.Sprintf("%s: %s", k, bucket(dc-dl)))
			note("target below lca", fmt.Sprintf("%s: %s", k, bucket(dt-dl)))
			note("lca name", k+": "+nameClass(f.stubName(lca)))
			if !bare[lca] {
				nfolded++
				note("lca", k+": folded without links")
			} else {
				note("lca", k+": retained without links")
			}
			if !contained[lca] {
				ncontained++
				note("lca under contained causes", k+": folded")
			} else {
				note("lca under contained causes", k+": retained")
			}
			if why := f.causeOpen(c, t, lca, targets); why != "" {
				note("open", k+": "+why)
				if why == "carrier outlasts an ancestor" {
					note("outlasts", k+": "+f.outlastWhy(c, lca))
					if f.end[c] == maxEnd {
						note("outlasts, carrier", k+": ends at the trace's end")
					} else {
						note("outlasts, carrier", k+": ended")
					}
				}
			}
		}
		if nlinks == 0 {
			continue
		}
		note("carriers", kind)
		note("carrier links", fmt.Sprintf("%s: %s", kind, bucket(nlinks)))
		if p := f.parent[i]; p != 0 {
			note("carrier parent name", kind+": "+nameClass(f.stubName(p-1)))
		}
		switch {
		case nfolded == nlinks:
			note("carrier: lcas folded without links", kind+": all")
		case nfolded > 0:
			note("carrier: lcas folded without links", kind+": some")
		default:
			note("carrier: lcas folded without links", kind+": none")
		}
		switch {
		case ncontained == nlinks:
			note("carrier: lcas folded under contained causes", kind+": all")
		case ncontained > 0:
			note("carrier: lcas folded under contained causes", kind+": some")
		default:
			note("carrier: lcas folded under contained causes", kind+": none")
		}
		var flags []string
		if span.Passthrough {
			flags = append(flags, "passthrough")
		}
		if span.IsFailed() {
			flags = append(flags, "failed")
		}
		if span.Blocked {
			flags = append(flags, "blocked")
		}
		if span.Partial {
			flags = append(flags, "partial")
		}
		if span.Canceled {
			flags = append(flags, "canceled")
		}
		if span.hasWaitLinks() {
			flags = append(flags, "wait links")
		}
		if span.effectsViaLinks.Len() > 0 {
			flags = append(flags, "has effects")
		}
		if f.end[i] == maxEnd {
			flags = append(flags, "ends at the trace's end")
		}
		var own int
		for _, child := range span.ChildSpans.Spans() {
			if child.ParentSpan == span {
				own++
			}
		}
		if own > 0 {
			flags = append(flags, "own children")
		}
		note("carrier flags", kind+": "+strings.Join(flags, "+"))
	}
	for t, n := range fanIn {
		note("fan-in", bucket(n))
		span := f.db.Spans.Map[f.spans[t]]
		if span == nil {
			note("target flags", "not held")
			continue
		}
		var flags []string
		if span.Pending {
			flags = append(flags, "pending")
		}
		if span.IsFailed() {
			flags = append(flags, "failed")
		}
		if span.causesViaLinks.Len() > 0 {
			flags = append(flags, "has causes")
		}
		if span.isLazyEvaluation() {
			flags = append(flags, "lazy evaluation")
		}
		if bare[t] {
			flags = append(flags, "retained without links")
		}
		note("target flags", strings.Join(flags, "+"))
	}
	order := []string{"carriers", "carrier links", "carrier flags", "carrier parent name", "links", "target", "target name",
		"lca", "lca under contained causes", "carrier: lcas folded without links", "carrier: lcas folded under contained causes",
		"open", "outlasts", "outlasts, carrier", "lca depth", "carrier below lca", "target below lca", "lca name", "fan-in", "target flags"}
	var out []FoldCount
	for _, dim := range order {
		vals := dims[dim]
		keys := make([]string, 0, len(vals))
		for k := range vals {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(a, b int) bool {
			if vals[keys[a]] != vals[keys[b]] {
				return vals[keys[a]] > vals[keys[b]]
			}
			return keys[a] < keys[b]
		})
		if (strings.HasSuffix(dim, "name") || dim == "outlasts") && len(keys) > 15 {
			keys = keys[:15]
		}
		for _, k := range keys {
			out = append(out, FoldCount{dim + " | " + k, vals[k]})
		}
	}
	return out
}

// WaitAnatomy classifies the wait links of the spans the fold retains: by
// reason, and for those it reads (neither on the carrier's own execution
// nor contained), why containment fails. A diagnostic.
func (f *Fold) WaitAnatomy() []FoldCount {
	counts := map[string]int{}
	carriers := map[string]map[uint32]bool{}
	tgts := map[string]map[uint32]bool{}
	note := func(key string, c, t uint32) {
		counts[key]++
		if carriers[key] == nil {
			carriers[key] = map[uint32]bool{}
			tgts[key] = map[uint32]bool{}
		}
		carriers[key][c] = true
		tgts[key][t] = true
	}
	for i, st := range f.state {
		if st&(foldReceived|foldRetained) != foldReceived|foldRetained {
			continue
		}
		span := f.db.Spans.Map[f.spans[i]]
		if span == nil {
			continue
		}
		c := uint32(i)
		for _, link := range span.Links {
			if !link.IsWait() {
				continue
			}
			reason := link.WaitReason
			if reason == "" {
				reason = "?"
			}
			t, ok := f.lookup(link.SpanContext.SpanID)
			if !ok || t == c {
				note(reason+": not in the trace / self", c, c)
				continue
			}
			if f.state[t]&foldReceived == 0 {
				note(reason+": target never received", c, t)
				continue
			}
			if slicesContains(f.noops[c], t) {
				note(reason+": on its own execution (folded)", c, t)
				continue
			}
			if st&foldContainedWaits != 0 && slicesContains(f.reads[c], t) && f.state[t]&foldRetained == 0 {
				note(reason+": contained (folded)", c, t)
				continue
			}
			var why []string
			lca := f.commonAncestor(c, t)
			switch {
			case lca == noSpan:
				why = append(why, "no common ancestor")
			case lca == t:
				why = append(why, "target an ancestor")
			case lca != c:
				why = append(why, "target outside")
			default:
				if !f.unread(c) {
					why = append(why, "carrier read")
					if st&foldCauseLinks != 0 {
						why = append(why, "carrier has causes")
					}
				}
				if f.state[t]&foldEscapes != 0 {
					why = append(why, "target escapes")
				}
				if isLazyEvaluationName(f.stubName(t)) {
					why = append(why, "target lazy evaluation")
				}
				if p := f.parent[t]; p != 0 && p-1 != c && f.state[p-1]&foldEscapes != 0 {
					why = append(why, "target's parent escapes")
				}
				if f.name[t] == f.name[c] {
					why = append(why, "same name")
				}
			}
			if len(why) == 0 {
				why = append(why, "containable now")
			}
			note(reason+": open: "+strings.Join(why, "+"), c, t)
			if f.state[t]&foldRetained != 0 {
				note(reason+": open, target "+retainedWhy(f, t), c, t)
			}
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []FoldCount
	for _, k := range keys {
		out = append(out, FoldCount{fmt.Sprintf("%s (carriers %d, targets %d)", k, len(carriers[k]), len(tgts[k])), counts[k]})
	}
	return out
}

// retainedWhy is a coarse reason retained stub t is retained for, other
// than as a wait target.
func retainedWhy(f *Fold, t uint32) string {
	st := f.state[t]
	switch {
	case st&foldCauseLinks != 0:
		return "a cause carrier"
	case st&foldWaitLinks != 0:
		return "a wait carrier"
	case st&(foldKind|foldAgent) != 0:
		return "a surfaced kind"
	case isLazyEvaluationName(f.stubName(t)):
		return "a lazy evaluation"
	}
	return "otherwise"
}

// outlastWhy says how carrier c's interval leaves that of lca or one of its
// ancestors: the first such ancestor, how far above lca, and by how much.
func (f *Fold) outlastWhy(c, lca uint32) string {
	cs, ce := f.start[c], f.end[c]
	d := 0
	for a := lca + 1; a != 0; a, d = f.parent[a-1], d+1 {
		as, ae := f.start[a-1], f.end[a-1]
		var why string
		switch {
		case cs < as:
			why = "starts before, by " + durBucket(as-cs)
		case ae != 0 && ce == 0:
			why = "runs past its end"
		case ae != 0 && ce > ae:
			why = "ends after, by " + durBucket(ce-ae)
		default:
			continue
		}
		where := fmt.Sprintf("%d above lca", d)
		if d == 0 {
			where = "lca"
		}
		if f.parent[a-1] == 0 {
			where = "root"
		}
		return fmt.Sprintf("%s (%s): %s", where, nameClass(f.stubName(a-1)), why)
	}
	return "?"
}

func durBucket(ns int64) string {
	switch {
	case ns < 1_000:
		return "<1µs"
	case ns < 1_000_000:
		return "<1ms"
	case ns < 100_000_000:
		return "<100ms"
	case ns < 10_000_000_000:
		return "<10s"
	default:
		return ">=10s"
	}
}

// LinkRuleWhatIfs simulates rules that retain neither end of an edge of a
// lazy evaluation -- its cause links, and the lazy waits on it -- while the
// edge is contained beneath a folded span. Unlike simulate, it reads the
// waits as the fold classified them (on the carrier's own execution and
// contained ones retain nothing), so its "as built" matches the fold. A
// diagnostic.
func (f *Fold) LinkRuleWhatIfs() []FoldCount {
	targets, _, _ := f.retainedLinks()
	type edge struct {
		c, t uint32
		lazy bool // a lazy wait, else a cause link
		// install: a lazy evaluation's link to a site its result was
		// installed at
		install bool
	}
	var edges []edge
	var seeds []uint32
	evalOf := map[uint32]uint32{}
	for i, st := range f.state {
		if st&foldReceived == 0 {
			continue
		}
		s := FoldSpan{f, uint32(i)}
		p := f.parent[i]
		if p == 0 || f.state[p-1]&(foldExpanded|foldOpenAll) != 0 || st&(foldAgent|foldKind) != 0 ||
			s.Progress() || ((s.Failed() || s.Reveal()) && s.MayRollUp()) || st&foldFunctionPlumbing != 0 ||
			(st&foldExecOp != 0 && f.neededForWaits(uint32(i))) {
			seeds = append(seeds, uint32(i))
		}
		if st&foldRetained == 0 {
			continue
		}
		span := f.db.Spans.Map[f.spans[i]]
		if span == nil {
			continue
		}
		c := uint32(i)
		for _, link := range span.Links {
			t, ok := f.lookup(link.SpanContext.SpanID)
			if !ok || t == c {
				continue
			}
			switch {
			case link.Purpose == telemetry.LinkPurposeCause || link.Purpose == "":
				edges = append(edges, edge{c, t, false, link.Purpose == telemetry.LinkPurposeCause && span.isLazyEvaluation()})
				if _, ok := evalOf[c]; !ok && link.Purpose == "" {
					evalOf[c] = t
				}
			case link.Purpose == telemetry.LinkPurposeErrorOrigin:
				seeds = append(seeds, t)
			case link.IsWait():
				if slicesContains(f.noops[c], t) ||
					(st&foldContainedWaits != 0 && f.state[t]&foldWaitTarget != 0 && f.isAncestor(c, t)) {
					continue
				}
				if link.WaitReason == "lazy" && isLazyEvaluationName(f.stubName(t)) {
					edges = append(edges, edge{c, t, true, false})
				} else {
					seeds = append(seeds, c, t)
				}
			}
		}
		if span.Status.Code != 0 {
			for _, origin := range telemetry.ParseErrorOrigins(span.Status.Description) {
				if t, ok := f.lookup(SpanID{SpanID: origin.SpanID()}); ok {
					seeds = append(seeds, t)
				}
			}
		}
	}
	var base []bool
	whyOpen := map[string]int{}
	var runAs func(containable func(e edge) bool, intervals, explain, beneath bool) int
	// dropInstalls leaves out the install-site links, as if the engine
	// didn't emit them
	var dropInstalls bool
	// virtual: a lazy evaluation's effects on its causes are kept beside
	// them (see the what-if below)
	var virtual bool
	run := func(containable func(e edge) bool, intervals, explain bool) int {
		return runAs(containable, intervals, explain, false)
	}
	runAs = func(containable func(e edge) bool, intervals, explain, beneath bool) int {
		kept := make([]bool, len(f.state))
		// pathKept: is a span from x up to lca, lca excluded, kept?
		pathKept := func(x, lca uint32) bool {
			for j := x; j != lca; j = f.parent[j] - 1 {
				if kept[j] {
					return true
				}
				if f.parent[j] == 0 {
					return true
				}
			}
			return false
		}
		var n int
		keep := func(i uint32) {
			for j := i; !kept[j]; {
				kept[j] = true
				if f.state[j]&foldReceived != 0 {
					n++
				}
				if f.parent[j] == 0 {
					return
				}
				j = f.parent[j] - 1
			}
		}
		for _, s := range seeds {
			keep(s)
		}
		if base == nil {
			base = slices.Clone(kept)
		}
		open := make([]bool, len(edges))
		for {
			grew := false
			for k, e := range edges {
				if open[k] || (dropInstalls && e.install) {
					continue
				}
				lca := f.commonAncestor(e.c, e.t)
				if virtual {
					var opens bool
					if e.lazy {
						// a row holding the carrier reads the wait, and
						// so the evaluation's causes
						opens = kept[e.c]
						if p, ok := evalOf[e.t]; ok && !opens {
							if l2 := f.commonAncestor(e.c, p); l2 == noSpan {
								opens = true
							} else {
								opens = pathKept(e.c, l2)
							}
						}
					} else {
						// the evaluation's own row, or its failure
						opens = kept[e.c] || f.state[e.c].cat() == stateFailed || f.state[e.c].cat() == stateCanceled ||
							!f.isLazyEvaluationStub(e.c) || f.state[e.t]&foldCauseLinks != 0
					}
					if opens {
						open[k] = true
						grew = true
						if explain {
							var why string
							by := func(x uint32) string {
								if base[x] {
									return "by the rules (" + retainedWhy(f, x) + ", " + nameClass(f.stubName(x)) + ")"
								}
								return "by another open edge"
							}
							switch {
							case e.lazy && kept[e.c]:
								why = "lazy wait: carrier retained " + by(e.c)
							case e.lazy:
								why = "lazy wait: a span between it and the call it resolves to is retained, or they don't meet"
							case kept[e.c]:
								why = "cause: evaluation retained " + by(e.c)
							case !f.isLazyEvaluationStub(e.c):
								why = "cause: not a lazy evaluation's"
							case f.state[e.t]&foldCauseLinks != 0:
								why = "cause: target has causes"
							default:
								why = "cause: evaluation failed or canceled"
							}
							whyOpen["virtual: "+why]++
						}
						keep(e.c)
						keep(e.t)
					}
					continue
				}
				var meets bool
				if lca != noSpan {
					if beneath {
						meets = kept[e.c] || kept[e.t] || pathKept(e.c, lca) || pathKept(e.t, lca) ||
							(!e.lazy && (f.state[e.c].cat() == stateFailed || f.state[e.c].cat() == stateCanceled))
						if p, ok := evalOf[e.t]; ok && e.lazy && !meets {
							// the wait resolves to the call the lazy
							// evaluation evaluates: it meets that too
							if l2 := f.commonAncestor(e.c, p); l2 == noSpan {
								lca = noSpan
							} else {
								meets = pathKept(e.c, l2) || pathKept(p, l2)
							}
						}
					} else {
						meets = kept[lca]
					}
				}
				if !containable(e) || lca == noSpan || meets ||
					(!e.lazy && f.causeOpenAs(e.c, e.t, lca, targets, intervals) != "") {
					open[k] = true
					grew = true
					if explain {
						kind := "cause"
						if e.lazy {
							kind = "lazy wait"
						}
						switch {
						case lca == noSpan:
							whyOpen[kind+": no common ancestor"]++
						case beneath && (base[e.c] || base[e.t]):
							which := "carrier"
							x := e.c
							if !base[e.c] {
								which, x = "target", e.t
							}
							whyOpen[kind+": "+which+" retained by the rules: "+retainedWhy(f, x)+" "+nameClass(f.stubName(x))]++
						case beneath && meets:
							whyOpen[kind+": between it and where it meets, retained or failed"]++
						case beneath:
							whyOpen[kind+": "+f.causeOpenAs(e.c, e.t, lca, targets, false)]++
						case base[lca]:
							whyOpen[kind+": meets where the rules retain: "+nameClass(f.stubName(lca))]++
						case kept[lca]:
							whyOpen[kind+": meets where another open edge retains"]++
						default:
							whyOpen[kind+": "+f.causeOpenAs(e.c, e.t, lca, targets, false)]++
						}
					}
					keep(e.c)
					keep(e.t)
				}
			}
			if !grew {
				return n
			}
		}
	}
	never := func(edge) bool { return false }
	causes := func(e edge) bool { return !e.lazy }
	lazyCauses := func(e edge) bool { return !e.lazy && f.isLazyEvaluationStub(e.c) }
	both := func(edge) bool { return true }
	out := []FoldCount{
		{"as built", run(never, false, false)},
		{"cause links contained where they meet folded", run(causes, false, false)},
		{"lazy evaluations' cause links and lazy waits contained where they meet folded", run(func(e edge) bool { return e.lazy || lazyCauses(e) }, false, false)},
		{"cause links and lazy waits contained where they meet folded", run(both, false, false)},
		{"cause links and lazy waits contained, within their meeting point's own interval and its ancestors'", run(both, true, false)},
		{"cause links and lazy waits folded while nothing between their ends and where they meet is retained, nor failed/canceled", runAs(both, false, false, true)},
		{"cause links folded so, lazy waits as built", runAs(causes, false, false, true)},
		{"no cause link or lazy wait retains anything (the floor for such rules)", len(seedsOnly(f, seeds))},
	}
	dropInstalls = true
	out = append(out,
		FoldCount{"no install-site links, the rest as built", run(never, false, false)},
		FoldCount{"no install-site links, cause links and lazy waits folded while nothing between their ends and where they meet is retained", runAs(both, false, false, true)},
	)
	dropInstalls = false
	virtual = true
	out = append(out, FoldCount{"lazy evaluations' effects kept beside their causes: retained for their own row, a failure, or a wait a row reads", run(both, false, true)})
	virtual = false
	keys := make([]string, 0, len(whyOpen))
	for k := range whyOpen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return whyOpen[keys[a]] > whyOpen[keys[b]] })
	if len(keys) > 25 {
		keys = keys[:25]
	}
	for _, k := range keys {
		out = append(out, FoldCount{"open edges: " + k, whyOpen[k]})
	}
	return out
}

// seedsOnly returns the received spans seeds and their ancestors hold.
func seedsOnly(f *Fold, seeds []uint32) []uint32 {
	kept := make([]bool, len(f.state))
	var out []uint32
	for _, s := range seeds {
		for j := s; !kept[j]; {
			kept[j] = true
			if f.state[j]&foldReceived != 0 {
				out = append(out, j)
			}
			if f.parent[j] == 0 {
				break
			}
			j = f.parent[j] - 1
		}
	}
	return out
}

func (f *Fold) isLazyEvaluationStub(i uint32) bool {
	return isLazyEvaluationName(f.stubName(i))
}

func (f *Fold) stubName(i uint32) string {
	if n := f.name[i]; n != 0 {
		return f.nameTab[n]
	}
	return "?"
}

// nameClass is a span name without its arguments.
func nameClass(name string) string {
	if i := strings.IndexByte(name, '('); i > 0 && !strings.HasPrefix(name, "resume ") && !strings.HasPrefix(name, "open stored part") {
		name = name[:i]
	}
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}

func bucket(n int) string {
	switch {
	case n <= 3:
		return fmt.Sprint(n)
	case n <= 5:
		return "4-5"
	case n <= 10:
		return "6-10"
	case n <= 30:
		return "11-30"
	case n <= 100:
		return "31-100"
	default:
		return ">100"
	}
}

// causeOpen reports why carrier c's cause link to t can't be contained
// beneath their common ancestor lca, whatever retains lca: "" if it can.
// Contained, the link changes nothing outside lca's subtree, as far as the
// stubs tell: t has no causes of its own (status flows on through them), c
// isn't the cause of others (it'd pass theirs on), both reach a received
// root, and c's interval lies within those of lca and its ancestors, whose
// activity the link would otherwise extend.
func (f *Fold) causeOpen(c, t, lca uint32, targets map[uint32]int) string {
	return f.causeOpenAs(c, t, lca, targets, true)
}

// causeOpenAs is causeOpen, leaving out the interval check unless
// intervals.
func (f *Fold) causeOpenAs(c, t, lca uint32, targets map[uint32]int, intervals bool) string {
	switch {
	case lca == noSpan:
		return "no common ancestor"
	case f.state[t]&foldCauseLinks != 0:
		return "target has causes"
	case targets[c]&targetCause != 0:
		return "carrier is a cause"
	}
	_, top := f.depthTop(lca)
	if f.state[top]&foldReceived == 0 {
		return "ancestry incomplete"
	}
	if !intervals {
		return ""
	}
	cs, ce := f.start[c], f.end[c]
	for a := lca + 1; a != 0; a = f.parent[a-1] {
		as, ae := f.start[a-1], f.end[a-1]
		if cs < as || (ae != 0 && (ce == 0 || ce > ae)) {
			return "carrier outlasts an ancestor"
		}
	}
	return ""
}

// simulateContainedCauses is simulate for a rule that retains neither end
// of a cause link for it while the link is contained: the span its ends meet
// at is folded, and nothing else keeps it open (causeOpen). Links are
// opened, retaining both ends, until no folded meeting point is left with
// an open link beneath it: a fixed point.
func (f *Fold) simulateContainedCauses(links map[uint32][]foldLink, realWaits map[uint32]bool, targets map[uint32]int) (int, map[string]int, []bool) {
	return f.simulateContainedCausesAs(links, realWaits, targets, true)
}

// simulateContainedCausesAs is simulateContainedCauses, ignoring the
// carriers' intervals (see causeOpenAs) unless intervals.
func (f *Fold) simulateContainedCausesAs(links map[uint32][]foldLink, realWaits map[uint32]bool, targets map[uint32]int, intervals bool) (int, map[string]int, []bool) {
	open := map[[2]uint32]bool{}
	for {
		v := foldVariant{causeSidecar: true, extraSeeds: map[uint32]string{}}
		for c, ls := range links {
			for _, l := range ls {
				if l.purpose == targetCause && open[[2]uint32{c, l.target}] {
					v.extraSeeds[c] = "carrier of an open cause link"
					if _, ok := v.extraSeeds[l.target]; !ok {
						v.extraSeeds[l.target] = "target of an open cause link"
					}
				}
			}
		}
		n, why, kept := f.simulate(v, links, realWaits)
		grew := false
		for c, ls := range links {
			for _, l := range ls {
				key := [2]uint32{c, l.target}
				if l.purpose != targetCause || open[key] {
					continue
				}
				lca := f.commonAncestor(c, l.target)
				if (lca != noSpan && kept[lca]) || f.causeOpenAs(c, l.target, lca, targets, intervals) != "" {
					open[key] = true
					grew = true
				}
			}
		}
		if !grew {
			return n, why, kept
		}
	}
}
