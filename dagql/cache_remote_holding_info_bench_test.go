package dagql

import (
	"fmt"
	"testing"

	"github.com/opencontainers/go-digest"
)

// benchmarkHeldEntryCloud returns a Cloud cache with one entry that the given
// number of engine caches hold, and those holdings' keys. Each holding names
// the entry by its own request digest, has two sessions, three parts and two
// dependencies, and reports one of eight terms over two inputs whose classes
// have 64 digests each, so the entry's classes and terms grow as a reconnect
// burst grows them.
func benchmarkHeldEntryCloud(b *testing.B, holders int) (*Cache, []HolderKey) {
	b.Helper()
	ctx := b.Context()
	cloud, err := NewCache(ctx, "", nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := cloud.CloseDiscardingPersistence(); err != nil {
			b.Error(err)
		}
	})
	attach := func(key HolderKey, desc RemoteHolding, sessions ...string) {
		if _, err := cloud.AttachRemoteHolding(ctx, key, desc); err != nil {
			b.Fatal(err)
		}
		for _, session := range sessions {
			if err := cloud.AddRemoteHold(ctx, key, session); err != nil {
				b.Fatal(err)
			}
		}
	}
	var inputs []digest.Digest
	for i := range 2 {
		input := testDigest(fmt.Sprintf("bench-input-%d", i))
		inputs = append(inputs, input)
		for j := range 64 {
			attach(HolderKey{Cache: "cache-inputs", Number: uint64(100*i + j + 1)}, RemoteHolding{
				Recipe:   input,
				Request:  testDigest(fmt.Sprintf("bench-input-%d-request-%d", i, j)),
				Field:    "input",
				TypeName: "Directory",
			}, "inputs")
		}
	}
	var keys []HolderKey
	for i := range holders {
		engine := CacheID(fmt.Sprintf("cache-%03d", i))
		for _, dep := range []uint64{1, 2} {
			attach(HolderKey{Cache: engine, Number: dep}, holdingOf(fmt.Sprintf("bench-dep-%d", dep)), "s1")
		}
		key := HolderKey{Cache: engine, Number: 10}
		attach(key, RemoteHolding{
			Recipe:        testDigest("bench-root"),
			Request:       testDigest(fmt.Sprintf("bench-root-request-%d", i)),
			Field:         "root",
			Term:          &RemoteTerm{Self: testDigest(fmt.Sprintf("bench-root-self-%d", i%8)), Inputs: inputs},
			ContentDigest: testDigest("bench-root-content"),
			TypeName:      "Container",
			Deps:          []uint64{1, 2},
			Parts:         []PersistedPartAddress{{Part: "fs"}, {Part: "metadata"}, {Part: "execMeta"}},
		}, "s1", "s2")
		keys = append(keys, key)
	}
	return cloud, keys
}

// BenchmarkRemoteHoldingInfo reads one holding of an entry with many holders:
// through the whole entry's description, as RemoteEntryInfo gives it, and
// through RemoteHoldingInfo. Each read asks about the next holding in turn.
func BenchmarkRemoteHoldingInfo(b *testing.B) {
	for _, holders := range []int{1, 22, 256} {
		cloud, keys := benchmarkHeldEntryCloud(b, holders)
		b.Run(fmt.Sprintf("holders=%d/entry", holders), func(b *testing.B) {
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				info, ok := cloud.RemoteEntryInfo(keys[i%len(keys)])
				if !ok || len(info.Holdings) != holders {
					b.Fatalf("entry of %v: %t, %d holdings", keys[i%len(keys)], ok, len(info.Holdings))
				}
				i++
			}
		})
		b.Run(fmt.Sprintf("holders=%d/holding", holders), func(b *testing.B) {
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				key := keys[i%len(keys)]
				held, ok := cloud.RemoteHoldingInfo(key)
				if !ok || held.Holding.Key != key {
					b.Fatalf("holding %v: %t, %v", key, ok, held.Holding.Key)
				}
				i++
			}
		})
	}
}
