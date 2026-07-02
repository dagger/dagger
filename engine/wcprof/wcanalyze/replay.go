package wcanalyze

import (
	"fmt"
	"runtime"
	"slices"
	"sync"
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
//     named resources (locks etc.) are kept as fixed delays; waits that
//     ended before their target's recorded end were abandoned
//     (cancellation) or mis-resolved and contribute nothing
//   - implicit joins: whenever the op reaches an action at original time t,
//     it first joins every child that had originally ended by t. This bakes
//     the observed ordering in as a constraint, which correctly models
//     synchronous child calls (no explicit wait edge exists for plain
//     function calls) and is conservatively safe for async children.
//
// Roots are chained by preserving original idle gaps between strictly
// sequential roots (e.g. successive queries from the CLI). The simulation
// runs in the original trace's time frame (the first root keeps its
// recorded start); mixing frames would corrupt the schedule because ops
// reached through cross-tree wait targets are anchored at original times
// when their own root has not been replayed yet.
//
// The timeline of every op is factor-independent, so it is compiled once
// per graph into a flat action program (replayProgram); each simulation is
// then a pure array-based DP over that program, cheap enough to run
// hundreds of counterfactuals over multi-million-op traces.

const joinEpsilonNS = int64(time.Millisecond)

// action kinds in the compiled program.
const (
	actSelf uint8 = iota
	actSpawn
	actWaitJoin
	// actWaitNoop is an abandoned wait on a known target: it contributes no
	// time (the op moved on at its own pace) but still marks an action point
	// for implicit joins.
	actWaitNoop
	actWaitFixed
)

type action struct {
	at   int64
	dur  int64 // self duration or fixed-wait duration (unscaled)
	ref  int32 // child / wait-target op index
	kind uint8
}

// replayProgram is the per-graph compiled form of every op's timeline.
type replayProgram struct {
	ops     []*Op
	idxByID map[uint64]int32

	classKeys []ClassKey
	classOf   []int32

	startNS []int64
	endNS   []int64
	parent  []int32 // -1 when none

	// actions[actOff[i]:actOff[i+1]] is op i's timeline, sorted by
	// (at, self<spawn<wait) to match replay ordering rules.
	actions []action
	actOff  []int32

	// pendIdx[pendOff[i]:pendOff[i+1]] is op i's children sorted by
	// (EndNS, ID): the implicit-join order.
	pendIdx []int32
	pendOff []int32

	roots []int32
}

func (g *Graph) program() *replayProgram {
	g.progOnce.Do(func() {
		g.prog = compileProgram(g)
	})
	return g.prog
}

func compileProgram(g *Graph) *replayProgram {
	n := len(g.Ops)
	p := &replayProgram{
		ops:     make([]*Op, 0, n),
		idxByID: make(map[uint64]int32, n),
		classOf: make([]int32, n),
		startNS: make([]int64, n),
		endNS:   make([]int64, n),
		parent:  make([]int32, n),
		actOff:  make([]int32, n+1),
		pendOff: make([]int32, n+1),
	}

	// deterministic dense indexing by op ID
	for _, op := range g.Ops {
		p.ops = append(p.ops, op)
	}
	slices.SortFunc(p.ops, func(a, b *Op) int {
		return int(a.ID - b.ID)
	})
	for i, op := range p.ops {
		p.idxByID[op.ID] = int32(i)
	}

	classIdx := make(map[ClassKey]int32)
	totalActions := 0
	totalPend := 0
	for i, op := range p.ops {
		p.startNS[i] = op.StartNS
		p.endNS[i] = op.EndNS
		p.parent[i] = -1
		if op.Parent != nil {
			if pi, ok := p.idxByID[op.Parent.ID]; ok {
				p.parent[i] = pi
			}
		}
		key := op.Key()
		ci, ok := classIdx[key]
		if !ok {
			ci = int32(len(p.classKeys))
			classIdx[key] = ci
			p.classKeys = append(p.classKeys, key)
		}
		p.classOf[i] = ci
		totalActions += len(op.SelfSegments()) + len(op.Children) + len(op.Waits)
		totalPend += len(op.Children)
	}

	p.actions = make([]action, 0, totalActions)
	p.pendIdx = make([]int32, 0, totalPend)
	actionRank := func(kind uint8) int {
		switch kind {
		case actSelf:
			return 0
		case actSpawn:
			return 1
		default:
			return 2
		}
	}
	for i, op := range p.ops {
		p.actOff[i] = int32(len(p.actions))
		for _, seg := range op.SelfSegments() {
			p.actions = append(p.actions, action{at: seg.Start, kind: actSelf, dur: seg.End - seg.Start})
		}
		for _, c := range op.Children {
			p.actions = append(p.actions, action{at: c.StartNS, kind: actSpawn, ref: p.idxByID[c.ID]})
		}
		for _, w := range op.Waits {
			a := action{at: w.StartNS}
			switch {
			case w.Target != nil && w.Target != op && w.EndNS >= w.Target.EndNS-joinEpsilonNS:
				a.kind = actWaitJoin
				a.ref = p.idxByID[w.Target.ID]
			case w.Target == nil:
				a.kind = actWaitFixed
				a.dur = w.Duration()
			default:
				a.kind = actWaitNoop
			}
			p.actions = append(p.actions, a)
		}
		span := p.actions[p.actOff[i]:]
		slices.SortStableFunc(span, func(a, b action) int {
			if a.at != b.at {
				if a.at < b.at {
					return -1
				}
				return 1
			}
			return actionRank(a.kind) - actionRank(b.kind)
		})

		p.pendOff[i] = int32(len(p.pendIdx))
		pend := slices.Clone(op.Children)
		slices.SortFunc(pend, func(a, b *Op) int {
			if a.EndNS != b.EndNS {
				return int(a.EndNS - b.EndNS)
			}
			return int(a.ID - b.ID)
		})
		for _, c := range pend {
			p.pendIdx = append(p.pendIdx, p.idxByID[c.ID])
		}
	}
	p.actOff[n] = int32(len(p.actions))
	p.pendOff[n] = int32(len(p.pendIdx))

	p.roots = make([]int32, 0, len(g.Roots))
	for _, r := range g.Roots {
		p.roots = append(p.roots, p.idxByID[r.ID])
	}
	return p
}

// Simulation replays the compiled program under per-class self-time factors.
type Simulation struct {
	g *Graph
	p *replayProgram
	// Factors scales self-time per class; missing keys mean 1.0.
	Factors map[ClassKey]float64

	factorOf []float64

	started   []bool
	finished  []bool
	inFlight  []bool
	simStart  []int64
	simFinish []int64

	// CycleWarnings counts wait/join cycles broken during replay.
	CycleWarnings int
	// FallbackAnchors counts ops anchored without their parent's replay
	// reaching their spawn (parent in flight or inconsistent data). They
	// anchor in the parent's shifted frame at their recorded offset.
	FallbackAnchors int
	// FallbackAnchorOps holds a sample of fallback-anchored ops.
	FallbackAnchorOps []*Op
}

// NewSimulation prepares a replay over g with the given per-class self-time
// factors (nil means baseline).
func NewSimulation(g *Graph, factors map[ClassKey]float64) *Simulation {
	p := g.program()
	n := len(p.ops)
	s := &Simulation{
		g:         g,
		p:         p,
		Factors:   factors,
		factorOf:  make([]float64, len(p.classKeys)),
		started:   make([]bool, n),
		finished:  make([]bool, n),
		inFlight:  make([]bool, n),
		simStart:  make([]int64, n),
		simFinish: make([]int64, n),
	}
	for i := range s.factorOf {
		s.factorOf[i] = 1
	}
	for key, f := range factors {
		for ci, ck := range p.classKeys {
			if ck == key {
				s.factorOf[ci] = f
			}
		}
	}
	return s
}

// Run replays all roots and returns the simulated makespan: the latest root
// finish minus the earliest root start.
func (s *Simulation) Run() (makespanNS int64, err error) {
	if len(s.p.roots) == 0 {
		return 0, fmt.Errorf("no root ops to simulate")
	}

	chainOrigEnd := s.p.startNS[s.p.roots[0]]
	chainSimEnd := chainOrigEnd
	firstStart := int64(-1)
	var lastFinish int64

	for _, r := range s.p.roots {
		var start int64
		if s.p.startNS[r] >= chainOrigEnd {
			// strictly after the previous chained root finished: preserve the
			// original idle gap (client think-time) but inherit any shift
			start = chainSimEnd + (s.p.startNS[r] - chainOrigEnd)
		} else {
			// overlaps the previous root: keep the same displacement
			start = s.p.startNS[r] + (chainSimEnd - chainOrigEnd)
		}
		s.setStart(r, start)
		finish := s.finish(r)
		if firstStart < 0 || s.simStart[r] < firstStart {
			firstStart = s.simStart[r]
		}
		lastFinish = max(lastFinish, finish)
		if s.p.endNS[r] >= chainOrigEnd {
			chainOrigEnd = s.p.endNS[r]
			chainSimEnd = finish
		}
	}
	return lastFinish - firstStart, nil
}

func (s *Simulation) setStart(i int32, v int64) {
	if !s.started[i] {
		s.started[i] = true
		s.simStart[i] = v
	}
}

// finish returns the simulated completion time of op i, replaying it (and
// transitively everything it depends on) on first use.
func (s *Simulation) finish(i int32) int64 {
	if s.finished[i] {
		return s.simFinish[i]
	}

	// Make sure the op has a simulated start: anchored by its parent's
	// replay at the spawn point. Replaying the parent may recursively replay
	// op itself (via an implicit join), which the memo check above handles
	// when we come back around.
	if !s.started[i] {
		if par := s.p.parent[i]; par >= 0 && !s.inFlight[par] {
			s.finish(par)
			if s.finished[i] {
				return s.simFinish[i]
			}
		}
		if !s.started[i] {
			// fallback: anchor in the parent's shifted frame, preserving the
			// op's recorded offset within its parent (recorded start for
			// true roots). Happens when the parent is mid-replay or data is
			// inconsistent.
			anchor := s.p.startNS[i]
			if par := s.p.parent[i]; par >= 0 && s.started[par] {
				anchor = s.simStart[par] + (s.p.startNS[i] - s.p.startNS[par])
			}
			s.setStart(i, anchor)
			s.FallbackAnchors++
			if len(s.FallbackAnchorOps) < 10 {
				s.FallbackAnchorOps = append(s.FallbackAnchorOps, s.p.ops[i])
			}
		}
	}

	if s.inFlight[i] {
		// cycle: break it by assuming original duration from the anchored start
		s.CycleWarnings++
		return s.simStart[i] + (s.p.endNS[i] - s.p.startNS[i])
	}
	s.inFlight[i] = true

	clock := s.simStart[i]
	factor := s.factorOf[s.p.classOf[i]]

	pendCur := s.p.pendOff[i]
	pendEnd := s.p.pendOff[i+1]
	joinUpTo := func(t int64) {
		for pendCur < pendEnd {
			c := s.p.pendIdx[pendCur]
			if s.p.endNS[c] > t {
				return
			}
			pendCur++
			if !s.started[c] {
				// child never anchored (e.g. spawn outside op interval);
				// anchor at current clock
				s.setStart(c, clock)
			}
			if f := s.finish(c); f > clock {
				clock = f
			}
		}
	}

	for ai := s.p.actOff[i]; ai < s.p.actOff[i+1]; ai++ {
		a := s.p.actions[ai]
		joinUpTo(a.at)
		switch a.kind {
		case actSelf:
			clock += int64(float64(a.dur) * factor)
		case actSpawn:
			s.setStart(a.ref, clock)
		case actWaitJoin:
			if f := s.finish(a.ref); f > clock {
				clock = f
			}
		case actWaitFixed:
			clock += a.dur
		case actWaitNoop:
			// abandoned wait: action point only
		}
	}
	joinUpTo(s.p.endNS[i])

	s.simFinish[i] = clock
	s.finished[i] = true
	s.inFlight[i] = false
	return clock
}

// SimTimes returns the simulated start/finish for an op (zero values when
// the op was not reached by the replay).
func (s *Simulation) SimTimes(op *Op) (startNS, finishNS int64) {
	i, ok := s.p.idxByID[op.ID]
	if !ok || !s.finished[i] {
		return 0, 0
	}
	return s.simStart[i], s.simFinish[i]
}

// simTimesOK is SimTimes plus whether the op completed in the replay.
func (s *Simulation) simTimesOK(op *Op) (startNS, finishNS int64, ok bool) {
	i, found := s.p.idxByID[op.ID]
	if !found || !s.finished[i] {
		return 0, 0, false
	}
	return s.simStart[i], s.simFinish[i], true
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
			_, f, ok := s.simTimesOK(cand)
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
// Simulations run in parallel.
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

	results = make([]WhatIfResult, len(keys))
	for ki, key := range keys {
		results[ki] = WhatIfResult{Key: key, SavedNS: make(map[float64]int64, len(factors))}
	}

	// each (class, factor) simulation is independent; bound parallelism to
	// keep per-sim state memory in check on huge traces
	type job struct{ ki, fi int }
	jobs := make(chan job)
	workers := min(runtime.GOMAXPROCS(0), 8)
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				sim := NewSimulation(g, map[ClassKey]float64{keys[j.ki]: factors[j.fi]})
				makespan, simErr := sim.Run()
				mu.Lock()
				if simErr != nil && err == nil {
					err = simErr
				} else {
					results[j.ki].SavedNS[factors[j.fi]] = baselineNS - makespan
				}
				mu.Unlock()
			}
		}()
	}
	for ki := range keys {
		for fi := range factors {
			jobs <- job{ki, fi}
		}
	}
	close(jobs)
	wg.Wait()
	if err != nil {
		return 0, nil, err
	}
	return baselineNS, results, nil
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
		start, finish, ok := sim.simTimesOK(op)
		if !ok {
			continue
		}
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
// op's simulated finish lateness (vs its recorded end) against the worst
// lateness among its dependencies.
func DriftOrigins(g *Graph, minOwnDriftNS int64, topN int) []DriftOrigin {
	sim := NewSimulation(g, nil)
	if _, err := sim.Run(); err != nil {
		return nil
	}
	finishDrift := func(op *Op) int64 {
		_, finish, ok := sim.simTimesOK(op)
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
			if start, _, ok := sim.simTimesOK(op); ok {
				maxDep = max(maxDep, start-op.StartNS)
			}
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
