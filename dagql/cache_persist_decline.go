package dagql

import (
	"context"
	"strings"
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
// The result's identity holds its session too (boundSessionInputName), so it
// is in a class of its session's own: a call made on it, such as a field
// that returns a result it holds, answers for that session only, though
// another session makes the bound call again with the same key.
// Like DeclinePersistence, it must be called with the resolver's own
// context, and does nothing outside a resolver the cache runs.
func BindToSession(ctx context.Context) {
	if d, _ := ctx.Value(persistDeclineKey{}).(*persistDecline); d != nil {
		d.declined.Store(true)
		d.bound.Store(true)
	}
}

// boundSessionInputName is the implicit input a result bound to its session
// carries in its identity, valued with the handle only that session holds
// (Cache.boundSessionHandle). A bound result is published under its call
// with this input, so its digests and terms are its session's and so is its
// class, and every call made on it derives its key from that identity. The
// session's own later lookups of the call try it with the input too
// (Cache.lookupCacheForRequest).
const boundSessionInputName = "cacheBoundSession"

// withBoundSessions returns a copy of frame that holds the bindings to the
// sessions handles name, in order: frame's identity, as those sessions'
// own. The copy drops frame's carried content-preferred digest, which was
// derived from the identity without the bindings.
func (frame *ResultCall) withBoundSessions(handles []SessionResourceHandle) *ResultCall {
	if frame == nil {
		return nil
	}
	names := make([]string, len(handles))
	for i, handle := range handles {
		names[i] = string(handle)
	}
	bound := frame.clone()
	bound.ContentPreferred = nil
	bound.ImplicitInputs = append(bound.ImplicitInputs, &ResultCallArg{
		Name:  boundSessionInputName,
		Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: strings.Join(names, ",")},
	})
	return bound
}

// withBoundSessions returns a copy of req whose call holds the bindings to
// the sessions handles name (ResultCall.withBoundSessions).
func (req *CallRequest) withBoundSessions(handles []SessionResourceHandle) *CallRequest {
	bound := req.Clone()
	bound.ResultCall = req.ResultCall.withBoundSessions(handles)
	return bound
}

// withBoundSession is withBoundSessions for one session's binding: the call
// as that session's own.
func (req *CallRequest) withBoundSession(handle SessionResourceHandle) *CallRequest {
	return req.withBoundSessions([]SessionResourceHandle{handle})
}
