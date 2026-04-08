package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql"
)

type Lazy[T dagql.Typed] interface {
	Evaluate(context.Context, T) error
	AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error)
	EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error)
}

type LazyState struct {
	LazyMu           *sync.Mutex
	LazyInitComplete bool
}

func NewLazyState() LazyState {
	return LazyState{
		LazyMu: new(sync.Mutex),
	}
}

func (lazy *LazyState) Evaluate(ctx context.Context, typeName string, run func(context.Context) error) error {
	if lazy.LazyInitComplete {
		return nil
	}
	if run == nil {
		lazy.LazyInitComplete = true
		return nil
	}

	if lazy.LazyMu == nil {
		return fmt.Errorf("invalid %s: missing LazyMu", typeName)
	}

	lazy.LazyMu.Lock()
	defer lazy.LazyMu.Unlock()

	if lazy.LazyInitComplete {
		return nil
	}
	if err := run(ctx); err != nil {
		return err
	}
	lazy.LazyInitComplete = true
	return nil
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
