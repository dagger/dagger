package dagql

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkCacheLookupInASessionThatHasBound times a call that hits, in a
// session that has bound nothing, in one that has bound a result of a call
// of another field, and in one that has bound a result of another call of
// the same field. A session looks a call up with its binding first only for
// a field it has bound a result for (lookupCacheForRequest), so that lookup is
// the cost binding adds.
func BenchmarkCacheLookupInASessionThatHasBound(b *testing.B) {
	for _, bound := range []string{"", "bound-elsewhere", "looked-up"} {
		name := "has bound nothing"
		if bound != "" {
			name = "has bound a call of " + bound
		}
		b.Run(name, func(b *testing.B) {
			benchmarkCacheLookupInSession(b, bound)
		})
	}
}

// benchmarkCacheLookupInSession times the hit of a call of the field
// looked-up, with four arguments, in a session that has bound a result of a
// call of the field bound, unless bound is empty.
func benchmarkCacheLookupInSession(b *testing.B, bound string) {
	ctx := b.Context()
	c, err := NewCache(ctx, "", nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	const session = "bench-session"
	sctx := currentEntryTestSession(ctx, c, session)
	if bound != "" {
		boundCall := cacheTestIntCall(bound)
		if _, err := c.GetOrInitCall(sctx, session, noopTypeResolver{}, &CallRequest{ResultCall: boundCall.clone()}, func(ctx context.Context) (AnyResult, error) {
			BindToSession(ctx)
			return cacheTestIntResult(boundCall.clone(), 1), nil
		}); err != nil {
			b.Fatal(err)
		}
	}
	// A call with arguments, as most are, so its digests take some work.
	frame := cacheTestIntCall("looked-up")
	for i, value := range []string{"github.com/dagger/dagger", "main", "./sdk/go", "v1.2.3"} {
		arg, err := resultCallArgFromInput(sctx, fmt.Sprintf("arg%d", i), NewString(value), false)
		if err != nil {
			b.Fatal(err)
		}
		frame.Args = append(frame.Args, arg)
	}
	mk := func(context.Context) (AnyResult, error) { return cacheTestIntResult(frame.clone(), 2), nil }
	if _, err := c.GetOrInitCall(sctx, session, noopTypeResolver{}, &CallRequest{ResultCall: frame.clone()}, mk); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, err := c.GetOrInitCall(sctx, session, noopTypeResolver{}, &CallRequest{ResultCall: frame.clone()}, mk)
		if err != nil {
			b.Fatal(err)
		}
		if !res.HitCache() {
			b.Fatal("the call must hit")
		}
	}
	b.StopTimer()
	if err := c.ReleaseSession(sctx, session); err != nil {
		b.Fatal(err)
	}
}
