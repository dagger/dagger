package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

type Lazy[T dagql.Typed] interface {
	Evaluate(context.Context, T) error
	AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error)
	EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error)
}

type LazyState struct {
	// LazyMu guards the latch transitions and groups. For whole-op
	// evaluation (Evaluate) it is additionally held across the body, as
	// it always was. Per-group evaluation (EvaluateGroup) holds it only
	// for latch checks and map access, never across a body: carrying the
	// hold-across-the-body shape to groups would serialize sibling groups
	// of one op against each other.
	LazyMu *sync.Mutex
	// lazyInitComplete is the whole-op latch. Atomic because the
	// long-standing lock-free fast path in Evaluate reads it while a
	// concurrent runner's body sets it, and per-part evaluation makes
	// direct-versus-cache overlap on one op routine; the latch is still
	// only ever set under LazyMu.
	lazyInitComplete atomic.Bool
	// groups holds per-group run-once state; nil until the first named-
	// group use, so ops evaluated whole allocate nothing new.
	groups map[dagql.LazyGroupKey]*lazyGroupOnce
}

// lazyGroupOnce is one group's object-side run-once state. mu is held
// across the group's body: it serializes every runner of this group
// (cache attempt or direct call) and makes the second a no-op via done,
// exactly the whole-op coordination Evaluate provides, per group. done
// is atomic so consumption checks never block on a sibling runner's
// in-flight body.
type lazyGroupOnce struct {
	mu   sync.Mutex
	done atomic.Bool
}

func NewLazyState() LazyState {
	return LazyState{
		LazyMu: new(sync.Mutex),
	}
}

func (lazy *LazyState) Evaluate(ctx context.Context, typeName string, run func(context.Context) error) (rerr error) {
	if lazy.lazyInitComplete.Load() {
		return nil
	}
	if run == nil {
		lazy.lazyInitComplete.Store(true)
		return nil
	}

	if lazy.LazyMu == nil {
		return fmt.Errorf("invalid %s: missing LazyMu", typeName)
	}

	lazy.LazyMu.Lock()
	defer lazy.LazyMu.Unlock()

	if lazy.lazyInitComplete.Load() {
		return nil
	}

	start := time.Now()
	slog.InfoContext(ctx, "start lazy evaluation",
		"field", typeName,
	)
	defer func() {
		args := []any{
			"field", typeName,
			"duration", time.Since(start),
		}
		if rerr != nil {
			args = append(args, "err", rerr)
		}
		slog.InfoContext(ctx, "end lazy evaluation", args...)
	}()

	if rerr = run(ctx); rerr != nil {
		return rerr
	}
	lazy.lazyInitComplete.Store(true)
	return nil
}

// EvaluateGroup runs one evaluation group's body exactly once. Per op
// exactly one of Evaluate and EvaluateGroup is in use, mirroring the
// cache-side regime split between whole-result and named-group
// evaluation.
func (lazy *LazyState) EvaluateGroup(ctx context.Context, typeName string, group dagql.LazyGroupKey, run func(context.Context) error) (rerr error) {
	if lazy.LazyMu == nil {
		return fmt.Errorf("invalid %s: missing LazyMu", typeName)
	}

	lazy.LazyMu.Lock()
	if lazy.lazyInitComplete.Load() {
		lazy.LazyMu.Unlock()
		return nil
	}
	g := lazy.groups[group]
	if g == nil {
		if lazy.groups == nil {
			lazy.groups = make(map[dagql.LazyGroupKey]*lazyGroupOnce)
		}
		g = &lazyGroupOnce{}
		lazy.groups[group] = g
	}
	lazy.LazyMu.Unlock()

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done.Load() {
		return nil
	}
	if run == nil {
		g.done.Store(true)
		return nil
	}

	start := time.Now()
	slog.InfoContext(ctx, "start lazy group evaluation",
		"field", typeName,
		"group", string(group),
	)
	defer func() {
		args := []any{
			"field", typeName,
			"group", string(group),
			"duration", time.Since(start),
		}
		if rerr != nil {
			args = append(args, "err", rerr)
		}
		slog.InfoContext(ctx, "end lazy group evaluation", args...)
	}()

	if rerr = run(ctx); rerr != nil {
		return rerr
	}
	g.done.Store(true)
	return nil
}

// GroupConsumed reports whether the group's body already ran to success
// (or the whole op is complete).
func (lazy *LazyState) GroupConsumed(group dagql.LazyGroupKey) bool {
	if lazy.LazyMu == nil {
		return false
	}
	lazy.LazyMu.Lock()
	defer lazy.LazyMu.Unlock()
	return lazy.groupConsumedLocked(group)
}

// seedConsumedGroups initializes restored body completion before publication.
// It leaves the whole-op latch unset so saved snapshots can still open.
// Callers must own construction of this state; it must have no runners yet.
func (lazy *LazyState) seedConsumedGroups(groups ...dagql.LazyGroupKey) {
	if lazy.groups == nil {
		lazy.groups = make(map[dagql.LazyGroupKey]*lazyGroupOnce, len(groups))
	}
	for _, group := range groups {
		g := lazy.groups[group]
		if g == nil {
			g = new(lazyGroupOnce)
			lazy.groups[group] = g
		}
		g.done.Store(true)
	}
}

// groupConsumedLocked requires LazyMu held. done is read atomically, not
// under g.mu, so a consumption check never blocks on a sibling runner's
// in-flight body; a stale false only costs a no-op re-entry into
// EvaluateGroup, which g.mu resolves.
func (lazy *LazyState) groupConsumedLocked(group dagql.LazyGroupKey) bool {
	if lazy.lazyInitComplete.Load() {
		return true
	}
	g := lazy.groups[group]
	if g == nil {
		return false
	}
	return g.done.Load()
}

// ContainerLazyState exposes the op's lazy latch state to the container
// group-routing layer. Promoted through embedding, it makes every
// embedding op satisfy that slice of LazyContainerParts for free.
func (lazy *LazyState) ContainerLazyState() *LazyState {
	return lazy
}

type LazyAccessor[V any, T dagql.Typed] struct {
	value V // should not be gotten/set directly except for actual evaluation implementations!
	isSet bool
	mu    sync.RWMutex
}

// WARN: res MUST be the dagql result wrapper for the same owner object as this
// accessor. The accessor cannot validate that today due to the current
// Directory/File/Container vs dagql.Result split, so callers must pass the
// matching result explicitly and carefully.
func (a *LazyAccessor[V, T]) GetOrEval(ctx context.Context, res dagql.Result[T]) (V, error) {
	var zero V

	c, err := dagql.EngineCache(ctx)
	if err != nil {
		return zero, err
	}
	err = c.Evaluate(ctx, res)
	if err != nil {
		return zero, err
	}

	// evaluate should have set our value now, so we can return it
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.isSet {
		return zero, fmt.Errorf("lazy accessor value not set after evaluation")
	}
	return a.value, nil
}

// Peek returns the current stored value without triggering lazy evaluation.
func (a *LazyAccessor[V, T]) Peek() (V, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.isSet {
		return a.value, true
	}
	var zero V
	return zero, false
}

// should only be called by implementations of evaluate for the relevant type!
func (a *LazyAccessor[V, T]) setValue(v V) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.value = v
	a.isSet = true
}

// SetValue is for constructors and lazy evaluation implementations that need to
// pre-seed or materialize an accessor explicitly.
func (a *LazyAccessor[V, T]) SetValue(v V) {
	a.setValue(v)
}
