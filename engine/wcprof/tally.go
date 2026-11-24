package wcprof

import (
	"context"
	"sync"
	"time"
)

// A TallyClass names calls recorded as a count, a summed duration and a
// count of failures on the op they run under (BeginTally), rather than as an
// op each: calls frequent enough that recording an op apiece costs the
// engine time. An op's tallies are recorded with it when it ends, a tally
// event per class (DumpTypeTally); a dump taken while it's open has its
// tallies so far.
type TallyClass struct {
	index int
	kind  OpKind
	class string
}

// maxTallyClasses bounds the classes an op can tally, so its tallies are a
// fixed array.
const maxTallyClasses = 4

// tallyClasses are the registered classes, by index. They're written only
// while packages initialize, before any op records.
var (
	tallyClasses    [maxTallyClasses]*TallyClass
	tallyClassCount int
)

// NewTallyClass registers a tallied call class, of ops of kind kind. Call it
// from a package-level var, so it runs while packages initialize: there are
// at most maxTallyClasses.
func NewTallyClass(kind OpKind, class string) *TallyClass {
	if tallyClassCount == maxTallyClasses {
		panic("wcprof: too many tally classes")
	}
	tc := &TallyClass{index: tallyClassCount, kind: kind, class: class}
	tallyClasses[tc.index] = tc
	tallyClassCount++
	return tc
}

// opTallies are an op's tallied calls, by class. Once its end has recorded
// them, done is set and no call is tallied on it.
type opTallies struct {
	mu     sync.Mutex
	done   bool
	counts [maxTallyClasses]struct{ count, ns, errors int64 }
}

// endedUntallied marks an op that ended with no tallied call, so a later
// call isn't tallied on it, without allocating for every op.
var endedUntallied = &opTallies{done: true}

// TallyCall is one tallied call in progress (BeginTally). Its zero value,
// for unprofiled work, records nothing.
type TallyCall struct {
	ctx   context.Context
	tc    *TallyClass
	start time.Time
}

// BeginTally starts a call of class tc under the op current in ctx, when ctx
// is profiled; otherwise it returns the zero TallyCall without reading the
// clock.
func BeginTally(ctx context.Context, tc *TallyClass) TallyCall {
	if recorderFor(ctx) == nil {
		return TallyCall{}
	}
	return TallyCall{ctx: ctx, tc: tc, start: time.Now()}
}

// End tallies the call, failed when err is set, on the op current in its
// context. When that op was only handed over by ID (ContextWithOpID), or has
// already ended, the call is recorded as an op of its own under it instead.
func (c TallyCall) End(err error) {
	if c.tc == nil {
		return
	}
	end := time.Now()
	r := recorderFor(c.ctx)
	if r == nil {
		return
	}
	if op := currentOpHandle(c.ctx); op != nil && op.r == r && op.tally(c.tc, end.Sub(c.start), err != nil) {
		return
	}
	outcome := OutcomeOK
	if err != nil {
		outcome = OutcomeError
	}
	RecordOpAt(c.ctx, c.tc.kind, c.tc.class, OpOpts{}, c.start, end, outcome)
}

// tally counts a call of tc on op, unless op's end has recorded its tallies.
func (op *Op) tally(tc *TallyClass, d time.Duration, failed bool) bool {
	t := op.tallies.Load()
	if t == nil {
		op.tallies.CompareAndSwap(nil, new(opTallies))
		t = op.tallies.Load()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return false
	}
	c := &t.counts[tc.index]
	c.count++
	c.ns += int64(d)
	if failed {
		c.errors++
	}
	return true
}

// tallyEvents are op's tallied calls as tally events, a class each, from its
// start to endNS. When final, at its end, no call is tallied on it after.
// Tally events reuse Event's fields as a wait does: TargetID is the count,
// ResultID the failures, Bytes the summed duration.
func (op *Op) tallyEvents(endNS int64, final bool) []Event {
	t := op.tallies.Load()
	if t == nil {
		if !final || op.tallies.CompareAndSwap(nil, endedUntallied) {
			return nil
		}
		t = op.tallies.Load()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if final {
		t.done = true
	}
	var evs []Event
	for i, c := range t.counts {
		if c.count == 0 {
			continue
		}
		tc := tallyClasses[i]
		evs = append(evs, Event{
			Type:     EventTypeTally,
			OpKind:   tc.kind,
			ParentID: op.id,
			ClassID:  op.r.Intern(tc.class),
			TargetID: uint64(c.count),
			ResultID: uint64(c.errors),
			Bytes:    c.ns,
			StartNS:  op.startNS,
			EndNS:    endNS,
		})
	}
	return evs
}
