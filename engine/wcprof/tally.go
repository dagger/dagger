package wcprof

import (
	"context"
	"sync/atomic"
	"time"
)

// A TallyClass names calls recorded as a count, a summed duration and a
// count of failures on the op they run under (Tally), rather than as an op
// each: calls frequent enough that recording an op apiece costs the engine
// time. An op's tallies are recorded with it when it ends, as a tally event
// per class (DumpTypeTally).
type TallyClass struct {
	index int
	class string
}

// maxTallyClasses bounds the classes an op can tally, so its tallies are a
// fixed array allocated on its first tallied call.
const maxTallyClasses = 4

// tallyClassNames are the registered classes, by index. They're written only
// while packages initialize, before any op records.
var (
	tallyClassNames [maxTallyClasses]string
	tallyClassCount int
)

// NewTallyClass registers a tallied call class. Call it from a package-level
// var, so it runs while packages initialize: there are at most
// maxTallyClasses.
func NewTallyClass(class string) *TallyClass {
	if tallyClassCount == maxTallyClasses {
		panic("wcprof: too many tally classes")
	}
	tc := &TallyClass{index: tallyClassCount, class: class}
	tallyClassNames[tc.index] = class
	tallyClassCount++
	return tc
}

type opTally struct {
	count, ns, errors atomic.Int64
}

type opTallies [maxTallyClasses]opTally

// Tally records one call of class tc, from start to now, failed when err is
// set, on the op current in ctx. When ctx's current op wasn't begun in it
// (ContextWithOpID handed its ID over), the call is recorded as an op of its
// own under it instead. It records nothing when ctx isn't profiled.
func Tally(ctx context.Context, tc *TallyClass, start time.Time, err error) {
	r := recorderFor(ctx)
	if r == nil {
		return
	}
	end := time.Now()
	op := currentOpHandle(ctx)
	if op == nil || op.r != r {
		outcome := OutcomeOK
		if err != nil {
			outcome = OutcomeError
		}
		RecordOpAt(ctx, OpKindInternal, tc.class, OpOpts{}, start, end, outcome)
		return
	}
	tallies := op.tallies.Load()
	if tallies == nil {
		op.tallies.CompareAndSwap(nil, new(opTallies))
		tallies = op.tallies.Load()
	}
	t := &tallies[tc.index]
	t.count.Add(1)
	t.ns.Add(int64(end.Sub(start)))
	if err != nil {
		t.errors.Add(1)
	}
}

// recordTallies records op's tallies, a tally event per class it counted,
// in shard sh. Tally events reuse Event's fields as a wait does: TargetID is
// the count, ResultID the failures, Bytes the summed duration.
func (op *Op) recordTallies(sh *shard, endNS int64) {
	tallies := op.tallies.Load()
	if tallies == nil {
		return
	}
	for i := range tallies {
		t := &tallies[i]
		n := t.count.Load()
		if n == 0 {
			continue
		}
		op.r.append(sh, Event{
			Type:     EventTypeTally,
			ParentID: op.id,
			ClassID:  op.r.Intern(tallyClassNames[i]),
			TargetID: uint64(n),
			ResultID: uint64(t.errors.Load()),
			Bytes:    t.ns.Load(),
			StartNS:  op.startNS,
			EndNS:    endNS,
		})
	}
}
