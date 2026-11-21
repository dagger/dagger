package core

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

// heldPartsExport is what a content export carried, named by the engine's
// entry numbers instead of the bundle's ordinals, which differ when its held
// rows are left out: each output's row, part, state, value, chain, prefix
// and owner, in order, and the rows it carried held.
type heldPartsExport struct {
	Outputs []heldPartsOutput
	Held    int
	Copied  int
	// unpredicted is the export's count of copied records naming a snapshot
	// their row's owner links don't.
	unpredicted int
}

type heldPartsOutput struct {
	Row     uint64
	Address string
	State   string
	Value   string
	Chain   []string
	Prefix  string
	Owner   []uint64
}

func heldPartsRun(t *testing.T, ctx context.Context, cache *dagql.Cache, selection dagql.ValueSelection) heldPartsExport {
	t.Helper()
	var got heldPartsExport
	require.NoError(t, cache.WithExportedValues(ctx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, ex *dagql.ExportedValues) error {
		number := func(o uint64) uint64 { return ex.Bundle.Values[o-1].SenderNumber }
		for _, out := range ex.Bundle.Outputs {
			value, err := json.Marshal(out.Value)
			require.NoError(t, err)
			address, err := json.Marshal(out.Address)
			require.NoError(t, err)
			o := heldPartsOutput{Row: number(uint64(out.Ordinal)), Address: string(address), State: out.State, Value: string(value)}
			if out.Chain != nil {
				for _, layer := range out.Chain.Layers {
					o.Chain = append(o.Chain, layer.Descriptor.Digest.String())
				}
			}
			if out.Prefix != nil {
				prefixAddress, err := json.Marshal(out.Prefix.Address)
				require.NoError(t, err)
				o.Prefix = fmt.Sprintf("%d %s %d", number(uint64(out.Prefix.Ordinal)), prefixAddress, out.Prefix.Layers)
			}
			if out.Owner != nil {
				for _, dep := range out.Owner.DependencyIDs {
					o.Owner = append(o.Owner, number(dep))
				}
			}
			got.Outputs = append(got.Outputs, o)
		}
		got.Held = len(ex.Rows.Held)
		got.Copied, got.unpredicted = ex.CopiedRows, ex.UnpredictedLinks
		return nil
	}))
	return got
}

// A content export carries the same parts, prefixes and owners whether the
// rows below its selected part go held or in full: it reads a held row's
// record for its parts, which core's values name by the snapshots their
// records link, and their owner links hold. Here a chain of ten containers,
// each step's fs built on the one before, under a withWorkdir that
// delegates its fs to the tenth: the selected part is the withWorkdir's fs,
// and the nine steps below are its chain's prefixes.
func TestValueExportHeldRowsKeepTheirParts(t *testing.T) {
	store := testutil.NewStore(t)
	ctx, cache, srv := transferCache(t, store, "", "a")
	platform := Platform{OS: "linux", Architecture: "amd64"}
	var snapshot bkcache.ImmutableRef
	var step dagql.ObjectResult[*Container]
	var last *Container
	for i := range 10 {
		snapshot, _ = store.Build(t, snapshot, fmt.Sprintf("held-%d.txt", i), fmt.Sprintf("held %d", i))
		last = NewContainer(platform)
		last.FS.setValue(partTestDirectory(snapshot, "/"))
		if i == 0 {
			step = attachTransferObject(t, ctx, cache, srv, "a", "heldBase", last)
		} else {
			step = attachDelegationChild(t, ctx, cache, srv, "a", "heldStep", step, last)
		}
	}
	child := NewContainer(platform)
	var err error
	child.FS, err = CloneContainerDirectoryAccessor(ctx, last.FS)
	require.NoError(t, err)
	child.Config.WorkingDir = "/held"
	workdir := attachDelegationChild(t, ctx, cache, srv, "a", "withWorkdir", step, child)
	outputs := []dagql.SelectedValueOutput{{Result: workdir, Address: dagql.PersistedPartAddress{Part: "fs"}}}

	full := heldPartsRun(t, ctx, cache, dagql.ValueSelection{Roots: []dagql.AnyResult{workdir}, Outputs: outputs})
	require.Zero(t, full.Held)
	require.NotEmpty(t, full.Outputs)

	known := dagql.NewKnownRows()
	require.NoError(t, cache.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{step}, Known: known}, config.RefConfig{}, func(_ context.Context, ex *dagql.ExportedValues) error {
		known.Merged(ex.Rows)
		return nil
	}))
	held := heldPartsRun(t, ctx, cache, dagql.ValueSelection{Roots: []dagql.AnyResult{workdir}, Outputs: outputs, Known: known})
	require.Positive(t, held.Held, "the steps below go held")
	require.Equal(t, full.Outputs, held.Outputs, "the same parts, prefixes and owners, on the same rows")
	require.Zero(t, held.unpredicted, "every record copied names only snapshots its owner links hold")
	require.Zero(t, full.unpredicted)
}
