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
//
// With one field, every logged miss is a call of one field on a receiver of
// its own, so every one has the same self digest, and the part lands an
// entry of that field too: every miss is one the part's terms could find,
// the most a count can be asked to check by term.
func BenchmarkLateRemoteMisses(b *testing.B) {
	const partEntries = 4000
	for _, n := range []int{1000, 4000, remoteLookupLogCapacity} {
		for _, shape := range []string{"writer false", "writer true", "one field"} {
			writer, oneField := shape == "writer true", shape == "one field"
			b.Run(fmt.Sprintf("%d misses logged, %s", n, shape), func(b *testing.B) {
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
				var receivers []AnyResult
				for i := range partEntries {
					res := store(cacheTestIntCall(fmt.Sprint("landed-", i)), i, false)
					landed[uint64(res.cacheSharedResult().id)] = struct{}{}
					receivers = append(receivers, res)
				}
				if oneField {
					res := store(&ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(Int(0).Type()), Field: "missed", Receiver: &ResultCallRef{ResultID: uint64(receivers[0].cacheSharedResult().id)}}, 0, false)
					landed[uint64(res.cacheSharedResult().id)] = struct{}{}
					for i := partEntries; i < n+1; i++ {
						receivers = append(receivers, store(cacheTestIntCall(fmt.Sprint("receiver-", i)), i, false))
					}
				}
				bridge.StartRemoteLookups()
				var prev AnyResult
				for i := range n {
					frame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(Int(0).Type()), Field: fmt.Sprint("missed-", i)}
					switch {
					case oneField:
						frame.Field = "missed"
						frame.Receiver = &ResultCallRef{ResultID: uint64(receivers[i+1].cacheSharedResult().id)}
					case i%8 != 0:
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

// BenchmarkRemoteMissLogTrim times the miss that finds the miss log full
// (noteRemoteMiss): it trims the log, dropping the misses a part returned
// and then the oldest quarter. Half the logged misses were returned, and
// each names a recipe and a term, and every fourth an extra digest.
func BenchmarkRemoteMissLogTrim(b *testing.B) {
	ctx := cacheTestContext(b.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(b, err)
	bridge, _, err := c.AttachRemoteCacheBridge()
	require.NoError(b, err)
	bridge.StartRemoteLookups()
	l := &bridge.lookups
	next := 0
	miss := func() RemoteMiss {
		next++
		m := RemoteMiss{Recipe: digest.FromString(fmt.Sprint("recipe-", next)), Self: digest.FromString(fmt.Sprint("self-", next%64)), Inputs: []digest.Digest{digest.FromString(fmt.Sprint("input-", next))}, At: time.Now()}
		if next%4 == 0 {
			m.Extra = []digest.Digest{digest.FromString(fmt.Sprint("content-", next))}
		}
		return m
	}
	fill := func() {
		for {
			l.mu.Lock()
			full := len(l.missed) >= remoteLookupLogCapacity
			l.queue = l.queue[:0]
			l.mu.Unlock()
			if full {
				break
			}
			bridge.noteRemoteMiss(miss())
		}
		l.mu.Lock()
		for i := range l.missed {
			if l.missed[i].seq%2 == 0 {
				l.missed[i].found = true
			}
		}
		l.mu.Unlock()
	}
	for b.Loop() {
		b.StopTimer()
		fill()
		m := miss()
		b.StartTimer()
		bridge.noteRemoteMiss(m)
	}
}
