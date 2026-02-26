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
	defer lazy.LazyMu.Unlock()

	// Single-flight: once locked, re-check completion and init callback.
	if lazy.LazyInitComplete {
		return nil
	}
	if lazy.LazyInit == nil {
		lazy.LazyInitComplete = true
		return nil
	}

	if err := lazy.LazyInit(ctx); err != nil {
		return err
	}

	lazy.LazyInitComplete = true
	return nil
}
