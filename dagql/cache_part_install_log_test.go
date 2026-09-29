package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/snapshots"
)

// A part installed from an offer is logged once, at info level, with the
// entry, the part, the bytes downloaded, the time taken and the source. The
// bytes are those read from the offer's addresses: every layer when the
// cache has none of the chain, none when it already has it all.
func TestOfferInstallLogged(t *testing.T) {
	for _, tc := range []struct {
		name string
		// held imports the chain into the cache's store first.
		held bool
	}{
		{name: "downloaded"},
		{name: "already held", held: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := newChainFixture(t, "install-log")
			f := newExhaustionFixture(t, chain)
			f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
			var size int64
			for _, layer := range chain.chain.Layers {
				size += layer.Descriptor.Size
			}
			require.Positive(t, size)
			want := size
			if tc.held {
				held, err := f.cache.snapshotManager.ImportChain(f.ctx, &snapshots.ExportChain{Layers: chain.chain.Layers, Provider: chain.chain.Provider})
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, held.Release(context.Background())) })
				want = 0
			}
			var logs bytes.Buffer
			f.ctx = slog.WithLogger(f.ctx, slog.New(slog.NewJSONHandler(&logs, nil)))

			require.NoError(t, f.run())
			require.True(t, f.installed(), "the part installed from its offer")

			var installs []map[string]any
			dec := json.NewDecoder(&logs)
			dec.UseNumber()
			for dec.More() {
				var line map[string]any
				require.NoError(t, dec.Decode(&line))
				if line["msg"] == "remote cache part installed" {
					installs = append(installs, line)
				}
			}
			require.Len(t, installs, 1)
			line := installs[0]
			require.Equal(t, "INFO", line["level"])
			require.Equal(t, json.Number(strconv.FormatUint(uint64(f.receiver.cacheSharedResult().id), 10)), line["entry"])
			require.Equal(t, "", line["output"])
			require.Equal(t, "snapshot", line["part"])
			require.Equal(t, json.Number(strconv.FormatInt(want, 10)), line["bytes"])
			require.Equal(t, "offer", line["source"])
			duration, err := line["duration"].(json.Number).Int64()
			require.NoError(t, err)
			require.Positive(t, duration)
		})
	}
}
