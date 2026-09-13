package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dagger/dagger/dagql/call"
)

// PersistEncodeContext is the explicit context handed to every persisted
// object, list and lazy codec while a row is being encoded. It names the row
// being encoded (its ID and recorded call) and supplies the only two reference
// operations a codec may use: ResultRef, which turns an attached result into
// its exact local row ID, and CallID, which preserves a typed call ID in its
// original handle or recipe form. Neither operation establishes value
// equality; both only describe references the row already owns.
type PersistEncodeContext struct {
	cache     PersistedObjectCache
	resultID  uint64
	call      *ResultCall
	path      PersistedRefPath
	quiescent bool
}

type quiescentPersistKey struct{}

// NewPersistEncodeContext returns an encode context for one owner row. cache
// may be nil for detached encoding, in which case every ResultRef fails: a
// detached codec cannot describe row references.
func NewPersistEncodeContext(cache PersistedObjectCache, resultID uint64, call *ResultCall) *PersistEncodeContext {
	return &PersistEncodeContext{cache: cache, resultID: resultID, call: call}
}

// Quiescent reports whether the shutdown persister is encoding after cache
// operations drained. Codecs may wait for transient read-only latch holders
// in this case. Live capture and detached encoding must not wait for bodies.
func (enc *PersistEncodeContext) Quiescent() bool {
	return enc != nil && enc.quiescent
}

// ResultID is the row being encoded, or zero for a detached (inline) value.
func (enc *PersistEncodeContext) ResultID() uint64 {
	if enc == nil {
		return 0
	}
	return enc.resultID
}

// Call is the recorded call of the row being encoded, if known.
func (enc *PersistEncodeContext) Call() *ResultCall {
	if enc == nil {
		return nil
	}
	return enc.call
}

// Cache exposes the row-identity lookup used by this context.
func (enc *PersistEncodeContext) Cache() PersistedObjectCache {
	if enc == nil {
		return nil
	}
	return enc.cache
}

// ResultRef returns the attached local row ID of res. A result that is not
// cache-backed cannot be referenced and is an error: codecs record references
// only to rows that attachment already owns.
func (enc *PersistEncodeContext) ResultRef(res AnyResult) (uint64, error) {
	if enc == nil || enc.cache == nil {
		return 0, fmt.Errorf("persist encode result ref: nil cache")
	}
	if res == nil {
		return 0, fmt.Errorf("persist encode result ref: nil result")
	}
	return enc.cache.PersistedResultID(res)
}

// CallID preserves a typed call ID in its tagged form. Handle-form IDs keep
// their local row identity and exact recursive type; recipe-form IDs keep their
// call description. Nothing is executed or loaded.
func (enc *PersistEncodeContext) CallID(id *call.ID) (string, error) {
	_ = enc
	if id == nil {
		return "", fmt.Errorf("persist encode call ID: nil ID")
	}
	return id.Encode()
}

// SnapshotRole records a declared storage role for the owner row. It only
// validates the declaration; storage is never opened.
func (enc *PersistEncodeContext) SnapshotRole(role, refKey string) (PersistedSnapshotRefLink, error) {
	_ = enc
	if role == "" {
		return PersistedSnapshotRefLink{}, fmt.Errorf("persist encode snapshot role: empty role")
	}
	if refKey == "" {
		return PersistedSnapshotRefLink{}, fmt.Errorf("persist encode snapshot role %q: empty snapshot identity", role)
	}
	return PersistedSnapshotRefLink{RefKey: refKey, Role: role}, nil
}

// item returns the context for an inline list item of this row.
func (enc *PersistEncodeContext) item(itemCall *ResultCall, index int) *PersistEncodeContext {
	return &PersistEncodeContext{
		cache:     enc.Cache(),
		call:      itemCall,
		path:      enc.path.Field("items").Index(index),
		quiescent: enc.Quiescent(),
	}
}

// PersistDecodeContext is the explicit context handed to every persisted
// object, list and lazy decoder. It names the row being decoded, its recorded
// call and the defining server, and supplies exact reference loads.
type PersistDecodeContext struct {
	server   *Server
	resultID uint64
	call     *ResultCall
}

// NewPersistDecodeContext returns a decode context for one owner row. dag is
// the defining server; it may be nil only when the payload declares no
// references and needs no schema.
func NewPersistDecodeContext(dag *Server, resultID uint64, call *ResultCall) *PersistDecodeContext {
	return &PersistDecodeContext{server: dag, resultID: resultID, call: call}
}

// Server is the defining server for the row being decoded.
func (dec *PersistDecodeContext) Server() *Server {
	if dec == nil {
		return nil
	}
	return dec.server
}

// ResultID is the row being decoded, or zero for an inline value.
func (dec *PersistDecodeContext) ResultID() uint64 {
	if dec == nil {
		return 0
	}
	return dec.resultID
}

// Call is the recorded call of the row being decoded.
func (dec *PersistDecodeContext) Call() *ResultCall {
	if dec == nil {
		return nil
	}
	return dec.call
}

// ResultRef loads the exact local row named by resultID. Zero is explicit
// absence and returns a nil result. The load never substitutes a
// session-selected equivalent; the outer client load still enforces
// transitive resource requirements before serving the owner.
//
// The context borrows the lifetime of the decode that created it: that
// decode already runs inside an admitted cache operation (the owner's load or
// import), so child loads use the internal loader and never begin a new
// operation. A close that started after the owner was admitted therefore
// waits for the child load instead of failing it with ErrCacheClosed, exactly
// as the previous internal object loader behaved; external loads that begin
// after close remain rejected.
func (dec *PersistDecodeContext) ResultRef(ctx context.Context, resultID uint64) (AnyResult, error) {
	if resultID == 0 {
		return nil, nil
	}
	if dec == nil || dec.server == nil {
		return nil, fmt.Errorf("persist decode result ref %d: referenced result requires a dagql server", resultID)
	}
	cache, err := EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("persist decode result ref %d: %w", resultID, err)
	}
	return cache.loadResultByResultID(ctx, "", dec.server, resultID)
}

// CallID restores a typed call ID from its tagged form. The empty string is
// explicit absence.
func (dec *PersistDecodeContext) CallID(raw string) (*call.ID, error) {
	_ = dec
	if raw == "" {
		return nil, nil
	}
	var id call.ID
	if err := id.Decode(raw); err != nil {
		return nil, fmt.Errorf("persist decode call ID: %w", err)
	}
	return &id, nil
}

// SnapshotRoles returns the declared storage roles recorded for the owner row.
func (dec *PersistDecodeContext) SnapshotRoles(ctx context.Context) ([]PersistedSnapshotRefLink, error) {
	if dec == nil || dec.resultID == 0 {
		return nil, fmt.Errorf("persist decode snapshot roles: zero result ID")
	}
	cache, err := EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("persist decode snapshot roles for result %d: %w", dec.resultID, err)
	}
	return cache.PersistedSnapshotLinksByResultID(ctx, dec.resultID)
}

// SnapshotRole resolves one declared storage role of the owner row. It only
// returns the recorded identity; storage is never opened here.
func (dec *PersistDecodeContext) SnapshotRole(ctx context.Context, role string) (PersistedSnapshotRefLink, error) {
	links, err := dec.SnapshotRoles(ctx)
	if err != nil {
		return PersistedSnapshotRefLink{}, err
	}
	for _, link := range links {
		if link.Role == role {
			return link, nil
		}
	}
	return PersistedSnapshotRefLink{}, fmt.Errorf("missing persisted snapshot link role %q for result %d", role, dec.ResultID())
}

// item returns the context for an inline list item of this row.
func (dec *PersistDecodeContext) item(itemCall *ResultCall) *PersistDecodeContext {
	return &PersistDecodeContext{server: dec.Server(), call: itemCall}
}

// DecodeLosslessJSON decodes exactly one JSON value, keeping numbers as
// json.Number so integers outside the float64 mantissa survive. Trailing
// content after the value is an error.
func DecodeLosslessJSON(raw []byte) (any, error) {
	var value any
	if err := UnmarshalLosslessJSON(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// UnmarshalLosslessJSON decodes exactly one JSON value into dst, keeping
// untyped numbers as json.Number.
func UnmarshalLosslessJSON(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing data after JSON value")
		}
		return fmt.Errorf("trailing data after JSON value: %w", err)
	}
	return nil
}

// PersistedRefKind classifies a declared reference position.
type PersistedRefKind string

const (
	// PersistedRefSelf is the envelope's own row identity. It is not an
	// ownership edge.
	PersistedRefSelf PersistedRefKind = "self"
	// PersistedRefChild is a required child row the owner depends on.
	PersistedRefChild PersistedRefKind = "child"
	// PersistedRefCall is a descriptive result reference inside a recorded
	// call frame. Nonzero refs keep their existing dependency ownership;
	// digest-only call descriptions add no row.
	PersistedRefCall PersistedRefKind = "call"
	// PersistedRefOutputRole is an immutable output storage role of the owner
	// row, described by a transferable snapshot identity.
	PersistedRefOutputRole PersistedRefKind = "output_role"
	// PersistedRefLocalBacking is a local mutable backing role of the owner
	// row. Its identity is engine-local configuration, never transferred.
	PersistedRefLocalBacking PersistedRefKind = "local_backing"
)

// PersistedRefPathElem is one declared position: a field name or a list
// index relative to the enclosing owner.
type PersistedRefPathElem struct {
	Field   string
	Index   int
	IsIndex bool
}

// PersistedRefPath is the declared position of a reference relative to its
// exact owner row, interpreted by codec family and envelope version.
type PersistedRefPath []PersistedRefPathElem

// Field appends a declared field position.
func (p PersistedRefPath) Field(name string) PersistedRefPath {
	cp := make(PersistedRefPath, len(p), len(p)+1)
	copy(cp, p)
	return append(cp, PersistedRefPathElem{Field: name})
}

// Index appends a declared list position.
func (p PersistedRefPath) Index(i int) PersistedRefPath {
	cp := make(PersistedRefPath, len(p), len(p)+1)
	copy(cp, p)
	return append(cp, PersistedRefPathElem{Index: i, IsIndex: true})
}

func (p PersistedRefPath) String() string {
	var sb strings.Builder
	for _, elem := range p {
		if elem.IsIndex {
			sb.WriteString("[" + strconv.Itoa(elem.Index) + "]")
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString(".")
		}
		sb.WriteString(elem.Field)
	}
	return sb.String()
}

// PersistedRef is one declared reference reported by VisitEncodedReferences.
// Row references carry ResultID; storage roles carry Role and RefKey. A
// visitor may replace ResultID or RefKey to relocate the reference; every other
// field is descriptive.
type PersistedRef struct {
	Kind     PersistedRefKind
	Path     PersistedRefPath
	ResultID uint64
	Role     string
	RefKey   string
}

// PersistedRefVisitor observes or rewrites one declared reference.
type PersistedRefVisitor func(ref *PersistedRef) error

// VisitPersistedRow reports a row reference at path and writes back a
// replacement. It is the shared helper for codec family visitors.
func VisitPersistedRow(visit PersistedRefVisitor, kind PersistedRefKind, path PersistedRefPath, id *uint64) (bool, error) {
	if id == nil || *id == 0 {
		return false, nil
	}
	ref := PersistedRef{Kind: kind, Path: path, ResultID: *id}
	if err := visit(&ref); err != nil {
		return false, err
	}
	if ref.ResultID == 0 {
		return false, fmt.Errorf("persisted reference %s: replaced row %d with zero", path, *id)
	}
	changed := ref.ResultID != *id
	*id = ref.ResultID
	return changed, nil
}

// VisitPersistedCallID reports the row reference inside a handle-form typed
// call ID and writes back a replacement with the same exact type. Recipe-form
// IDs are typed call descriptions with no row references and pass through
// untouched.
func VisitPersistedCallID(visit PersistedRefVisitor, kind PersistedRefKind, path PersistedRefPath, raw *string) (bool, error) {
	if raw == nil || *raw == "" {
		return false, nil
	}
	var id call.ID
	if err := id.Decode(*raw); err != nil {
		return false, fmt.Errorf("persisted reference %s: decode call ID: %w", path, err)
	}
	if !id.IsHandle() {
		return false, nil
	}
	resultID := id.EngineResultID()
	if resultID == 0 {
		return false, nil
	}
	changed, err := VisitPersistedRow(visit, kind, path, &resultID)
	if err != nil || !changed {
		return false, err
	}
	encoded, err := call.NewEngineResultID(resultID, id.Type()).Encode()
	if err != nil {
		return false, fmt.Errorf("persisted reference %s: encode call ID: %w", path, err)
	}
	*raw = encoded
	return true, nil
}

// VisitPersistedSnapshotRoles reports every declared storage role of the owner
// row with one classification and writes back replaced identities in place.
func VisitPersistedSnapshotRoles(visit PersistedRefVisitor, kind PersistedRefKind, path PersistedRefPath, links []PersistedSnapshotRefLink) error {
	for i := range links {
		ref := PersistedRef{Kind: kind, Path: path.Field("snapshotLinks").Index(i), Role: links[i].Role, RefKey: links[i].RefKey}
		if err := visit(&ref); err != nil {
			return err
		}
		if ref.RefKey == "" {
			return fmt.Errorf("persisted snapshot role %q: replaced identity with empty key", links[i].Role)
		}
		links[i].RefKey = ref.RefKey
	}
	return nil
}

// PersistedPayloadVisit is the input to a codec family's reference visitor:
// the envelope version, the owner's recorded call, the declared position of
// the payload, its bytes and the owner's storage links. SnapshotLinks may be
// rewritten in place through VisitPersistedSnapshotRoles.
type PersistedPayloadVisit struct {
	Version       int
	Call          *ResultCall
	Path          PersistedRefPath
	Payload       json.RawMessage
	SnapshotLinks []PersistedSnapshotRefLink
}

// PersistedPayloadVisitor walks the declared references of one codec family's
// payload without constructing the typed value. It returns the payload,
// rewritten only when a reference was replaced. It performs no provider,
// schema, filesystem or execution work.
type PersistedPayloadVisitor interface {
	VisitPersistedReferences(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error)
}

// PersistedNoReferences is the visitor for a family whose payload declares no
// references and no storage roles.
type PersistedNoReferences struct{}

func (PersistedNoReferences) VisitPersistedReferences(v PersistedPayloadVisit, _ PersistedRefVisitor) (json.RawMessage, error) {
	if len(v.SnapshotLinks) > 0 {
		return nil, fmt.Errorf("persisted payload at %q declares no references but has %d snapshot links", v.Path, len(v.SnapshotLinks))
	}
	return v.Payload, nil
}

// PersistedObjectFamily identifies one Go payload family: the codec that
// produced an object envelope's bytes. TypeName identifies the GraphQL value;
// the family identifies how its bytes are read.
type PersistedObjectFamily struct {
	// Name is the stable family identifier written into envelopes.
	Name string
	// Typed is a zero value of the Go type whose codec produces this family.
	Typed Typed
	// Visitor walks the family's declared references.
	Visitor PersistedPayloadVisitor
}

var (
	persistedFamiliesMu     sync.RWMutex
	persistedFamiliesByName = map[string]PersistedObjectFamily{}
	persistedFamiliesByType = map[reflect.Type]PersistedObjectFamily{}
)

// RegisterPersistedObjectFamily registers a codec family at initialization.
// Registration is independent of schema installation: user schemas add
// GraphQL types, never payload families. Duplicate names or Go types panic.
func RegisterPersistedObjectFamily(family PersistedObjectFamily) {
	if family.Name == "" {
		panic("persisted object family: empty name")
	}
	if family.Typed == nil {
		panic(fmt.Sprintf("persisted object family %q: nil typed value", family.Name))
	}
	if family.Visitor == nil {
		panic(fmt.Sprintf("persisted object family %q: nil visitor", family.Name))
	}
	goType := reflect.TypeOf(family.Typed)
	persistedFamiliesMu.Lock()
	defer persistedFamiliesMu.Unlock()
	if _, dup := persistedFamiliesByName[family.Name]; dup {
		panic(fmt.Sprintf("persisted object family %q registered twice", family.Name))
	}
	if existing, dup := persistedFamiliesByType[goType]; dup {
		panic(fmt.Sprintf("persisted object family %q: Go type %s already registered as %q", family.Name, goType, existing.Name))
	}
	persistedFamiliesByName[family.Name] = family
	persistedFamiliesByType[goType] = family
}

// PersistedObjectFamilyFor returns the family registered for the Go type of
// self.
func PersistedObjectFamilyFor(self Typed) (PersistedObjectFamily, bool) {
	if self == nil {
		return PersistedObjectFamily{}, false
	}
	persistedFamiliesMu.RLock()
	defer persistedFamiliesMu.RUnlock()
	family, ok := persistedFamiliesByType[reflect.TypeOf(self)]
	return family, ok
}

// PersistedObjectFamilyByName returns the registered family with that name.
func PersistedObjectFamilyByName(name string) (PersistedObjectFamily, bool) {
	persistedFamiliesMu.RLock()
	defer persistedFamiliesMu.RUnlock()
	family, ok := persistedFamiliesByName[name]
	return family, ok
}

// PersistedObjectFamilies lists every registered family, sorted by name.
func PersistedObjectFamilies() []PersistedObjectFamily {
	persistedFamiliesMu.RLock()
	defer persistedFamiliesMu.RUnlock()
	families := make([]PersistedObjectFamily, 0, len(persistedFamiliesByName))
	for _, family := range persistedFamiliesByName {
		families = append(families, family)
	}
	sort.Slice(families, func(i, j int) bool { return families[i].Name < families[j].Name })
	return families
}

// PersistedRecord is one persisted row as the reference visitor sees it: its
// identity, generic envelope, recorded call and declared storage links.
type PersistedRecord struct {
	ResultID      uint64
	Envelope      PersistedResultEnvelope
	Call          *ResultCall
	SnapshotLinks []PersistedSnapshotRefLink
}

// VisitEncodedReferences walks every declared reference of one persisted row:
// its own identity, list item rows, object payload references through the
// registered family visitor, recorded call references, and storage roles. The
// visitor may replace row identities and storage keys; the returned record
// carries the rewritten forms and the input is left untouched. Shared call
// subgraphs stay shared. No schema, provider, storage or typed construction is
// involved.
func VisitEncodedReferences(rec PersistedRecord, visit PersistedRefVisitor) (PersistedRecord, error) {
	if visit == nil {
		return PersistedRecord{}, fmt.Errorf("visit encoded references: nil visitor")
	}
	out := PersistedRecord{
		ResultID:      rec.ResultID,
		SnapshotLinks: slices.Clone(rec.SnapshotLinks),
	}
	if rec.Envelope.ResultID != 0 && rec.Envelope.ResultID != rec.ResultID {
		return PersistedRecord{}, fmt.Errorf("visit encoded references: envelope names row %d but record is row %d", rec.Envelope.ResultID, rec.ResultID)
	}
	if rec.ResultID != 0 {
		if _, err := VisitPersistedRow(visit, PersistedRefSelf, nil, &out.ResultID); err != nil {
			return PersistedRecord{}, err
		}
	}
	env, err := visitPersistedEnvelope(rec.Envelope, rec.Call, out.SnapshotLinks, nil, true, visit)
	if err != nil {
		return PersistedRecord{}, err
	}
	if env.ResultID != 0 {
		// The root envelope repeats the owner identity already visited above.
		env.ResultID = out.ResultID
	}
	out.Envelope = env
	if rec.Call != nil {
		out.Call = rec.Call.clone()
		if err := visitResultCallReferences(out.Call, nil, visit); err != nil {
			return PersistedRecord{}, err
		}
	}
	return out, nil
}

func visitPersistedEnvelope(env PersistedResultEnvelope, ownerCall *ResultCall, links []PersistedSnapshotRefLink, path PersistedRefPath, root bool, visit PersistedRefVisitor) (PersistedResultEnvelope, error) {
	if env.Version != persistedResultEnvelopeVersion {
		return PersistedResultEnvelope{}, fmt.Errorf("visit persisted envelope at %q: unsupported version %d", path, env.Version)
	}
	if root && env.Kind == persistedResultKindRef {
		return PersistedResultEnvelope{}, fmt.Errorf("visit persisted envelope: root envelope cannot be a result reference")
	}
	// Only an object payload's family visitor classifies storage roles, so a
	// null, scalar or list root that carries links would leave their keys
	// unclassified and unrelocated with no report. Capture cannot produce
	// that pairing today, but this contract is driven from stored records as
	// well as from live values, so reject it rather than skip it silently.
	if root && env.Kind != persistedResultKindObject && len(links) > 0 {
		return PersistedResultEnvelope{}, fmt.Errorf("visit persisted envelope at %q: %s root declares %d storage role(s); only an object payload classifies storage roles", path, env.Kind, len(links))
	}
	switch env.Kind {
	case persistedResultKindNull, persistedResultKindScalar:
		if len(env.Items) != 0 || len(env.ObjectJSON) != 0 {
			return PersistedResultEnvelope{}, fmt.Errorf("visit persisted envelope at %q: %s kind carries a body", path, env.Kind)
		}
		return env, nil
	case persistedResultKindRef:
		if root {
			return PersistedResultEnvelope{}, fmt.Errorf("visit persisted envelope: root envelope cannot be a result reference")
		}
		if _, err := VisitPersistedRow(visit, PersistedRefChild, path, &env.ResultID); err != nil {
			return PersistedResultEnvelope{}, err
		}
		return env, nil
	case persistedResultKindObject:
		family, ok := PersistedObjectFamilyByName(env.ObjectCodec)
		if !ok {
			return PersistedResultEnvelope{}, fmt.Errorf("visit persisted envelope at %q: unknown object codec family %q for type %q", path, env.ObjectCodec, env.TypeName)
		}
		payloadLinks := links
		if !root {
			payloadLinks = nil
		}
		// Every storage role the owner declares must be classified by its
		// family exactly once: an unreported role would keep an unrelocated
		// key silently, and a duplicate report could hide an omitted one.
		roleReports := make(map[string]int, len(payloadLinks))
		countingVisit := func(ref *PersistedRef) error {
			switch ref.Kind {
			case PersistedRefOutputRole, PersistedRefLocalBacking:
				if _, declared := roleReports[ref.Role]; !declared {
					return fmt.Errorf("storage role %q is not declared by the owner's snapshot links", ref.Role)
				}
				roleReports[ref.Role]++
			}
			return visit(ref)
		}
		for _, link := range payloadLinks {
			if _, dup := roleReports[link.Role]; dup {
				return PersistedResultEnvelope{}, fmt.Errorf("visit persisted %s payload at %q: storage role %q declared twice", family.Name, path, link.Role)
			}
			roleReports[link.Role] = 0
		}
		rewritten, err := family.Visitor.VisitPersistedReferences(PersistedPayloadVisit{
			Version:       env.Version,
			Call:          ownerCall,
			Path:          path.Field("objectJSON"),
			Payload:       env.ObjectJSON,
			SnapshotLinks: payloadLinks,
		}, countingVisit)
		if err != nil {
			return PersistedResultEnvelope{}, fmt.Errorf("visit persisted %s payload at %q: %w", family.Name, path, err)
		}
		for _, link := range payloadLinks {
			if n := roleReports[link.Role]; n != 1 {
				return PersistedResultEnvelope{}, fmt.Errorf("visit persisted %s payload at %q: storage role %q classified %d times, expected exactly once", family.Name, path, link.Role, n)
			}
		}
		env.ObjectJSON = rewritten
		return env, nil
	case persistedResultKindList:
		items := make([]PersistedResultEnvelope, len(env.Items))
		for i, item := range env.Items {
			itemCall := ownerCall
			if ownerCall != nil {
				itemCall = persistedListItemCall(ownerCall, i+1)
			}
			rewritten, err := visitPersistedEnvelope(item, itemCall, nil, path.Field("items").Index(i), false, visit)
			if err != nil {
				return PersistedResultEnvelope{}, err
			}
			items[i] = rewritten
		}
		env.Items = items
		return env, nil
	default:
		return PersistedResultEnvelope{}, fmt.Errorf("visit persisted envelope at %q: unsupported kind %q", path, env.Kind)
	}
}

// visitResultCallReferences walks the descriptive references of a recorded
// call in place: receiver, module, argument and implicit-input literals, and
// nested call descriptions. Each shared frame is visited once.
func visitResultCallReferences(frame *ResultCall, path PersistedRefPath, visit PersistedRefVisitor) error {
	return visitResultCallReferencesSeen(frame, path.Field("call"), visit, map[*ResultCall]struct{}{})
}

func visitResultCallReferencesSeen(frame *ResultCall, path PersistedRefPath, visit PersistedRefVisitor, seen map[*ResultCall]struct{}) error {
	if frame == nil {
		return nil
	}
	if _, done := seen[frame]; done {
		return nil
	}
	seen[frame] = struct{}{}
	if frame.Module != nil {
		if err := visitResultCallRefReferences(frame.Module.ResultRef, path.Field("module"), visit, seen); err != nil {
			return err
		}
	}
	if err := visitResultCallRefReferences(frame.Receiver, path.Field("receiver"), visit, seen); err != nil {
		return err
	}
	for i, arg := range frame.Args {
		if arg == nil {
			continue
		}
		if err := visitResultCallLiteralReferences(arg.Value, path.Field("args").Index(i), visit, seen); err != nil {
			return err
		}
	}
	for i, input := range frame.ImplicitInputs {
		if input == nil {
			continue
		}
		if err := visitResultCallLiteralReferences(input.Value, path.Field("implicitInputs").Index(i), visit, seen); err != nil {
			return err
		}
	}
	return nil
}

func visitResultCallRefReferences(ref *ResultCallRef, path PersistedRefPath, visit PersistedRefVisitor, seen map[*ResultCall]struct{}) error {
	if ref == nil {
		return nil
	}
	if ref.ResultID != 0 {
		changed, err := VisitPersistedRow(visit, PersistedRefCall, path, &ref.ResultID)
		if err != nil {
			return err
		}
		if changed {
			// The runtime fast path names the old row; it is never persisted
			// and must not survive a relocation.
			ref.shared = nil
		}
	}
	return visitResultCallReferencesSeen(ref.Call, path, visit, seen)
}

func visitResultCallLiteralReferences(lit *ResultCallLiteral, path PersistedRefPath, visit PersistedRefVisitor, seen map[*ResultCall]struct{}) error {
	if lit == nil {
		return nil
	}
	switch lit.Kind {
	case ResultCallLiteralKindResultRef:
		return visitResultCallRefReferences(lit.ResultRef, path, visit, seen)
	case ResultCallLiteralKindList:
		for i, item := range lit.ListItems {
			if err := visitResultCallLiteralReferences(item, path.Index(i), visit, seen); err != nil {
				return err
			}
		}
	case ResultCallLiteralKindObject:
		for _, field := range lit.ObjectFields {
			if field == nil {
				continue
			}
			if err := visitResultCallLiteralReferences(field.Value, path.Field(field.Name), visit, seen); err != nil {
				return err
			}
		}
	}
	return nil
}
