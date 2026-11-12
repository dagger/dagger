package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// reexportTestRows and reexportTestLayers are test-base's: the largest export
// the service refused, 2,206 records, and its chains' length, about 23 base
// layers under a row's own.
const (
	reexportTestRows   = 2206
	reexportTestLayers = 24
)

// reexportTestChain is row i's part as the Cloud stores it: a chain of the
// base layers every row shares, then the row's own.
func reexportTestChain(i int) dagql.OfferedChain {
	layer := func(name string) bkcache.ExportLayer {
		created := time.Date(2026, 11, 11, 4, 0, 0, 0, time.UTC)
		return bkcache.ExportLayer{
			Descriptor: ocispecs.Descriptor{
				MediaType:   ocispecs.MediaTypeImageLayerGzip,
				Digest:      digest.FromString(name),
				Size:        int64(1000 + len(name)),
				Annotations: map[string]string{"containerd.io/uncompressed": digest.FromString("raw " + name).String()},
			},
			Description: "exec /usr/local/go/bin/go build -o /out " + name,
			CreatedAt:   &created,
		}
	}
	var chain dagql.OfferedChain
	for j := range reexportTestLayers - 1 {
		chain.Layers = append(chain.Layers, layer(fmt.Sprint("base-", j)))
	}
	chain.Layers = append(chain.Layers, layer(fmt.Sprint("own-", i)))
	return chain
}

// reexportTestAddresses gives a Cloud bundle's outputs what the service gives
// them before it sends a merge (withAddresses): a signed download address for
// each layer, and a renewal key naming the storing entry. The addresses are
// padded to the length of a store's, so the message is the size an engine
// sends.
func reexportTestAddresses(result dagql.StoredBundleResult) dagql.ValueBundle {
	bundle := result.Bundle
	for i := range bundle.Outputs {
		out := &bundle.Outputs[i]
		if out.Chain == nil {
			continue
		}
		chain := *out.Chain
		chain.Addresses = map[digest.Digest]dagql.BlobAddress{}
		for _, l := range chain.Layers {
			d := l.Descriptor.Digest.Encoded()
			chain.Addresses[l.Descriptor.Digest] = dagql.BlobAddress{
				URL:           "https://storage.example.com/cache/" + strings.Repeat("x", 74) + "/blobs/sha256/" + d + "?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIAEXAMPLEEXAMPLE%2F20261111%2Feu-west-1%2Fs3%2Faws4_request&X-Amz-Date=20261111T040000Z&X-Amz-Expires=3600&X-Amz-SignedHeaders=host&X-Amz-Signature=" + d,
				ExpiresAtUnix: 1794372000,
			}
		}
		chain.RenewalKey = fmt.Sprintf("%d/%s", result.Donors[i], out.Address.Part)
		out.Chain = &chain
	}
	return bundle
}

// exportedBytes is the size of the Exported message an export's bundle goes
// in: what the engine checks against protocol.MaxMessageBytes.
func exportedBytes(t *testing.T, bundle *dagql.ValueBundle) int {
	t.Helper()
	env, err := protocol.NewEnvelope(1, 1, protocol.TypeExported, protocol.Exported{ExportID: "export", Bundle: bundle})
	require.NoError(t, err)
	data, err := json.Marshal(env)
	require.NoError(t, err)
	return len(data)
}

// cloudRecords are the records a Cloud's bundle of root carries: what the
// Cloud holds of root's closure, whatever its numbers for it.
func cloudRecords(t *testing.T, cloud *dagql.Cache, root uint64) []string {
	t.Helper()
	got, err := cloud.StoredBundle(t.Context(), []uint64{root}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Empty(t, got.Gone)
	require.Empty(t, got.NoValue)
	var records []string
	for _, value := range got.Bundle.Values {
		value.SenderNumber, value.SenderReplacements = 0, 0
		data, err := json.Marshal(value)
		require.NoError(t, err)
		records = append(records, string(data))
	}
	return records
}

func newReexportTestCloud(t *testing.T) *dagql.Cache {
	t.Helper()
	cloud, err := dagql.NewCache(t.Context(), "", nil, nil, dagql.WithBlobStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	return cloud
}

// An engine that imported rows from the Cloud, and then sends them in full,
// as it does after the Cloud refused them as stale held rows or on a new
// connection, sends records the size of those the Cloud first had from the
// engine that made them: not the Cloud's offers of their parts, which the
// Cloud drops when it merges a bundle, since the upload decides what it
// stores. And the Cloud that merges them holds what it held before.
//
// The seed engine makes test-base's largest refused export's rows, and the
// Cloud stores each row's part as a chain of test-base's length. A second
// engine imports the rows from the Cloud's bundle, with an address for every
// layer, and exports them all in full to a Cloud that has lost them.
func TestRemoteCacheAdapterReexportLeavesOutTheCloudsOffers(t *testing.T) {
	t.Parallel()
	seed := newAdapterTestEngine(t)
	var root dagql.AnyResult
	for i := range reexportTestRows {
		row, _ := seed.publishFile(t, fmt.Sprint("row-", i), root)
		root = row
	}
	export := func(adapter *RemoteCacheAdapter, number uint64) *dagql.ValueBundle {
		t.Helper()
		var got remotecache.Export
		require.NoError(t, adapter.Export(t.Context(), protocol.Export{Roots: []uint64{number}}, nil, func(_ context.Context, exp remotecache.Export) error {
			got = exp
			return nil
		}))
		require.Empty(t, got.Skipped)
		require.NotNil(t, got.Bundle)
		require.Len(t, got.Bundle.Values, reexportTestRows)
		return got.Bundle
	}
	first := export(seed.adapter, entryNumber(t, root))
	firstBytes := exportedBytes(t, first)

	// The Cloud merges the seed's export and stores every row's part.
	cloud := newReexportTestCloud(t)
	merged, err := cloud.MergeValues(t.Context(), "seed", *first)
	require.NoError(t, err)
	require.True(t, merged.Committed)
	require.Len(t, merged.Values, reexportTestRows)
	var cloudRoot uint64
	for i, value := range merged.Values {
		if value.Ordinal == first.Roots[0].Ordinal {
			cloudRoot = value.Number
		}
		stored, err := cloud.SetStoredPart(t.Context(), value.Number, dagql.PersistedPartOffer{Address: snapshotPart, Value: dagql.SnapshotValue{Kind: "file", Path: "/pending"}, Chain: reexportTestChain(i)}, 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	require.NotZero(t, cloudRoot)
	held := cloudRecords(t, cloud, cloudRoot)

	// A fresh engine imports the rows, each with its part's offer.
	built, err := cloud.StoredBundle(t.Context(), []uint64{cloudRoot}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Outputs, reexportTestRows)
	fresh := newAdapterTestEngine(t)
	imported, err := fresh.adapter.Merge(t.Context(), 1, protocol.Merge{Bundle: reexportTestAddresses(built)}, nil)
	require.NoError(t, err)
	var freshRoot uint64
	for _, value := range imported.Values {
		if value.Ordinal == built.Bundle.Roots[0].Ordinal {
			freshRoot = value.Number
		}
		require.Len(t, value.Offered, 1, "the imported row holds the Cloud's offer of its part")
	}
	require.NotZero(t, freshRoot)

	// It sends them all in full.
	again := export(fresh.adapter, freshRoot)
	withOffers := 0
	for _, value := range again.Values {
		require.Empty(t, value.Held)
		if len(value.Record.Envelope.PendingOffers) > 0 {
			withOffers++
		}
	}
	againBytes := exportedBytes(t, again)
	assert.Zero(t, withOffers, "rows carrying the Cloud's offers back to it")
	assert.Less(t, againBytes, protocol.MaxMessageBytes, "the import's export is %s", mib(againBytes))
	require.LessOrEqual(t, againBytes, firstBytes*5/4, "the import's export is %s against the seed's %s", mib(againBytes), mib(firstBytes))

	// A Cloud that lost the rows holds the same records once it merges them.
	lost := newReexportTestCloud(t)
	remerged, err := lost.MergeValues(t.Context(), "fresh", *again)
	require.NoError(t, err)
	require.True(t, remerged.Committed)
	var lostRoot uint64
	for _, value := range remerged.Values {
		if value.Ordinal == again.Roots[0].Ordinal {
			lostRoot = value.Number
		}
	}
	require.Equal(t, held, cloudRecords(t, lost, lostRoot))
}

func mib(n int) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", float64(n)/(1<<20)), "0"), ".") + " MiB"
}
