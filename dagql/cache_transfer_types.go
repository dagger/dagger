package dagql

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// TransferOrdinal names a row within a bundle. Zero denotes optional absence.
type TransferOrdinal uint64

type ValueSelection struct {
	Roots   []AnyResult
	Outputs []SelectedValueOutput
	// LeaveOutOutputsOutsideClosure leaves out a selected output whose entry
	// the captured closure doesn't hold, instead of refusing the selection.
	LeaveOutOutputsOutsideClosure bool
	// EveryRoot sends every root or none: a capture that would leave a root
	// out consumes nothing, and returns a RootsLeftOutError naming the roots
	// it left out, before it opens any chain.
	EveryRoot bool
	// Known are the rows the recipient has merged: the bundle sends each one
	// that hasn't changed as a held row.
	Known *KnownRows
}
type SelectedValueOutput struct {
	Result  AnyResult
	Address PersistedPartAddress
}
type PersistedPartAddress struct {
	OutputPath PersistedRefPath `json:"outputPath,omitempty"`
	Part       PartKey          `json:"part"`
}
type SnapshotValue struct {
	Kind     string                      `json:"kind"`
	Path     string                      `json:"path,omitempty"`
	Platform *ocispecs.Platform          `json:"platform,omitempty"`
	Services []TransferredServiceBinding `json:"services,omitempty"`
}
type TransferredServiceBinding struct {
	ServiceResultID uint64   `json:"serviceResultID"`
	Hostname        string   `json:"hostname"`
	Aliases         []string `json:"aliases,omitempty"`
}
type BlobAddress struct {
	URL           string `json:"url"`
	ExpiresAtUnix int64  `json:"expiresAtUnix,omitempty"`
}
type OfferedChain struct {
	Layers     []snapshots.ExportLayer       `json:"layers"`
	Addresses  map[digest.Digest]BlobAddress `json:"addresses,omitempty"`
	RenewalKey string                        `json:"renewalKey,omitempty"`
	// Packs maps each layer stored in a pack, with other small blobs, to its
	// pack's ID; the layer's address is then the pack's. Unlike an address,
	// which renewal replaces, a pack is part of the offer's meaning: an offer
	// that moves a layer into or out of a pack replaces the one before it.
	Packs map[digest.Digest]digest.Digest `json:"packs,omitempty"`
}
type PersistedOfferOwner struct {
	DependencyIDs []uint64 `json:"dependencyIDs,omitempty"`
}
type PersistedPartOffer struct {
	Address PersistedPartAddress `json:"address"`
	Value   SnapshotValue        `json:"value"`
	Chain   OfferedChain         `json:"chain"`
	Owner   PersistedOfferOwner  `json:"owner"`
}
type TransferredValue struct {
	Ordinal TransferOrdinal `json:"ordinal"`
	// SenderNumber is the entry's number in the sending cache. An engine that
	// imports the value keeps it as its Cloud holding's number.
	SenderNumber uint64 `json:"senderNumber"`
	// SenderReplacements is the entry's replacement count in the sending
	// cache, read with the record: it orders the value state the record
	// describes.
	SenderReplacements uint64 `json:"senderReplacements,omitempty"`
	// SenderStamp is, on a value a blob-backed cache sends in full, its
	// entry's change stamp (sharedResult.changedAt): the write count of the
	// last change to what a stored bundle carries of the entry, in the start
	// of the cache the bundle's SenderCache names. An engine that merged the
	// value keeps it, so that a later value of the same entry at the same
	// stamp, a record it holds already, isn't taught again
	// (unchangedCloudRowLocked).
	SenderStamp uint64 `json:"senderStamp,omitempty"`
	// Record is the record as its sender holds it: it names itself, by
	// ResultID, and every row it references by the sender's entry numbers,
	// which the receiver maps through the bundle's SenderNumbers. Everything
	// else in the bundle names rows by ordinal, as do the offers a record
	// carries.
	Record        PersistedRecord `json:"record"`
	DependencyIDs []uint64        `json:"dependencyIDs,omitempty"`
	ExpiresAtUnix int64           `json:"expiresAtUnix,omitempty"`
	// Held, when set, makes this a held row: the recipient's own value of
	// this recipe stands for the row, which carries no record. Its Record is
	// empty but for ResultID, its SenderNumber. SenderNumber,
	// SenderReplacements and ExpiresAtUnix still describe the sending entry,
	// and other rows' records reference the row by its SenderNumber, their
	// DependencyIDs by its ordinal. A held row stands for its closure,
	// but its DependencyIDs can name rows below it that the bundle carries,
	// which then go with it, and an output can sit on it; the recipient
	// checks such an output against its own copy of the row. The Cloud sends
	// held rows to an engine (StoredBundle's HeldBy), with no dependencies
	// and no outputs, and an engine sends them to the Cloud in an export.
	Held digest.Digest `json:"held,omitempty"`
	// Parts are, on a value a blob-backed cache sends, the parts its record
	// proves complete and those it declares absent or pending, as the cache
	// worked them out when it stored the record: a receiver reads them here
	// instead of decoding the record's payload. Nil on a value whose sender
	// doesn't carry them, an engine's.
	Parts *TransferredRecordParts `json:"parts,omitempty"`

	// storedEnvelope is, in a bundle a blob-backed cache built, the record's
	// envelope as the cache holds it, compressed (storedBody), in place of
	// Record.Envelope: the bundle's encoding writes it out, and a merge of
	// the bundle in the same process decodes it (cloneValueBundle).
	storedEnvelope []byte
	// served is, on the Cloud, the run its stored record named when the
	// bundle was built (StoredBundle), for the merged reply's holding of
	// it (holding.served); nil when it named none. It is never sent.
	served *servedRun
	// storedCall is, beside a stored envelope, the record's call frame as
	// the cache holds it in the same body, compressed, in place of
	// Record.Call, and storedCallBytes its JSON's length: the bundle's
	// encoding writes it out, and cloneValueBundle decodes it.
	storedCall      []byte
	storedCallBytes int64
}

// MarshalJSON encodes the value as its fields say, with a stored envelope
// written out as it is held, decompressed but not decoded: the same JSON as
// the value with its envelope decoded, field for field.
func (v TransferredValue) MarshalJSON() ([]byte, error) {
	type plain TransferredValue
	if v.storedEnvelope == nil {
		return json.Marshal(plain(v))
	}
	stored, err := v.storedJSON()
	if err != nil {
		return nil, err
	}
	return json.Marshal(stored)
}

// storedJSON is a value with a stored envelope as its JSON writes it: the
// envelope and call frame as the cache holds them, decompressed as they are
// written, not decoded.
func (v TransferredValue) storedJSON() (storedValueJSON, error) {
	call := storedJSONBody{what: "call frame", compressed: v.storedCall}
	if v.storedCall == nil {
		raw, err := json.Marshal(v.Record.Call)
		if err != nil {
			return storedValueJSON{}, err
		}
		call.raw = raw
	}
	return storedValueJSON{
		Ordinal:            v.Ordinal,
		SenderNumber:       v.SenderNumber,
		SenderReplacements: v.SenderReplacements,
		SenderStamp:        v.SenderStamp,
		Record:             storedRecordJSON{ResultID: v.Record.ResultID, Envelope: storedJSONBody{what: "envelope", compressed: v.storedEnvelope}, Call: call, SnapshotLinks: v.Record.SnapshotLinks},
		DependencyIDs:      v.DependencyIDs,
		ExpiresAtUnix:      v.ExpiresAtUnix,
		Held:               v.Held,
		Parts:              v.Parts,
	}, nil
}

// TransferredRecordParts are a record's parts as its sender worked them out
// (TransferredValue.Parts): those it proves complete
// (memoRecordCompletePartKeys) and those it declares absent or pending
// (declaredPartsOf), each sorted by its key.
type TransferredRecordParts struct {
	Complete []PersistedPartAddress    `json:"complete,omitempty"`
	Declared []TransferredDeclaredPart `json:"declared,omitempty"`
}

// TransferredDeclaredPart is a part a record declares absent or pending, its
// state and the kind of its value (declaredPart).
type TransferredDeclaredPart struct {
	Address PersistedPartAddress `json:"address"`
	State   string               `json:"state"`
	Kind    string               `json:"kind,omitempty"`
}

// storedValueJSON is TransferredValue's encoding, field for field, with its
// record's envelope as encoded JSON.
type storedValueJSON struct {
	Ordinal            TransferOrdinal  `json:"ordinal"`
	SenderNumber       uint64           `json:"senderNumber"`
	SenderReplacements uint64           `json:"senderReplacements,omitempty"`
	SenderStamp        uint64           `json:"senderStamp,omitempty"`
	Record             storedRecordJSON `json:"record"`
	DependencyIDs      []uint64         `json:"dependencyIDs,omitempty"`
	ExpiresAtUnix      int64            `json:"expiresAtUnix,omitempty"`
	Held               digest.Digest    `json:"held,omitempty"`

	Parts *TransferredRecordParts `json:"parts,omitempty"`
}

// storedRecordJSON is a PersistedRecord with its envelope and call as
// encoded JSON.
type storedRecordJSON struct {
	ResultID      uint64                     `json:"resultID"`
	Envelope      storedJSONBody             `json:"envelope"`
	Call          storedJSONBody             `json:"call"`
	SnapshotLinks []PersistedSnapshotRefLink `json:"snapshotLinks,omitempty"`
}

// storedJSONBody is JSON a stored value writes out as it is: its envelope or
// call frame compressed as the cache holds it (storedBody), decompressed
// only as it is written; or a call frame's JSON, raw.
type storedJSONBody struct {
	what       string
	compressed []byte
	raw        json.RawMessage
}

// MarshalJSON returns the body's JSON, decompressed.
func (b storedJSONBody) MarshalJSON() ([]byte, error) {
	if b.compressed == nil {
		return b.raw.MarshalJSON()
	}
	return b.decompress(nil)
}

// decompress appends the compressed body's JSON to dst.
func (b storedJSONBody) decompress(dst []byte) ([]byte, error) {
	_, dec, err := storedBodyCodec()
	if err != nil {
		return nil, err
	}
	raw, err := dec.DecodeAll(b.compressed, dst)
	if err != nil {
		return nil, fmt.Errorf("decompress stored %s: %w", b.what, err)
	}
	return raw, nil
}

type TransferredRoot struct {
	Ordinal       TransferOrdinal `json:"ordinal"`
	ExpiresAtUnix int64           `json:"expiresAtUnix,omitempty"`
}
type TransferredOutput struct {
	Ordinal TransferOrdinal      `json:"ordinal"`
	Address PersistedPartAddress `json:"address"`
	State   string               `json:"state"`
	Value   *SnapshotValue       `json:"value,omitempty"`
	Chain   *OfferedChain        `json:"chain,omitempty"`
	// Prefix, instead of Chain, names the chain the part's layers are the
	// first of: a part that a selected chain's snapshot walk passed carries
	// no copy of the layers the chain carries.
	Prefix *ChainPrefix         `json:"prefix,omitempty"`
	Owner  *PersistedOfferOwner `json:"owner,omitempty"`
	// Layers, with Chain, names the chain's layers by their place in the
	// bundle's Layers, which the chain doesn't carry (ShareChainLayers).
	Layers []int `json:"layers,omitempty"`
}

// ChainPrefix names the first Layers layers of the chain of the bundle's
// output at Ordinal and Address, which carries the chain itself.
type ChainPrefix struct {
	Ordinal TransferOrdinal      `json:"ordinal"`
	Address PersistedPartAddress `json:"address"`
	Layers  int                  `json:"layers"`
}
type ValueBundle struct {
	Version int                 `json:"version"`
	Roots   []TransferredRoot   `json:"roots"`
	Values  []TransferredValue  `json:"values"`
	Outputs []TransferredOutput `json:"outputs,omitempty"`
	// Calls are calls that returned one of the bundle's entries other than
	// their own, which a cache that runs them indexes on the entry they
	// returned. Only the Cloud sends them, from the call spans it read.
	Calls []TransferredCall `json:"calls,omitempty"`
	// Layers and Addresses are the layers its outputs name by place, each
	// with the pack their chains store it in, and their blobs' download
	// addresses, each listed once (ShareChainLayers).
	Layers    []BundleLayer                 `json:"layers,omitempty"`
	Addresses map[digest.Digest]BlobAddress `json:"addresses,omitempty"`
	// SenderCache is, on a bundle a blob-backed cache with an identity
	// builds, that identity: the cache's ID and start count
	// (CacheIdentity.String). Its values' change stamps count writes from
	// that start of that cache, so two bundles' stamps compare only when it
	// is the same: a cache started again, or a new cache, counts from the
	// start. Empty for a cache with no identity, whose stamps compare with
	// none.
	SenderCache string `json:"senderCache,omitempty"`
}

// BundleLayer is a layer a bundle's outputs name by place (ShareChainLayers),
// and the pack the chains that name it there store it in, if any
// (OfferedChain.Packs), as a blob-backed cache holds a stored layer: a layer
// one chain stores alone and another in a pack is two of the bundle's layers.
type BundleLayer struct {
	Layer snapshots.ExportLayer `json:"layer"`
	Pack  digest.Digest         `json:"pack,omitempty"`
}

// TransferredCall is a call that returned an existing entry: the call as its
// span encodes it (ResultCall.callPB, the dagger.io/dag.call attribute), the
// row of the entry it returned, and, for each of the call's inputs in the
// encoding's order, the row of the entry that places it: an entry of the
// input's class, the one under the input's own recipe or another the Cloud
// holds in that class from an engine's word, which the bundle carries or the
// recipient holds; 0 for a digest-witnessed string's digest, which the
// recipient's own call reproduces. A merge puts each input in its row's
// class, derives the call's identity from the encoding and teaches it onto
// the entry it returned, as the entry's cache did when the call completed.
// It owns nothing.
type TransferredCall struct {
	Call   string            `json:"call"`
	Value  TransferOrdinal   `json:"value"`
	Inputs []TransferOrdinal `json:"inputs,omitempty"`
}
type ImportedValue struct {
	Ordinal  TransferOrdinal
	ResultID uint64
}

// ValueBundleVersion is the version of the bundles an engine of this source
// writes, and the only one it merges: a merge refuses a bundle of any other.
// cmd/engine-image-entries compares another engine image's bundle with it, to
// know whether an engine of this source can start from those entries.
//
// 9: a value a blob-backed cache sends carries its record's complete and
// declared parts (TransferredValue.Parts).
// 10: a layer the remote cache carries is a listing, after the content blobs
// of its larger files (snapshots.MediaTypeListing).
// 11: a bundle's listed layers carry their packs, and an output whose chain
// names its layers by place carries none (BundleLayer).
// 12: a value a blob-backed cache sends in full carries its entry's change
// stamp, and the bundle the cache's identity (TransferredValue.SenderStamp,
// ValueBundle.SenderCache).
// 13: a call names, for each of its inputs, the row that places it, an
// entry of the input's class (TransferredCall.Inputs).
const ValueBundleVersion = 13

// PersistedTransferCodec operates only on encoded copies, without resolving
// schemas, operations, result references or storage.
type PersistedTransferCodec interface {
	NormalizeForeign(PersistedPayloadVisit) (ForeignPayload, error)
	ValidateForeign(PersistedPayloadVisit) error
	MapSnapshotParts(PersistedPayloadVisit) ([]CapturedCodecOutput, error)
}
type ForeignPayload struct{ JSON json.RawMessage }
type CapturedCodecOutput struct {
	Address PersistedPartAddress
	State   string
	// ValueKind remains known when pending output metadata is not yet known.
	ValueKind  string
	Value      *SnapshotValue
	Role       string
	SnapshotID string
}
