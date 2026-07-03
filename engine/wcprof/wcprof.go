// Package wcprof implements cheap wall-clock profiling for the engine.
//
// It records operation intervals, wait (blocked-on) intervals, and link
// events into an in-memory buffer, for offline analysis of where wall-clock
// time goes and which operation classes are bottlenecks. It is intentionally
// not OTel: events are fixed-size structs appended to sharded in-memory
// buffers, with all strings interned. The expensive work (graph
// reconstruction, counterfactual simulation) happens offline after dumping
// the buffer via the engine debug endpoint.
//
// Recording can be enabled two ways:
//
//   - Engine-global: the _DAGGER_WCPROF environment variable, the engine's
//     --wcprof flag, or POSTing "on" to the /debug/wcprof/enabled endpoint.
//     All work on the engine is recorded until "off" is POSTed to the same
//     endpoint.
//   - Per-session: a client connecting with ClientMetadata.Profile set (the
//     hidden --profile CLI flag). Only work attributable to that session
//     (including its nested module/SDK clients) is recorded: the session
//     server marks the session's contexts via ContextWithProfiling, and the
//     mark propagates to derived contexts.
//
// When profiling has never been enabled, all recording calls are a single
// atomic load + nil check.
package wcprof

import (
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// EventType discriminates the union in Event.
type EventType uint8

const (
	EventTypeInvalid EventType = iota
	// EventTypeOp is a completed operation interval.
	EventTypeOp
	// EventTypeWait is a completed wait interval: an op was blocked on
	// another op (or named resource) from Start to End.
	EventTypeWait
	// EventTypeLink is a non-blocking correlation between an op and another
	// op or an interned identifier.
	EventTypeLink
)

// OpKind classifies operations.
type OpKind uint8

const (
	OpKindInvalid OpKind = iota
	// OpKindCall is one dagql GetOrInitCall invocation (per caller,
	// including cache hits and singleflight joiners).
	OpKindCall
	// OpKindCallExec is the shared execution of a call's resolver function.
	// Singleflighted callers all wait on one of these.
	OpKindCallExec
	// OpKindLazy is one run of a lazy evaluation callback for a result.
	OpKindLazy
	// OpKindExecPhase is a setup/run phase of a container exec
	// (e.g. exec.setupNetwork, exec.runContainer).
	OpKindExecPhase
	// OpKindExec is the overall run of a container by the executor.
	OpKindExec
	// OpKindServiceStart is the start (incl. health check) of a service.
	OpKindServiceStart
	// OpKindSessionPhase is a per-query session serving phase
	// (e.g. session.workspaceLoad, session.query).
	OpKindSessionPhase
	// OpKindIO is a leaf I/O operation (git fetch, image pull, filesync...).
	OpKindIO
	// OpKindInternal is engine-internal background work (gc, persistence).
	OpKindInternal
)

var opKindNames = map[OpKind]string{
	OpKindCall:         "call",
	OpKindCallExec:     "call_exec",
	OpKindLazy:         "lazy",
	OpKindExecPhase:    "exec_phase",
	OpKindExec:         "exec",
	OpKindServiceStart: "service_start",
	OpKindSessionPhase: "session_phase",
	OpKindIO:           "io",
	OpKindInternal:     "internal",
}

func (k OpKind) String() string {
	if s, ok := opKindNames[k]; ok {
		return s
	}
	return "invalid"
}

// WorkType is a coarse attribution of an op's self-time, used by analysis to
// separate actionable engine overhead from user workload time and external
// I/O time.
type WorkType uint8

const (
	WorkTypeEngine WorkType = iota
	// WorkTypeUser is time running user-controlled work (e.g. the container
	// process itself).
	WorkTypeUser
	// WorkTypeExternal is time bound on external systems (registry pulls,
	// git remotes, host filesync...).
	WorkTypeExternal
)

var workTypeNames = map[WorkType]string{
	WorkTypeEngine:   "engine",
	WorkTypeUser:     "user",
	WorkTypeExternal: "external",
}

func (w WorkType) String() string {
	if s, ok := workTypeNames[w]; ok {
		return s
	}
	return "invalid"
}

// Outcome describes how an op completed.
type Outcome uint8

const (
	OutcomeNone Outcome = iota
	// OutcomeHit: dagql call satisfied from cache.
	OutcomeHit
	// OutcomeExecuted: dagql call missed cache and this caller spawned the
	// execution.
	OutcomeExecuted
	// OutcomeJoined: dagql call missed cache and joined an in-flight
	// execution started by another caller.
	OutcomeJoined
	// OutcomeDoNotCache: dagql call executed inline without caching.
	OutcomeDoNotCache
	// OutcomeOK: generic success for non-call ops.
	OutcomeOK
	// OutcomeError: op failed.
	OutcomeError
	// OutcomeCanceled: op canceled.
	OutcomeCanceled
)

var outcomeNames = map[Outcome]string{
	OutcomeNone:       "",
	OutcomeHit:        "hit",
	OutcomeExecuted:   "executed",
	OutcomeJoined:     "joined",
	OutcomeDoNotCache: "do_not_cache",
	OutcomeOK:         "ok",
	OutcomeError:      "error",
	OutcomeCanceled:   "canceled",
}

func (o Outcome) String() string {
	if s, ok := outcomeNames[o]; ok {
		return s
	}
	return "invalid"
}

// WaitReason describes why an op was blocked.
type WaitReason uint8

const (
	WaitReasonInvalid WaitReason = iota
	// WaitReasonCallExec: blocked waiting for a call's resolver execution
	// (the caller that spawned it).
	WaitReasonCallExec
	// WaitReasonSingleflight: blocked joining another caller's in-flight
	// execution of the same call.
	WaitReasonSingleflight
	// WaitReasonLazy: blocked waiting for a lazy evaluation to finish.
	WaitReasonLazy
	// WaitReasonService: blocked waiting for a service to start/be healthy.
	WaitReasonService
	// WaitReasonLock: blocked acquiring a lock (target is an interned
	// resource name, not an op).
	WaitReasonLock
	// WaitReasonExec: blocked waiting for a container exec to finish.
	WaitReasonExec
	// WaitReasonIO: blocked on external I/O.
	WaitReasonIO
)

var waitReasonNames = map[WaitReason]string{
	WaitReasonCallExec:     "call_exec",
	WaitReasonSingleflight: "singleflight",
	WaitReasonLazy:         "lazy",
	WaitReasonService:      "service",
	WaitReasonLock:         "lock",
	WaitReasonExec:         "exec",
	WaitReasonIO:           "io",
}

func (r WaitReason) String() string {
	if s, ok := waitReasonNames[r]; ok {
		return s
	}
	return "invalid"
}

// LinkKind classifies link events.
type LinkKind uint8

const (
	LinkKindInvalid LinkKind = iota
	// LinkKindNestedClient: op (an exec) hosts a nested client whose ID is
	// the link's interned ident. Ops recorded for that client belong under
	// this exec.
	LinkKindNestedClient
	// LinkKindResult: op produced or returned the dagql result with
	// ResultID.
	LinkKindResult
	// LinkKindReusedResult: op was satisfied by reusing the dagql result
	// with ResultID (cache hit).
	LinkKindReusedResult
)

var linkKindNames = map[LinkKind]string{
	LinkKindNestedClient: "nested_client",
	LinkKindResult:       "result",
	LinkKindReusedResult: "reused_result",
}

func (k LinkKind) String() string {
	if s, ok := linkKindNames[k]; ok {
		return s
	}
	return "invalid"
}

// Event is one fixed-size profiling record. Field meaning varies by Type:
//
//   - Op: OpID/ParentID identify the op and its structural parent. StartNS
//     and EndNS bound the interval. ClassID/IdentID/ClientID are interned
//     strings. ResultID is the dagql shared result ID when known.
//   - Wait: ParentID is the waiting op, TargetID the awaited op (or 0 when
//     waiting on a named resource, in which case IdentID names it).
//   - Link: ParentID is the from-op, TargetID an op (optional), IdentID an
//     interned identifier (optional), ResultID a dagql result (optional).
type Event struct {
	Type     EventType
	OpKind   OpKind
	WorkType WorkType
	Outcome  Outcome
	Reason   WaitReason
	LinkKind LinkKind

	OpID     uint64
	ParentID uint64
	TargetID uint64
	ResultID uint64

	ClassID  uint32
	IdentID  uint32
	ClientID uint32

	StartNS int64
	EndNS   int64
}

const defaultMaxEvents = 4 << 20 // ~4M events ≈ 300MB

const numShards = 16

type shard struct {
	mu     sync.Mutex
	events []Event
	// openOps tracks begun-but-unfinished ops for dump-time visibility.
	openOps map[uint64]openOp
}

type openOp struct {
	op       OpKind
	work     WorkType
	classID  uint32
	identID  uint32
	clientID uint32
	parentID uint64
	startNS  int64
}

// Recorder collects profiling events. Safe for concurrent use.
type Recorder struct {
	epoch     time.Time // monotonic anchor
	wallEpoch int64     // wall-clock unix nanos at epoch

	nextOpID atomic.Uint64
	dropped  atomic.Uint64
	maxTotal int64
	total    atomic.Int64

	strings stringTable

	shards [numShards]shard
}

// NewRecorder returns a recorder with the given event cap (<=0 means
// default).
func NewRecorder(maxEvents int64) *Recorder {
	if maxEvents <= 0 {
		maxEvents = defaultMaxEvents
	}
	now := time.Now()
	r := &Recorder{
		epoch:     now,
		wallEpoch: now.UnixNano(),
		maxTotal:  maxEvents,
	}
	r.strings.init()
	for i := range r.shards {
		r.shards[i].openOps = make(map[uint64]openOp)
	}
	return r
}

// Now returns the current recorder-relative timestamp in nanoseconds.
func (r *Recorder) Now() int64 {
	return int64(time.Since(r.epoch))
}

// Intern interns s and returns its table ID.
func (r *Recorder) Intern(s string) uint32 {
	return r.strings.intern(s)
}

func (r *Recorder) newOpID() uint64 {
	return r.nextOpID.Add(1)
}

func (r *Recorder) shardFor(id uint64) *shard {
	return &r.shards[id%numShards]
}

func (r *Recorder) append(sh *shard, ev Event) {
	if r.total.Add(1) > r.maxTotal {
		r.total.Add(-1)
		r.dropped.Add(1)
		return
	}
	sh.mu.Lock()
	sh.events = append(sh.events, ev)
	sh.mu.Unlock()
}

//
// enablement
//

var (
	// global is the recorder, created the first time any profiling is enabled
	// and retained from then on (so buffered events survive a disable and
	// periodic-drain dumps from one engine run always share an epoch).
	global atomic.Pointer[Recorder]
	// globalOn means record all work on the engine, regardless of whether
	// the context is marked for per-session profiling.
	globalOn atomic.Bool
)

func init() {
	if os.Getenv("_DAGGER_WCPROF") != "" {
		EnableGlobal()
	}
}

// Active returns the recorder, or nil if profiling was never enabled. The
// recorder outlives DisableGlobal so buffered events can still be dumped;
// use GloballyEnabled/Enabled to ask whether work is being recorded.
func Active() *Recorder {
	return global.Load()
}

// GloballyEnabled reports whether engine-global recording is on.
func GloballyEnabled() bool {
	return globalOn.Load()
}

// EnableGlobal turns on recording of all work on the engine.
func EnableGlobal() {
	EnsureRecorder()
	globalOn.Store(true)
}

// DisableGlobal turns off engine-global recording. The recorder and its
// buffered events are retained for dumping, sessions that explicitly enabled
// profiling keep recording, and already-recording flows (contexts carrying a
// recorded op) run to completion.
func DisableGlobal() {
	globalOn.Store(false)
}

// EnsureRecorder creates the recorder if it does not exist yet, without
// turning on engine-global recording. Used when a session opts into
// profiling.
func EnsureRecorder() {
	if global.Load() != nil {
		return
	}
	maxEvents := int64(0)
	if v := os.Getenv("_DAGGER_WCPROF_MAX_EVENTS"); v != "" {
		// best-effort parse; ignore malformed values
		var parsed int64
		for _, c := range v {
			if c < '0' || c > '9' {
				parsed = 0
				break
			}
			parsed = parsed*10 + int64(c-'0')
		}
		maxEvents = parsed
	}
	global.CompareAndSwap(nil, NewRecorder(maxEvents))
}

//
// string interning
//

type stringTable struct {
	mu      sync.RWMutex
	byValue map[string]uint32
	values  []string
}

func (t *stringTable) init() {
	t.byValue = make(map[string]uint32)
	// ID 0 is always the empty string so 0 can mean "none".
	t.values = []string{""}
	t.byValue[""] = 0
}

func (t *stringTable) intern(s string) uint32 {
	if s == "" {
		return 0
	}
	t.mu.RLock()
	id, ok := t.byValue[s]
	t.mu.RUnlock()
	if ok {
		return id
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if id, ok := t.byValue[s]; ok {
		return id
	}
	id = uint32(len(t.values))
	t.values = append(t.values, s)
	t.byValue[s] = id
	return id
}

func (t *stringTable) snapshot() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, len(t.values))
	copy(out, t.values)
	return out
}
