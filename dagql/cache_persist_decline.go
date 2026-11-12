package dagql

import (
	"context"
	"sync/atomic"
)

// persistDeclineKey carries the persistDecline of the call whose resolver
// runs under a context.
type persistDeclineKey struct{}

// persistDecline records that a call's resolver declined to persist its
// result, and whether it also bound the result to its session
// (BindToSession).
type persistDecline struct{ declined, bound atomic.Bool }

func (d *persistDecline) isDeclined() bool { return d != nil && d.declined.Load() }

func (d *persistDecline) isBound() bool { return d != nil && d.bound.Load() }

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

// BindToSession makes the result of the call whose resolver runs under ctx
// usable only by the session that computed it, and declines to persist it.
// A resolver calls it when its result must not outlive its session though
// the call's key doesn't name the session: it stands for calls another
// session would make with another outcome, or for an outcome another session
// must make again. Other sessions are never served the result, whether its
// session still uses it or anything else still holds it: each computes its
// own. The cache gates the hit as it gates one on a session resource the
// session hasn't loaded, so what is built on the result is bound with it.
// Like DeclinePersistence, it must be called with the resolver's own
// context, and does nothing outside a resolver the cache runs.
func BindToSession(ctx context.Context) {
	if d, _ := ctx.Value(persistDeclineKey{}).(*persistDecline); d != nil {
		d.declined.Store(true)
		d.bound.Store(true)
	}
}
