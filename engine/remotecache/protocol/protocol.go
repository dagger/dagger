// Package protocol defines the messages an engine and the remote cache
// service exchange over one WebSocket. Both sides import it, so a change to
// the wire format fails to build on the other side. Bundles, offers and
// addresses are dagql's own transfer types.
//
// Every message is one JSON object in one text message: an Envelope. Either
// side may send requests, and many may be in flight both ways. A reply names
// its request's ID in Re; a reply of type TypeError ends that request only.
package protocol

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots"
)

const (
	// EnginePath is the service endpoint an engine dials.
	EnginePath = "/v1/engine"

	// MaxMessageBytes is the largest message either side accepts, set as
	// each side's read limit. A reader closes the connection on a larger
	// message, so a sender answers TypeError with TooLargeMessage instead.
	MaxMessageBytes = 64 << 20

	// PingInterval is how often the engine pings the service. A ping with
	// no pong within PingTimeout closes the connection.
	PingInterval = 15 * time.Second
	PingTimeout  = 15 * time.Second

	// CloseTokenRevoked is the WebSocket close status of a connection whose
	// token the service found revoked.
	CloseTokenRevoked = 4001
)

// Error messages both sides recognize.
const (
	TooLargeMessage = "too large"
)

// Type names a message.
type Type string

const (
	TypeHello    Type = "hello"
	TypeWelcome  Type = "welcome"
	TypeExport   Type = "export"
	TypeExported Type = "exported"
	TypeUpload   Type = "upload"
	TypeUploaded Type = "uploaded"
	TypeMerge    Type = "merge"
	TypeMerged   Type = "merged"
	TypeOffer    Type = "offer"
	TypeOffered  Type = "offered"
	TypeRenew    Type = "renew"
	TypeRenewed  Type = "renewed"
	TypeLookup   Type = "lookup"
	TypeLookedUp Type = "lookedUp"
	TypeError    Type = "error"
)

// ReplyType returns the type of a successful reply to a request, and false
// for a type that is not a request.
func ReplyType(request Type) (Type, bool) {
	switch request {
	case TypeHello:
		return TypeWelcome, true
	case TypeExport:
		return TypeExported, true
	case TypeUpload:
		return TypeUploaded, true
	case TypeMerge:
		return TypeMerged, true
	case TypeOffer:
		return TypeOffered, true
	case TypeRenew:
		return TypeRenewed, true
	case TypeLookup:
		return TypeLookedUp, true
	}
	return "", false
}

// Envelope frames every message. ID numbers a message per sender, from 1; a
// reply names its request's ID in Re, and Re 0 means the message is not a
// reply.
type Envelope struct {
	ID   uint64          `json:"id"`
	Re   uint64          `json:"re,omitempty"`
	Type Type            `json:"type"`
	Body json.RawMessage `json:"body"`
}

// NewEnvelope encodes body into an envelope.
func NewEnvelope(id, re uint64, typ Type, body any) (Envelope, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return Envelope{}, fmt.Errorf("encode %s body: %w", typ, err)
	}
	return Envelope{ID: id, Re: re, Type: typ, Body: raw}, nil
}

// Decode decodes the envelope's body into v.
func (e Envelope) Decode(v any) error {
	if err := json.Unmarshal(e.Body, v); err != nil {
		return fmt.Errorf("decode %s body: %w", e.Type, err)
	}
	return nil
}

// Error is the body of a TypeError reply.
type Error struct {
	Message string `json:"message"`
}

// Hello is the engine's first message on a connection.
type Hello struct {
	// CacheID and Generation are the engine cache's identity and start count.
	CacheID       string `json:"cacheID"`
	Generation    uint64 `json:"generation"`
	EngineVersion string `json:"engineVersion"`
	EngineName    string `json:"engineName"`
	// HeldRows reports that the engine merges bundles with held rows
	// (dagql.TransferredValue.Held). The service sends them only to an
	// engine that sets it.
	HeldRows bool `json:"heldRows,omitempty"`
	// Ephemeral says the engine's cache does not outlive this run of it, as
	// for a nested engine a check starts as a service: no later process
	// comes back under this identity, and it may be killed without a stop.
	Ephemeral bool `json:"ephemeral,omitempty"`
}

// Welcome is the service's reply to Hello.
type Welcome struct{}

// Export asks the engine for the values of some of its entries, by number,
// and the selected parts' bytes.
type Export struct {
	Roots   []uint64       `json:"roots"`
	Outputs []ExportOutput `json:"outputs,omitempty"`
}

// ExportOutput selects one part of an entry for upload.
type ExportOutput struct {
	Number  uint64                     `json:"number"`
	Address dagql.PersistedPartAddress `json:"address"`
}

// SkipReason says why a root was left out of an export or a merge.
type SkipReason string

const (
	// SkipGone: the engine no longer has the entry.
	SkipGone SkipReason = "gone"
	// SkipBusy: the entry could not be captured now; ask again later. The
	// skip's Message says what was busy.
	SkipBusy SkipReason = "busy"
	// SkipRefused: the engine refused to capture the entry, and would again;
	// the skip's Message says why.
	SkipRefused SkipReason = "refused"
	// SkipExpired: a merged root had expired when the merge decided.
	SkipExpired SkipReason = "expired"
)

// Exported answers Export. Bundle is nil when no root survived. The engine
// keeps the bundle's chains open under ExportID until Upload, or until the
// connection ends.
type Exported struct {
	ExportID string             `json:"exportID,omitempty"`
	Bundle   *dagql.ValueBundle `json:"bundle,omitempty"`
	Skipped  []SkippedRoot      `json:"skipped,omitempty"`
}

// SkippedRoot is an exported root left out, by number. Message is the
// engine's error for a busy or refused root, cut to a bounded length.
type SkippedRoot struct {
	Number  uint64     `json:"number"`
	Reason  SkipReason `json:"reason"`
	Message string     `json:"message,omitempty"`
}

// Upload gives the engine one presigned PUT per blob the service needs from
// an export. An empty map needs nothing, and the engine releases the chains.
type Upload struct {
	ExportID string                   `json:"exportID"`
	URLs     map[digest.Digest]string `json:"urls"`
}

// Uploaded answers Upload.
type Uploaded struct {
	Done   []digest.Digest `json:"done"`
	Failed []UploadFailure `json:"failed,omitempty"`
}

// UploadFailure is one blob the engine could not upload.
type UploadFailure struct {
	Digest  digest.Digest `json:"digest"`
	Message string        `json:"message"`
}

// Merge gives the engine a bundle of the Cloud's values. Its SenderNumbers
// are the Cloud's numbers, and its outputs carry GET addresses. A held row
// names, by recipe, a value the engine is expected to hold; the engine
// resolves it as it would keep a record of that recipe.
type Merge struct {
	Bundle dagql.ValueBundle `json:"bundle"`
	// Lookup is the ID of the engine's Lookup this merge answers, or 0 for
	// a merge of the service's own placement. The engine commits a merge
	// that answers a lookup ahead of the others.
	Lookup uint64 `json:"lookup,omitempty"`
}

// Merged answers Merge with what the merge's commit left, read under the lock
// that decided it.
type Merged struct {
	// Generation and EngineTime (Unix nanoseconds) are the engine cache's
	// start count and clock at the commit. They order the reply's retention.
	Generation uint64         `json:"generation"`
	EngineTime int64          `json:"engineTime"`
	Values     []MergedValue  `json:"values"`
	Retained   []RetainedRoot `json:"retained"`
	Skipped    []SkippedValue `json:"skipped,omitempty"`
	// Stale are the bundle's held rows the engine didn't hold when the
	// merge decided: no current entry for the recipe, one with no value of
	// its own, or an expired one the row hasn't outlived. A reply with Stale
	// committed nothing, and its Values, Retained and Skipped are empty.
	Stale []dagql.TransferOrdinal `json:"stale,omitempty"`
}

// MergedValue is the engine entry a bundle record landed on, and its value
// state at the replacement count.
type MergedValue struct {
	Ordinal       dagql.TransferOrdinal        `json:"ordinal"`
	Number        uint64                       `json:"number"`
	Replacements  uint64                       `json:"replacements"`
	ExpiresAtUnix int64                        `json:"expiresAtUnix"`
	Deps          []uint64                     `json:"deps"`
	Complete      []dagql.PersistedPartAddress `json:"complete"`
	Offered       []dagql.PersistedPartAddress `json:"offered"`
}

// RetainedRoot is a merged root the engine retains, until ExpiresAtUnix
// (0: never).
type RetainedRoot struct {
	Ordinal       dagql.TransferOrdinal `json:"ordinal"`
	ExpiresAtUnix int64                 `json:"expiresAtUnix"`
}

// SkippedValue is a bundle root left out, by ordinal.
type SkippedValue struct {
	Ordinal dagql.TransferOrdinal `json:"ordinal"`
	Reason  SkipReason            `json:"reason"`
}

// Offer places the Cloud's parts on the engine's own entries, by the
// engine's numbers.
type Offer struct {
	Items []OfferItem `json:"items"`
}

// OfferItem offers parts to one engine entry. Each offer names one of the
// item entry's own parts; OfferedItem's Replacements and Deps are that
// entry's.
type OfferItem struct {
	Number uint64      `json:"number"`
	Offers []CloudPart `json:"offers"`
}

// CloudPart is one offered part and the Cloud counterpart of the entry its
// address resolves to (dagql.CloudPartOffer).
type CloudPart struct {
	Offer              dagql.PersistedPartOffer `json:"offer"`
	CloudNumber        uint64                   `json:"cloudNumber"`
	CloudExpiresAtUnix int64                    `json:"cloudExpiresAtUnix"`
	CloudStored        bool                     `json:"cloudStored"`
}

// Offered answers Offer, one item per offered item, in order.
type Offered struct {
	Items []OfferedItem `json:"items"`
}

// OfferedItem answers one OfferItem: Gone, or the entry's replacement count
// and direct dependencies with each part's outcome, read where the outcome
// was decided.
type OfferedItem struct {
	Number       uint64        `json:"number"`
	Gone         bool          `json:"gone,omitempty"`
	Replacements uint64        `json:"replacements,omitempty"`
	Deps         []uint64      `json:"deps,omitempty"`
	Parts        []OfferedPart `json:"parts,omitempty"`
}

// OfferedPart is one offered part's outcome.
type OfferedPart struct {
	Address dagql.PersistedPartAddress `json:"address"`
	Outcome OfferOutcome               `json:"outcome"`
	Message string                     `json:"message,omitempty"`
}

// OfferOutcome is what the engine did with an offered part.
type OfferOutcome string

const (
	OfferAccepted         OfferOutcome = "accepted"
	OfferAlreadyComplete  OfferOutcome = "alreadyComplete"
	OfferExecutionStarted OfferOutcome = "executionStarted"
	OfferUnavailable      OfferOutcome = "unavailable"
	OfferInvalid          OfferOutcome = "invalid"
)

// OfferOutcomeOf names a dagql offer outcome on the wire.
func OfferOutcomeOf(outcome dagql.OfferOutcome) (OfferOutcome, error) {
	switch outcome {
	case dagql.OfferAccepted:
		return OfferAccepted, nil
	case dagql.OfferAlreadyComplete:
		return OfferAlreadyComplete, nil
	case dagql.OfferExecutionStarted:
		return OfferExecutionStarted, nil
	case dagql.OfferUnavailable:
		return OfferUnavailable, nil
	case dagql.OfferInvalid:
		return OfferInvalid, nil
	}
	return "", fmt.Errorf("unknown offer outcome %d", outcome)
}

// Renew asks the service for fresh download addresses of an offered chain's
// layers.
type Renew struct {
	RenewalKey string                  `json:"renewalKey"`
	Layers     []snapshots.ExportLayer `json:"layers"`
	NeededBlob digest.Digest           `json:"neededBlob"`
}

// Renewed answers Renew: an address per layer, or Unavailable when a layer is
// missing.
type Renewed struct {
	Addresses   map[digest.Digest]dagql.BlobAddress `json:"addresses,omitempty"`
	Unavailable bool                                `json:"unavailable,omitempty"`
}

// The bounds of a Lookup.
const (
	// LookupMisses is the most misses one Lookup names.
	LookupMisses = 32
	// LookupExtras is the most extra digests one miss names.
	LookupExtras = 8
)

// Lookup asks the service for the Cloud's values of calls the engine's cache
// missed. The service answers at once with what it will do for each, and
// sends what it found as ordinary merges that name the lookup.
type Lookup struct {
	Misses []LookupMiss `json:"misses"`
	// WaitMillis is how long the engine's evaluation of a missed call waits
	// for a merge the answer names, for the service's metrics.
	WaitMillis int64 `json:"waitMillis"`
}

// LookupMiss is one call the engine's cache missed: its recipe digest, and
// its extra digests, which the engine's own lookup tries after the recipe.
// Field names the call, for logs.
type LookupMiss struct {
	Recipe digest.Digest   `json:"recipe"`
	Extra  []digest.Digest `json:"extra,omitempty"`
	Field  string          `json:"field,omitempty"`
}

// LookedUp answers Lookup, one item per miss, in order. The service sends it
// before any merge it names.
type LookedUp struct {
	Misses []LookedUpMiss `json:"misses"`
}

// LookedUpMiss is what the service does for one miss. Merge is the ID of the
// Merge request that carries the call's value, with LookupInFlight and
// LookupMerging, and Parts says how much of the value's bytes it carries.
type LookedUpMiss struct {
	Status LookupStatus `json:"status"`
	Merge  uint64       `json:"merge,omitempty"`
	Parts  LookupParts  `json:"parts,omitempty"`
}

// LookupStatus is what the service does for a missed call.
type LookupStatus string

const (
	// LookupUnknown: the Cloud knows none of the call's digests.
	LookupUnknown LookupStatus = "unknown"
	// LookupNotStored: the Cloud has no stored, unexpired value of the call.
	LookupNotStored LookupStatus = "notStored"
	// LookupInFlight: a merge already sent to the engine carries the value.
	LookupInFlight LookupStatus = "inFlight"
	// LookupMerging: a merge sent now, after the answer, carries the value.
	LookupMerging LookupStatus = "merging"
	// LookupDeferred: the value goes at the front of the service's queue,
	// with no merge named.
	LookupDeferred LookupStatus = "deferred"
)

// LookupParts is how many of the value's parts a named merge carries bytes
// for.
type LookupParts string

const (
	LookupPartsComplete LookupParts = "complete"
	LookupPartsPartial  LookupParts = "partial"
	LookupPartsNone     LookupParts = "none"
)
