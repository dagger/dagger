package dagql

import (
	"bytes"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// heldPartsByDecodingLocked is the check of a held row's parts as the
// Cloud's commit made it before it read its copy's stored facts: the
// copy's record decoded, and its payload mapped to its parts. It is the
// reference the facts are compared with, output by output. Requires
// egraphMu.
func heldPartsByDecodingLocked(c *Cache, res *sharedResult, outputs []TransferredOutput) bool {
	if res.noValueLocked() {
		return false
	}
	rec, err := c.storedRecordLocked(res)
	if err != nil {
		return false
	}
	mapped, err := mapTransferredOutputs(rec)
	if err != nil {
		return false
	}
	parts := make(map[string]CapturedCodecOutput, len(mapped))
	for _, part := range mapped {
		if key, err := partAddressKey(part.Address); err == nil {
			parts[key] = part
		}
	}
	for _, output := range outputs {
		key, err := partAddressKey(output.Address)
		if err != nil {
			return false
		}
		part, ok := parts[key]
		if !ok || checkOutputPart(output, part) != nil {
			return false
		}
	}
	return true
}

// The Cloud checks the parts an engine puts on a held row against its own
// copy of the row from the facts that copy's stored body keeps: the parts
// it declares absent or pending, and those it proves complete. Whether its
// copy is compressed or not yet, the check gives the verdict decoding the
// record does, for an output at a part in each state, and at none.
func TestCloudChecksAHeldRowsPartsFromItsCopysFacts(t *testing.T) {
	t.Parallel()
	parts := []transferTestPart{
		{Part: "snapshot", State: "pending", Kind: "directory"},
		{Part: "fs", State: "completed", Kind: "directory"},
		{Part: "mount:/a", State: "absent", Kind: "directory"},
		{Part: "metadata", State: "metadata"},
		{Part: "meta", State: "pending", Kind: "snapshot"},
	}
	at := func(part, state string) TransferredOutput {
		return TransferredOutput{Address: PersistedPartAddress{Part: PartKey(part)}, State: state}
	}
	upload := func(part, kind string) TransferredOutput {
		output := at(part, "completed")
		output.Value = &SnapshotValue{Kind: kind, Path: "/"}
		output.Chain = &OfferedChain{Layers: storedBenchPart(1).Chain.Layers}
		output.Owner = &PersistedOfferOwner{}
		return output
	}
	cases := []struct {
		name    string
		outputs []TransferredOutput
		has     bool
	}{
		{"a pending part's upload", []TransferredOutput{upload("snapshot", "directory")}, true},
		{"a pending part's upload of another kind", []TransferredOutput{upload("snapshot", "file")}, false},
		{"another pending part's upload", []TransferredOutput{upload("meta", "snapshot")}, true},
		{"a pending part, pending", []TransferredOutput{at("snapshot", "pending")}, true},
		{"a pending part, absent", []TransferredOutput{at("snapshot", "absent")}, false},
		{"a complete part's upload", []TransferredOutput{upload("fs", "directory")}, false},
		{"a complete part, completed", []TransferredOutput{at("fs", "completed")}, true},
		{"a complete part, pending", []TransferredOutput{at("fs", "pending")}, false},
		{"a complete part, as metadata", []TransferredOutput{at("fs", "metadata")}, false},
		{"an absent part, absent", []TransferredOutput{at("mount:/a", "absent")}, true},
		{"an absent part, pending", []TransferredOutput{at("mount:/a", "pending")}, false},
		{"an absent part's upload", []TransferredOutput{upload("mount:/a", "directory")}, false},
		{"the consumed metadata part", []TransferredOutput{at("metadata", "metadata")}, true},
		{"the consumed metadata part, completed", []TransferredOutput{at("metadata", "completed")}, false},
		{"the consumed metadata part, pending", []TransferredOutput{at("metadata", "pending")}, false},
		{"a part the copy lacks", []TransferredOutput{at("other", "pending")}, false},
		{"several, each there", []TransferredOutput{upload("snapshot", "directory"), at("mount:/a", "absent"), at("metadata", "metadata")}, true},
		{"several, one not", []TransferredOutput{upload("snapshot", "directory"), at("fs", "pending")}, false},
	}
	for _, compressed := range []bool{false, true} {
		name := "whole"
		if compressed {
			name = "compressed"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, engine, srv := transferTestCache(t)
			out := persistedListTestResult(t, ctx, engine, srv, "held-parts-"+name, &transferTestValue{Text: "parts", Parts: parts})
			cctx, cloud := storedPartTestCache(t, WithBlobStore())
			if !compressed {
				storedTestHoldWhole(cloud)
			}
			reply, err := cloud.MergeValues(cctx, "engine", exportTestBundle(t, ctx, engine, out))
			require.NoError(t, err)
			require.True(t, reply.Committed)
			if compressed {
				storedTestCompress(cloud)
			}
			entry, _ := cloudEntryOfRecipe(t, cloud, engine, out)
			require.NotNil(t, entry)
			cloud.egraphMu.RLock()
			defer cloud.egraphMu.RUnlock()
			require.Equal(t, compressed, entry.stored != nil, "the copy is held as its test needs")
			for _, tc := range cases {
				want := heldPartsByDecodingLocked(cloud, entry, tc.outputs)
				require.Equal(t, tc.has, want, "%s: the reference", tc.name)
				require.Equal(t, want, cloud.entryHasPartsLocked(entry, tc.outputs), "%s", tc.name)
			}
		})
	}
}

// A held row's parts are checked against the Cloud's compressed copy of the
// row without decoding it: the commit maps no payload of the copy's, where
// it once decompressed, decoded and mapped the copy's whole record for each
// held row with an output, under its hold of the graph lock.
func TestCloudChecksAHeldRowsPartsWithoutDecodingItsCopy(t *testing.T) {
	// Not parallel: the codec's steps are seen through a package hook.
	engine, outOnly, held, out, root := heldPartTestBundles(t, "part-undecoded")
	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(cctx, "engine", outOnly)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	storedTestCompress(cloud)
	entry, _ := cloudEntryOfRecipe(t, cloud, engine, out)
	require.NotNil(t, entry)
	cloud.egraphMu.RLock()
	require.NotNil(t, entry.stored, "the copy is compressed")
	cloud.egraphMu.RUnlock()

	var mapped atomic.Int32
	steps := func(step string, v PersistedPayloadVisit) {
		// The held row's own payload, {"text":"pending"}.
		if step == "map" && bytes.Contains(v.Payload, []byte(`"pending"`)) {
			mapped.Add(1)
		}
	}
	transferTestCodecSteps.Store(&steps)
	defer transferTestCodecSteps.Store(nil)
	reply, err = cloud.MergeValues(cctx, "engine", held)
	require.NoError(t, err)
	require.True(t, reply.Committed, "the held row's part is there")
	rootEntry, _ := cloudEntryOfRecipe(t, cloud, engine, root)
	require.NotNil(t, rootEntry)
	require.Zero(t, mapped.Load(), "the held row's copy is never mapped")
}
