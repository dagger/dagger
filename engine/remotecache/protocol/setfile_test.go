package protocol

import (
	"bytes"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots"
)

// setFileTestBundle is a bundle of records values with one output whose
// chain has two layers, which a part lists once.
func setFileTestBundle(records int) dagql.ValueBundle {
	bundle := dagql.ValueBundle{Version: 8}
	for i := range records {
		bundle.Values = append(bundle.Values, dagql.TransferredValue{Ordinal: dagql.TransferOrdinal(i + 1), SenderNumber: uint64(100 + i)})
	}
	layer := func(s string) snapshots.ExportLayer {
		var l snapshots.ExportLayer
		l.Descriptor.Digest = digest.FromString(s)
		l.Descriptor.Size = int64(len(s))
		return l
	}
	bundle.Outputs = []dagql.TransferredOutput{{
		Ordinal: 1,
		Address: dagql.PersistedPartAddress{Part: "snapshot"},
		Chain:   &dagql.OfferedChain{Layers: []snapshots.ExportLayer{layer("base"), layer("top")}, RenewalKey: "101/part"},
	}}
	return bundle
}

// A set file written of two parts reads back as its header and the same
// parts, each of which decodes to its bundle, with its layers back on its
// chain and its renewal key kept.
func TestSetFileRoundTrip(t *testing.T) {
	t.Parallel()
	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	dec, err := zstd.NewReader(nil)
	require.NoError(t, err)
	defer dec.Close()

	bundles := []dagql.ValueBundle{setFileTestBundle(2), setFileTestBundle(3)}
	header := SetFileHeader{Version: 8, Check: "test-base", Line: "pr-1", Kind: "head", Built: time.Unix(1700000000, 0).UTC()}
	var parts [][]byte
	for _, bundle := range bundles {
		part, entry, err := EncodeSetFilePart(enc, bundle)
		require.NoError(t, err)
		require.Equal(t, len(bundle.Values), entry.Records)
		entry.Unretained = []dagql.TransferOrdinal{bundle.Values[0].Ordinal}
		parts = append(parts, part)
		header.Parts = append(header.Parts, entry)
	}
	var file bytes.Buffer
	require.NoError(t, WriteSetFile(&file, header, parts))

	gotHeader, gotParts, err := ReadSetFile(file.Bytes())
	require.NoError(t, err)
	require.Equal(t, header, gotHeader)
	require.Equal(t, parts, gotParts)
	for i, part := range gotParts {
		bundle, err := DecodeSetFilePart(dec, part)
		require.NoError(t, err)
		require.Len(t, bundle.Values, len(bundles[i].Values))
		require.Len(t, bundle.Layers, 2, "the chain's layers are listed once")
		require.NoError(t, dagql.ResolveChainPrefixes(&bundle))
		require.Equal(t, bundles[i].Outputs[0].Chain.Layers, bundle.Outputs[0].Chain.Layers)
		require.Equal(t, "101/part", bundle.Outputs[0].Chain.RenewalKey)
	}
}

// A file whose parts don't fill it exactly, or that has no header line, is
// refused before any part is decoded.
func TestSetFileRefusesAFileOfAnotherLength(t *testing.T) {
	t.Parallel()
	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	part, entry, err := EncodeSetFilePart(enc, setFileTestBundle(1))
	require.NoError(t, err)
	var file bytes.Buffer
	require.NoError(t, WriteSetFile(&file, SetFileHeader{Version: 8, Parts: []SetFilePart{entry}}, [][]byte{part}))

	_, _, err = ReadSetFile(file.Bytes()[:file.Len()-1])
	require.ErrorContains(t, err, "bytes, with")
	_, _, err = ReadSetFile(append(bytes.Clone(file.Bytes()), 0))
	require.ErrorContains(t, err, "after its last part")
	_, _, err = ReadSetFile([]byte(`{"version":8}`))
	require.ErrorContains(t, err, "no header line")
}
