package wcanalyze

import (
	"fmt"
	"slices"
	"time"
)

// The replay simulator re-executes the recorded op graph as a discrete-event
// schedule under counterfactual hypotheses ("class X's self-time scaled by
// f"), assuming unlimited resources (never CPU/disk bound).
//
// Each op is replayed as its chronological timeline of actions:
//
//   - self segments: advance the op's clock by the (scaled) duration
//   - child spawns: anchor the child's simulated start at the current clock
//   - waits: clock = max(clock, simulated finish of the target); waits on
//     named resources (locks etc.) are kept as fixed delays
//   - implicit joins: whenever the op reaches an action at original time t,
//     it first joins every child that had originally ended by t. This bakes
//     the observed ordering in as a constraint, which correctly models
//     synchronous child calls (no explicit wait edge exists for plain
//     function calls) and is conservatively safe for async children.
//
// Roots are chained by preserving original idle gaps between strictly
// sequential roots (e.g. successive queries from the CLI).
type Simulation struct {
	g *Graph
	// Factors scales self-time per class; missing keys mean 1.0.
	Factors map[ClassKey]float64

	simStart  map[uint64]int64
	simFinish map[uint64]int64
	inFlight  map[uint64]bool

	// lastConstraint records, per op, which action finally set its clock:
	// the op ID of the joined child / wait target, or 0 for self/fixed time.
	lastConstraint map[uint64]uint64

	// CycleWarnings counts wait/join cycles broken during replay.
	CycleWarnings int
	// FallbackAnchors counts ops anchored without their parent's replay
	// reaching their spawn (parent in flight or inconsistent data). Large
	// counts degrade counterfactual propagation.
	FallbackAnchors int
	// FallbackAnchorOps holds a sample of fallback-anchored ops.
	FallbackAnchorOps []*Op
}

// NewSimulation prepares a replay over g with the given per-class self-time
// factors (nil means baseline).
func NewSimulation(g *Graph, factors map[ClassKey]float64) *Simulation {
	return &Simulation{
		g:              g,
		Factors:        factors,
		simStart:       make(map[uint64]int64, len(g.Ops)),
		simFinish:      make(map[uint64]int64, len(g.Ops)),
		inFlight:       make(map[uint64]bool),
		lastConstraint: make(map[uint64]uint64),
	}
}

func (s *Simulation) factor(op *Op) float64 {
	if s.Factors == nil {
		return 1
	}
	if f, ok := s.Factors[op.Key()]; ok {
		return f
	}
	return 1
}

// Run replays all roots and returns the simulated makespan: the latest root
// finish minus the earliest root start.
func (s *Simulation) Run() (makespanNS int64, err error) {
	if len(s.g.Roots) == 0 {
		return 0, fmt.Errorf("no root ops to simulate")
	}

	// The simulation runs in the original trace's time frame (the first root
	// keeps its recorded start). Mixing frames would corrupt the schedule:
	// ops reached through cross-tree wait targets are anchored at original
	// times when their own root has not been replayed yet.
	chainOrigEnd := s.g.Roots[0].StartNS
	chainSimEnd := s.g.Roots[0].StartNS
	firstStart := int64(-1)
	var lastFinish int64

	for _, root := range s.g.Roots {
		var start int64
		if root.StartNS >= chainOrigEnd {
			// strictly after the previous chained root finished: preserve the
			// original idle gap (client think-time) but inherit any shift
			start = chainSimEnd + (root.StartNS - chainOrigEnd)
		} else {
			// overlaps the previous root: keep the same displacement
			start = root.StartNS + (chainSimEnd - chainOrigEnd)
		}
		s.setSimStart(root, start)
		finish := s.finish(root)
		if firstStart < 0 || start < firstStart {
			firstStart = start
		}
		lastFinish = max(lastFinish, finish)
		if root.EndNS >= chainOrigEnd {
			chainOrigEnd = root.EndNS
			chainSimEnd = finish
		}
	}
	return lastFinish - firstStart, nil
}

func (s *Simulation) setSimStart(op *Op, start int64) {
	if _, ok := s.simStart[op.ID]; !ok {
		s.simStart[op.ID] = start
	}
}

// finish returns the simulated completion time of op, replaying it (and
// transitively everything it depends on) on first use.
func (s *Simulation) finish(op *Op) int64 {
	if f, ok := s.simFinish[op.ID]; ok {
		return f
	}

	// Make sure the op has a simulated start: anchored by its parent's
	// replay at the spawn point. Replaying the parent may recursively replay
	// op itself (via an implicit join), which the memo check above handles
	// when we come back around.
	if _, ok := s.simStart[op.ID]; !ok {
		if op.Parent != nil && !s.inFlight[op.Parent.ID] {
			s.finish(op.Parent)
			if f, ok := s.simFinish[op.ID]; ok {
				return f
			}
		}
		if _, ok := s.simStart[op.ID]; !ok {
			// fallback: anchor in the parent's shifted frame, preserving the
			// op's recorded offset within its parent (recorded start for
			// true roots). Happens when the parent is mid-replay or data is
			// inconsistent.
			anchor := op.StartNS
			if op.Parent != nil {
				if ps, ok := s.simStart[op.Parent.ID]; ok {
					anchor = ps + (op.StartNS - op.Parent.StartNS)
				}
			}
			s.simStart[op.ID] = anchor
			s.FallbackAnchors++
			if len(s.FallbackAnchorOps) < 10 {
				s.FallbackAnchorOps = append(s.FallbackAnchorOps, op)
			}
		}
	}

	if s.inFlight[op.ID] {
		// cycle: break it by assuming original duration from the anchored start
		s.CycleWarnings++
		return s.simStart[op.ID] + op.Duration()
	}
	s.inFlight[op.ID] = true
	defer delete(s.inFlight, op.ID)

	clock := s.simStart[op.ID]
	factor := s.factor(op)

	// Build the chronological action list.
	type action struct {
		at   int64
		kind int // 0=self, 1=spawn, 2=wait
		self segment
		ch   *Op
		w    *WaitEdge
	}
	selfSegs := op.SelfSegments()
	actions := make([]action, 0, len(selfSegs)+len(op.Children)+len(op.Waits))
	for _, seg := range selfSegs {
		actions = append(actions, action{at: seg.Start, kind: 0, self: seg})
	}
	for _, c := range op.Children {
		actions = append(actions, action{at: c.StartNS, kind: 1, ch: c})
	}
	for _, w := range op.Waits {
		actions = append(actions, action{at: w.StartNS, kind: 2, w: w})
	}
	slices.SortStableFunc(actions, func(a, b action) int {
		if a.at != b.at {
			if a.at < b.at {
				return -1
			}
			return 1
		}
		return a.kind - b.kind
	})

	// children pending an implicit join, in original end order
	pending := slices.Clone(op.Children)
	slices.SortFunc(pending, func(a, b *Op) int {
		if a.EndNS != b.EndNS {
			return int(a.EndNS - b.EndNS)
		}
		return int(a.ID - b.ID)
	})
	joinUpTo := func(t int64) {
		for len(pending) > 0 && pending[0].EndNS <= t {
			c := pending[0]
			pending = pending[1:]
			if _, started := s.simStart[c.ID]; !started {
				// child never anchored (e.g. spawn outside op interval);
				// anchor at current clock
				s.setSimStart(c, clock)
			}
			if f := s.finish(c); f > clock {
				clock = f
				s.lastConstraint[op.ID] = c.ID
			}
		}
	}

	for _, a := range actions {
		joinUpTo(a.at)
		switch a.kind {
		case 0:
			dur := a.self.End - a.self.Start
			clock += int64(float64(dur) * factor)
			s.lastConstraint[op.ID] = 0
		case 1:
			s.setSimStart(a.ch, clock)
		case 2:
			w := a.w
			// Only model the wait as a join when the waiter actually stayed
			// until the target completed. A wait that ended earlier was
			// abandoned (cancellation) or points at the wrong op (ident
			// mis-resolution); constraining on the target's full finish
			// would inflate the schedule.
			const joinEpsilonNS = int64(time.Millisecond)
			if w.Target != nil && w.Target != op && w.EndNS >= w.Target.EndNS-joinEpsilonNS {
				if f := s.finish(w.Target); f > clock {
					clock = f
					s.lastConstraint[op.ID] = w.Target.ID
				}
			} else if w.Target == nil {
				// resource wait (lock, attachables...) or unresolved target:
				// keep as a fixed delay
				clock += w.Duration()
				s.lastConstraint[op.ID] = 0
			}
			// abandoned waits on known targets contribute nothing: the op
			// moved on at its own pace, captured by later actions
		}
	}
	joinUpTo(op.EndNS)

	s.simFinish[op.ID] = clock
	return clock
}

// ExplainFinish walks the constraint chain from op downward through the
// dependency (child/wait-target) with the latest simulated finish at each
// step. Used for debugging replay-model fidelity and as a simulated critical
// chain.
func (s *Simulation) ExplainFinish(op *Op, maxDepth int) []*Op {
	chain := []*Op{op}
	seen := map[uint64]bool{op.ID: true}
	for len(chain) < maxDepth {
		var next *Op
		var nextFinish int64
		consider := func(cand *Op) {
			if cand == nil || seen[cand.ID] {
				return
			}
			f, ok := s.simFinish[cand.ID]
			if !ok {
				return
			}
			if f > nextFinish {
				next, nextFinish = cand, f
			}
		}
		for _, c := range op.Children {
			consider(c)
		}
		for _, w := range op.Waits {
			consider(w.Target)
		}
		if next == nil {
			break
		}
		chain = append(chain, next)
		seen[next.ID] = true
		op = next
	}
	return chain
}

// SimTimes returns the simulated start/finish for an op.
func (s *Simulation) SimTimes(op *Op) (startNS, finishNS int64) {
	return s.simStart[op.ID], s.simFinish[op.ID]
}

// WhatIfResult is the simulated impact of scaling one class's self-time.
type WhatIfResult struct {
	Key ClassKey
	// SavedNS[f] is baseline makespan minus the makespan with the class
	// scaled by factor f.
	SavedNS map[float64]int64
}

// ActualMakespanNS returns the observed makespan over root ops.
func ActualMakespanNS(g *Graph) int64 {
	if len(g.Roots) == 0 {
		return 0
	}
	start := g.Roots[0].StartNS
	var end int64
	for _, r := range g.Roots {
		start = min(start, r.StartNS)
		end = max(end, r.EndNS)
	}
	return end - start
}

// maxWhatIfClasses bounds how many classes are simulated (each class costs
// one full replay per factor); the candidates are the top classes by total
// self-time.
const maxWhatIfClasses = 200

// RunWhatIfs computes baseline makespan and, for every class with total self
// time >= minSelfNS (up to maxWhatIfClasses, by total self-time), the
// makespan saving when scaling that class's self time by each factor.
func RunWhatIfs(g *Graph, factors []float64, minSelfNS int64) (baselineNS int64, results []WhatIfResult, err error) {
	baseSim := NewSimulation(g, nil)
	baselineNS, err = baseSim.Run()
	if err != nil {
		return 0, nil, err
	}

	totalSelf := make(map[ClassKey]int64)
	for _, op := range g.Ops {
		totalSelf[op.Key()] += op.SelfNS()
	}

	keys := make([]ClassKey, 0, len(totalSelf))
	for key, self := range totalSelf {
		if self >= minSelfNS {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b ClassKey) int {
		if totalSelf[a] != totalSelf[b] {
			return int(totalSelf[b] - totalSelf[a])
		}
		if a.Kind != b.Kind {
			if a.Kind < b.Kind {
				return -1
			}
			return 1
		}
		if a.Class < b.Class {
			return -1
		}
		if a.Class > b.Class {
			return 1
		}
		return 0
	})
	if len(keys) > maxWhatIfClasses {
		keys = keys[:maxWhatIfClasses]
	}

	for _, key := range keys {
		res := WhatIfResult{Key: key, SavedNS: make(map[float64]int64, len(factors))}
		for _, f := range factors {
			sim := NewSimulation(g, map[ClassKey]float64{key: f})
			makespan, err := sim.Run()
			if err != nil {
				return 0, nil, err
			}
			res.SavedNS[f] = baselineNS - makespan
		}
		results = append(results, res)
	}
	return baselineNS, results, nil
}

// OpDrift describes how far an op's simulated schedule diverged from its
// recorded one in a baseline replay (factor 1 everywhere). Large positive
// drift indicates the replay model over-constrains that op.
type OpDrift struct {
	Op           *Op
	SimStartNS   int64
	SimFinishNS  int64
	StartDriftNS int64 // simStart - origStart
	DurDriftNS   int64 // (simFinish-simStart) - origDuration
}

// BaselineDrift replays at factor 1 and returns the ops whose simulated
// duration grew the most versus their recorded duration, plus the ops whose
// simulated start moved latest. Used to debug replay-model fidelity.
func BaselineDrift(g *Graph, topN int) (durDrift, startDrift []OpDrift) {
	sim := NewSimulation(g, nil)
	if _, err := sim.Run(); err != nil {
		return nil, nil
	}
	drifts := make([]OpDrift, 0, len(g.Ops))
	for _, op := range g.Ops {
		finish, ok := sim.simFinish[op.ID]
		if !ok {
			continue
		}
		start := sim.simStart[op.ID]
		drifts = append(drifts, OpDrift{
			Op:           op,
			SimStartNS:   start,
			SimFinishNS:  finish,
			StartDriftNS: start - op.StartNS,
			DurDriftNS:   (finish - start) - op.Duration(),
		})
	}
	byDur := slices.Clone(drifts)
	slices.SortFunc(byDur, func(a, b OpDrift) int { return int(b.DurDriftNS - a.DurDriftNS) })
	if len(byDur) > topN {
		byDur = byDur[:topN]
	}
	byStart := drifts
	slices.SortFunc(byStart, func(a, b OpDrift) int { return int(b.StartDriftNS - a.StartDriftNS) })
	if len(byStart) > topN {
		byStart = byStart[:topN]
	}
	return byDur, byStart
}

// DriftOrigin is an op whose baseline-simulated duration inflated beyond its
// recorded duration by more than its dependencies' inflation explains: the
// place where replay-model error is introduced (rather than inherited).
type DriftOrigin struct {
	Op         *Op
	DurDriftNS int64
	// OwnDriftNS is DurDrift minus the largest drift among children and wait
	// targets.
	OwnDriftNS int64
}

// DriftOrigins finds where baseline replay error originates, comparing each
// op's simulated finish lateness (vs its recorded end, after removing the
// global root shift) against the worst lateness among its dependencies.
func DriftOrigins(g *Graph, minOwnDriftNS int64, topN int) []DriftOrigin {
	sim := NewSimulation(g, nil)
	if _, err := sim.Run(); err != nil {
		return nil
	}
	finishDrift := func(op *Op) int64 {
		finish, ok := sim.simFinish[op.ID]
		if !ok {
			return 0
		}
		return finish - op.EndNS
	}
	var origins []DriftOrigin
	for _, op := range g.Ops {
		d := finishDrift(op)
		if d < minOwnDriftNS {
			continue
		}
		var maxDep int64
		for _, c := range op.Children {
			maxDep = max(maxDep, finishDrift(c))
		}
		for _, w := range op.Waits {
			if w.Target != nil {
				maxDep = max(maxDep, finishDrift(w.Target))
			}
		}
		if op.Parent != nil {
			// start lateness inherited from the parent's anchor
			maxDep = max(maxDep, sim.simStart[op.ID]-op.StartNS)
		}
		own := d - maxDep
		if own >= minOwnDriftNS {
			origins = append(origins, DriftOrigin{Op: op, DurDriftNS: d, OwnDriftNS: own})
		}
	}
	slices.SortFunc(origins, func(a, b DriftOrigin) int { return int(b.OwnDriftNS - a.OwnDriftNS) })
	if len(origins) > topN {
		origins = origins[:topN]
	}
	return origins
}

// BlockingChain walks back from the op that finishes last in the baseline
// simulation, at each step following the child or wait whose interval ends
// latest, yielding an approximate end-of-workload critical chain.
func BlockingChain(g *Graph, maxDepth int) []*Op {
	if len(g.Roots) == 0 {
		return nil
	}
	last := g.Roots[0]
	for _, r := range g.Roots {
		if r.EndNS > last.EndNS {
			last = r
		}
	}
	chain := []*Op{last}
	cur := last
	seen := map[uint64]bool{last.ID: true}
	for len(chain) < maxDepth {
		var next *Op
		var nextEnd int64
		for _, c := range cur.Children {
			if c.EndNS > nextEnd && !seen[c.ID] {
				next, nextEnd = c, c.EndNS
			}
		}
		for _, w := range cur.Waits {
			if w.Target != nil && w.Target.EndNS > nextEnd && !seen[w.Target.ID] {
				next, nextEnd = w.Target, w.Target.EndNS
			}
		}
		if next == nil {
			break
		}
		chain = append(chain, next)
		seen[next.ID] = true
		cur = next
	}
	return chain
}
