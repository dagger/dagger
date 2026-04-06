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

// TODO: use a more proper implementation here; this is a simple one to get
// started. In particular, we need to handle context cancellation from one
// caller without necessarily breaking all other callers. One option is to use
// the implementation of ChangesCache in the filesync package.
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
