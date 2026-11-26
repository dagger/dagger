package dagql

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// BenchmarkLateRemoteMisses times one restored part's late count
// (LateRemoteMisses) on a connection that logged n misses, none of which the
// part lands, as most of a long-lived connection's misses are for most
// parts: each call missed, ran and was stored, and the part's entries are
// others. The calls come in chains of eight, so each but the first names a
// term over the call before it, and every fourth carries a content digest.
// With a writer, a goroutine holds the graph lock for writing for 200 µs of
// every 500 µs meanwhile, standing in for the engine's own calls and merges.
// It reports the graph lock's holds for reading that one count takes.
func BenchmarkLateRemoteMisses(b *testing.B) {
	const partEntries = 4000
	for _, n := range []int{1000, 4000, remoteLookupLogCapacity} {
		for _, writer := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d misses logged, writer %v", n, writer), func(b *testing.B) {
				ctx := cacheTestContext(b.Context())
				c, err := NewCache(ctx, "", nil, nil)
				require.NoError(b, err)
				ctx = ContextWithCache(ctx, c)
				bridge, _, err := c.AttachRemoteCacheBridge()
				require.NoError(b, err)
				store := func(frame *ResultCall, v int, reported bool) AnyResult {
					res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame, CacheReported: reported}, func(context.Context) (AnyResult, error) {
						return cacheTestIntResult(frame, v), nil
					})
					require.NoError(b, err)
					return res
				}
				// The part's entries, made before the connection serves
				// lookups, so none of them is a logged miss.
				landed := make(map[uint64]struct{}, partEntries)
				for i := range partEntries {
					res := store(cacheTestIntCall(fmt.Sprint("landed-", i)), i, false)
					landed[uint64(res.cacheSharedResult().id)] = struct{}{}
				}
				bridge.StartRemoteLookups()
				var prev AnyResult
				for i := range n {
					frame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(Int(0).Type()), Field: fmt.Sprint("missed-", i)}
					if i%8 != 0 {
						frame.Receiver = &ResultCallRef{ResultID: uint64(prev.cacheSharedResult().id)}
					}
					if i%4 == 0 {
						frame.ExtraDigests = []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: digest.FromString(fmt.Sprint("content-", i))}}
					}
					prev = store(frame, i, true)
					// Keep the queue from refusing misses: only the log is
					// under test.
					bridge.lookups.mu.Lock()
					bridge.lookups.queue = bridge.lookups.queue[:0]
					bridge.lookups.mu.Unlock()
				}
				bridge.lookups.mu.Lock()
				logged := len(bridge.lookups.missed)
				bridge.lookups.mu.Unlock()
				require.Equal(b, n, logged)

				var holds atomic.Int64
				stopObserving := c.ObserveGraphReadHolds(func(wcprof.LockHolder, time.Duration, time.Duration) { holds.Add(1) })
				require.Empty(b, c.LateRemoteMisses(time.Now(), landed))
				stopObserving()

				done := make(chan struct{})
				var writers sync.WaitGroup
				if writer {
					writers.Go(func() {
						for {
							select {
							case <-done:
								return
							default:
							}
							c.egraphMu.Lock()
							time.Sleep(200 * time.Microsecond)
							c.egraphMu.Unlock()
							time.Sleep(300 * time.Microsecond)
						}
					})
				}
				b.ResetTimer()
				for b.Loop() {
					if late := c.LateRemoteMisses(time.Now(), landed); len(late) != 0 {
						b.Fatalf("%d late misses, want none", len(late))
					}
				}
				b.StopTimer()
				close(done)
				writers.Wait()
				b.ReportMetric(float64(holds.Load()), "graph-read-holds/op")
			})
		}
	}
}
