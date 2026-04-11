package dagql

import (
	"context"
	"encoding/json"
	"fmt"

	set "github.com/hashicorp/go-set/v3"
	"github.com/vektah/gqlparser/v2/ast"
)

const (
	persistedResultKindNull   = "null"
	persistedResultKindObject = "object_self"
	persistedResultKindScalar = "scalar_json"
	persistedResultKindList   = "list"
)

// PersistedResultEnvelope is the shared on-disk payload envelope for persisted
// result self values.
//
// This is intentionally opaque at the DB level (stored as self_payload bytes),
// while still carrying enough structured data to decode common SDK-return
// shapes (scalars, object IDs, lists, nested combinations).
type PersistedResultEnvelope struct {
	Version               int                       `json:"version"`
	Kind                  string                    `json:"kind"`
	TypeName              string                    `json:"typeName,omitempty"`
	ResultID              uint64                    `json:"resultID,omitempty"`
	SessionResourceHandle SessionResourceHandle     `json:"sessionResourceHandle,omitempty"`
	ObjectJSON            json.RawMessage           `json:"objectJSON,omitempty"`
	ScalarJSON            json.RawMessage           `json:"scalarJSON,omitempty"`
	ElemTypeName          string                    `json:"elemTypeName,omitempty"`
	Items                 []PersistedResultEnvelope `json:"items,omitempty"`
}

type PersistedObjectCache interface {
	PersistedResultID(AnyResult) (uint64, error)
}

// PersistedObject is implemented by object self payloads that can be encoded
// directly for import-time cache persistence.
type PersistedObject interface {
	Typed
	EncodePersistedObject(context.Context, PersistedObjectCache) (json.RawMessage, error)
}

// PersistedObjectDecoder is implemented by zero-value object types that know
// how to reconstruct a persisted object self payload without replaying the
// original dagql call chain.
type PersistedObjectDecoder interface {
	Typed
	DecodePersistedObject(context.Context, *Server, uint64, *ResultCall, json.RawMessage) (Typed, error)
}

// PersistedSelfCodec is the shared interface used to encode/decode result self
// payloads for disk persistence.
type PersistedSelfCodec interface {
	EncodeResult(context.Context, PersistedObjectCache, AnyResult) (PersistedResultEnvelope, error)
	DecodeResult(context.Context, *Server, uint64, *ResultCall, PersistedResultEnvelope) (AnyResult, error)
}

type defaultPersistedSelfCodec struct{}

var DefaultPersistedSelfCodec PersistedSelfCodec = defaultPersistedSelfCodec{}

func (defaultPersistedSelfCodec) EncodeResult(ctx context.Context, cache PersistedObjectCache, res AnyResult) (PersistedResultEnvelope, error) {
	return encodePersistedResultEnvelope(ctx, cache, res)
}

func (defaultPersistedSelfCodec) DecodeResult(ctx context.Context, dag *Server, resultID uint64, call *ResultCall, env PersistedResultEnvelope) (AnyResult, error) {
	return decodePersistedResultEnvelope(ctx, dag, resultID, call, env)
}

func encodePersistedResultEnvelope(ctx context.Context, cache PersistedObjectCache, res AnyResult) (PersistedResultEnvelope, error) {
	if res == nil {
		return PersistedResultEnvelope{
			Version: 2,
			Kind:    persistedResultKindNull,
		}, nil
	}
	var resultID uint64
	if cache != nil {
		if persistedResultID, err := cache.PersistedResultID(res); err == nil {
			resultID = persistedResultID
		}
	}
	var sessionResourceHandle SessionResourceHandle
	if shared := res.cacheSharedResult(); shared != nil {
		sessionResourceHandle = shared.sessionResourceHandle
	}

	if _, ok := res.(AnyObjectResult); ok {
		encoder, ok := res.Unwrap().(PersistedObject)
		if !ok {
			return PersistedResultEnvelope{}, fmt.Errorf("encode persisted object payload: type %q does not implement persisted object encoding", res.Type().Name())
		}
		objectJSON, err := encoder.EncodePersistedObject(ctx, cache)
		if err != nil {
			return PersistedResultEnvelope{}, fmt.Errorf("encode persisted object payload: %w", err)
		}
		return PersistedResultEnvelope{
			Version:               2,
			Kind:                  persistedResultKindObject,
			TypeName:              res.Type().Name(),
			ResultID:              resultID,
			SessionResourceHandle: sessionResourceHandle,
			ObjectJSON:            objectJSON,
		}, nil
	}
	if encoder, ok := res.Unwrap().(PersistedObject); ok {
		objectJSON, err := encoder.EncodePersistedObject(ctx, cache)
		if err != nil {
			return PersistedResultEnvelope{}, fmt.Errorf("encode persisted object payload: %w", err)
		}
		return PersistedResultEnvelope{
			Version:               2,
			Kind:                  persistedResultKindObject,
			TypeName:              res.Type().Name(),
			ResultID:              resultID,
			SessionResourceHandle: sessionResourceHandle,
			ObjectJSON:            objectJSON,
		}, nil
	}

	if enumerable, ok := res.Unwrap().(Enumerable); ok {
		shared := res.cacheSharedResult()
		if shared == nil || shared.loadResultCall() == nil {
			return PersistedResultEnvelope{}, fmt.Errorf("encode persisted list: missing authoritative call")
		}
		parentCall := shared.loadResultCall()
		itemEnvs := make([]PersistedResultEnvelope, 0, enumerable.Len())
		for i := 1; i <= enumerable.Len(); i++ {
			item, err := enumerable.NthValue(i, parentCall)
			if err != nil {
				return PersistedResultEnvelope{}, fmt.Errorf("encode persisted list item %d: %w", i, err)
			}
			itemEnv, err := encodePersistedResultEnvelope(ctx, cache, item)
			if err != nil {
				return PersistedResultEnvelope{}, fmt.Errorf("encode persisted list item %d envelope: %w", i, err)
			}
			itemEnvs = append(itemEnvs, itemEnv)
		}
		return PersistedResultEnvelope{
			Version:               2,
			Kind:                  persistedResultKindList,
			TypeName:              res.Type().Name(),
			ResultID:              resultID,
			SessionResourceHandle: sessionResourceHandle,
			ElemTypeName:          enumerable.Element().Type().Name(),
			Items:                 itemEnvs,
		}, nil
	}

	scalarJSON, err := json.Marshal(res.Unwrap())
	if err != nil {
		return PersistedResultEnvelope{}, fmt.Errorf("encode scalar_json payload: %w", err)
	}
	return PersistedResultEnvelope{
		Version:               2,
		Kind:                  persistedResultKindScalar,
		TypeName:              res.Type().Name(),
		ResultID:              resultID,
		SessionResourceHandle: sessionResourceHandle,
		ScalarJSON:            scalarJSON,
	}, nil
}

func decodePersistedResultEnvelope(ctx context.Context, dag *Server, resultID uint64, call *ResultCall, env PersistedResultEnvelope) (AnyResult, error) {
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

	switch env.Kind {
	case persistedResultKindNull:
		return nil, nil
	case persistedResultKindObject:
		if dag == nil {
			return nil, fmt.Errorf("decode object_id envelope: missing current dagql server in context")
		}
		if call == nil {
			return nil, fmt.Errorf("decode object_id envelope: missing authoritative call")
		}
		objType, ok := dag.ObjectType(env.TypeName)
		if !ok {
			return nil, fmt.Errorf("decode object_id envelope: unknown object type %q", env.TypeName)
		}
		decoder, ok := objType.Typed().(PersistedObjectDecoder)
		if !ok {
			return nil, fmt.Errorf("decode object_id envelope: object type %q does not implement persisted decode", env.TypeName)
		}
		decodeCtx := ctx
		if call != nil {
			decodeCtx = ContextWithCall(ctx, call)
		}
		valSelf, err := decoder.DecodePersistedObject(decodeCtx, dag, resultID, call, env.ObjectJSON)
		if err != nil {
			return nil, fmt.Errorf("decode object_id envelope load: %w", err)
		}
		valRes, err := NewResultForCall(valSelf, call)
		if err != nil {
			return nil, fmt.Errorf("decode object_id envelope result: %w", err)
		}
		objRes, err := objType.New(valRes)
		if err != nil {
			return nil, fmt.Errorf("decode object_id envelope instantiate: %w", err)
		}
		return setHandle(objRes), nil
	case persistedResultKindScalar:
		if call == nil {
			return nil, fmt.Errorf("decode scalar_json envelope: missing authoritative call")
		}
		var raw any
		if err := json.Unmarshal(env.ScalarJSON, &raw); err != nil {
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
				return setHandle(res), nil
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
		return setHandle(res), nil
	case persistedResultKindList:
		if call == nil {
			return nil, fmt.Errorf("decode list envelope: missing authoritative call")
		}
		items := make([]AnyResult, 0, len(env.Items))
		for i, itemEnv := range env.Items {
			itemCall := call.fork()
			itemCall.Nth = int64(i + 1)
			if itemCall.Type != nil {
				itemCall.Type = itemCall.Type.Elem
			}
			itemCtx := ContextWithCall(ctx, itemCall)
			itemRes, err := decodePersistedResultEnvelope(itemCtx, dag, itemEnv.ResultID, itemCall, itemEnv)
			if err != nil {
				return nil, fmt.Errorf("decode list item %d: %w", i+1, err)
			}
			items = append(items, itemRes)
		}

		var elem Typed
		for _, item := range items {
			if item == nil {
				continue
			}
			elem = item.Unwrap()
			break
		}
		if elem == nil {
			elem = persistedTypedRef{name: env.ElemTypeName}
		}

		res, err := NewResultForCall(DynamicResultArrayOutput{
			Elem:   elem,
			Values: items,
		}, call)
		if err != nil {
			return nil, err
		}
		return setHandle(res), nil
	default:
		return nil, fmt.Errorf("decode persisted result envelope: unsupported kind %q", env.Kind)
	}
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

// PersistedSnapshotRefLink is a generic non-opaque link from a persisted result
// self payload to one durable snapshot ref key.
type PersistedSnapshotRefLink struct {
	RefKey string
	Role   string
}

// PersistedSnapshotRefLinkProvider is the shared interface used by persistable
// self payloads to expose snapshot ref links for `result_snapshot_links`.
type PersistedSnapshotRefLinkProvider interface {
	PersistedSnapshotRefLinks() []PersistedSnapshotRefLink
}

func snapshotOwnerLinksFromTyped(self Typed) []PersistedSnapshotRefLink {
	if self == nil {
		return nil
	}
	linker, ok := any(self).(PersistedSnapshotRefLinkProvider)
	if !ok {
		return nil
	}
	links := linker.PersistedSnapshotRefLinks()
	if len(links) == 0 {
		return nil
	}
	cpy := make([]PersistedSnapshotRefLink, len(links))
	copy(cpy, links)
	return cpy
}

type persistedTypedRef struct {
	name string
}

func (r persistedTypedRef) Type() *ast.Type {
	return &ast.Type{
		NamedType: r.name,
		NonNull:   true,
	}
}
