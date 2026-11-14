package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/pack"
	"github.com/dagger/dagger/engine/snapshots"
)

func testLayer() snapshots.ExportLayer {
	created := time.Unix(1790000000, 0).UTC()
	return snapshots.ExportLayer{
		Descriptor: ocispecs.Descriptor{
			MediaType: ocispecs.MediaTypeImageLayerGzip,
			Digest:    digest.FromString("layer"),
			Size:      42,
		},
		Description: "layer",
		CreatedAt:   &created,
	}
}

func testAddress() dagql.PersistedPartAddress {
	return dagql.PersistedPartAddress{OutputPath: dagql.PersistedRefPath{}.Field("rootfs"), Part: "fs"}
}

func testOffer() dagql.PersistedPartOffer {
	layer := testLayer()
	return dagql.PersistedPartOffer{
		Address: testAddress(),
		Value: dagql.SnapshotValue{
			Kind:     "directory",
			Services: []dagql.TransferredServiceBinding{{ServiceResultID: 7, Hostname: "db"}},
		},
		Chain: dagql.OfferedChain{
			Layers:     []snapshots.ExportLayer{layer},
			Addresses:  map[digest.Digest]dagql.BlobAddress{layer.Descriptor.Digest: {URL: "https://blobs/get", ExpiresAtUnix: 1790003600}},
			RenewalKey: "12/fs",
		},
		Owner: dagql.PersistedOfferOwner{DependencyIDs: []uint64{7}},
	}
}

func testBundle() dagql.ValueBundle {
	return dagql.ValueBundle{
		Version: 3,
		Roots:   []dagql.TransferredRoot{{Ordinal: 1, ExpiresAtUnix: 1790086400}},
		Values: []dagql.TransferredValue{{
			Ordinal:            1,
			SenderNumber:       12,
			SenderReplacements: 1,
			DependencyIDs:      []uint64{3},
		}},
	}
}

// Every message survives the envelope and JSON unchanged, so the engine and
// the service read what the other side wrote.
func TestMessagesRoundTrip(t *testing.T) {
	bundle := testBundle()
	layer := testLayer()
	for _, tc := range []struct {
		typ  Type
		body any
		into func() any
	}{
		{TypeHello, Hello{CacheID: "cache-1", Generation: 4, EngineVersion: "v0.20.0", EngineName: "dagger-engine"}, func() any { return &Hello{} }},
		{TypeHello, Hello{CacheID: "cache-1", Generation: 5, EngineVersion: "v0.20.0", EngineName: "dagger-engine", Period: 3, Settled: true}, func() any { return &Hello{} }},
		{TypeOpen, Open{Period: 4}, func() any { return &Open{} }},
		{TypeOpened, Opened{}, func() any { return &Opened{} }},
		{TypeSettle, Settle{Period: 4, Sessions: []SettleSession{{ID: "sess-1", Marker: true}, {ID: "sess-2"}}}, func() any { return &Settle{} }},
		{TypeSettled, Settled{}, func() any { return &Settled{} }},
		{TypeClaims, Claims{Sessions: []SessionClaim{{ID: "sess-1", Claim: "claim-1"}}}, func() any { return &Claims{} }},
		{TypeClaimed, Claimed{}, func() any { return &Claimed{} }},
		{TypeHello, Hello{CacheID: "cache-1", Generation: 6, EngineVersion: "v0.20.0", EngineName: "dagger-engine", Restored: []digest.Digest{digest.FromString("head"), digest.FromString("whole")}}, func() any { return &Hello{} }},
		{TypeRestore, Restore{Sets: []RestoreSet{{Check: "test-base", Head: &SetFile{Digest: digest.FromString("head"), Size: 712, Address: dagql.BlobAddress{URL: "https://blobs/head"}, Table: AddressTable{Blobs: map[digest.Digest]dagql.BlobAddress{layer.Descriptor.Digest: {URL: "https://blobs/get", ExpiresAtUnix: 1790007200}}, Packs: map[digest.Digest]dagql.BlobAddress{digest.FromString("pack"): {URL: "https://blobs/pack"}}}}}}}, func() any { return &Restore{} }},
		{TypeRestore, Restore{Sets: []RestoreSet{{Check: "test-base", Whole: &SetFile{Digest: digest.FromString("whole"), Size: 23_300_000, Address: dagql.BlobAddress{URL: "https://blobs/whole"}, Table: AddressTable{}}}}}, func() any { return &Restore{} }},
		{TypeRestored, Restored{Generation: 2, EngineTime: 1790000000123456789, Files: []RestoredFile{
			{Digest: digest.FromString("head"), Complete: true, Parts: 2, Records: 12212, Rows: RowCounts{Kept: 4745, Created: 7467}, FirstCommit: 1790000000023456789, Entries: EntryNumbers{40, 41, 0, 12}},
			{Digest: digest.FromString("whole"), Parts: 0, Error: "unsupported value bundle version 9"},
			{Digest: digest.FromString("again"), Complete: true, Already: true},
		}}, func() any { return &Restored{} }},
		{TypeError, Error{Message: StaleSettle}, func() any { return &Error{} }},
		{TypeWelcome, Welcome{}, func() any { return &Welcome{} }},
		{TypeExport, Export{Roots: []uint64{12, 13}, Outputs: []ExportOutput{{Number: 12, Address: testAddress()}}}, func() any { return &Export{} }},
		{TypeExported, Exported{ExportID: "x-1", Bundle: &bundle, Skipped: []SkippedRoot{{Number: 13, Reason: SkipBusy}}}, func() any { return &Exported{} }},
		{TypeExported, Exported{Skipped: []SkippedRoot{{Number: 12, Reason: SkipGone}}}, func() any { return &Exported{} }},
		{TypeExported, Exported{Skipped: []SkippedRoot{{Number: 14, Reason: SkipRefused, Message: "row 3: refused"}}}, func() any { return &Exported{} }},
		{TypeUpload, Upload{ExportID: "x-1", URLs: map[digest.Digest]string{layer.Descriptor.Digest: "https://blobs/put"}}, func() any { return &Upload{} }},
		{TypeUpload, Upload{ExportID: "x-1", URLs: map[digest.Digest]string{}, Packs: []UploadPack{{Pack: digest.FromString("pack"), URL: "https://blobs/put-pack", Members: []pack.Member{{Digest: layer.Descriptor.Digest, Size: 42}}}}}, func() any { return &Upload{} }},
		{TypeUpload, Upload{ExportID: "x-2", URLs: map[digest.Digest]string{}, Merged: true}, func() any { return &Upload{} }},
		{TypeUploaded, Uploaded{Done: []digest.Digest{layer.Descriptor.Digest}, Failed: []UploadFailure{{Digest: digest.FromString("other"), Message: "503"}}}, func() any { return &Uploaded{} }},
		{TypeMerge, Merge{Bundle: bundle}, func() any { return &Merge{} }},
		{TypeMerge, Merge{Bundle: bundle, Lookup: 31}, func() any { return &Merge{} }},
		{TypeMerged, Merged{
			Generation: 4,
			EngineTime: 1790000000123456789,
			Values:     []MergedValue{{Ordinal: 1, Number: 40, Replacements: 1, ExpiresAtUnix: 1790086400, Deps: []uint64{3}, Complete: []dagql.PersistedPartAddress{testAddress()}, Offered: []dagql.PersistedPartAddress{}}},
			Retained:   []RetainedRoot{{Ordinal: 1, ExpiresAtUnix: 1790086400}},
			Skipped:    []SkippedValue{{Ordinal: 2, Reason: SkipExpired}},
		}, func() any { return &Merged{} }},
		{TypeOffer, Offer{Items: []OfferItem{{Number: 40, Offers: []CloudPart{{Offer: testOffer(), CloudNumber: 12, CloudExpiresAtUnix: 1790086400, CloudStored: true}}}}}, func() any { return &Offer{} }},
		{TypeOffered, Offered{Items: []OfferedItem{
			{Number: 40, Replacements: 2, Deps: []uint64{3, 5}, Parts: []OfferedPart{{Address: testAddress(), Outcome: OfferAccepted}, {Address: testAddress(), Outcome: OfferInvalid, Message: "owner lacks service 7"}}},
			{Number: 41, Gone: true},
		}}, func() any { return &Offered{} }},
		{TypeRenew, Renew{RenewalKey: "12/fs", Layers: []snapshots.ExportLayer{layer}, NeededBlob: layer.Descriptor.Digest}, func() any { return &Renew{} }},
		{TypeRenew, Renew{RenewalKey: "12/fs", Layers: []snapshots.ExportLayer{layer}, NeededBlob: layer.Descriptor.Digest, Packs: map[digest.Digest]digest.Digest{layer.Descriptor.Digest: digest.FromString("pack")}}, func() any { return &Renew{} }},
		{TypeRenewed, Renewed{Addresses: map[digest.Digest]dagql.BlobAddress{layer.Descriptor.Digest: {URL: "https://blobs/get2", ExpiresAtUnix: 1790007200}}}, func() any { return &Renewed{} }},
		{TypeRenewed, Renewed{Unavailable: true}, func() any { return &Renewed{} }},
		{TypeLookup, Lookup{
			Misses: []LookupMiss{{Recipe: digest.FromString("recipe"), Extra: []digest.Digest{digest.FromString("extra")}, Field: "withExec"}, {Recipe: digest.FromString("other")}},
		}, func() any { return &Lookup{} }},
		{TypeLookedUp, LookedUp{Misses: []LookedUpMiss{
			{Status: LookupMerging, Merge: 40, Parts: LookupPartsComplete},
			{Status: LookupInFlight, Merge: 38, Parts: LookupPartsPartial},
			{Status: LookupUnknown},
			{Status: LookupNotStored},
			{Status: LookupDeferred},
		}}, func() any { return &LookedUp{} }},
		{TypeError, Error{Message: TooLargeMessage}, func() any { return &Error{} }},
	} {
		t.Run(string(tc.typ), func(t *testing.T) {
			env, err := NewEnvelope(9, 17, tc.typ, tc.body)
			require.NoError(t, err)
			raw, err := json.Marshal(env)
			require.NoError(t, err)
			var got Envelope
			require.NoError(t, json.Unmarshal(raw, &got))
			require.Equal(t, uint64(9), got.ID)
			require.Equal(t, uint64(17), got.Re)
			require.Equal(t, tc.typ, got.Type)
			body := tc.into()
			require.NoError(t, got.Decode(body))
			require.Equal(t, tc.body, derefBody(body))
		})
	}
}

// derefBody returns the value a decode target points at.
func derefBody(v any) any {
	switch b := v.(type) {
	case *Hello:
		return *b
	case *Welcome:
		return *b
	case *Export:
		return *b
	case *Exported:
		return *b
	case *Upload:
		return *b
	case *Uploaded:
		return *b
	case *Merge:
		return *b
	case *Merged:
		return *b
	case *Offer:
		return *b
	case *Offered:
		return *b
	case *Renew:
		return *b
	case *Renewed:
		return *b
	case *Lookup:
		return *b
	case *LookedUp:
		return *b
	case *Open:
		return *b
	case *Opened:
		return *b
	case *Settle:
		return *b
	case *Settled:
		return *b
	case *Claims:
		return *b
	case *Claimed:
		return *b
	case *Restore:
		return *b
	case *Restored:
		return *b
	case *Error:
		return *b
	}
	panic("unknown body type")
}

// The framing of design section 7.1: a request, and a reply naming it.
func TestEnvelopeFraming(t *testing.T) {
	request, err := NewEnvelope(17, 0, TypeExport, Export{Roots: []uint64{12}})
	require.NoError(t, err)
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, `{"id": 17, "type": "export", "body": {"roots": [12]}}`, string(raw))

	reply, err := NewEnvelope(9, 17, TypeError, Error{Message: TooLargeMessage})
	require.NoError(t, err)
	raw, err = json.Marshal(reply)
	require.NoError(t, err)
	require.JSONEq(t, `{"id": 9, "re": 17, "type": "error", "body": {"message": "too large"}}`, string(raw))
}

// An offer names the Cloud counterpart per offered part, not per item.
func TestOfferShape(t *testing.T) {
	raw, err := json.Marshal(Offer{Items: []OfferItem{{Number: 40, Offers: []CloudPart{{Offer: testOffer(), CloudNumber: 12, CloudExpiresAtUnix: 1790086400, CloudStored: true}}}}})
	require.NoError(t, err)
	var shape struct {
		Items []struct {
			Number uint64 `json:"number"`
			Offers []struct {
				Offer              json.RawMessage `json:"offer"`
				CloudNumber        uint64          `json:"cloudNumber"`
				CloudExpiresAtUnix int64           `json:"cloudExpiresAtUnix"`
				CloudStored        bool            `json:"cloudStored"`
			} `json:"offers"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &shape))
	require.Len(t, shape.Items, 1)
	require.Equal(t, uint64(40), shape.Items[0].Number)
	require.Len(t, shape.Items[0].Offers, 1)
	part := shape.Items[0].Offers[0]
	require.Equal(t, uint64(12), part.CloudNumber)
	require.Equal(t, int64(1790086400), part.CloudExpiresAtUnix)
	require.True(t, part.CloudStored)
	require.NotEmpty(t, part.Offer)
}

// Every request has a reply type, and nothing else does.
func TestReplyType(t *testing.T) {
	for request, reply := range map[Type]Type{
		TypeHello:   TypeWelcome,
		TypeExport:  TypeExported,
		TypeUpload:  TypeUploaded,
		TypeMerge:   TypeMerged,
		TypeOffer:   TypeOffered,
		TypeRenew:   TypeRenewed,
		TypeLookup:  TypeLookedUp,
		TypeOpen:    TypeOpened,
		TypeSettle:  TypeSettled,
		TypeClaims:  TypeClaimed,
		TypeRestore: TypeRestored,
	} {
		got, ok := ReplyType(request)
		require.True(t, ok, request)
		require.Equal(t, reply, got)
	}
	for _, notRequest := range []Type{TypeWelcome, TypeExported, TypeUploaded, TypeMerged, TypeOffered, TypeRenewed, TypeLookedUp, TypeOpened, TypeSettled, TypeClaimed, TypeRestored, TypeError} {
		_, ok := ReplyType(notRequest)
		require.False(t, ok, notRequest)
	}
}

// Every dagql offer outcome has a wire name.
func TestOfferOutcomeOf(t *testing.T) {
	for outcome, want := range map[dagql.OfferOutcome]OfferOutcome{
		dagql.OfferAccepted:         OfferAccepted,
		dagql.OfferAlreadyComplete:  OfferAlreadyComplete,
		dagql.OfferExecutionStarted: OfferExecutionStarted,
		dagql.OfferUnavailable:      OfferUnavailable,
		dagql.OfferInvalid:          OfferInvalid,
	} {
		got, err := OfferOutcomeOf(outcome)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	_, err := OfferOutcomeOf(dagql.OfferOutcome(200))
	require.Error(t, err)
}

// A lookup and its answer have the wire names the service reads. A merge
// always carries its lookup, 0 for a merge of the service's own placement,
// and a merge's reply always carries its stale rows.
func TestLookupShape(t *testing.T) {
	raw, err := json.Marshal(Lookup{Misses: []LookupMiss{{Recipe: "xxh3:1", Extra: []digest.Digest{"xxh3:2"}, Field: "withExec"}}})
	require.NoError(t, err)
	require.JSONEq(t, `{"misses": [{"recipe": "xxh3:1", "extra": ["xxh3:2"], "field": "withExec"}]}`, string(raw))

	raw, err = json.Marshal(LookedUp{Misses: []LookedUpMiss{{Status: LookupMerging, Merge: 40, Parts: LookupPartsNone}, {Status: LookupUnknown}}})
	require.NoError(t, err)
	require.JSONEq(t, `{"misses": [{"status": "merging", "merge": 40, "parts": "none"}, {"status": "unknown"}]}`, string(raw))

	for status, want := range map[LookupStatus]string{
		LookupUnknown:   "unknown",
		LookupNotStored: "notStored",
		LookupInFlight:  "inFlight",
		LookupMerging:   "merging",
		LookupDeferred:  "deferred",
	} {
		require.Equal(t, want, string(status))
	}
	for parts, want := range map[LookupParts]string{
		LookupPartsComplete: "complete",
		LookupPartsPartial:  "partial",
		LookupPartsNone:     "none",
	} {
		require.Equal(t, want, string(parts))
	}

	raw, err = json.Marshal(Merge{})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"lookup":0`)
	raw, err = json.Marshal(Merged{})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"stale":`)
}

// A merge lists a layer its outputs share, and the layer's address, once,
// and its outputs get their chains back, with the packs they name, as the
// engine's merge resolves them;
// an export still carries each output's chain whole.
func TestMergeListsSharedLayersOnce(t *testing.T) {
	layer := testLayer()
	addresses := map[digest.Digest]dagql.BlobAddress{layer.Descriptor.Digest: {URL: "https://blobs.example/layer"}}
	bundle := dagql.ValueBundle{Version: 8, Roots: []dagql.TransferredRoot{{Ordinal: 1}}}
	for i := range 2 {
		bundle.Values = append(bundle.Values, dagql.TransferredValue{Ordinal: dagql.TransferOrdinal(i + 1), SenderNumber: uint64(i + 1)})
		bundle.Outputs = append(bundle.Outputs, dagql.TransferredOutput{
			Ordinal: dagql.TransferOrdinal(i + 1),
			Address: testAddress(),
			State:   "completed",
			Chain:   &dagql.OfferedChain{Layers: []snapshots.ExportLayer{layer}, Addresses: addresses, Packs: map[digest.Digest]digest.Digest{layer.Descriptor.Digest: digest.FromString("pack")}},
		})
	}
	count := func(body any) int {
		env, err := NewEnvelope(1, 0, TypeMerge, body)
		require.NoError(t, err)
		return strings.Count(string(env.Body), "https://blobs.example/layer")
	}
	require.Equal(t, 1, count(Merge{Bundle: bundle}))
	require.Equal(t, 2, count(Exported{Bundle: &bundle}))

	env, err := NewEnvelope(1, 0, TypeMerge, Merge{Bundle: bundle, Lookup: 3})
	require.NoError(t, err)
	var got Merge
	require.NoError(t, env.Decode(&got))
	require.NoError(t, dagql.ResolveChainPrefixes(&got.Bundle), "the engine's merge resolves the layers")
	require.Equal(t, Merge{Bundle: bundle, Lookup: 3}, got)
}
