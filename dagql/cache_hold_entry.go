package dagql

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrUnknownEntry is returned for an entry number the cache has not
// registered: it never had one, or collected it.
var ErrUnknownEntry = errors.New("unknown entry")

// HoldEntry holds the registered entry numbered number, without a session,
// and returns it undecoded, as a result an export and OfferParts accept, with
// a function that releases the hold.
//
// The hold is an ownership unit on the entry, like a task's. While it lasts
// the entry is not collected, and it counts as a use: a publication or merge
// that finds the entry's value expired retires the entry rather than
// replacing its value in place, so what an export or offer reads of the entry
// stays that one value.
//
// It refuses an entry known only through other caches' holdings, which has no
// value (errEntryHasNoValue). A cached nil result has one, and is held.
func (c *Cache) HoldEntry(ctx context.Context, number uint64) (AnyResult, func(context.Context) error, error) {
	op, err := c.beginCacheOperation()
	if err != nil {
		return nil, nil, err
	}
	defer op.finish(false)
	c.egraphMu.Lock()
	res := c.resultsByID[sharedResultID(number)]
	switch {
	case res == nil:
		c.egraphMu.Unlock()
		return nil, nil, fmt.Errorf("hold entry %d: %w", number, ErrUnknownEntry)
	case res.noValueLocked():
		c.egraphMu.Unlock()
		return nil, nil, fmt.Errorf("hold entry %d: %w", number, errEntryHasNoValue)
	}
	c.incrementIncomingOwnershipLocked(ctx, res)
	c.egraphMu.Unlock()

	var once sync.Once
	release := func(ctx context.Context) error {
		var err error
		once.Do(func() {
			ctx = context.WithoutCancel(ctx)
			c.egraphMu.Lock()
			queue, decErr := c.decrementIncomingOwnershipLocked(ctx, res, nil)
			releases, collectErr := c.collectUnownedResultsLocked(ctx, queue)
			c.egraphMu.Unlock()
			err = errors.Join(decErr, collectErr, runOnReleaseFuncs(ctx, releases))
		})
		return err
	}
	return Result[Typed]{shared: res}, release, nil
}
