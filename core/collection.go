package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// CollectionConfig contains author declarations only. Resolved types are read
// from the owning ObjectTypeDef, so they follow its namespace and lifetime.
type CollectionConfig struct {
	Enabled bool   `json:"enabled,omitempty"`
	Keys    string `json:"keys,omitempty"`
	Get     string `json:"get,omitempty"`
	Delta   string `json:"delta,omitempty"`
}

func (obj *ObjectTypeDef) WithCollectionMember(role, name string) (*ObjectTypeDef, error) {
	obj = obj.Clone()
	if obj.Collection == nil {
		obj.Collection = &CollectionConfig{}
	} else {
		config := *obj.Collection
		obj.Collection = &config
	}
	if role == "collection" {
		obj.Collection.Enabled = true
		return obj, nil
	}
	if name == "" {
		return nil, fmt.Errorf("collection %s member name must not be empty", role)
	}
	var member *string
	switch role {
	case "keys":
		member = &obj.Collection.Keys
	case "get":
		member = &obj.Collection.Get
	case "delta":
		member = &obj.Collection.Delta
	default:
		return nil, fmt.Errorf("unknown collection member role %q", role)
	}
	name = gqlFieldName(name)
	if *member != "" && *member != name {
		return nil, fmt.Errorf("collection %q has multiple %s members: %q and %q", obj.OriginalName, role, *member, name)
	}
	*member = name
	return obj, nil
}

type CollectionMembers struct {
	Keys  *FieldTypeDef
	Get   *Function
	Delta *FieldTypeDef
}

func (obj *ObjectTypeDef) CollectionMembers() (*CollectionMembers, error) {
	if obj.Collection == nil {
		return nil, nil
	}
	if !obj.Collection.Enabled {
		return nil, fmt.Errorf("object %q has collection member markers but is not marked as a collection", obj.OriginalName)
	}
	keysName, getName, deltaName := obj.Collection.Keys, obj.Collection.Get, obj.Collection.Delta
	if keysName == "" {
		keysName = "keys"
	}
	if getName == "" {
		getName = "get"
	}
	if deltaName == "" {
		deltaName = "delta"
	}
	keys, ok := obj.FieldByName(keysName)
	if !ok {
		return nil, fmt.Errorf("collection %q requires a stored keys field %q", obj.OriginalName, keysName)
	}
	keyList := keys.TypeDef.Self()
	if keyList == nil || keyList.Optional || keyList.Kind != TypeDefKindList || !keyList.AsList.Valid {
		return nil, fmt.Errorf("collection %q keys field must be a non-null list", obj.OriginalName)
	}
	keyType := keyList.AsList.Value.Self().ElementTypeDef.Self()
	if keyType == nil || keyType.Optional || !isCollectionKeyType(keyType) {
		return nil, fmt.Errorf("collection %q keys must be non-null scalars or enum values", obj.OriginalName)
	}
	get, ok := obj.FunctionByName(getName)
	if !ok || len(get.Args) != 1 {
		return nil, fmt.Errorf("collection %q requires a get function %q with exactly one argument", obj.OriginalName, getName)
	}
	arg := get.Args[0].Self()
	argType := arg.TypeDef.Self()
	if argType == nil || argType.Optional || !keyType.IsSubtypeOf(argType) || !argType.IsSubtypeOf(keyType) {
		return nil, fmt.Errorf("collection %q get argument must have the non-null key type %s", obj.OriginalName, keyType.ToType())
	}
	valueType := get.ReturnType.Self()
	if valueType == nil || valueType.Optional || valueType.Kind != TypeDefKindObject {
		return nil, fmt.Errorf("collection %q get function must return a non-null object", obj.OriginalName)
	}
	members := &CollectionMembers{Keys: keys, Get: get}
	if delta, ok := obj.FieldByName(deltaName); ok {
		typ := delta.TypeDef.Self()
		if typ.Kind != TypeDefKindObject || !typ.AsObject.Valid || typ.AsObject.Value.Self().Name != "CollectionDelta" {
			return nil, fmt.Errorf("collection %q delta field must have type CollectionDelta", obj.OriginalName)
		}
		members.Delta = delta
	} else if obj.Collection.Delta != "" {
		return nil, fmt.Errorf("collection %q delta field %q does not exist", obj.OriginalName, deltaName)
	}
	return members, nil
}

func isCollectionKeyType(typ *TypeDef) bool {
	switch typ.Kind {
	case TypeDefKindString, TypeDefKindInteger, TypeDefKindFloat, TypeDefKindBoolean, TypeDefKindScalar, TypeDefKindEnum:
		return true
	default:
		return false
	}
}

// CollectionTypeDef keeps the attached author object rather than copying its
// type references. Each exposed member is derived from that object.
type CollectionTypeDef struct {
	Object dagql.ObjectResult[*ObjectTypeDef]
}

func (*CollectionTypeDef) Type() *ast.Type {
	return &ast.Type{NamedType: "CollectionTypeDef", NonNull: true}
}

func (def *CollectionTypeDef) Clone() *CollectionTypeDef {
	cp := *def
	return &cp
}

func (def *CollectionTypeDef) AttachDependencyResults(_ context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	res, err := attach(def.Object)
	if err != nil {
		return nil, err
	}
	obj, ok := res.(dagql.ObjectResult[*ObjectTypeDef])
	if !ok {
		return nil, fmt.Errorf("collection typedef object has unexpected type %T", res)
	}
	def.Object = obj
	return []dagql.AnyResult{obj}, nil
}

func (def *CollectionTypeDef) EncodePersistedObject(_ context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	id, err := encodePersistedObjectRef(cache, def.Object, "collection typedef object")
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	return encodePersistedObjectPayload(id)
}

func (*CollectionTypeDef) DecodePersistedObject(ctx context.Context, srv *dagql.Server, _ uint64, _ *dagql.ResultCall, raw json.RawMessage) (dagql.Typed, error) {
	var id uint64
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, err
	}
	obj, err := loadPersistedObjectResultByResultID[*ObjectTypeDef](ctx, srv, id, "collection typedef object")
	if err != nil {
		return nil, err
	}
	return &CollectionTypeDef{Object: obj}, nil
}

type CollectionDelta struct {
	AddedKeys   []string `field:"true" doc:"Current keys absent from the original collection, in current order."`
	RemovedKeys []string `field:"true" doc:"Original keys absent from the current collection, in original order."`
}

func (*CollectionDelta) Type() *ast.Type {
	return &ast.Type{NamedType: "CollectionDelta", NonNull: true}
}

func (delta *CollectionDelta) Clone() *CollectionDelta {
	return &CollectionDelta{AddedKeys: slices.Clone(delta.AddedKeys), RemovedKeys: slices.Clone(delta.RemovedKeys)}
}

func (delta *CollectionDelta) EncodePersistedObject(_ context.Context, _ dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	return encodePersistedObjectPayload(delta)
}

func (*CollectionDelta) DecodePersistedObject(_ context.Context, _ *dagql.Server, _ uint64, _ *dagql.ResultCall, raw json.RawMessage) (dagql.Typed, error) {
	var delta CollectionDelta
	if err := json.Unmarshal(raw, &delta); err != nil {
		return nil, err
	}
	return &delta, nil
}
