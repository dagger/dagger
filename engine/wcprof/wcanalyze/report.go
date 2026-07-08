package wcanalyze

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"
)

// ClassStats aggregates ops of one class.
type ClassStats struct {
	Key      ClassKey
	WorkType string
	Count    int
	Outcomes map[string]int

	TotalWallNS int64
	TotalSelfNS int64
	MaxSelfNS   int64
	P50SelfNS   int64
	P95SelfNS   int64

	// DupExecuted counts ops beyond the first execution per ident (same
	// operation executed more than once).
	DupExecuted int
}

// AggregateClasses computes per-class statistics over all ops.
func AggregateClasses(g *Graph) []*ClassStats {
	byKey := make(map[ClassKey]*ClassStats)
	selfSamples := make(map[ClassKey][]int64)
	executedIdents := make(map[ClassKey]map[string]int)

	for _, op := range g.Ops {
		key := op.Key()
		st := byKey[key]
		if st == nil {
			st = &ClassStats{Key: key, Outcomes: make(map[string]int), WorkType: op.WorkType}
			byKey[key] = st
		}
		st.Count++
		st.Outcomes[op.Outcome]++
		st.TotalWallNS += op.Duration()
		self := op.SelfNS()
		st.TotalSelfNS += self
		st.MaxSelfNS = max(st.MaxSelfNS, self)
		selfSamples[key] = append(selfSamples[key], self)
		if op.Outcome == "executed" || op.Kind == "call_exec" {
			if op.Ident != "" {
				if executedIdents[key] == nil {
					executedIdents[key] = make(map[string]int)
				}
				executedIdents[key][op.Ident]++
			}
		}
	}

	out := make([]*ClassStats, 0, len(byKey))
	for key, st := range byKey {
		samples := selfSamples[key]
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		if len(samples) > 0 {
			st.P50SelfNS = samples[len(samples)/2]
			st.P95SelfNS = samples[(len(samples)*95)/100]
		}
		for _, n := range executedIdents[key] {
			if n > 1 {
				st.DupExecuted += n - 1
			}
		}
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b *ClassStats) int {
		if a.TotalSelfNS != b.TotalSelfNS {
			return int(b.TotalSelfNS - a.TotalSelfNS)
		}
		return strings.Compare(a.Key.String(), b.Key.String())
	})
	return out
}

// DeadAir returns gaps (longer than threshold) inside the trace where no
// recorded op was running.
func DeadAir(g *Graph, thresholdNS int64) []segment {
	intervals := make([]segment, 0, len(g.Ops))
	for _, op := range g.Ops {
		intervals = append(intervals, segment{op.StartNS, op.EndNS})
	}
	if len(intervals) == 0 {
		return nil
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].Start < intervals[j].Start })
	var gaps []segment
	cursor := intervals[0].Start
	for _, iv := range intervals {
		if iv.Start > cursor && iv.Start-cursor >= thresholdNS {
			gaps = append(gaps, segment{cursor, iv.Start})
		}
		cursor = max(cursor, iv.End)
	}
	return gaps
}

func fmtDur(ns int64) string {
	d := time.Duration(ns)
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	case d >= time.Microsecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	default:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	}
}

// ReportOptions controls WriteReport.
type ReportOptions struct {
	TopClasses     int
	WhatIfFactors  []float64
	MinClassSelfNS int64
	DeadAirMinNS   int64
	ChainDepth     int
}

func (o *ReportOptions) defaults() {
	if o.TopClasses == 0 {
		o.TopClasses = 30
	}
	if len(o.WhatIfFactors) == 0 {
		o.WhatIfFactors = []float64{0, 0.5, 0.9}
	}
	if o.MinClassSelfNS == 0 {
		o.MinClassSelfNS = int64(time.Millisecond)
	}
	if o.DeadAirMinNS == 0 {
		o.DeadAirMinNS = int64(50 * time.Millisecond)
	}
	if o.ChainDepth == 0 {
		o.ChainDepth = 25
	}
}

// WriteReport renders the full human-readable analysis.
func WriteReport(w io.Writer, g *Graph, opts ReportOptions) error {
	opts.defaults()

	fmt.Fprintf(w, "wcprof analysis\n")
	fmt.Fprintf(w, "===============\n\n")
	fmt.Fprintf(w, "ops: %d  roots: %d  open at dump: %d  dropped events: %d\n",
		len(g.Ops), len(g.Roots), g.OpenOps, g.DroppedEvents)
	if g.DroppedEvents > 0 {
		fmt.Fprintf(w, "WARNING: %d events were dropped (buffer cap); analysis is incomplete\n", g.DroppedEvents)
	}
	actual := ActualMakespanNS(g)
	fmt.Fprintf(w, "trace span: %s   makespan over roots: %s\n\n", fmtDur(g.TraceEndNS-g.TraceStartNS), fmtDur(actual))

	// Baseline + what-ifs.
	baseSim := NewSimulation(g, nil)
	if _, err := baseSim.Run(); err == nil && (baseSim.CycleWarnings > 0 || baseSim.FallbackAnchors > 0) {
		fmt.Fprintf(w, "sim diagnostics: %d broken cycles, %d fallback anchors\n", baseSim.CycleWarnings, baseSim.FallbackAnchors)
	}
	baseline, whatIfs, err := RunWhatIfs(g, opts.WhatIfFactors, opts.MinClassSelfNS)
	if err != nil {
		fmt.Fprintf(w, "simulation unavailable: %v\n\n", err)
	} else {
		drift := float64(0)
		if actual > 0 {
			drift = 100 * float64(baseline-actual) / float64(actual)
		}
		fmt.Fprintf(w, "simulated baseline makespan: %s (drift vs actual: %+.1f%%)\n\n", fmtDur(baseline), drift)

		fmt.Fprintf(w, "what-if: makespan saved if a class's self-time is scaled by factor f\n")
		fmt.Fprintf(w, "(ranked by saving at f=%.2g; this is the bottleneck candidate list)\n\n", midFactor(opts.WhatIfFactors))
		fmt.Fprintf(w, "%-55s %-8s", "class", "worktype")
		for _, f := range opts.WhatIfFactors {
			fmt.Fprintf(w, " %12s", fmt.Sprintf("save@%.2g", f))
		}
		fmt.Fprintf(w, "\n")

		stats := AggregateClasses(g)
		workTypeByKey := make(map[ClassKey]string, len(stats))
		for _, st := range stats {
			workTypeByKey[st.Key] = st.WorkType
		}

		mid := midFactor(opts.WhatIfFactors)
		slices.SortFunc(whatIfs, func(a, b WhatIfResult) int {
			if a.SavedNS[mid] != b.SavedNS[mid] {
				return int(b.SavedNS[mid] - a.SavedNS[mid])
			}
			return strings.Compare(a.Key.String(), b.Key.String())
		})
		shown := 0
		for _, res := range whatIfs {
			if shown >= opts.TopClasses {
				break
			}
			if res.SavedNS[mid] <= 0 && shown > 5 {
				continue
			}
			fmt.Fprintf(w, "%-55s %-8s", truncate(res.Key.String(), 55), workTypeByKey[res.Key])
			for _, f := range opts.WhatIfFactors {
				fmt.Fprintf(w, " %12s", fmtDur(res.SavedNS[f]))
			}
			fmt.Fprintf(w, "\n")
			shown++
		}
		fmt.Fprintf(w, "\n")
	}

	// Class table.
	fmt.Fprintf(w, "top classes by total self-time\n\n")
	fmt.Fprintf(w, "%-55s %-8s %8s %12s %12s %10s %10s %10s %s\n",
		"class", "worktype", "count", "total-self", "total-wall", "p50-self", "p95-self", "max-self", "outcomes")
	stats := AggregateClasses(g)
	for i, st := range stats {
		if i >= opts.TopClasses {
			break
		}
		var outcomes []string
		for o, n := range st.Outcomes {
			if o == "" {
				o = "?"
			}
			outcomes = append(outcomes, fmt.Sprintf("%s:%d", o, n))
		}
		sort.Strings(outcomes)
		dup := ""
		if st.DupExecuted > 0 {
			dup = fmt.Sprintf(" dup-exec:%d", st.DupExecuted)
		}
		fmt.Fprintf(w, "%-55s %-8s %8d %12s %12s %10s %10s %10s %s%s\n",
			truncate(st.Key.String(), 55), st.WorkType, st.Count,
			fmtDur(st.TotalSelfNS), fmtDur(st.TotalWallNS),
			fmtDur(st.P50SelfNS), fmtDur(st.P95SelfNS), fmtDur(st.MaxSelfNS),
			strings.Join(outcomes, " "), dup)
	}
	fmt.Fprintf(w, "\n")

	// Blocking chain.
	chain := BlockingChain(g, opts.ChainDepth)
	if len(chain) > 1 {
		fmt.Fprintf(w, "end-of-workload blocking chain (latest-finishing path)\n\n")
		for _, op := range chain {
			fmt.Fprintf(w, "  %-12s %-50s dur=%-10s self=%-10s [%s..%s]\n",
				op.Kind, truncate(op.Class, 50), fmtDur(op.Duration()), fmtDur(op.SelfNS()),
				fmtDur(op.StartNS-g.TraceStartNS), fmtDur(op.EndNS-g.TraceStartNS))
		}
		fmt.Fprintf(w, "\n")
	}

	// Dead air.
	gaps := DeadAir(g, opts.DeadAirMinNS)
	if len(gaps) > 0 {
		fmt.Fprintf(w, "dead air (no recorded op running; uninstrumented blocking or client-side stalls)\n\n")
		for _, gap := range gaps {
			fmt.Fprintf(w, "  %s gap at +%s\n", fmtDur(gap.End-gap.Start), fmtDur(gap.Start-g.TraceStartNS))
		}
		fmt.Fprintf(w, "\n")
	}

	// Orphans / diagnostics.
	if len(g.OrphanWaits) > 0 {
		var total int64
		for _, ow := range g.OrphanWaits {
			total += ow.Duration()
		}
		fmt.Fprintf(w, "diagnostics: %d waits with no owning op (total %s)\n", len(g.OrphanWaits), fmtDur(total))
	}
	return nil
}

func midFactor(factors []float64) float64 {
	if len(factors) == 0 {
		return 0.5
	}
	return factors[len(factors)/2]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
