package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

func collectionTypeRef(ctx context.Context, dag *dagql.Server, obj dagql.ObjectResult[*ObjectTypeDef]) (dagql.ObjectResult[*TypeDef], error) {
	id, err := obj.ID()
	if err != nil {
		return dagql.ObjectResult[*TypeDef]{}, err
	}
	var result dagql.ObjectResult[*TypeDef]
	err = dag.Select(ctx, dag.Root(), &result, dagql.Selector{Field: "typeDef"}, dagql.Selector{
		Field: "__withObjectTypeDef", Args: []dagql.NamedInput{{Name: "objectTypeDef", Value: dagql.NewID[*ObjectTypeDef](id)}},
	})
	return result, err
}

func CollectionBatchType(ctx context.Context, dag *dagql.Server, obj dagql.ObjectResult[*ObjectTypeDef]) (dagql.Nullable[dagql.ObjectResult[*TypeDef]], error) {
	null := dagql.Null[dagql.ObjectResult[*TypeDef]]()
	members, err := obj.Self().CollectionMembers()
	if err != nil || members == nil {
		return null, err
	}
	if len(obj.Self().Functions) == 1 {
		return null, nil
	}
	var batch dagql.ObjectResult[*ObjectTypeDef]
	name := obj.Self().Name + "_Batch"
	err = dag.Select(ctx, dag.Root(), &batch, dagql.Selector{
		Field: "__objectTypeDef", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}},
	}, dagql.Selector{Field: "__withName", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}}})
	if err != nil {
		return null, err
	}
	for _, fn := range obj.Self().Functions {
		if fn.Self().Name == members.Get.Name {
			continue
		}
		id, err := fn.ID()
		if err != nil {
			return null, err
		}
		if err := dag.Select(ctx, batch, &batch, dagql.Selector{
			Field: "__withFunction", Args: []dagql.NamedInput{{Name: "function", Value: dagql.NewID[*Function](id)}},
		}); err != nil {
			return null, err
		}
	}
	typ, err := collectionTypeRef(ctx, dag, batch)
	if err != nil {
		return null, err
	}
	return dagql.NonNull(typ), nil
}

// CollectionPublicObject exposes the engine operations to TypeDef consumers.
// The author definition remains unchanged for SDK calls and serialization.
func CollectionPublicObject(ctx context.Context, dag *dagql.Server, obj dagql.ObjectResult[*ObjectTypeDef]) (*ObjectTypeDef, error) {
	members, err := obj.Self().CollectionMembers()
	if err != nil {
		return nil, fmt.Errorf("project collection %q: %w", obj.Self().Name, err)
	}
	if members == nil {
		return nil, fmt.Errorf("object %q is not a collection", obj.Self().Name)
	}
	projected := obj.Self().Clone()
	projected.Collection = nil
	projected.Fields = nil
	projected.Functions = nil
	projected.Constructor = dagql.Null[dagql.ObjectResult[*Function]]()
	addField := func(name string, typ dagql.ObjectResult[*TypeDef]) error {
		id, err := typ.ID()
		if err != nil {
			return err
		}
		var field dagql.ObjectResult[*FieldTypeDef]
		err = dag.Select(ctx, dag.Root(), &field, dagql.Selector{
			Field: "__fieldTypeDef", Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(name)}, {Name: "typeDef", Value: dagql.NewID[*TypeDef](id)},
			},
		})
		if err == nil {
			projected.Fields = append(projected.Fields, field)
		}
		return err
	}
	listID, err := members.Get.ReturnType.ID()
	if err != nil {
		return nil, err
	}
	var listType dagql.ObjectResult[*TypeDef]
	err = dag.Select(ctx, dag.Root(), &listType, dagql.Selector{Field: "typeDef"}, dagql.Selector{
		Field: "withListOf", Args: []dagql.NamedInput{{Name: "elementType", Value: dagql.NewID[*TypeDef](listID)}},
	})
	if err != nil {
		return nil, err
	}
	if err := addField("keys", members.Keys.TypeDef); err != nil {
		return nil, err
	}
	if err := addField("list", listType); err != nil {
		return nil, err
	}
	selfType, err := collectionTypeRef(ctx, dag, obj)
	if err != nil {
		return nil, err
	}
	for _, spec := range []struct {
		name, arg     string
		result, input dagql.ObjectResult[*TypeDef]
	}{
		{"get", "key", members.Get.ReturnType, members.Get.Args[0].Self().TypeDef},
		{"subset", "keys", selfType, members.Keys.TypeDef},
	} {
		resultID, err := spec.result.ID()
		if err != nil {
			return nil, err
		}
		inputID, err := spec.input.ID()
		if err != nil {
			return nil, err
		}
		var fn dagql.ObjectResult[*Function]
		err = dag.Select(ctx, dag.Root(), &fn, dagql.Selector{
			Field: "function", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(spec.name)}, {Name: "returnType", Value: dagql.NewID[*TypeDef](resultID)}},
		}, dagql.Selector{
			Field: "withArg", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(spec.arg)}, {Name: "typeDef", Value: dagql.NewID[*TypeDef](inputID)}},
		})
		if err != nil {
			return nil, err
		}
		projected.Functions = append(projected.Functions, fn)
	}
	batch, err := CollectionBatchType(ctx, dag, obj)
	if err != nil {
		return nil, err
	}
	if batch.Valid {
		if err := addField("batch", batch.Value); err != nil {
			return nil, err
		}
	}
	return projected, nil
}
