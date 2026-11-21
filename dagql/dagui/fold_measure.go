package dagui

import (
	"fmt"
	"sort"
	"strings"

	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// FoldCount is a named count of spans.
type FoldCount struct {
	Name  string
	Spans int
}

// FoldBreakdown is what a fold retains, and why (see Fold.Breakdown).
type FoldBreakdown struct {
	// Spans counts the spans received; Retained those retained.
	Spans, Retained int
	// Reasons attributes each retained span to the first reason, in this
	// order, that retains it.
	Reasons []FoldCount
	// WhatIf is the retained count under variants of the policy, computed
	// from the stubs: the spans the variant retains and their ancestors.
	WhatIf []FoldCount
	// WhatIfReasons is, by variant, why it retains what it does.
	WhatIfReasons map[string][]FoldCount
	// FoldedCalls counts the calls kept for folded spans (keepCall).
	FoldedCalls int
}

// foldVariant is a what-if of the retention rules (see Fold.Breakdown).
type foldVariant struct {
	name string
	// waitSidecar: wait links live in a sidecar, so neither their carrier
	// nor their target is retained for them.
	waitSidecar bool
	// causeSidecar: likewise for cause links.
	causeSidecar bool
	// kindsMayRollUp: surfaced kinds are retained only where they may roll
	// up (not beneath a boundary or encapsulation).
	kindsMayRollUp bool
	// noopWaitsFolded: waits no row reads (noopSelfWait) retain neither
	// end.
	noopWaitsFolded bool
}

// Breakdown reports what the fold retains and why, and what variants of its
// rules would retain. A diagnostic: it walks every stub and retained span.
func (f *Fold) Breakdown() FoldBreakdown {
	var out FoldBreakdown
	out.FoldedCalls = f.keptCalls
	targets, links, realWaits := f.retainedLinks()
	f.breakdownReasons(&out, targets, links, realWaits)
	return out
}

// KeptWithoutLinks reports, for a span, whether the fold would retain it
// if neither wait nor cause links retained anything.
func (f *Fold) KeptWithoutLinks() func(SpanID) bool {
	_, links, realWaits := f.retainedLinks()
	_, _, kept := f.simulate(foldVariant{waitSidecar: true, causeSidecar: true}, links, realWaits)
	return func(id SpanID) bool {
		i, ok := f.lookup(id)
		return ok && kept[i]
	}
}

// retainedLinks gathers the links of the retained spans: the purposes each
// target is linked with, each span's links, and the spans with waits rows
// read.
func (f *Fold) retainedLinks() (map[uint32]int, map[uint32][]foldLink, map[uint32]bool) {
	// the targets of retained spans' links, by purpose
	targets := map[uint32]int{}
	links := map[uint32][]foldLink{}
	// realWaits holds the carriers of a wait the waiting below rows reads:
	// one that isn't a call's wait on its own execution twin
	realWaits := map[uint32]bool{}
	for i, st := range f.state {
		if st&(foldReceived|foldRetained) != foldReceived|foldRetained {
			continue
		}
		span := f.db.Spans.Map[f.spans[i]]
		if span == nil {
			continue
		}
		add := func(purpose int, kind string, id SpanID) {
			j, ok := f.lookup(id)
			if !ok || j == uint32(i) {
				return
			}
			target := span.db.Spans.Map[id]
			switch {
			case target == nil || !target.Received:
			case spanInSubtree(target, span):
				kind += ", target beneath"
			case spanInSubtree(span, target):
				kind += ", target an ancestor"
			default:
				kind += ", target elsewhere"
			}
			targets[j] |= purpose &^ targetWaitNoop
			links[uint32(i)] = append(links[uint32(i)], foldLink{j, purpose, kind})
		}
		for _, link := range span.Links {
			id := link.SpanContext.SpanID
			switch link.Purpose {
			case telemetry.LinkPurposeCause, "":
				kind := "cause link from a plain span"
				switch {
				case strings.HasPrefix(span.Name, "resume "):
					kind = "cause link from a resume"
				case span.isLazyEvaluation():
					kind = "cause link from a lazy evaluation"
				}
				add(targetCause, kind, id)
			case telemetryattrs.LinkPurposeWait:
				if noopSelfWait(span, link) {
					add(targetWait|targetWaitNoop, "self-wait", id)
				} else {
					realWaits[uint32(i)] = true
					add(targetWait, "wait reason="+link.WaitReason, id)
				}
			case telemetry.LinkPurposeErrorOrigin:
				add(targetOrigin, "error origin", id)
			}
		}
		if span.Status.Code != 0 {
			for _, origin := range telemetry.ParseErrorOrigins(span.Status.Description) {
				add(targetOrigin, "error origin", SpanID{SpanID: origin.SpanID()})
			}
		}
	}
	return targets, links, realWaits
}

func (f *Fold) breakdownReasons(out *FoldBreakdown, targets map[uint32]int, links map[uint32][]foldLink, realWaits map[uint32]bool) {
	reasons := []string{
		"root", "child of an expanded span", "agent",
		"surfaced kind (may roll up)", "surfaced kind (beneath a boundary)",
		"failed/revealed/progress", "cause-link carrier", "wait-link carrier",
		"module plumbing / exec beneath it", "error-origin target",
		"cause-link target", "wait-link target", "ancestor only",
	}
	counts := make([]int, len(reasons))
	for i, st := range f.state {
		if st&foldReceived == 0 {
			continue
		}
		out.Spans++
		if st&foldRetained == 0 {
			continue
		}
		out.Retained++
		s := FoldSpan{f, uint32(i)}
		p := f.parent[i]
		var r int
		switch {
		case p == 0:
			r = 0
		case f.state[p-1]&(foldExpanded|foldOpenAll) != 0:
			r = 1
		case st&foldAgent != 0:
			r = 2
		case st&foldKind != 0 && s.MayRollUp():
			r = 3
		case st&foldKind != 0:
			r = 4
		case (s.Failed() || s.Reveal() || s.Progress()) && s.MayRollUp():
			r = 5
		case st&foldCauseLinks != 0:
			r = 6
		case st&foldWaitLinks != 0:
			r = 7
		case f.neededForWaits(uint32(i)):
			r = 8
		case targets[uint32(i)]&targetOrigin != 0:
			r = 9
		case targets[uint32(i)]&targetCause != 0:
			r = 10
		case targets[uint32(i)]&targetWait != 0:
			r = 11
		default:
			r = 12
		}
		counts[r]++
	}
	for i, name := range reasons {
		out.Reasons = append(out.Reasons, FoldCount{name, counts[i]})
	}

	for _, v := range []foldVariant{
		{name: "as built"},
		{name: "no-op self-waits folded", noopWaitsFolded: true},
		{name: "no-op self-waits folded + cause links in the sidecar", noopWaitsFolded: true, causeSidecar: true},
		{name: "wait links in the sidecar", waitSidecar: true},
		{name: "wait + cause links in the sidecar", waitSidecar: true, causeSidecar: true},
		{name: "surfaced kinds beneath boundaries as stubs", kindsMayRollUp: true},
		{name: "all three", waitSidecar: true, causeSidecar: true, kindsMayRollUp: true},
	} {
		n, why, _ := f.simulate(v, links, realWaits)
		out.WhatIf = append(out.WhatIf, FoldCount{v.name, n})
		if out.WhatIfReasons == nil {
			out.WhatIfReasons = map[string][]FoldCount{}
		}
		for reason, count := range why {
			out.WhatIfReasons[v.name] = append(out.WhatIfReasons[v.name], FoldCount{reason, count})
		}
		sort.Slice(out.WhatIfReasons[v.name], func(a, b int) bool {
			return out.WhatIfReasons[v.name][a].Spans > out.WhatIfReasons[v.name][b].Spans
		})
	}
}

// LinkBreakdown classifies the wait and cause links of the spans db holds:
// by reason, by where the target sits relative to the carrier, and whether
// the waiting below rows reads them at all. A diagnostic.
func (db *DB) LinkBreakdown() []FoldCount {
	counts := map[string]int{}
	carriers := map[string]map[*Span]bool{}
	targets := map[string]map[*Span]bool{}
	note := func(key string, carrier, target *Span) {
		counts[key]++
		if carriers[key] == nil {
			carriers[key] = map[*Span]bool{}
			targets[key] = map[*Span]bool{}
		}
		carriers[key][carrier] = true
		if target != nil {
			targets[key][target] = true
		}
	}
	for _, span := range db.Spans.Order {
		for _, link := range span.Links {
			target := db.Spans.Map[link.SpanContext.SpanID]
			var where string
			switch {
			case target == nil || !target.Received:
				where = "target not in scope"
			case target.ParentSpan == span && target.Name == span.Name:
				where = "target is its same-name child (exec twin)"
			case spanInSubtree(target, span):
				where = "target beneath it"
			case spanInSubtree(span, target):
				where = "target is an ancestor"
			case target.ParentSpan == span.ParentSpan:
				where = "target is a sibling"
			default:
				where = "target elsewhere"
			}
			switch link.Purpose {
			case telemetryattrs.LinkPurposeWait:
				reason := link.WaitReason
				if reason == "" {
					reason = "?"
				}
				skipped := (link.WaitReason == "call_exec" || link.WaitReason == "singleflight") &&
					target != nil && target.Name == span.Name && spanInSubtree(target, span)
				note("wait reason="+reason, span, target)
				note("wait: "+where, span, target)
				if skipped {
					note("wait: self-wait (skipped by collectOwnWaits when the carrier is the row)", span, target)
				}
				if !link.WaitEnd.After(link.WaitStart) {
					note("wait: empty window", span, target)
				}
			case telemetry.LinkPurposeCause, "":
				kind := "plain"
				switch {
				case strings.HasPrefix(span.Name, "resume "):
					kind = "resume"
				case span.isLazyEvaluation():
					kind = "lazy evaluation"
				}
				note("cause carrier "+kind+": "+where, span, target)
			case telemetry.LinkPurposeErrorOrigin:
				note("error origin: "+where, span, target)
			default:
				note("other purpose "+link.Purpose, span, target)
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
		out = append(out, FoldCount{fmt.Sprintf("%s (carriers %d, targets %d)", k, len(carriers[k]), len(targets[k])), counts[k]})
	}
	return out
}

// CauseBreakdown classifies the cause links of the spans db holds by what
// they feed -- failure, cancellation, pending resolution, activity beyond
// the cause's ancestors' own intervals -- and by where their ends meet,
// relative to what folded (the spans folded retains): a link whose ends
// meet beneath a folded span, and that feeds nothing a row shows from
// outside, is a candidate for folding both ends. A diagnostic.
func (db *DB) CauseBreakdown(kept func(SpanID) bool) []FoldCount {
	counts := map[string]int{}
	var total int
	for _, effect := range db.Spans.Order {
		for _, link := range effect.Links {
			if link.Purpose != telemetry.LinkPurposeCause && link.Purpose != "" {
				continue
			}
			total++
			cause := db.Spans.Map[link.SpanContext.SpanID]
			if cause == nil || !cause.Received {
				counts["cause not in scope"]++
				continue
			}
			var lca *Span
			for a := effect; a != nil && lca == nil; a = a.ParentSpan {
				if spanInSubtree(cause, a) {
					lca = a
				}
			}
			var feeds []string
			if effect.IsFailed() {
				feeds = append(feeds, "failure")
			}
			if effect.IsCanceled() {
				feeds = append(feeds, "cancellation")
			}
			if effect.Blocked || effect.Partial || cause.Pending {
				feeds = append(feeds, "pending/blocked")
			}
			if effect.installedAt(cause) {
				feeds = append(feeds, "install site")
			}
			// the effect's time is credited to the cause and all its
			// ancestors (PropagateStatusToParentsAndLinks)
			for a := cause; a != nil; a = a.ParentSpan {
				if effect.StartTime.Before(a.StartTime) || effect.EndTime.After(a.EndTime) || effect.IsRunning() {
					feeds = append(feeds, "activity beyond the cause's ancestors")
					break
				}
			}
			where := "ends meet at no common ancestor"
			if lca != nil {
				where = "ends meet beneath a span retained without links"
				if !kept(lca.ID) {
					where = "ends meet beneath a span folded without links"
				} else if lca == effect || lca == cause {
					where = "one end is the other's ancestor"
				}
			}
			kind := "plain"
			switch {
			case strings.HasPrefix(effect.Name, "resume "):
				kind = "resume"
			case effect.isLazyEvaluation():
				kind = "lazy evaluation"
			}
			feed := "feeds only roll-ups and placement"
			if len(feeds) > 0 {
				feed = "feeds " + strings.Join(feeds, "+")
			}
			counts[kind+"; "+where+"; "+feed]++
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return counts[keys[a]] > counts[keys[b]] })
	out := []FoldCount{{"cause links", total}}
	for _, k := range keys {
		out = append(out, FoldCount{k, counts[k]})
	}
	return out
}

// the purposes of the links Breakdown follows
const (
	targetCause = 1 << iota
	targetWait
	targetOrigin
	// targetWaitNoop marks a wait no row's waiting reads (noopSelfWait)
	targetWaitNoop
)

// noopSelfWait reports whether link, a wait of span, is one no row's waiting
// reads: a call waiting on its own execution twin, its same-name descendant
// (see collectOwnWaits), on a span that isn't the deferred evaluation of
// another (whose waits rows of other names read).
func noopSelfWait(span *Span, link SpanLink) bool {
	if link.WaitReason != "call_exec" && link.WaitReason != "singleflight" {
		return false
	}
	if span.causesViaLinks.Len() > 0 {
		return false
	}
	if p := span.ParentSpan; p != nil && p.Name == span.Name && p.causesViaLinks.Len() > 0 {
		// the twin of a deferred evaluation, whose waits the evaluated
		// call's row reads
		return false
	}
	target := span.db.Spans.Map[link.SpanContext.SpanID]
	return target != nil && target.Name == span.Name && spanInSubtree(target, span)
}

type foldLink struct {
	target  uint32
	purpose int
	kind    string
}

// simulate counts the received spans variant v retains: its seeds, the
// targets of their links it keeps, and the ancestors of both; and why each
// is retained.
func (f *Fold) simulate(v foldVariant, links map[uint32][]foldLink, realWaits map[uint32]bool) (int, map[string]int, []bool) {
	follows := func(l foldLink) bool {
		switch {
		case l.purpose == targetCause && v.causeSidecar,
			l.purpose&targetWait != 0 && v.waitSidecar,
			l.purpose&targetWaitNoop != 0 && v.noopWaitsFolded:
			return false
		}
		return true
	}
	carrierKind := func(i uint32) string {
		for _, l := range links[i] {
			if follows(l) && l.purpose&targetOrigin == 0 {
				return "carrier of a " + l.kind
			}
		}
		return "link carrier"
	}
	seed := func(i uint32) string {
		st := f.state[i]
		s := FoldSpan{f, i}
		p := f.parent[i]
		switch {
		case p == 0:
			return "root"
		case f.state[p-1]&(foldExpanded|foldOpenAll) != 0:
			return "child of an expanded span"
		case st&foldAgent != 0:
			return "agent"
		case st&foldKind != 0 && (!v.kindsMayRollUp || s.MayRollUp()):
			return "surfaced kind"
		case (s.Failed() || s.Reveal() || s.Progress()) && s.MayRollUp():
			return "failed/revealed/progress"
		case st&foldCauseLinks != 0 && !v.causeSidecar:
			return carrierKind(i)
		case st&foldWaitLinks != 0 && !v.waitSidecar && (!v.noopWaitsFolded || realWaits[i]):
			return carrierKind(i)
		case st&foldFunctionPlumbing != 0:
			return "module plumbing"
		case st&foldExecOp != 0 && f.neededForWaits(i):
			return "exec beneath plumbing"
		}
		return ""
	}
	kept := make([]bool, len(f.state))
	why := make([]string, len(f.state))
	var stack []uint32
	for i, st := range f.state {
		if st&foldReceived == 0 {
			continue
		}
		if r := seed(uint32(i)); r != "" {
			why[i] = r
			stack = append(stack, uint32(i))
		}
	}
	var n int
	reasons := map[string]int{}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if kept[i] {
			continue
		}
		kept[i] = true
		if f.state[i]&foldReceived != 0 {
			n++
			reasons[why[i]]++
		}
		if p := f.parent[i]; p != 0 && !kept[p-1] {
			if why[p-1] == "" {
				why[p-1] = "ancestor only"
			}
			stack = append(stack, p-1)
		}
		for _, l := range links[i] {
			if !follows(l) || kept[l.target] {
				continue
			}
			if why[l.target] == "" {
				why[l.target] = "target of a " + l.kind
			}
			stack = append(stack, l.target)
		}
	}
	return n, reasons, kept
}
