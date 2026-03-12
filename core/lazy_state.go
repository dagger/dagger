package core

import (
	"context"
	"fmt"
	"sync"
)

// TODO: use a more proper implementation here; this is a simple one to get
// started. In particular, we need to handle context cancellation from one
// caller without necessarily breaking all other callers. One option is to use
// the implementation of ChangesCache in the filesync package.
type LazyInitFunc func(context.Context) error

// LazyState holds lazy initialization configuration and completion state.
// It is embedded by core objects that support deferred evaluation.
type LazyState struct {
	LazyMu   *sync.Mutex
	LazyInit LazyInitFunc
	// AfterEvaluate runs exactly once after the lazy init completes
	// successfully. It is intended for observers that need to react to natural
	// realization, not to trigger realization themselves.
	AfterEvaluate []func(context.Context)
	// LazyInitComplete tracks whether this object's lazy init callback has
	// already been executed.
	LazyInitComplete bool
}

func NewLazyState() LazyState {
	return LazyState{
		LazyMu: new(sync.Mutex),
	}
}

func (lazy *LazyState) Evaluate(ctx context.Context, typeName string) error {
	if lazy.LazyInitComplete {
		return nil
	}

	if lazy.LazyInit == nil {
		lazy.LazyInitComplete = true
		return nil
	}

	if lazy.LazyMu == nil {
		return fmt.Errorf("invalid %s: missing LazyMu", typeName)
	}

	lazy.LazyMu.Lock()

	// Single-flight: once locked, re-check completion and init callback.
	if lazy.LazyInitComplete {
		lazy.LazyMu.Unlock()
		return nil
	}
	if lazy.LazyInit == nil {
		lazy.LazyInitComplete = true
		callbacks := lazy.AfterEvaluate
		lazy.AfterEvaluate = nil
		lazy.LazyMu.Unlock()
		for _, callback := range callbacks {
			if callback != nil {
				callback(ctx)
			}
		}
		return nil
	}

	if err := lazy.LazyInit(ctx); err != nil {
		lazy.LazyMu.Unlock()
		return err
	}

	lazy.LazyInitComplete = true
	callbacks := lazy.AfterEvaluate
	lazy.AfterEvaluate = nil
	lazy.LazyMu.Unlock()
	for _, callback := range callbacks {
		if callback != nil {
			callback(ctx)
		}
	}
	return nil
}

// OnEvaluateComplete registers a one-shot callback to run after natural lazy
// evaluation succeeds. It returns true if the value had already completed
// evaluation at registration time.
func (lazy *LazyState) OnEvaluateComplete(fn func(context.Context)) bool {
	if lazy == nil {
		return true
	}
	if lazy.LazyMu == nil {
		lazy.LazyMu = new(sync.Mutex)
	}
	lazy.LazyMu.Lock()
	defer lazy.LazyMu.Unlock()
	if lazy.LazyInitComplete {
		return true
	}
	lazy.AfterEvaluate = append(lazy.AfterEvaluate, fn)
	return false
}
