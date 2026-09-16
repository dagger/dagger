package dagql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// LazyTaskSpec supplies a coordination body, independent of the value's native
// lazy callback. A successful publication retains the continuation on failure.
type LazyTaskSpec struct {
	Body           func(context.Context) error
	AfterOwnerSync func(context.Context) error
	Settled        func(context.Context) error
	OwnerSyncReady <-chan struct{}
	NoJoin         bool
}

var ErrLazyTaskBusy = errors.New("lazy task already active or awaiting bookkeeping")

type PartTaskToken struct {
	row           *sharedResult
	key           LazyGroupKey
	generation    uint64
	active        atomic.Bool
	installed     atomic.Pointer[InstalledOutputs]
	ownerSync     chan struct{}
	ownerSyncOnce sync.Once
	settled       atomic.Bool
}

// InstalledOutputs is immutable after publication. Installation identity is
// distinct from the generation executing a later bookkeeping-only retry.
type InstalledOutputs struct {
	outputs     []installedPartOutput
	protections []*partProtection
}
type installedPartOutput struct {
	address      PersistedPartAddress
	installation uint64
}
type partTaskContextKey struct{}

func PartTaskFromContext(ctx context.Context) *PartTaskToken {
	token, _ := ctx.Value(partTaskContextKey{}).(*PartTaskToken)
	return token
}

type lazyTaskContinuation struct {
	spec                     LazyTaskSpec
	token                    *PartTaskToken
	synced, cleaned, settled bool
}

func (t *lazyTaskContinuation) finish(ctx context.Context, c *Cache, row *sharedResult) error {
	if t.spec.OwnerSyncReady != nil {
		select {
		case <-t.spec.OwnerSyncReady:
		case <-t.token.ownerSync:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	if !t.synced {
		if err := c.syncResultSnapshotLeases(ctx, row); err != nil {
			return err
		}
		t.synced = true
	}
	if !t.cleaned {
		if installed := t.token.installed.Load(); installed != nil {
			for _, protection := range installed.protections {
				if err := protection.release(ctx); err != nil {
					return err
				}
			}
		}
		if t.spec.AfterOwnerSync != nil {
			if err := t.spec.AfterOwnerSync(ctx); err != nil {
				return err
			}
		}
		t.cleaned = true
	}
	if !t.settled {
		if t.spec.Settled != nil {
			if err := t.spec.Settled(ctx); err != nil {
				return err
			}
		}
		t.settled = true
	}
	t.token.settled.Store(true)
	return nil
}

func (c *Cache) RunLazyTask(ctx context.Context, receiver AnyResult, key LazyGroupKey, spec LazyTaskSpec) (rerr error) {
	op, row, err := c.beginEvaluateOne(ctx, receiver)
	if err != nil {
		return err
	}
	if row == nil {
		return fmt.Errorf("lazy task: missing receiver")
	}
	defer func() {
		if op.finish(rerr == nil) {
			rerr = errors.Join(rerr, ErrCacheSessionReleased)
		}
	}()
	if key == LazyGroupWhole {
		return fmt.Errorf("lazy task: synthetic task requires a named key")
	}
	c.egraphMu.Lock()
	if c.resultsByID[row.id] != row {
		c.egraphMu.Unlock()
		return fmt.Errorf("lazy task: unregistered receiver")
	}
	c.incrementIncomingOwnershipLocked(ctx, row)
	c.egraphMu.Unlock()
	defer func() { rerr = errors.Join(rerr, c.releasePartRow(context.WithoutCancel(ctx), row)) }()
	return c.runLazyTask(ctx, receiver, row, key, nil, &spec)
}

func (c *Cache) releasePartRow(ctx context.Context, row *sharedResult) error {
	c.egraphMu.Lock()
	queue, err := c.decrementIncomingOwnershipLocked(ctx, row, nil)
	callbacks, collectErr := c.collectUnownedResultsLocked(ctx, queue)
	c.egraphMu.Unlock()
	return errors.Join(err, collectErr, runOnReleaseFuncs(ctx, callbacks))
}

func isPartTaskKey(key LazyGroupKey) bool {
	return strings.HasPrefix(string(key), "obtain:") || strings.HasPrefix(string(key), "acquire:") || strings.HasPrefix(string(key), "producer:")
}

func (t *PartTaskToken) openOwnerSync() {
	t.ownerSyncOnce.Do(func() {
		if t.ownerSync != nil {
			close(t.ownerSync)
		}
	})
}
