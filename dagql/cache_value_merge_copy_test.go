package dagql

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// A merge prepares its own copy of a bundle (cloneValueBundle): what it
// changes of the copy's outputs, every map, slice and pointer below them
// included, never reaches the caller's bundle, and an output that can't be
// encoded is refused.
func TestMergeCopyOfABundleLeavesItsOutputsUntouched(t *testing.T) {
	t.Parallel()
	created := time.Unix(1_700_000_000, 0).UTC()
	output := func(created *time.Time) TransferredOutput {
		return TransferredOutput{
			Ordinal: 1,
			Address: PersistedPartAddress{OutputPath: PersistedRefPath{{Field: "rootfs"}}, Part: "snapshot"},
			State:   "complete",
			Value: &SnapshotValue{
				Kind:     "directory",
				Platform: &ocispecs.Platform{OS: "linux", OSFeatures: []string{"feature"}},
				Services: []TransferredServiceBinding{{ServiceResultID: 2, Hostname: "db", Aliases: []string{"alias"}}},
			},
			Chain: &OfferedChain{
				Layers:     []snapshots.ExportLayer{{Description: "layer", CreatedAt: created}},
				Addresses:  map[digest.Digest]BlobAddress{"sha256:aa": {URL: "https://example.invalid/aa"}},
				RenewalKey: "renewal",
				Packs:      map[digest.Digest]digest.Digest{"sha256:aa": "sha256:pack"},
			},
			Prefix: &ChainPrefix{Ordinal: 1, Address: PersistedPartAddress{OutputPath: PersistedRefPath{{Field: "mount"}}, Part: "snapshot"}, Layers: 1},
			Owner:  &PersistedOfferOwner{DependencyIDs: []uint64{2}},
			Layers: []int{0},
		}
	}

	t.Run("a change to the copy leaves the input as it was", func(t *testing.T) {
		t.Parallel()
		in := ValueBundle{Version: ValueBundleVersion, Outputs: []TransferredOutput{output(&created)}}
		before, err := json.Marshal(in.Outputs)
		require.NoError(t, err)

		out, err := cloneValueBundle(in)
		require.NoError(t, err)
		require.Equal(t, in.Outputs, out.Outputs)

		got := &out.Outputs[0]
		got.Address.OutputPath[0].Field = "changed"
		got.Value.Kind = "changed"
		got.Value.Platform.OS = "changed"
		got.Value.Platform.OSFeatures[0] = "changed"
		got.Value.Services[0].ServiceResultID = 9
		got.Value.Services[0].Aliases[0] = "changed"
		got.Chain.RenewalKey = "changed"
		got.Chain.Addresses["sha256:aa"] = BlobAddress{URL: "https://example.invalid/changed"}
		got.Chain.Packs["sha256:aa"] = "sha256:changed"
		got.Chain.Layers = append(got.Chain.Layers, snapshots.ExportLayer{Description: "appended"})
		got.Prefix.Address.OutputPath[0].Field = "changed"
		got.Prefix.Layers = 9
		got.Owner.DependencyIDs[0] = 9
		got.Layers[0] = 9

		after, err := json.Marshal(in.Outputs)
		require.NoError(t, err)
		require.JSONEq(t, string(before), string(after))
		require.Len(t, in.Outputs[0].Chain.Layers, 1)
	})

	t.Run("an output whose layer time can't be encoded is refused", func(t *testing.T) {
		t.Parallel()
		unencodable := time.Date(10_000, 1, 1, 0, 0, 0, 0, time.UTC)
		_, err := cloneValueBundle(ValueBundle{Version: ValueBundleVersion, Outputs: []TransferredOutput{output(&unencodable)}})
		require.Error(t, err)
	})
}
