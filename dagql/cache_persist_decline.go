package dagql

import (
	"context"
	"sync/atomic"
)

// persistDeclineKey carries the persistDecline of the call whose resolver
// runs under a context.
type persistDeclineKey struct{}

// persistDecline records that a call's resolver declined to persist its
// result.
type persistDecline struct{ declined atomic.Bool }

func (d *persistDecline) isDeclined() bool { return d != nil && d.declined.Load() }

// DeclinePersistence asks the cache not to persist the result of the call
// whose resolver runs under ctx, as if its field weren't persistable: the
// result gets no retention edge, so it isn't kept after the sessions using
// it end, and a later hit on it doesn't persist it either. A resolver of a
// persistable field calls it when only its result shows that keeping it
// would cost more than it saves, such as a large read. It must be called
// with the resolver's own context: a callback or lazy body the resolver
// starts shares its caller's mark, so a call there would decline for that
// caller. Outside a resolver the cache runs, it does nothing.
func DeclinePersistence(ctx context.Context) {
	if d, _ := ctx.Value(persistDeclineKey{}).(*persistDecline); d != nil {
		d.declined.Store(true)
	}
}
