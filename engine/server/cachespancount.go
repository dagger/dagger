package server

import (
	"context"
	"sync/atomic"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// cacheSpanCounter counts one session's cache spans: the spans that end
// naming a cache entry with dagger.io/cache.result.id, call spans with cache
// evidence and lazy-evaluation spans alike, that the session's store keeps.
// The session-end carrier declares the total (dagger.io/cache.session.spans),
// so a consumer of the session's spans, such as the Cloud reading them from
// the store, can tell when it has read all of them.
//
// Unlike wcprofSpanCounter it counts per session, not per trace: one is
// registered on each session's tracer provider, so sessions that share a
// trace count apart.
type cacheSpanCounter struct {
	count atomic.Int64
}

func (c *cacheSpanCounter) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

// OnEnd counts the span when it ends naming a cache entry and has an origin
// client, by the condition the session's span exporter applies before the
// store (spanOriginClientID): a span without one never reaches the store, so
// never the Cloud. The origin is stamped at the span's start and the result
// number at its end, so a span is counted once.
func (c *cacheSpanCounter) OnEnd(s sdktrace.ReadOnlySpan) {
	if spanOriginClientID(s) == "" {
		return
	}
	for _, kv := range s.Attributes() {
		if kv.Key == telemetryattrs.CacheResultIDAttr {
			c.count.Add(1)
			return
		}
	}
}

func (c *cacheSpanCounter) Shutdown(context.Context) error   { return nil }
func (c *cacheSpanCounter) ForceFlush(context.Context) error { return nil }

// Count returns the number of cache spans the session has ended so far.
func (c *cacheSpanCounter) Count() int64 {
	if c == nil {
		return 0
	}
	return c.count.Load()
}
