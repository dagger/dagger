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
	Ordinal       TransferOrdinal `json:"ordinal"`
	Record        PersistedRecord `json:"record"`
	DependencyIDs []uint64        `json:"dependencyIDs,omitempty"`
	ExpiresAtUnix int64           `json:"expiresAtUnix,omitempty"`
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
	Owner   *PersistedOfferOwner `json:"owner,omitempty"`
}
type ValueBundle struct {
	Version int                 `json:"version"`
	Roots   []TransferredRoot   `json:"roots"`
	Values  []TransferredValue  `json:"values"`
	Outputs []TransferredOutput `json:"outputs,omitempty"`
}
type ImportedValue struct {
	Ordinal  TransferOrdinal
	ResultID uint64
}

const valueBundleVersion = 1

// PersistedTransferCodec operates only on encoded copies, without resolving
// schemas, producers, result references or storage.
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
