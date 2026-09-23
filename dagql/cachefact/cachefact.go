// Package cachefact defines the cache facts one engine's dagql cache emits as
// OpenTelemetry log records, and their JSON encoding.
//
// A fact is one bookkeeping event of one engine's cache: a result registered,
// digests taught, a dependency set attached, a retention edge changed, a part
// completed, results removed, and the engine's own start, liveness and stop.
// Facts name results by result key: the engine instance (a resource attribute
// of the record) plus the engine-local result number. They carry digests,
// never engine-local equivalence-class or term numbers.
//
// Every fact of one engine instance carries a dense sequence number, assigned
// in the order the cache applied the mutations the facts describe.
//
// The package depends only on the standard library so that consumers outside
// the engine can decode facts without the engine's dependencies.
package cachefact

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the fact format version, carried in AttrVersion.
const Version = "1"

// Wire names of the OTel log record that carries one fact. Attribute values
// are strings: Cloud ingestion JSON-encodes attribute values into a string map
// (see the cache-evidence contract comment in engine/telemetryattrs).
const (
	// ScopeName is the instrumentation scope of fact records.
	ScopeName = "dagger.io/cache"

	// AttrKind is the record attribute holding the fact's Kind.
	AttrKind = "dagger.io/cache.fact"
	// AttrSeq is the record attribute holding the fact's sequence number as a
	// decimal string.
	AttrSeq = "dagger.io/cache.fact.seq"
	// AttrVersion is the record attribute holding Version.
	AttrVersion = "dagger.io/cache.fact.version"

	// ResourceEngineInstance is the resource attribute naming the engine
	// instance: one random ID per engine process.
	ResourceEngineInstance = "dagger.io/engine.instance"
)

// Kind names the kind of a fact.
type Kind string

const (
	KindEngineStart Kind = "engine.start"
	KindEngineAlive Kind = "engine.alive"
	KindEngineStop  Kind = "engine.stop"
	KindResult      Kind = "result"
	KindClass       Kind = "class"
	KindTerm        Kind = "term"
	KindDeps        Kind = "deps"
	KindIdentity    Kind = "identity"
	KindRetention   Kind = "retention"
	KindPart        Kind = "part"
	KindRemoved     Kind = "removed"
)

// Body is the kind-specific content of a fact.
type Body interface {
	FactKind() Kind
}

// Fact is one cache fact: its sequence number and its body. Body holds a
// value of one of this package's body types, never a pointer; decoding
// produces values.
//
// Its JSON form is one flat object: "seq" and "kind" followed by the body's
// fields.
type Fact struct {
	Seq  uint64
	Body Body
}

// Kind returns the kind of the fact's body, or "" for a fact without a body.
func (f Fact) Kind() Kind {
	if f.Body == nil {
		return ""
	}
	return f.Body.FactKind()
}

// ErrUnknownKind is returned when decoding a fact of a kind this package does
// not define.
var ErrUnknownKind = errors.New("unknown cache fact kind")

type factHeader struct {
	Seq  uint64 `json:"seq"`
	Kind Kind   `json:"kind"`
}

func (f Fact) MarshalJSON() ([]byte, error) {
	if f.Body == nil {
		return nil, errors.New("marshal cache fact: nil body")
	}
	header, err := json.Marshal(factHeader{Seq: f.Seq, Kind: f.Body.FactKind()})
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(f.Body)
	if err != nil {
		return nil, fmt.Errorf("marshal cache fact %s: %w", f.Body.FactKind(), err)
	}
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return nil, fmt.Errorf("marshal cache fact %s: body is not a JSON object", f.Body.FactKind())
	}
	// Splice the body's fields after the header's: {"seq":..,"kind":..,<body>}.
	out := make([]byte, 0, len(header)+len(body))
	out = append(out, header[:len(header)-1]...)
	if len(body) > 2 {
		out = append(out, ',')
		out = append(out, body[1:]...)
	} else {
		out = append(out, '}')
	}
	return out, nil
}

func (f *Fact) UnmarshalJSON(data []byte) error {
	var header factHeader
	if err := json.Unmarshal(data, &header); err != nil {
		return fmt.Errorf("unmarshal cache fact: %w", err)
	}
	body, err := decodeBody(header.Kind, data)
	if err != nil {
		return err
	}
	f.Seq = header.Seq
	f.Body = body
	return nil
}

// Decode decodes one fact from its JSON form.
func Decode(data []byte) (Fact, error) {
	var f Fact
	err := json.Unmarshal(data, &f)
	return f, err
}

func decodeBody(kind Kind, data []byte) (Body, error) {
	switch kind {
	case KindEngineStart:
		return decodeAs[EngineStart](kind, data)
	case KindEngineAlive:
		return decodeAs[EngineAlive](kind, data)
	case KindEngineStop:
		return decodeAs[EngineStop](kind, data)
	case KindResult:
		return decodeAs[Result](kind, data)
	case KindClass:
		return decodeAs[Class](kind, data)
	case KindTerm:
		return decodeAs[TermFact](kind, data)
	case KindDeps:
		return decodeAs[Deps](kind, data)
	case KindIdentity:
		return decodeAs[Identity](kind, data)
	case KindRetention:
		return decodeAs[Retention](kind, data)
	case KindPart:
		return decodeAs[Part](kind, data)
	case KindRemoved:
		return decodeAs[Removed](kind, data)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
}

func decodeAs[T Body](kind Kind, data []byte) (Body, error) {
	var body T
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("unmarshal cache fact %s: %w", kind, err)
	}
	return body, nil
}

// Digest is one digest with its label.
//
// For a digest posting the label is LabelRecipe for a call's recipe digest,
// and the extra digest's own label otherwise (for example "content" or
// "remote-cache"). An extra digest recorded without a label has an empty
// label.
type Digest struct {
	Digest string `json:"digest"`
	Label  string `json:"label"`
}

// LabelRecipe labels the recipe digest of a call frame.
const LabelRecipe = "recipe"

// Provenance is how a term input was known when the term was associated: as
// a result, or as a digest only.
type Provenance string

const (
	ProvenanceResult Provenance = "result"
	ProvenanceDigest Provenance = "digest"
)

// TermInput is one input of a term: a digest of the input's equivalence class
// and the input's provenance.
type TermInput struct {
	Digest     string     `json:"digest"`
	Provenance Provenance `json:"provenance"`
}

// Term is an operation's self digest over its ordered structural inputs.
type Term struct {
	Self   string      `json:"self"`
	Inputs []TermInput `json:"inputs"`
}

// Origin is how a result entered the engine's cache.
type Origin string

const (
	// OriginComputed is a result the engine computed or attached itself.
	OriginComputed Origin = "computed"
	// OriginImported is a result imported from a value bundle.
	OriginImported Origin = "imported"
	// OriginRestored is a result restored from the engine's persisted cache
	// at boot.
	OriginRestored Origin = "restored"
)

// Boot says whether an engine started with an empty cache or restored one.
type Boot string

const (
	BootFresh    Boot = "fresh"
	BootRestored Boot = "restored"
)

// EngineStart is emitted once per engine process, after the cache's boot
// restore finished.
type EngineStart struct {
	EngineVersion string `json:"engineVersion"`
	EngineName    string `json:"engineName"`
	Boot          Boot   `json:"boot"`
	// RestoredResults is the number of results the boot restore installed.
	RestoredResults int `json:"restoredResults"`
	// CloudEngineID is the Dagger Cloud engine ID, when the environment names
	// one.
	CloudEngineID string `json:"cloudEngineId,omitempty"`
}

func (EngineStart) FactKind() Kind { return KindEngineStart }

// EngineAlive is emitted periodically while the engine runs.
type EngineAlive struct {
	// DroppedFacts counts facts the engine dropped since it started. A
	// non-zero count means the fact stream of this instance is incomplete.
	DroppedFacts uint64 `json:"droppedFacts"`
}

func (EngineAlive) FactKind() Kind { return KindEngineAlive }

// EngineStop is emitted on graceful shutdown, after the cache persisted its
// state.
type EngineStop struct {
	PersistedResults int  `json:"persistedResults"`
	Clean            bool `json:"clean"`
}

func (EngineStop) FactKind() Kind { return KindEngineStop }

// Result announces a result registered in the cache under a new result number.
//
// For OriginComputed and OriginImported, Digests are the result's exact digest
// postings and Terms the terms the result is associated with. For
// OriginRestored, Digests hold one representative digest per output
// equivalence class and Terms is empty; the classes and terms themselves are
// announced by the Class and TermFact facts that precede the restored results.
//
// Deps and the retention fields are set for OriginImported and OriginRestored
// only. A computed result's dependencies and retention follow in their own Deps
// and Retention facts.
type Result struct {
	ID         uint64   `json:"id"`
	Origin     Origin   `json:"origin"`
	Field      string   `json:"field"`
	TypeName   string   `json:"typeName"`
	RecordType string   `json:"recordType"`
	Digests    []Digest `json:"digests"`
	Terms      []Term   `json:"terms"`

	CreatedAtUnixNano int64 `json:"createdAtUnixNano"`
	// ExpiresAtUnix is the result's own expiry (0: none). It bounds when the
	// result can serve as a cache hit.
	ExpiresAtUnix int64 `json:"expiresAtUnix"`

	Deps     []uint64 `json:"deps,omitempty"`
	Retained bool     `json:"retained,omitempty"`
	// RetentionExpiresAtUnix is the retention edge's expiry (0: none).
	RetentionExpiresAtUnix int64 `json:"retentionExpiresAtUnix,omitempty"`
	Unpruneable            bool  `json:"unpruneable,omitempty"`
}

func (Result) FactKind() Kind { return KindResult }

// Class announces one equivalence class restored at boot, with every digest
// of the class.
type Class struct {
	Digests []Digest `json:"digests"`
}

func (Class) FactKind() Kind { return KindClass }

// TermFact announces one term restored at boot. Inputs and Output name
// equivalence classes by one representative digest each: the smallest digest
// of the class. It is sent after every Class fact of the same boot.
type TermFact struct {
	Self   string      `json:"self"`
	Inputs []TermInput `json:"inputs"`
	Output string      `json:"output"`
}

func (TermFact) FactKind() Kind { return KindTerm }

// Deps gives a computed result's complete dependency set. A later Deps fact
// for the same result replaces the earlier set.
type Deps struct {
	ID       uint64   `json:"id"`
	Deps     []uint64 `json:"deps"`
	Complete bool     `json:"complete"`
}

func (Deps) FactKind() Kind { return KindDeps }

// TermUse says how a teach used its term.
type TermUse string

const (
	// TermUseReused: the result was already associated with the term.
	TermUseReused TermUse = "reused"
	// TermUseAssociated: the result was newly associated with an existing
	// congruent term.
	TermUseAssociated TermUse = "associated"
	// TermUseCreated: the teach created the term.
	TermUseCreated TermUse = "created"
)

// Identity records digests and a term taught onto an existing result, for
// example on a cache hit or when a content digest is learned.
type Identity struct {
	ID uint64 `json:"id"`
	// Digests are the digest postings this teach added to the result. It can
	// be empty when the teach merged classes without adding a posting.
	Digests []Digest `json:"digests"`
	// Term is the term the teach used; TermUse says how.
	Term    Term    `json:"term"`
	TermUse TermUse `json:"termUse"`
	// ExpiresAtUnix is the result's own expiry after the teach (0: none).
	ExpiresAtUnix int64 `json:"expiresAtUnix"`
}

func (Identity) FactKind() Kind { return KindIdentity }

// Retention records a result's retention edge: the record that keeps the
// result alive after its session. Retained false means the edge was dropped.
// An unpruneable edge never expires, and making an edge unpruneable also
// clears the result's own expiry.
type Retention struct {
	ID       uint64 `json:"id"`
	Retained bool   `json:"retained"`
	// ExpiresAtUnix is the edge's expiry (0: none), when Retained.
	ExpiresAtUnix int64 `json:"expiresAtUnix,omitempty"`
	Unpruneable   bool  `json:"unpruneable,omitempty"`
}

func (Retention) FactKind() Kind { return KindRetention }

// PartStateCompleted means a part's bytes are final on the engine.
const PartStateCompleted = "completed"

// Part records a state change of one filesystem part of a result.
type Part struct {
	ID uint64 `json:"id"`
	// OutputPath is the path of the output inside the result's value, empty
	// for the value itself.
	OutputPath string `json:"outputPath"`
	Part       string `json:"part"`
	State      string `json:"state"`
}

func (Part) FactKind() Kind { return KindPart }

// RemovedReason says why results left the cache.
type RemovedReason string

const (
	// RemovedSessionRelease: a released session owned the last reference.
	RemovedSessionRelease RemovedReason = "session_release"
	// RemovedPrune: a prune dropped the retention edge that owned the last
	// reference.
	RemovedPrune RemovedReason = "prune"
	// RemovedRollback: publication of a freshly registered result failed.
	RemovedRollback RemovedReason = "rollback"
	// RemovedReleased: any other release of the last reference, for example
	// the end of a call's publication hold or of an export's hold.
	RemovedReleased RemovedReason = "released"
)

// Removed lists results that left the cache in one critical section.
type Removed struct {
	IDs    []uint64      `json:"ids"`
	Reason RemovedReason `json:"reason"`
}

func (Removed) FactKind() Kind { return KindRemoved }
