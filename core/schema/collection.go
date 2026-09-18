package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

func installCollectionSchema(s *moduleSchema, dag *dagql.Server) {
	dag.InstallObject(dagql.NewClass[*core.CollectionTypeDef](dag).View(AfterVersion("v1.0.0-0")))
	dag.InstallObject(dagql.NewClass[*core.CollectionDelta](dag).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.CollectionDelta]{}.Install(dag)
	dagql.Fields[*core.TypeDef]{
		dagql.Func("withCollection", s.typeDefWithCollection).View(AfterVersion("v1.0.0-0")).Doc("Mark this object as a collection."),
		dagql.Func("withCollectionKeys", s.typeDefWithCollectionKeys).View(AfterVersion("v1.0.0-0")).Doc("Select the stored keys field for this collection."),
		dagql.Func("withCollectionGet", s.typeDefWithCollectionGet).View(AfterVersion("v1.0.0-0")).Doc("Select the item lookup function for this collection."),
		dagql.Func("withCollectionDelta", s.typeDefWithCollectionDelta).View(AfterVersion("v1.0.0-0")).Doc("Select the field that receives changes from the original collection."),
		dagql.Func("asCollection", s.typeDefAsCollection).View(AfterVersion("v1.0.0-0")).Doc("Collection metadata, or null if this object is not a collection."),
	}.Install(dag)
	dagql.Fields[*core.ObjectTypeDef]{
		dagql.Func("__withCollectionMember", s.objectTypeDefWithCollectionMember),
		dagql.NodeFunc("__collectionProjection", s.collectionProjection),
	}.Install(dag)
	dagql.Fields[*core.CollectionTypeDef]{
		dagql.Func("keyType", s.collectionKeyType).Doc("The type of collection keys."),
		dagql.Func("valueType", s.collectionValueType).Doc("The object type returned by get."),
		dagql.Func("batchType", s.collectionBatchType).Doc("The type of batch operations, or null when there are none."),
	}.Install(dag)
	dagql.Fields[*core.Query]{
		dagql.Func("__collectionDelta", s.collectionDelta),
	}.Install(dag)
}

func (s *moduleSchema) collectionProjection(ctx context.Context, obj dagql.ObjectResult[*core.ObjectTypeDef], _ struct{}) (*core.ObjectTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	return core.CollectionPublicObject(ctx, dag, obj)
}

func (s *moduleSchema) typeDefWithCollection(ctx context.Context, def *core.TypeDef, _ struct{}) (*core.TypeDef, error) {
	return withCollectionMember(ctx, def, "collection", "")
}

func (s *moduleSchema) typeDefWithCollectionKeys(ctx context.Context, def *core.TypeDef, args struct{ Name string }) (*core.TypeDef, error) {
	return withCollectionMember(ctx, def, "keys", args.Name)
}

func (s *moduleSchema) typeDefWithCollectionGet(ctx context.Context, def *core.TypeDef, args struct{ Name string }) (*core.TypeDef, error) {
	return withCollectionMember(ctx, def, "get", args.Name)
}

func (s *moduleSchema) typeDefWithCollectionDelta(ctx context.Context, def *core.TypeDef, args struct{ Name string }) (*core.TypeDef, error) {
	return withCollectionMember(ctx, def, "delta", args.Name)
}

func withCollectionMember(ctx context.Context, def *core.TypeDef, role, name string) (*core.TypeDef, error) {
	if def.Kind != core.TypeDefKindObject || !def.AsObject.Valid {
		return nil, fmt.Errorf("only object types can declare collection members")
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var obj dagql.ObjectResult[*core.ObjectTypeDef]
	err = dag.Select(ctx, def.AsObject.Value, &obj, dagql.Selector{
		Field: "__withCollectionMember",
		Args:  []dagql.NamedInput{{Name: "role", Value: dagql.String(role)}, {Name: "name", Value: dagql.String(name)}},
	})
	if err != nil {
		return nil, err
	}
	return def.WithObjectTypeDef(obj), nil
}

func (s *moduleSchema) objectTypeDefWithCollectionMember(_ context.Context, obj *core.ObjectTypeDef, args struct{ Role, Name string }) (*core.ObjectTypeDef, error) {
	return obj.WithCollectionMember(args.Role, args.Name)
}

func (s *moduleSchema) typeDefAsCollection(_ context.Context, def *core.TypeDef, _ struct{}) (dagql.Nullable[*core.CollectionTypeDef], error) {
	if !def.AsObject.Valid || def.AsObject.Value.Self().Collection == nil || !def.AsObject.Value.Self().Collection.Enabled {
		return dagql.Null[*core.CollectionTypeDef](), nil
	}
	if _, err := def.AsObject.Value.Self().CollectionMembers(); err != nil {
		return dagql.Null[*core.CollectionTypeDef](), err
	}
	return dagql.NonNull(&core.CollectionTypeDef{Object: def.AsObject.Value}), nil
}

func (s *moduleSchema) collectionKeyType(_ context.Context, def *core.CollectionTypeDef, _ struct{}) (dagql.ObjectResult[*core.TypeDef], error) {
	members, err := def.Object.Self().CollectionMembers()
	if err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, err
	}
	return members.Keys.TypeDef.Self().AsList.Value.Self().ElementTypeDef, nil
}

func (s *moduleSchema) collectionValueType(_ context.Context, def *core.CollectionTypeDef, _ struct{}) (dagql.ObjectResult[*core.TypeDef], error) {
	members, err := def.Object.Self().CollectionMembers()
	if err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, err
	}
	return members.Get.ReturnType, nil
}

func (s *moduleSchema) collectionBatchType(ctx context.Context, def *core.CollectionTypeDef, _ struct{}) (dagql.Nullable[dagql.ObjectResult[*core.TypeDef]], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.Null[dagql.ObjectResult[*core.TypeDef]](), err
	}
	return core.CollectionBatchType(ctx, dag, def.Object)
}

func (s *moduleSchema) collectionDelta(_ context.Context, _ *core.Query, args struct{ AddedKeys, RemovedKeys, BaseKeys []string }) (*core.CollectionDelta, error) {
	return &core.CollectionDelta{AddedKeys: args.AddedKeys, RemovedKeys: args.RemovedKeys, BaseKeys: args.BaseKeys}, nil
}
