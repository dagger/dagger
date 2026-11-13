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
	"github.com/dagger/dagger/engine/remotecache/pack"
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
	// StaleSettle ends a settle whose period is no longer the engine's
	// current one, or that an open with a later period superseded.
	StaleSettle = "stale settle"
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
	TypeOpen     Type = "open"
	TypeOpened   Type = "opened"
	TypeSettle   Type = "settle"
	TypeSettled  Type = "settled"
	TypeClaims   Type = "claims"
	TypeClaimed  Type = "claimed"
	TypeRestore  Type = "restore"
	TypeRestored Type = "restored"
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
	case TypeOpen:
		return TypeOpened, true
	case TypeSettle:
		return TypeSettled, true
	case TypeClaims:
		return TypeClaimed, true
	case TypeRestore:
		return TypeRestored, true
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

// EncodeEnvelope returns the message an envelope of body is, the bytes
// json.Marshal gives for NewEnvelope's envelope, in one encoding: NewEnvelope
// encodes the body into its own bytes, and encoding the envelope checks and
// copies them again.
func EncodeEnvelope(id, re uint64, typ Type, body any) ([]byte, error) {
	data, err := json.Marshal(outgoing{ID: id, Re: re, Type: typ, Body: body})
	if err != nil {
		return nil, fmt.Errorf("encode %s body: %w", typ, err)
	}
	return data, nil
}

// outgoing is an Envelope whose body is encoded with it.
type outgoing struct {
	ID   uint64 `json:"id"`
	Re   uint64 `json:"re,omitempty"`
	Type Type   `json:"type"`
	Body any    `json:"body"`
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
	// Period is the engine's current idle period (Open), and Settled is set
	// when the engine connects already settled for it: the service then asks
	// it for nothing until an Open with a later period.
	Period  uint64 `json:"period"`
	Settled bool   `json:"settled,omitempty"`
	// Restored are the set files (Restore) this engine process merged
	// whole, by digest: the service doesn't offer them again.
	Restored []digest.Digest `json:"restored,omitempty"`
}

// Welcome is the service's reply to Hello.
type Welcome struct{}

// Open tells the service that a session started on an engine with no live
// session: its idle periods are numbered from 1, and Period is the one that
// begins when the sessions now starting have ended. The service may ask the
// engine for exports again, and a settle for an earlier period is stale.
type Open struct {
	Period uint64 `json:"period"`
}

// Opened is the service's reply to Open.
type Opened struct{}

// Settle asks the service, once the engine has no live session, to finish
// what it wants of the engine for Period: Sessions are the sessions that
// ended and that no Settled has covered, kept across canceled requests and
// reconnections.
type Settle struct {
	Period   uint64          `json:"period"`
	Sessions []SettleSession `json:"sessions"`
}

// SettleSession is one ended session: ID is the engine session's ID, as its
// telemetry names it (dagger.io/engine.session), and Marker is set when the
// engine sent the session's completion marker, the span the service reads as
// its end row. A session without one has no end row to wait for.
type SettleSession struct {
	ID     string `json:"id"`
	Marker bool   `json:"marker"`
}

// Claims tells the service which Dagger Cloud claim each of the engine's
// sessions started under, which the sessions' telemetry doesn't carry. The
// engine sends each session with a claim
// once on a connection, as it starts or, for a session live at hello or
// ended and not yet settled, after it; always before the settle that lists
// the session. A session with no claim isn't sent.
type Claims struct {
	Sessions []SessionClaim `json:"sessions"`
}

// SessionClaim is one session and its claim: ID is the engine session's ID,
// as its telemetry names it (dagger.io/engine.session), Claim is the claim
// ID its client connected with, and Started is when the session started,
// on the engine's clock, the clock its spans' times are on: the service
// counts how far into the claim's run each of the run's calls came from its
// first session's start.
type SessionClaim struct {
	ID      string    `json:"id"`
	Claim   string    `json:"claim"`
	Started time.Time `json:"started"`
}

// Claimed is the service's reply to Claims.
type Claimed struct{}

// Settled is the service's reply to Settle, sent once it is done with every
// listed session and has no export of the engine in flight or still to ask
// for. The service then asks
// the engine for nothing until an Open with a later period. A settle for a
// period that is not the engine's current one ends with StaleSettle instead.
type Settled struct{}

// Export asks the engine for the values of some of its entries, by number,
// and the selected parts' bytes. Its bundle sends each row the service merged
// over this connection, and that hasn't changed since, as a held row
// (Upload.Merged). In an export that selects parts, a held row keeps its
// links to the rows below it, and a part can sit on it. The Cloud's merge
// refuses the whole bundle as stale if any held row's recipe has no live
// entry there, or that entry lacks a part the export puts on the held row,
// and commits nothing; the service then sends Upload without Merged, and
// asks for the roots again.
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
// connection ends. The bundle's records carry no offers of parts: an
// engine's offers are the Cloud's own, and its parts go as the bundle's
// outputs.
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
// an export, and one per pack of small blobs it needs stored together. An
// upload with neither needs nothing, and the engine releases the chains.
//
// Merged says the service merged the export's bundle into the Cloud. The
// engine then knows the rows the bundle carried in full, for this
// connection's later exports. An Upload without Merged, for a merge that
// failed, was refused as stale or was skipped, makes the rows the bundle held
// unknown again, so that the next export of the same roots carries them in
// full. It holds nothing that export carried or held; it can hold rows below
// them that earlier merges proved.
//
// Stale, on an Upload without Merged for a merge refused as stale, names by
// the engine's numbers the held rows the Cloud found stale: only those become
// unknown, and the bundle's other held rows stay known. The Cloud names every
// held row stale by one check of the merge; one stale by another check is
// named by the next export's refusal, so each refusal makes at least one row
// unknown until the export merges.
type Upload struct {
	ExportID string                   `json:"exportID"`
	URLs     map[digest.Digest]string `json:"urls"`
	Packs    []UploadPack             `json:"packs,omitempty"`
	Merged   bool                     `json:"merged,omitempty"`
	Stale    []uint64                 `json:"stale,omitempty"`
}

// UploadPack is one pack to upload: its ID, its presigned PUT, and its
// members in order. The engine PUTs the pack's header, then each member.
type UploadPack struct {
	Pack    digest.Digest `json:"pack"`
	URL     string        `json:"url"`
	Members []pack.Member `json:"members"`
}

// Uploaded answers Upload. Done and Failed name blobs and packs; a pack is
// done or failed whole.
type Uploaded struct {
	Done   []digest.Digest `json:"done"`
	Failed []UploadFailure `json:"failed,omitempty"`
}

// UploadFailure is one blob or pack the engine could not upload.
type UploadFailure struct {
	Digest  digest.Digest `json:"digest"`
	Message string        `json:"message"`
}

// Merge gives the engine a bundle of the Cloud's values. Its SenderNumbers
// are the Cloud's numbers, and its outputs carry GET addresses. A held row
// names, by recipe, a value the engine is expected to hold; the engine
// resolves it as it would keep a record of that recipe. The rows a committed
// merge lands on are the Cloud's records, so the engine's later exports over
// the connection send them as held rows, as they do the rows of an export
// the service merged (Upload.Merged).
type Merge struct {
	Bundle dagql.ValueBundle `json:"bundle"`
	// Lookup is the ID of the engine's Lookup this merge answers, or 0 for
	// a merge of the service's own placement. The engine commits a merge
	// that answers a lookup ahead of the others.
	Lookup uint64 `json:"lookup"`
}

// MarshalJSON encodes a merge with its bundle's layers and addresses listed
// once (dagql.ShareChainLayers): a merge of a large closure would otherwise
// list each shared base layer, with its address, once for every output on
// it, which put merges over MaxMessageBytes. The engine's merge gives each
// output its chain back (dagql.ResolveChainPrefixes).
func (m Merge) MarshalJSON() ([]byte, error) {
	bundle, err := dagql.ShareChainLayers(m.Bundle)
	if err != nil {
		return nil, err
	}
	type merge Merge
	return json.Marshal(merge{Bundle: bundle, Lookup: m.Lookup})
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
	Stale []dagql.TransferOrdinal `json:"stale"`
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
	// Packs maps each of the layers stored in a pack to its pack's ID, as
	// the chain's offer carried them.
	Packs map[digest.Digest]digest.Digest `json:"packs,omitempty"`
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
}

// LookupMiss is one call the engine's cache missed: its recipe digest, its
// extra digests, which the engine's own lookup tries after the recipe, and
// its term, which that lookup tries last: the call's self digest over its
// input digests, matched by the inputs' classes. Field names the call, for
// logs. Caller is the recipe digest of the cached call the missed call was
// made under (dagger.io/cache.caller), or empty at its session's top.
type LookupMiss struct {
	Recipe digest.Digest   `json:"recipe"`
	Extra  []digest.Digest `json:"extra,omitempty"`
	Field  string          `json:"field,omitempty"`
	Self   digest.Digest   `json:"self,omitempty"`
	Inputs []digest.Digest `json:"inputs,omitempty"`
	Caller digest.Digest   `json:"caller,omitempty"`
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

// Restore asks the engine to fetch published files of its claims' sets and
// merge their parts (SetFile), each through the path a Merge takes. The
// engine merges one file at a time, each file's parts in the file's order:
// a head before any whole it hasn't started, and otherwise in the order the
// restores arrived, so a set's head, sent first, is merged before its whole. A restore can come at any time after Welcome. Nothing in
// a restore fails it: a file that can't be fetched, read or merged is
// reported in Restored, and leaves the engine as it was without it, but for
// the parts that merged.
type Restore struct {
	Sets []RestoreSet `json:"sets"`
}

// RestoreSet is one claim's set: its head, its whole, or both. Check names
// the set's check, for logs only.
type RestoreSet struct {
	Check string   `json:"check"`
	Head  *SetFile `json:"head,omitempty"`
	Whole *SetFile `json:"whole,omitempty"`
}

// SetFile is one published file of a set: the digest and size of its bytes,
// which name it in Restored and Hello, its download address, and the
// download addresses of the blobs and packs its parts name (AddressTable).
type SetFile struct {
	Digest  digest.Digest     `json:"digest"`
	Size    int64             `json:"size"`
	Address dagql.BlobAddress `json:"address"`
	Table   AddressTable      `json:"table"`
}

// Restored answers Restore once every file it named is done, one item per
// file, as the restore named them, heads first. Generation and EngineTime are
// the engine cache's start count and clock when it answers, as Merged has
// them.
type Restored struct {
	Generation uint64         `json:"generation"`
	EngineTime int64          `json:"engineTime"`
	Files      []RestoredFile `json:"files"`
}

// RestoredFile is what the engine did with one file: Complete when every
// part merged, Already when this engine process had merged it whole and did
// nothing. Parts counts the parts that merged, Records the records they
// carried, and Rows what their merges did with them. FirstCommit is the
// engine's clock, in Unix nanoseconds, when its first part committed, and
// Error is what stopped it, if anything did.
type RestoredFile struct {
	Digest      digest.Digest `json:"digest"`
	Complete    bool          `json:"complete,omitempty"`
	Already     bool          `json:"already,omitempty"`
	Parts       int           `json:"parts"`
	Records     int           `json:"records"`
	Rows        RowCounts     `json:"rows"`
	FirstCommit int64         `json:"firstCommit,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// RowCounts counts merged records by what their merge did with each
// (dagql.MergeRowCounts).
type RowCounts struct {
	Held     int `json:"held,omitempty"`
	Kept     int `json:"kept,omitempty"`
	Created  int `json:"created,omitempty"`
	Stored   int `json:"stored,omitempty"`
	Replaced int `json:"replaced,omitempty"`
	Retired  int `json:"retired,omitempty"`
}

// AddressTable is a set file's download addresses: one for each blob and
// pack its parts' outputs name, by the layer's digest or the pack's ID. A
// set file carries none of its own, so that only the table is signed again
// as addresses expire. One the store couldn't sign is left out: a read of
// its layer renews it, by its chain's renewal key.
type AddressTable struct {
	Blobs map[digest.Digest]dagql.BlobAddress `json:"blobs,omitempty"`
	Packs map[digest.Digest]dagql.BlobAddress `json:"packs,omitempty"`
}
