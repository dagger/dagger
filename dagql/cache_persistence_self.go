package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	set "github.com/hashicorp/go-set/v3"
	"github.com/vektah/gqlparser/v2/ast"
)

const (
	persistedResultKindNull   = "null"
	persistedResultKindObject = "object_self"
	persistedResultKindScalar = "scalar_json"
	persistedResultKindList   = "list"
	// persistedResultKindRef names a separately attached row from inside a
	// list. It carries only the row identity; the row's own envelope holds
	// its body.
	persistedResultKindRef = "result_ref"
)

// persistedResultEnvelopeVersion is the current interpretation of
// PersistedResultEnvelope. Every change to how an envelope is read cuts a new
// version, including scoped (OutputPath, Role) snapshot links; older versions are never read (see cachePersistenceSchemaVersion).
//
// 3: attached absent values keep their row identity as null envelopes;
// scalars decode losslessly; lists rebuild their declared recursive type from
// the recorded call instead of a flattened element name; object envelopes
// name their codec family; list items naming another row are result_ref
// envelopes without a duplicated body.
// 4: root origin and independent pending offer ownership.
const persistedResultEnvelopeVersion = 4

// PersistedResultEnvelope is the shared on-disk payload envelope for persisted
// result self values.
//
// This is intentionally opaque at the DB level (stored as self_payload bytes),
// while still carrying enough structured data to decode common SDK-return
// shapes (scalars, object IDs, lists, nested combinations).
type PersistedResultEnvelope struct {
	Imported      bool                 `json:"imported,omitempty"`
	PendingOffers []PersistedPartOffer `json:"pendingOffers,omitempty"`
	Version       int                  `json:"version"`
	Kind          string               `json:"kind"`
	// TypeName identifies the GraphQL value type of object and scalar
	// envelopes.
	TypeName string `json:"typeName,omitempty"`
	// ObjectCodec identifies the Go payload family that produced ObjectJSON.
	// Every user-named module object shares one family; TypeName still names
	// the concrete GraphQL type.
	ObjectCodec string `json:"objectCodec,omitempty"`
	// ResultID is the attached row identity of the value. The root envelope
	// of a row repeats that row's ID; a null root keeps the identity of an
	// attached absent value; a result_ref item names another attached row.
	ResultID              uint64                    `json:"resultID,omitempty"`
	SessionResourceHandle SessionResourceHandle     `json:"sessionResourceHandle,omitempty"`
	ObjectJSON            json.RawMessage           `json:"objectJSON,omitempty"`
	ScalarJSON            json.RawMessage           `json:"scalarJSON,omitempty"`
	Items                 []PersistedResultEnvelope `json:"items,omitempty"`
}

type PersistedObjectCache interface {
	PersistedResultID(AnyResult) (uint64, error)
}

type PersistedObjectEncoding struct {
	JSON          json.RawMessage
	SnapshotLinks []PersistedSnapshotRefLink
}

// PersistedObject is implemented by object self payloads that can be encoded
// directly for import-time cache persistence. Every implementing Go type must
// also be registered as a PersistedObjectFamily so its declared references can
// be visited without constructing the value.
type PersistedObject interface {
	Typed
	EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error)
}

// PersistedObjectDecoder is implemented by zero-value object types that know
// how to reconstruct a persisted object self payload without re-evaluating the
// original dagql call chain.
type PersistedObjectDecoder interface {
	Typed
	DecodePersistedObject(context.Context, *PersistDecodeContext, json.RawMessage) (Typed, error)
}

// PersistedSelfCodec is the shared interface used to encode/decode result self
// payloads for disk persistence.
type PersistedSelfCodec interface {
	EncodeResult(context.Context, PersistedObjectCache, AnyResult) (PersistedResultEncoding, error)
	DecodeResult(context.Context, *Server, uint64, *ResultCall, PersistedResultEnvelope) (AnyResult, error)
}

type defaultPersistedSelfCodec struct{}

var DefaultPersistedSelfCodec PersistedSelfCodec = defaultPersistedSelfCodec{}

type PersistedResultEncoding struct {
	Envelope      PersistedResultEnvelope
	SnapshotLinks []PersistedSnapshotRefLink
}

func (defaultPersistedSelfCodec) EncodeResult(ctx context.Context, cache PersistedObjectCache, res AnyResult) (PersistedResultEncoding, error) {
	var (
		resultID uint64
		frame    *ResultCall
	)
	if res != nil {
		if shared := res.cacheSharedResult(); shared != nil {
			frame = shared.loadResultCall()
			if cache != nil {
				if persistedResultID, err := cache.PersistedResultID(res); err == nil {
					resultID = persistedResultID
				}
			}
		}
	}
	enc := NewPersistEncodeContext(cache, resultID, frame)
	enc.quiescent, _ = ctx.Value(quiescentPersistKey{}).(bool)
	return encodePersistedResultEnvelope(ctx, enc, res, true)
}

func (defaultPersistedSelfCodec) DecodeResult(ctx context.Context, dag *Server, resultID uint64, call *ResultCall, env PersistedResultEnvelope) (ret AnyResult, rerr error) {
	dec := NewPersistDecodeContext(dag, resultID, call)
	dec.roles, _ = ctx.Value(copiedDecodeRolesKey{}).(*copiedDecodeRoles)
	if dec.roles != nil && dec.roles.ResultID != resultID {
		return nil, fmt.Errorf("persist decode snapshot roles: carrier owner mismatch")
	}
	if dec.roles == nil && resultID != 0 && env.Kind == persistedResultKindList {
		if cache, err := EngineCache(ctx); err == nil {
			if links, err := cache.PersistedSnapshotLinksByResultID(ctx, resultID); err == nil {
				dec.roles = &copiedDecodeRoles{ResultID: resultID, Links: cloneSnapshotRefLinks(links), Imported: env.Imported}
			}
		}
	}
	dec.imported = env.Imported
	if dec.roles != nil && dec.roles.ResultID == resultID {
		dec.imported = dec.roles.Imported
		dec.host = dec.roles.Host
	}
	var cleanup OnReleaseFunc
	dec.cleanup = &cleanup
	defer func() {
		if rerr != nil && cleanup != nil {
			rerr = errors.Join(rerr, cleanup(context.WithoutCancel(ctx)))
		}
	}()
	ret, rerr = decodePersistedResultEnvelope(ctx, dec, env, true)
	if rerr == nil && ret != nil {
		ret.cacheSharedResult().onRelease = cleanup
	}
	return ret, rerr
}

// persistedAbsentEnvelope describes an attached absent value: the row keeps
// its identity, recorded call, requirements and list position while its body
// is null.
func persistedAbsentEnvelope(resultID uint64, handle SessionResourceHandle) PersistedResultEnvelope {
	return PersistedResultEnvelope{
		Version:               persistedResultEnvelopeVersion,
		Kind:                  persistedResultKindNull,
		ResultID:              resultID,
		SessionResourceHandle: handle,
	}
}

//nolint:gocyclo // one classification per envelope kind; splitting hides the order of the checks
func encodePersistedResultEnvelope(ctx context.Context, enc *PersistEncodeContext, res AnyResult, root bool) (PersistedResultEncoding, error) {
	if res == nil {
		return PersistedResultEncoding{Envelope: PersistedResultEnvelope{
			Version: persistedResultEnvelopeVersion,
			Kind:    persistedResultKindNull,
		}}, nil
	}

	// Record the row's own identity before dereferencing: a nullable view
	// may dereference to another view or to absence, and either way the
	// envelope describes this row.
	resultID := enc.ResultID()
	var sessionResourceHandle SessionResourceHandle
	shared := res.cacheSharedResult()
	if shared != nil {
		sessionResourceHandle = shared.sessionResourceHandle
	}
	if !root && resultID != 0 {
		// A separately attached item is only named; its own row carries the
		// body.
		return PersistedResultEncoding{Envelope: PersistedResultEnvelope{
			Version:  persistedResultEnvelopeVersion,
			Kind:     persistedResultKindRef,
			ResultID: resultID,
		}}, nil
	}

	value, present := res.DerefValue()
	if !present || value == nil || value.Unwrap() == nil {
		return encodePersistedAbsentValue(enc, resultID, sessionResourceHandle, root)
	}
	self := value.Unwrap()

	isObject := false
	if _, ok := value.(AnyObjectResult); ok {
		isObject = true
	}
	if valueShared := value.cacheSharedResult(); valueShared != nil && valueShared.isObject {
		isObject = true
	}
	encoder, isEncoder := self.(PersistedObject)
	if isObject && !isEncoder {
		return PersistedResultEncoding{}, fmt.Errorf("encode persisted object payload: type %q does not implement persisted object encoding", value.Type().Name())
	}
	if isEncoder {
		family, ok := PersistedObjectFamilyFor(self)
		if !ok {
			return PersistedResultEncoding{}, fmt.Errorf("encode persisted object payload: type %q (%T) has no registered persisted object family", value.Type().Name(), self)
		}
		if versions, ok := ctx.Value(capturedOutputVersionsKey{}).(*capturedOutputVersions); ok {
			if output, ok := self.(PersistedOutputVersion); ok {
				if err := versions.record(output); err != nil {
					return PersistedResultEncoding{}, err
				}
			}
		}
		objectEncoding, err := encoder.EncodePersistedObject(ctx, enc)
		if err != nil {
			return PersistedResultEncoding{}, fmt.Errorf("encode persisted object payload: %w", err)
		}
		return PersistedResultEncoding{
			Envelope: PersistedResultEnvelope{
				Version:               persistedResultEnvelopeVersion,
				Kind:                  persistedResultKindObject,
				TypeName:              value.Type().Name(),
				ObjectCodec:           family.Name,
				ResultID:              resultID,
				SessionResourceHandle: sessionResourceHandle,
				ObjectJSON:            objectEncoding.JSON,
			},
			SnapshotLinks: prefixSnapshotLinks(objectEncoding.SnapshotLinks, enc.path),
		}, nil
	}

	if enumerable, ok := self.(Enumerable); ok {
		parentCall := enc.Call()
		if parentCall == nil {
			return PersistedResultEncoding{}, fmt.Errorf("encode persisted list: missing authoritative call")
		}
		if parentCall.Type == nil || parentCall.Type.Elem == nil {
			return PersistedResultEncoding{}, fmt.Errorf("encode persisted list: recorded call type %s does not declare a list", parentCall.Type.toAST())
		}
		itemEnvs := make([]PersistedResultEnvelope, 0, enumerable.Len())
		var itemLinks []PersistedSnapshotRefLink
		for i := 1; i <= enumerable.Len(); i++ {
			item, err := enumerable.NthValue(i, parentCall)
			if err != nil {
				return PersistedResultEncoding{}, fmt.Errorf("encode persisted list item %d: %w", i, err)
			}
			itemCall := persistedListItemCall(parentCall, i)
			itemEnc := enc.item(itemCall, i-1)
			if item != nil {
				if itemShared := item.cacheSharedResult(); itemShared != nil && itemShared.id != 0 {
					itemEnc.resultID = uint64(itemShared.id)
				}
			}
			itemEncoding, err := encodePersistedResultEnvelope(ctx, itemEnc, item, false)
			if err != nil {
				return PersistedResultEncoding{}, fmt.Errorf("encode persisted list item %d envelope: %w", i, err)
			}
			itemEnvs = append(itemEnvs, itemEncoding.Envelope)
			itemLinks = append(itemLinks, itemEncoding.SnapshotLinks...)
		}
		return PersistedResultEncoding{
			Envelope: PersistedResultEnvelope{
				Version:               persistedResultEnvelopeVersion,
				Kind:                  persistedResultKindList,
				TypeName:              value.Type().Name(),
				ResultID:              resultID,
				SessionResourceHandle: sessionResourceHandle,
				Items:                 itemEnvs,
			},
			SnapshotLinks: itemLinks,
		}, nil
	}

	if _, ok := self.(Input); !ok {
		if _, ok := self.(ScalarType); !ok {
			return PersistedResultEncoding{}, fmt.Errorf("encode scalar_json payload: type %q does not implement persisted object encoding or scalar input encoding", value.Type().Name())
		}
	}
	scalarJSON, err := json.Marshal(self)
	if err != nil {
		return PersistedResultEncoding{}, fmt.Errorf("encode scalar_json payload: %w", err)
	}
	return PersistedResultEncoding{
		Envelope: PersistedResultEnvelope{
			Version:               persistedResultEnvelopeVersion,
			Kind:                  persistedResultKindScalar,
			TypeName:              value.Type().Name(),
			ResultID:              resultID,
			SessionResourceHandle: sessionResourceHandle,
			ScalarJSON:            scalarJSON,
		},
	}, nil
}

// encodePersistedAbsentValue writes an absent value. An attached absent row
// is checked against its recorded declaration at capture: a nil value under a
// non-null declaration fails the save naming the owner instead of writing a
// row that restart cannot import.
func encodePersistedAbsentValue(enc *PersistEncodeContext, resultID uint64, handle SessionResourceHandle, root bool) (PersistedResultEncoding, error) {
	if root && resultID != 0 {
		if _, err := persistedAbsentValue(enc.Call()); err != nil {
			return PersistedResultEncoding{}, fmt.Errorf("persist absent value of result %d: %w", resultID, err)
		}
	}
	return PersistedResultEncoding{Envelope: persistedAbsentEnvelope(resultID, handle)}, nil
}

// persistedListItemCall derives the recorded call of the i-th (1-based) item
// of a list from its parent's recorded call.
func persistedListItemCall(parentCall *ResultCall, nth int) *ResultCall {
	itemCall := parentCall.fork()
	itemCall.Nth = int64(nth)
	if itemCall.Type != nil {
		itemCall.Type = itemCall.Type.Elem
	}
	return itemCall
}

// persistedTypeDescriptor rebuilds a declared type as a Typed value from the
// recorded call type using the existing runtime shapes: a non-null list is a
// DynamicResultArrayOutput, a nullable level is a DynamicNullable and a named
// leaf is a persistedTypedRef. Nothing is derived from concrete items.
func persistedTypeDescriptor(typ *ResultCallType) (Typed, error) {
	if typ == nil {
		return nil, fmt.Errorf("missing declared type")
	}
	var inner Typed
	switch {
	case typ.Elem != nil:
		elem, err := persistedTypeDescriptor(typ.Elem)
		if err != nil {
			return nil, err
		}
		inner = DynamicResultArrayOutput{Elem: elem}
	case typ.NamedType != "":
		inner = persistedTypedRef{name: typ.NamedType}
	default:
		return nil, fmt.Errorf("declared type has neither element nor name")
	}
	if !typ.NonNull {
		return DynamicNullable{Elem: inner}, nil
	}
	return inner, nil
}

// persistedAbsentValue rebuilds an attached absent value from its declared
// type: an invalid DynamicNullable whose Elem describes the declared inner
// type. A non-null declaration cannot be absent.
func persistedAbsentValue(call *ResultCall) (Typed, error) {
	if call == nil || call.Type == nil {
		return nil, fmt.Errorf("attached absent value has no declared type")
	}
	if call.Type.NonNull {
		return nil, fmt.Errorf("attached absent value declared non-null %s", call.Type.toAST())
	}
	inner := call.Type.clone()
	inner.NonNull = true
	elem, err := persistedTypeDescriptor(inner)
	if err != nil {
		return nil, err
	}
	return DynamicNullable{Elem: elem}, nil
}

func decodePersistedResultEnvelope(ctx context.Context, dec *PersistDecodeContext, env PersistedResultEnvelope, root bool) (AnyResult, error) {
	if env.Version != persistedResultEnvelopeVersion {
		return nil, fmt.Errorf("decode persisted result envelope: unsupported version %d (kind %q)", env.Version, env.Kind)
	}
	setHandle := func(res AnyResult) AnyResult {
		if res == nil || env.SessionResourceHandle == "" {
			return res
		}
		shared := res.cacheSharedResult()
		if shared == nil {
			return res
		}
		shared.sessionResourceHandle = env.SessionResourceHandle
		reqs := set.NewTreeSet(compareSessionResourceHandles)
		reqs.Insert(env.SessionResourceHandle)
		shared.requiredSessionResources = reqs
		return res
	}
	call := dec.Call()
	dag := dec.Server()

	switch env.Kind {
	case persistedResultKindNull:
		if env.ResultID == 0 {
			return nil, nil
		}
		if call == nil {
			return nil, fmt.Errorf("decode null envelope: missing authoritative call")
		}
		absent, err := persistedAbsentValue(call)
		if err != nil {
			return nil, fmt.Errorf("decode null envelope for result %d: %w", env.ResultID, err)
		}
		res, err := NewResultForCall(absent, call)
		if err != nil {
			return nil, err
		}
		return setHandle(res), nil
	case persistedResultKindRef:
		if root {
			return nil, fmt.Errorf("decode result_ref envelope: root envelope cannot be a result reference")
		}
		if env.ResultID == 0 {
			return nil, fmt.Errorf("decode result_ref envelope: zero result ID")
		}
		return dec.ResultRef(ctx, env.ResultID)
	case persistedResultKindObject:
		res, err := decodePersistedObjectEnvelope(ctx, dec, env, call, dag)
		if err != nil {
			return nil, err
		}
		return setHandle(res), nil
	case persistedResultKindScalar:
		res, err := decodePersistedScalarEnvelope(env, call, dag)
		if err != nil {
			return nil, err
		}
		return setHandle(res), nil
	case persistedResultKindList:
		res, err := decodePersistedListEnvelope(ctx, dec, env, call)
		if err != nil {
			return nil, err
		}
		return setHandle(res), nil
	default:
		return nil, fmt.Errorf("decode persisted result envelope: unsupported kind %q", env.Kind)
	}
}

// decodePersistedObjectEnvelope decodes an object_id envelope through its
// registered codec family; the caller applies the session resource handle.
func decodePersistedObjectEnvelope(ctx context.Context, dec *PersistDecodeContext, env PersistedResultEnvelope, call *ResultCall, dag *Server) (AnyResult, error) {
	if dag == nil {
		return nil, fmt.Errorf("decode object_id envelope: missing current dagql server in context")
	}
	if call == nil {
		return nil, fmt.Errorf("decode object_id envelope: missing authoritative call")
	}
	family, ok := PersistedObjectFamilyByName(env.ObjectCodec)
	if !ok {
		return nil, fmt.Errorf("decode object_id envelope: unknown object codec family %q for type %q", env.ObjectCodec, env.TypeName)
	}
	objType, ok := dag.ObjectType(env.TypeName)
	if !ok {
		return nil, fmt.Errorf("decode object_id envelope: unknown object type %q", env.TypeName)
	}
	decoder, ok := objType.Typed().(PersistedObjectDecoder)
	if !ok {
		return nil, fmt.Errorf("decode object_id envelope: object type %q does not implement persisted decode", env.TypeName)
	}
	if decoderFamily, ok := PersistedObjectFamilyFor(objType.Typed()); !ok || decoderFamily.Name != family.Name {
		return nil, fmt.Errorf("decode object_id envelope: object type %q decodes family %q, envelope carries %q", env.TypeName, decoderFamily.Name, family.Name)
	}
	decodeCtx := ContextWithCall(ctx, call)
	valSelf, err := decoder.DecodePersistedObject(decodeCtx, dec, env.ObjectJSON)
	if err != nil {
		return nil, fmt.Errorf("decode object_id envelope load: %w", err)
	}
	if release, ok := valSelf.(OnReleaser); ok && dec.cleanup != nil {
		*dec.cleanup = joinOnRelease(*dec.cleanup, release.OnRelease)
	}
	if host, ok := valSelf.(HasPartHost); ok && dec.host != nil {
		host.BindPartHost(dec.host)
	}
	valRes, err := NewResultForCall(valSelf, call)
	if err != nil {
		return nil, fmt.Errorf("decode object_id envelope result: %w", err)
	}
	if len(dec.scope.OutputPath) != 0 {
		valRes.cacheSharedResult().inlineBorrow = dec.host
	}
	objRes, err := objType.New(valRes)
	if err != nil {
		return nil, fmt.Errorf("decode object_id envelope instantiate: %w", err)
	}
	return persistedNullableView(objRes, call), nil
}

// decodePersistedScalarEnvelope decodes a scalar_json envelope through the
// server's scalar type when it has one, else the builtin scalar decoder.
func decodePersistedScalarEnvelope(env PersistedResultEnvelope, call *ResultCall, dag *Server) (AnyResult, error) {
	if call == nil {
		return nil, fmt.Errorf("decode scalar_json envelope: missing authoritative call")
	}
	raw, err := DecodeLosslessJSON(env.ScalarJSON)
	if err != nil {
		return nil, fmt.Errorf("decode scalar_json envelope payload: %w", err)
	}
	if dag != nil {
		scalarType, ok := dag.ScalarType(env.TypeName)
		if ok {
			input, err := scalarType.DecodeInput(raw)
			if err != nil {
				return nil, fmt.Errorf("decode scalar_json envelope input: %w", err)
			}
			res, err := NewResultForCall(input, call)
			if err != nil {
				return nil, err
			}
			return persistedNullableView(res, call), nil
		}
	}
	builtin, err := decodeBuiltinPersistedScalar(env.TypeName, raw)
	if err != nil {
		return nil, fmt.Errorf("decode scalar_json envelope builtin input: %w", err)
	}
	res, err := NewResultForCall(builtin, call)
	if err != nil {
		return nil, err
	}
	return persistedNullableView(res, call), nil
}

// decodePersistedListEnvelope decodes a list envelope item by item.
func decodePersistedListEnvelope(ctx context.Context, dec *PersistDecodeContext, env PersistedResultEnvelope, call *ResultCall) (AnyResult, error) {
	if call == nil {
		return nil, fmt.Errorf("decode list envelope: missing authoritative call")
	}
	if call.Type == nil || call.Type.Elem == nil {
		return nil, fmt.Errorf("decode list envelope: recorded call type %s does not declare a list", call.Type.toAST())
	}
	elem, err := persistedTypeDescriptor(call.Type.Elem)
	if err != nil {
		return nil, fmt.Errorf("decode list envelope element type: %w", err)
	}
	items := make([]AnyResult, 0, len(env.Items))
	for i, itemEnv := range env.Items {
		itemCall := persistedListItemCall(call, i+1)
		itemCtx := ContextWithCall(ctx, itemCall)
		itemRes, err := decodePersistedResultEnvelope(itemCtx, dec.item(itemCall, i), itemEnv, false)
		if err != nil {
			return nil, fmt.Errorf("decode list item %d: %w", i+1, err)
		}
		items = append(items, itemRes)
	}
	res, err := NewResultForCall(DynamicResultArrayOutput{
		Elem:   elem,
		Values: items,
	}, call)
	if err != nil {
		return nil, err
	}
	if len(dec.scope.OutputPath) != 0 {
		res.cacheSharedResult().inlineBorrow = dec.host
	}
	return persistedNullableView(res, call), nil
}

// persistedNullableView presents a decoded present value the way a fresh
// nullable result is presented: when the recorded call declares a nullable
// type and the value is not itself a nullable wrapper, the same row is viewed
// through its nullable wrapper so Type() reports the declaration while
// DerefValue exposes the value with the same identity.
func persistedNullableView(res AnyResult, call *ResultCall) AnyResult {
	if res == nil || call == nil || call.Type == nil || call.Type.NonNull {
		return res
	}
	if _, derefable := res.Unwrap().(Derefable); derefable {
		return res
	}
	return res.NullableWrapped()
}

func decodeBuiltinPersistedScalar(typeName string, raw any) (Typed, error) {
	switch typeName {
	case "String":
		input, err := String("").DecodeInput(raw)
		if err != nil {
			return nil, err
		}
		typed, ok := input.(Typed)
		if !ok {
			return nil, fmt.Errorf("builtin scalar String did not decode to Typed: %T", input)
		}
		return typed, nil
	case "Int":
		input, err := Int(0).DecodeInput(raw)
		if err != nil {
			return nil, err
		}
		typed, ok := input.(Typed)
		if !ok {
			return nil, fmt.Errorf("builtin scalar Int did not decode to Typed: %T", input)
		}
		return typed, nil
	case "Float":
		input, err := Float(0).DecodeInput(raw)
		if err != nil {
			return nil, err
		}
		typed, ok := input.(Typed)
		if !ok {
			return nil, fmt.Errorf("builtin scalar Float did not decode to Typed: %T", input)
		}
		return typed, nil
	case "Boolean":
		input, err := Boolean(false).DecodeInput(raw)
		if err != nil {
			return nil, err
		}
		typed, ok := input.(Typed)
		if !ok {
			return nil, fmt.Errorf("builtin scalar Boolean did not decode to Typed: %T", input)
		}
		return typed, nil
	default:
		return nil, fmt.Errorf("unknown scalar type %q and no dagql server in context", typeName)
	}
}

// persistedBuiltinScalarName reports whether a scalar type name is one of the
// builtins every server can decode without a resolved schema.
func persistedBuiltinScalarName(typeName string) bool {
	switch typeName {
	case "String", "Int", "Float", "Boolean":
		return true
	default:
		return false
	}
}

// PersistedSnapshotRefLink is a generic non-opaque link from a persisted result
// self payload to one durable snapshot ref key.
type PersistedSnapshotRefLink struct {
	RefKey     string
	Role       string
	OutputPath PersistedRefPath `json:"outputPath,omitempty"`
}

// PersistedSnapshotRefLinkProvider is the shared interface used by persistable
// self payloads to expose snapshot ref links for `result_snapshot_links`.
type PersistedSnapshotRefLinkProvider interface {
	PersistedSnapshotRefLinks() []PersistedSnapshotRefLink
}

// persistedTypedRef is the descriptor-only leaf of a rebuilt declared type. It
// names a type without carrying a value.
type persistedTypedRef struct {
	name string
}

func (r persistedTypedRef) Type() *ast.Type {
	return &ast.Type{
		NamedType: r.name,
		NonNull:   true,
	}
}
