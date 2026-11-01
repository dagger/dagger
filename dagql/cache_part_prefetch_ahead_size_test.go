package dagql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// defaultPrefetchFixtureConfig is the engine's own prefetch configuration,
// with the fixture's lanes: every blob in the large lane, no workers, and the
// fixture's budget and hold time.
func defaultPrefetchFixtureConfig(cfg *partPrefetchConfig) {
	def := defaultPartPrefetchConfig()
	def.largeBlob, def.workers, def.smallWorkers = cfg.largeBlob, 0, 0
	def.budget, def.ttl = cfg.budget, cfg.ttl
	*cfg = def
}

// With the engine's configuration, an offer whose chain is far smaller than
// the parts worth unpacking ahead is only downloaded and held: nothing is
// unpacked ahead of demand, and the part's install unpacks its layers from
// the held blobs, downloading none.
func TestPartPrefetchSmallChainOnlyHeld(t *testing.T) {
	f := newPrefetchFixture(t, true, defaultPrefetchFixtureConfig)
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	holds, _ := f.holds()
	require.Equal(t, 2, holds, "both blobs are held for the install")
	require.Never(t, func() bool {
		return len(f.logs.lines(t, "remote cache part applied ahead")) > 0 || f.store.Applies.Load() > 0
	}, time.Second, 10*time.Millisecond, "a small chain is unpacked ahead of demand")

	require.NoError(t, f.run())
	require.True(t, f.installed())
	installs := f.logs.lines(t, "remote cache part installed")
	require.Len(t, installs, 1)
	require.Zero(t, installs[0].int(t, "bytes"), "the install downloads nothing")
	require.EqualValues(t, 2, installs[0].int(t, "prefetched"))
	require.EqualValues(t, 2, f.store.Applies.Load(), "the install unpacks both layers")
	require.Empty(t, f.logs.lines(t, "remote cache part ahead used"))
}

// An offer whose chain is exactly the size from which chains are unpacked
// ahead is unpacked ahead once its blobs are held, and its install then
// unpacks nothing.
func TestPartPrefetchChainAtAheadSizeImportedAhead(t *testing.T) {
	f := newPrefetchFixture(t, true, defaultPrefetchFixtureConfig)
	var size int64
	for _, layer := range f.offer.record.Chain.Layers {
		size += layer.Descriptor.Size
	}
	f.prefetch.mu.Lock()
	f.prefetch.cfg.aheadOffer = size
	f.prefetch.mu.Unlock()
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	ahead := f.aheadLine(t)
	require.Equal(t, "done", ahead.fields["outcome"])
	require.EqualValues(t, 2, ahead.int(t, "unpacked_layers"))

	require.NoError(t, f.run())
	require.True(t, f.installed())
	require.EqualValues(t, 2, f.store.Applies.Load(), "the install unpacks nothing")
}
