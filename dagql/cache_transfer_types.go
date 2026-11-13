package dagql

import (
	"encoding/json"

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

	// storedEnvelope is, in a bundle a blob-backed cache built, the record's
	// envelope as the cache holds it, compressed (storedBody), in place of
	// Record.Envelope: the bundle's encoding writes it out, and a merge of
	// the bundle in the same process decodes it (cloneValueBundle).
	storedEnvelope []byte
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
// envelope and call frame as the cache holds them, decompressed, not
// decoded.
func (v TransferredValue) storedJSON() (storedValueJSON, error) {
	body := &storedBody{envelope: v.storedEnvelope, frame: v.storedCall, frameBytes: v.storedCallBytes}
	raw, err := body.envelopeJSON()
	if err != nil {
		return storedValueJSON{}, err
	}
	var call json.RawMessage
	if v.storedCall != nil {
		call, err = body.frameJSON()
	} else {
		call, err = json.Marshal(v.Record.Call)
	}
	if err != nil {
		return storedValueJSON{}, err
	}
	return storedValueJSON{
		Ordinal:            v.Ordinal,
		SenderNumber:       v.SenderNumber,
		SenderReplacements: v.SenderReplacements,
		Record:             storedRecordJSON{ResultID: v.Record.ResultID, Envelope: raw, Call: call, SnapshotLinks: v.Record.SnapshotLinks},
		DependencyIDs:      v.DependencyIDs,
		ExpiresAtUnix:      v.ExpiresAtUnix,
		Held:               v.Held,
	}, nil
}

// storedValueJSON is TransferredValue's encoding, field for field, with its
// record's envelope as encoded JSON.
type storedValueJSON struct {
	Ordinal            TransferOrdinal  `json:"ordinal"`
	SenderNumber       uint64           `json:"senderNumber"`
	SenderReplacements uint64           `json:"senderReplacements,omitempty"`
	Record             storedRecordJSON `json:"record"`
	DependencyIDs      []uint64         `json:"dependencyIDs,omitempty"`
	ExpiresAtUnix      int64            `json:"expiresAtUnix,omitempty"`
	Held               digest.Digest    `json:"held,omitempty"`
}

// storedRecordJSON is a PersistedRecord with its envelope and call as
// encoded JSON.
type storedRecordJSON struct {
	ResultID      uint64                     `json:"resultID"`
	Envelope      json.RawMessage            `json:"envelope"`
	Call          json.RawMessage            `json:"call"`
	SnapshotLinks []PersistedSnapshotRefLink `json:"snapshotLinks,omitempty"`
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
	// Layers and Addresses are the layers its outputs name by place, and
	// their blobs' download addresses, each listed once (ShareChainLayers).
	Layers    []snapshots.ExportLayer       `json:"layers,omitempty"`
	Addresses map[digest.Digest]BlobAddress `json:"addresses,omitempty"`
}

// TransferredCall is a call that returned an existing entry: the call as its
// span encodes it (ResultCall.callPB, the dagger.io/dag.call attribute), and
// the row of the entry it returned. A merge derives the call's identity from
// the encoding and teaches it onto that entry, as the entry's cache did when
// the call completed. It owns nothing.
type TransferredCall struct {
	Call  string          `json:"call"`
	Value TransferOrdinal `json:"value"`
}
type ImportedValue struct {
	Ordinal  TransferOrdinal
	ResultID uint64
}

const valueBundleVersion = 8

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
