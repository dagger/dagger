package wcprof

import (
	"context"
	"encoding/json"
)

type opCtxKey struct{}

type profCtxKey struct{}

// CurrentOpID returns the profiling op ID carried by ctx, or 0.
func CurrentOpID(ctx context.Context) uint64 {
	id, _ := ctx.Value(opCtxKey{}).(uint64)
	return id
}

// ContextWithOpID returns a context carrying the given op ID as the current
// op. Useful when handing work to a goroutine that should be attributed to an
// existing op.
func ContextWithOpID(ctx context.Context, opID uint64) context.Context {
	if opID == 0 {
		return ctx
	}
	return context.WithValue(ctx, opCtxKey{}, opID)
}

// ContextWithProfiling marks ctx (and any context derived from it) for
// recording even when engine-global recording is off. The session server
// applies this to contexts of sessions that opted into profiling.
func ContextWithProfiling(ctx context.Context) context.Context {
	return context.WithValue(ctx, profCtxKey{}, true)
}

// Enabled reports whether work running under ctx should be recorded. Call
// sites use it to skip computing op metadata (class/ident strings) when
// profiling is off. When profiling has never been enabled this is a single
// atomic load + nil check.
func Enabled(ctx context.Context) bool {
	return recorderFor(ctx) != nil
}

// recorderFor returns the recorder if work under ctx should be recorded:
// either engine-global recording is on, or ctx belongs to a profiled flow
// (it carries a recorded op as its current op, or a per-session profiling
// mark from ContextWithProfiling).
func recorderFor(ctx context.Context) *Recorder {
	r := global.Load()
	if r == nil {
		return nil
	}
	if globalOn.Load() {
		return r
	}
	if CurrentOpID(ctx) != 0 {
		return r
	}
	if on, _ := ctx.Value(profCtxKey{}).(bool); on {
		return r
	}
	return nil
}

// Op is a handle for an in-progress operation. A nil *Op is valid and all
// methods are no-ops, so call sites do not need to check whether profiling is
// enabled.
type Op struct {
	r           *Recorder
	id          uint64
	parentID    uint64
	kind        OpKind
	work        WorkType
	classID     uint32
	identID     uint32
	clientID    uint32
	metaID      uint32
	startNS     int64
	outcomeHint Outcome
}

// OpOpts carries optional per-op metadata.
type OpOpts struct {
	// Ident is an instance identity for the op (e.g. recipe digest, exec ID).
	Ident string
	// ClientID is the dagger client this op serves, when known.
	ClientID string
	// ResultID may be set at Begin time when already known.
	WorkType WorkType
	// Argv is the user command for a container-exec op (already scrubbed and
	// bounded by the caller). The recorder marshals it to the canonical scalar
	// JSON-array string and interns it as the op's MetaID, so offline analysis
	// can rank the exec by its real command. Empty for non-exec ops.
	Argv []string
}

// internArgv marshals argv to the canonical scalar JSON-array string and interns
// it, returning the interned string id (0 when empty or on a marshal error —
// never a partial or panicking emit). The OTel source marshals the SAME scrubbed
// slice the same way (engine/engineutil/otelprof.go), so both sources carry a
// byte-identical encoding and reconstruct an identical argv (cross-source parity).
func (r *Recorder) internArgv(argv []string) uint32 {
	if len(argv) == 0 {
		return 0
	}
	b, err := json.Marshal(argv)
	if err != nil {
		return 0
	}
	return r.Intern(string(b))
}

// BeginOp starts recording an operation. It returns a derived context that
// carries the new op as the current op (so nested ops and waits parent to
// it), and a handle to end it. When profiling is disabled it returns ctx
// unchanged and a nil handle.
func BeginOp(ctx context.Context, kind OpKind, class string, opts OpOpts) (context.Context, *Op) {
	r := recorderFor(ctx)
	if r == nil {
		return ctx, nil
	}
	op := &Op{
		r:        r,
		id:       r.newOpID(),
		parentID: CurrentOpID(ctx),
		kind:     kind,
		work:     opts.WorkType,
		classID:  r.Intern(class),
		identID:  r.Intern(opts.Ident),
		clientID: r.Intern(opts.ClientID),
		metaID:   r.internArgv(opts.Argv),
		startNS:  r.Now(),
	}
	sh := r.shardFor(op.id)
	sh.mu.Lock()
	sh.openOps[op.id] = openOp{
		op:       kind,
		work:     opts.WorkType,
		classID:  op.classID,
		identID:  op.identID,
		clientID: op.clientID,
		metaID:   op.metaID,
		parentID: op.parentID,
		startNS:  op.startNS,
	}
	sh.mu.Unlock()
	return ContextWithOpID(ctx, op.id), op
}

// ID returns the op's ID, or 0 for a nil handle.
func (op *Op) ID() uint64 {
	if op == nil {
		return 0
	}
	return op.id
}

// SetIdent updates the op's instance identity (useful when it only becomes
// known after the op began, e.g. a recipe digest derived mid-call). Must be
// called from the goroutine that owns the op, before End.
func (op *Op) SetIdent(ident string) {
	if op == nil {
		return
	}
	op.identID = op.r.Intern(ident)
}

// outcomeHint carries an outcome decided mid-op (e.g. joined vs executed),
// read back by the code that ends the op.
func (op *Op) SetOutcomeHint(outcome Outcome) {
	if op == nil {
		return
	}
	op.outcomeHint = outcome
}

// OutcomeHint returns the hint set by SetOutcomeHint, or OutcomeNone.
func (op *Op) OutcomeHint() Outcome {
	if op == nil {
		return OutcomeNone
	}
	return op.outcomeHint
}

// End records the op's completion.
func (op *Op) End(outcome Outcome) {
	op.EndWithResult(outcome, 0)
}

// EndErr records completion with OutcomeOK or OutcomeError based on err.
func (op *Op) EndErr(err error) {
	if err != nil {
		op.End(OutcomeError)
		return
	}
	op.End(OutcomeOK)
}

// EndWithResult records the op's completion, associating it with a dagql
// shared result ID when non-zero.
func (op *Op) EndWithResult(outcome Outcome, resultID uint64) {
	if op == nil {
		return
	}
	sh := op.r.shardFor(op.id)
	sh.mu.Lock()
	delete(sh.openOps, op.id)
	sh.mu.Unlock()
	op.r.append(sh, Event{
		Type:     EventTypeOp,
		OpKind:   op.kind,
		WorkType: op.work,
		Outcome:  outcome,
		OpID:     op.id,
		ParentID: op.parentID,
		ResultID: resultID,
		ClassID:  op.classID,
		IdentID:  op.identID,
		ClientID: op.clientID,
		MetaID:   op.metaID,
		StartNS:  op.startNS,
		EndNS:    op.r.Now(),
	})
}

// Wait is a handle for an in-progress wait interval. A nil *Wait is valid
// and End is a no-op.
type Wait struct {
	r        *Recorder
	waiterID uint64
	targetID uint64
	identID  uint32
	reason   WaitReason
	startNS  int64
}

// BeginWait starts recording that the current op (from ctx) is blocked on
// targetOpID for the given reason. Returns nil when profiling is disabled.
func BeginWait(ctx context.Context, targetOpID uint64, reason WaitReason) *Wait {
	return beginWait(ctx, targetOpID, "", reason)
}

// BeginWaitIdent is BeginWait for waits on a named resource (e.g. a lock)
// rather than another op.
func BeginWaitIdent(ctx context.Context, ident string, reason WaitReason) *Wait {
	return beginWait(ctx, 0, ident, reason)
}

func beginWait(ctx context.Context, targetOpID uint64, ident string, reason WaitReason) *Wait {
	r := recorderFor(ctx)
	if r == nil {
		return nil
	}
	return &Wait{
		r:        r,
		waiterID: CurrentOpID(ctx),
		targetID: targetOpID,
		identID:  r.Intern(ident),
		reason:   reason,
		startNS:  r.Now(),
	}
}

// SetTarget updates the awaited op (useful when the target becomes known
// only after the wait began).
func (w *Wait) SetTarget(targetOpID uint64) {
	if w == nil {
		return
	}
	w.targetID = targetOpID
}

// End records the wait interval.
func (w *Wait) End() {
	if w == nil {
		return
	}
	endNS := w.r.Now()
	sh := w.r.shardFor(w.waiterID)
	w.r.append(sh, Event{
		Type:     EventTypeWait,
		Reason:   w.reason,
		ParentID: w.waiterID,
		TargetID: w.targetID,
		IdentID:  w.identID,
		StartNS:  w.startNS,
		EndNS:    endNS,
	})
}

// NowNS returns the current recorder-relative timestamp, or 0 when profiling
// is disabled.
func NowNS() int64 {
	r := Active()
	if r == nil {
		return 0
	}
	return r.Now()
}

// RecordOp records an already-completed op interval with explicit
// timestamps (from NowNS). Useful when an op's boundaries are observed from
// callbacks where holding an *Op handle would race. Returns the op ID, or 0
// when profiling is disabled.
func RecordOp(ctx context.Context, kind OpKind, class string, opts OpOpts, startNS, endNS int64, outcome Outcome) uint64 {
	r := recorderFor(ctx)
	if r == nil {
		return 0
	}
	id := r.newOpID()
	sh := r.shardFor(id)
	r.append(sh, Event{
		Type:     EventTypeOp,
		OpKind:   kind,
		WorkType: opts.WorkType,
		Outcome:  outcome,
		OpID:     id,
		ParentID: CurrentOpID(ctx),
		ClassID:  r.Intern(class),
		IdentID:  r.Intern(opts.Ident),
		ClientID: r.Intern(opts.ClientID),
		MetaID:   r.internArgv(opts.Argv),
		StartNS:  startNS,
		EndNS:    endNS,
	})
	return id
}

// Link records a non-blocking correlation from fromOpID (0 means the current
// op in ctx) to an op, an interned ident, and/or a dagql result ID.
func Link(ctx context.Context, kind LinkKind, fromOpID, targetOpID uint64, ident string, resultID uint64) {
	r := recorderFor(ctx)
	if r == nil {
		return
	}
	if fromOpID == 0 {
		fromOpID = CurrentOpID(ctx)
	}
	sh := r.shardFor(fromOpID)
	now := r.Now()
	r.append(sh, Event{
		Type:     EventTypeLink,
		LinkKind: kind,
		ParentID: fromOpID,
		TargetID: targetOpID,
		IdentID:  r.Intern(ident),
		ResultID: resultID,
		StartNS:  now,
		EndNS:    now,
	})
}
