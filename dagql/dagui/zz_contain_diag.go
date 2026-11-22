package dagui

// TEMPORARY diagnostic, not for commit.

import (
	"fmt"
	"sort"
	"strings"
)

var ZZReadHook func(f *Fold, target, waiter, lca uint32)

var zzReadBy = map[uint32]string{}

func ZZInstallReadHook() {
	zzReadBy = map[uint32]string{}
	ZZReadHook = func(f *Fold, target, waiter, lca uint32) {
		where := "lca noSpan"
		if lca != noSpan {
			where = "lca " + f.nameTab[f.name[lca]]
		}
		_, top := f.depthTop(target)
		zzReadBy[target] = fmt.Sprintf("noteWait from %s (received=%v) %s; target received=%v top=%s(received=%v) start=%d waiterEnd=%d",
			f.nameTab[f.name[waiter]], f.state[waiter]&foldReceived != 0, where, f.state[target]&foldReceived != 0,
			f.nameTab[f.name[top]], f.state[top]&foldReceived != 0, f.start[target], f.end[waiter])
	}
}

func ZZContainDiag(full, folded *DB) string {
	f := folded.fold
	depth := func(s *Span) int {
		d := 0
		for p := s.ParentSpan; p != nil; p = p.ParentSpan {
			d++
		}
		return d
	}
	lca := func(a, b *Span) *Span {
		da, db := depth(a), depth(b)
		for ; da > db; da-- {
			a = a.ParentSpan
		}
		for ; db > da; db-- {
			b = b.ParentSpan
		}
		for a != b {
			if a == nil || b == nil {
				return nil
			}
			a, b = a.ParentSpan, b.ParentSpan
		}
		return a
	}
	firstEsc := map[*Span]string{}
	minLca := map[*Span]int{}
	mark := func(s, top *Span, why string) {
		d := -1
		if top != nil {
			d = depth(top)
		}
		for ; s != nil && s != top; s = s.ParentSpan {
			if old, ok := minLca[s]; !ok || d < old {
				minLca[s] = d
			}
			if _, ok := firstEsc[s]; !ok {
				firstEsc[s] = why
			}
		}
	}
	short := func(s string) string {
		if len(s) > 40 {
			return s[:40]
		}
		return s
	}
	for _, s := range full.Spans.Order {
		for _, l := range s.Links {
			t := full.Spans.Map[l.SpanContext.SpanID]
			switch l.Purpose {
			case "wait":
				if t == nil {
					mark(s, nil, "wait "+l.WaitReason+" to out-of-scope")
					continue
				}
				top := lca(s, t)
				mark(s, top, fmt.Sprintf("wait %s %s -> %s", l.WaitReason, short(s.Name), short(t.Name)))
			case "cause", "":
				if t == nil {
					mark(s, nil, "cause to out-of-scope")
					continue
				}
				top := lca(s, t)
				why := fmt.Sprintf("cause %s -> %s", short(s.Name), short(t.Name))
				mark(s, top, why)
				mark(t, top, why)
			}
		}
	}
	waitedBy := map[*Span][]string{}
	insideWaiters := map[*Span][]*Span{}
	for _, s := range full.Spans.Order {
		for _, l := range s.Links {
			if !l.IsWait() {
				continue
			}
			t := full.Spans.Map[l.SpanContext.SpanID]
			if t == nil || t == s {
				continue
			}
			if spanInSubtree(t, s) {
				insideWaiters[t] = append(insideWaiters[t], s)
				continue
			}
			waitedBy[t] = append(waitedBy[t], fmt.Sprintf("%s by %s", l.WaitReason, short(s.Name)))
		}
	}
	var readRoot func(s *Span, depth int) string
	readRoot = func(s *Span, depth int) string {
		if depth > 20 {
			return "deep"
		}
		switch {
		case s.causesViaLinks.Len() > 0:
			return "cause links on " + short(s.Name)
		case len(waitedBy[s]) > 0:
			return "outside wait " + waitedBy[s][0] + " on " + short(s.Name)
		}
		if p := s.ParentSpan; p != nil && p.Name == s.Name {
			if i, ok := f.lookup(p.ID); ok && f.state[i]&foldRead != 0 {
				return readRoot(p, depth+1) + " <twin"
			}
		}
		for _, w := range insideWaiters[s] {
			if i, ok := f.lookup(w.ID); ok && f.state[i]&foldRead != 0 {
				return readRoot(w, depth+1) + " <" + short(w.Name)
			}
		}
		return "unknown on " + short(s.Name)
	}
	readWhy := map[string]int{}
	cats := map[string]int{}
	catTargets := map[string]int{}
	whys := map[string]int{}
	perTarget := map[string]int{}
	readers := map[string]int{}
	outsideWaiters := map[*Span][]*Span{}
	for _, s := range full.Spans.Order {
		for _, l := range s.Links {
			if !l.IsWait() {
				continue
			}
			if t := full.Spans.Map[l.SpanContext.SpanID]; t != nil && t != s && !spanInSubtree(t, s) {
				outsideWaiters[t] = append(outsideWaiters[t], s)
			}
		}
	}
	samples := 0
	var sampleOut strings.Builder
	escWhy := map[string]int{}
	for _, s := range full.Spans.Order {
		beneath := 0
		reachEsc := false
		dc := depth(s)
		ci, cok := f.lookup(s.ID)
		for _, l := range s.Links {
			if !l.IsWait() {
				continue
			}
			t := full.Spans.Map[l.SpanContext.SpanID]
			if t == nil || t == s || !spanInSubtree(t, s) || noopSelfWait(s, l) {
				continue
			}
			beneath++
			if d, ok := minLca[t]; ok && d < dc {
				reachEsc = true
			}
			ti, tok := f.lookup(t.ID)
			if !cok || !tok {
				perTarget["not in fold"]++
				continue
			}
			tst := f.state[ti]
			var why string
			switch {
			case tst&foldRetained == 0:
				why = "folded"
			case !f.unread(ci):
				why = "carrier (or twin parent) read"
			case tst&foldEscapes != 0:
				why = "target unsealed"
				escWhy[firstEsc[t]]++
			case isLazyEvaluationName(t.Name):
				why = "target lazy evaluation"
			case t.ParentSpan != nil && t.ParentSpan != s && func() bool {
				pi, ok := f.lookup(t.ParentSpan.ID)
				return ok && f.state[pi]&foldEscapes != 0
			}():
				why = "target's parent unsealed"
				escWhy["parent: "+firstEsc[t.ParentSpan]]++
			case tst&foldRead != 0:
				why = "target read (waited on from outside its carrier)"
				if samples < 6 {
					samples++
					fmt.Fprintf(&sampleOut, "  read sample: %s %s: %s\n", t.Name, t.ID, zzReadBy[ti])
				}
				if by, ok := zzReadBy[ti]; ok {
					k := by
					if i := strings.Index(k, ";"); i > 0 {
						k = k[:i]
					}
					readers["read by "+k]++
				} else {
					readers["read by propagation"]++
				}
				for _, a := range insideWaiters[t] {
					if a == s {
						continue
					}
					ai, _ := f.lookup(a.ID)
					if f.state[ai]&foldRead == 0 {
						continue
					}
					rel := "unrelated"
					if spanInSubtree(s, a) {
						rel = "ancestor of carrier"
					}
					readers[fmt.Sprintf("also waited on by read %s (%s), read because %s", short(a.Name), rel, readRoot(a, 0))]++
				}
				for _, x := range outsideWaiters[t] {
					shown := shownBlocker(t, x)
					rel := "elsewhere"
					switch {
					case spanInSubtree(s, x):
						rel = "waiter holds carrier"
					case spanInSubtree(x, s):
						rel = "waiter inside carrier"
					}
					sh := "target itself"
					if shown != t {
						sh = "shown as ancestor " + short(shown.Name)
						if spanInSubtree(s, shown) && shown != s {
							sh = "shown as ancestor above carrier " + short(shown.Name)
						} else if shown == s {
							sh = "shown as carrier"
						}
					}
					xi, _ := f.lookup(x.ID)
					readers[fmt.Sprintf("%s by %s (%s; %s; waiter retained=%v)", short(t.Name), short(x.Name), rel, sh, f.state[xi]&foldRetained != 0)]++
				}
			default:
				why = "retained otherwise (policy/ordering)"
				if samples < 6 {
					samples++
					retainedKids := 0
					for c := f.firstChild[ti]; c != 0; c = f.nextSib[c-1] {
						if f.state[c-1]&foldRetained != 0 {
							retainedKids++
						}
					}
					pn := ""
					if t.ParentSpan != nil {
						pn = t.ParentSpan.Name
					}
					fmt.Fprintf(&sampleOut, "  sample: carrier %s %s (state %b) -> target %s %s parent %q state %b; retained kids %d; shouldRetain=%v needed=%v policy=%v kids=%d\n",
						s.Name, s.ID, f.state[ci], t.Name, t.ID, pn, tst, retainedKids, f.shouldRetain(ti), f.neededForWaits(ti), f.opts.Retain(FoldSpan{f, ti}), t.ChildSpans.Len())
				}
			}
			perTarget[why]++
		}
		if beneath == 0 {
			continue
		}
		i, ok := f.lookup(s.ID)
		if !ok {
			cats["not in fold"]++
			continue
		}
		st := f.state[i]
		_, trueEsc := firstEsc[s]
		var cat string
		switch {
		case st&foldContainedWaits != 0:
			cat = "contained"
		case st&foldEscapes != 0 && !trueEsc:
			cat = "escapes (ordering only)"
		case st&foldEscapes != 0 && !reachEsc:
			cat = "escapes (not from targets' subtrees)"
			whys[firstEsc[s]]++
		case st&foldEscapes != 0:
			cat = "escapes (truly, from a target's subtree)"
		case st&foldRead != 0:
			cat = "read"
		default:
			cat = "other"
		}
		if st&foldRead != 0 && cat != "read" {
			cat += " +read"
		}
		if st&foldRead != 0 {
			readWhy[readRoot(s, 0)] += beneath
		}
		cats[cat]++
		catTargets[cat] += beneath
	}
	var out strings.Builder
	fmt.Fprintf(&out, "targets of waits beneath their carrier (not no-op), by fold state:\n")
	for k, v := range perTarget {
		fmt.Fprintf(&out, "  %-45s %9d\n", k, v)
	}
	out.WriteString(sampleOut.String())
	{
		type kv struct {
			k string
			v int
		}
		var top []kv
		for k, v := range readers {
			top = append(top, kv{k, v})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].v > top[j].v })
		fmt.Fprintf(&out, "outside waiters on read targets (target by waiter; relation; how the waiter's row shows it):\n")
		for _, e := range top[:min(15, len(top))] {
			fmt.Fprintf(&out, "  %8d  %s\n", e.v, e.k)
		}
	}
	{
		type kv struct {
			k string
			v int
		}
		var top []kv
		for k, v := range escWhy {
			top = append(top, kv{k, v})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].v > top[j].v })
		fmt.Fprintf(&out, "first edge crossing unsealed targets:\n")
		for _, e := range top[:min(15, len(top))] {
			fmt.Fprintf(&out, "  %8d  %s\n", e.v, e.k)
		}
	}
	fmt.Fprintf(&out, "carriers of waits beneath them (not no-op), by fold state:\n")
	for k, v := range cats {
		fmt.Fprintf(&out, "  %-35s %8d carriers %9d targets\n", k, v, catTargets[k])
	}
	type kv struct {
		k string
		v int
	}
	var top []kv
	for k, v := range whys {
		top = append(top, kv{k, v})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].v > top[j].v })
	fmt.Fprintf(&out, "first escaping edge of truly escaping carriers:\n")
	for _, e := range top[:min(20, len(top))] {
		fmt.Fprintf(&out, "  %6d  %s\n", e.v, e.k)
	}
	top = top[:0]
	for k, v := range readWhy {
		top = append(top, kv{k, v})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].v > top[j].v })
	fmt.Fprintf(&out, "why read carriers are read (weighted by their targets):\n")
	for _, e := range top[:min(20, len(top))] {
		fmt.Fprintf(&out, "  %6d  %s\n", e.v, e.k)
	}
	return out.String()
}
