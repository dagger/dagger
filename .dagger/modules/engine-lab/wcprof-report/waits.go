package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// ---- waits -----------------------------------------------------------------

type waitAgg struct {
	reason, waiter, target string
	durs                   []int64
	total                  int64
	waiters                map[string]bool
}

func (a *waitAgg) add(w *Wait, waiter string) {
	a.durs = append(a.durs, w.Dur())
	a.total += w.Dur()
	a.waiters[waiter] = true
}

func waitTarget(w *Wait) string {
	switch {
	case w.Target != nil:
		return w.Target.Key()
	case w.Ident != "":
		return w.Ident
	}
	return "(unrecorded)"
}

func waitWaiter(w *Wait) string {
	if w.Waiter == nil {
		return "(unrecorded)"
	}
	return w.Waiter.Key()
}

// waitsView aggregates recorded waits: who blocks on what, for how long.
// A call waiting on its own call_exec child is left out: every executed
// call does that, and the time already shows as the child's.
func waitsView(o *out, g *Graph, opts Options) {
	var (
		ws       []*Wait
		ownExec  int
		byReason = map[string]*waitAgg{}
		byTarget = map[string]*waitAgg{}
		byPair   = map[string]*waitAgg{}
	)
	for _, w := range g.Waits {
		if !opts.Filter.Empty() && !opts.Filter.Match(w.Waiter) {
			continue
		}
		if w.Target != nil && w.Waiter != nil && w.Target.Parent == w.Waiter {
			ownExec++
			continue
		}
		ws = append(ws, w)
	}
	get := func(m map[string]*waitAgg, reason, waiter, target string) *waitAgg {
		k := reason + "\x00" + waiter + "\x00" + target
		a := m[k]
		if a == nil {
			a = &waitAgg{reason: reason, waiter: waiter, target: target, waiters: map[string]bool{}}
			m[k] = a
		}
		return a
	}
	for _, w := range ws {
		waiter, target := waitWaiter(w), waitTarget(w)
		get(byReason, w.Reason, "", "").add(w, waiter)
		get(byTarget, w.Reason, "", target).add(w, waiter)
		get(byPair, w.Reason, waiter, target).add(w, waiter)
	}
	var total int64
	for _, w := range ws {
		total += w.Dur()
	}
	o.printf("waits: %d (waiters matching %s), %s summed across concurrent waits", len(ws), opts.Filter, fmtDur(total))
	if ownExec > 0 {
		o.printf("(left out: %d waits of a call on its own call_exec child; that time shows as the child's)", ownExec)
	}
	if len(ws) == 0 {
		return
	}
	reasons := sortedWaitAggs(byReason)
	parts := make([]string, 0, len(reasons))
	for _, a := range reasons {
		parts = append(parts, fmt.Sprintf("%s %d (%s)", a.reason, len(a.durs), fmtDur(a.total)))
	}
	o.printf("by reason: %s", strings.Join(parts, ", "))

	o.printf("")
	o.printf("by target (what is waited on):")
	o.printf("  %7s %7s %10s %9s %9s  %-12s %s", "count", "waiters", "total", "p50", "max", "reason", "target")
	for i, a := range sortedWaitAggs(byTarget) {
		if i == opts.Top {
			o.printf("  … %d more targets (raise `top`)", len(byTarget)-opts.Top)
			break
		}
		o.printf("  %7d %7d %10s %9s %9s  %-12s %s", len(a.durs), len(a.waiters), fmtDur(a.total),
			fmtDur(quantile(a.durs, 0.5)), fmtDur(slices.Max(a.durs)), a.reason, a.target)
	}

	o.printf("")
	o.printf("by waiter → target:")
	o.printf("  %7s %10s %9s %9s  %-12s %s", "count", "total", "p50", "max", "reason", "waiter → target")
	for i, a := range sortedWaitAggs(byPair) {
		if i == opts.Top {
			o.printf("  … %d more pairs (raise `top`)", len(byPair)-opts.Top)
			break
		}
		o.printf("  %7d %10s %9s %9s  %-12s %s → %s", len(a.durs), fmtDur(a.total),
			fmtDur(quantile(a.durs, 0.5)), fmtDur(slices.Max(a.durs)), a.reason, a.waiter, a.target)
	}
	o.printf("(waiters: distinct waiting classes; see a waiter's context with tree/children and `op`)")
}

func sortedWaitAggs(m map[string]*waitAgg) []*waitAgg {
	aggs := make([]*waitAgg, 0, len(m))
	for _, a := range m {
		aggs = append(aggs, a)
	}
	slices.SortFunc(aggs, func(a, b *waitAgg) int {
		return cmp.Or(cmpInt64(b.total, a.total), cmp.Compare(a.reason, b.reason),
			cmp.Compare(a.target, b.target), cmp.Compare(a.waiter, b.waiter))
	})
	return aggs
}
