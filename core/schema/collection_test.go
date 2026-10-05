package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestCollectionSchema(t *testing.T) {
	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "collection-schema", SessionID: "collection-schema"})
	server := &currentTypeDefsTestServer{}
	root := core.NewRoot(server)
	ctx = core.ContextWithQuery(ctx, root)
	base, err := NewCoreSchemaBase(ctx, server)
	require.NoError(t, err)
	dag, err := base.Fork(ctx, root, "v1.0.0")
	require.NoError(t, err)
	server.dag = dag

	var key, keys, item, collection dagql.ObjectResult[*core.TypeDef]
	require.NoError(t, dag.Select(ctx, dag.Root(), &key,
		dagql.Selector{Field: "typeDef"},
		dagql.Selector{Field: "withKind", Args: []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindString}}}))
	keyID, err := key.ID()
	require.NoError(t, err)
	require.NoError(t, dag.Select(ctx, dag.Root(), &keys,
		dagql.Selector{Field: "typeDef"},
		dagql.Selector{Field: "withListOf", Args: []dagql.NamedInput{{Name: "elementType", Value: dagql.NewID[*core.TypeDef](keyID)}}}))
	keysID, err := keys.ID()
	require.NoError(t, err)
	require.NoError(t, dag.Select(ctx, dag.Root(), &item,
		dagql.Selector{Field: "typeDef"},
		dagql.Selector{Field: "withObject", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String("Item")}}}))
	itemID, err := item.ID()
	require.NoError(t, err)
	var get, batch dagql.ObjectResult[*core.Function]
	require.NoError(t, dag.Select(ctx, dag.Root(), &get,
		dagql.Selector{Field: "function", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String("lookup")}, {Name: "returnType", Value: dagql.NewID[*core.TypeDef](itemID)}}},
		dagql.Selector{Field: "withArg", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String("name")}, {Name: "typeDef", Value: dagql.NewID[*core.TypeDef](keyID)}}}))
	getID, err := get.ID()
	require.NoError(t, err)
	require.NoError(t, dag.Select(ctx, dag.Root(), &batch,
		dagql.Selector{Field: "function", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String("summary")}, {Name: "returnType", Value: dagql.NewID[*core.TypeDef](keyID)}}}))
	batchID, err := batch.ID()
	require.NoError(t, err)
	require.NoError(t, dag.Select(ctx, dag.Root(), &collection,
		dagql.Selector{Field: "typeDef"},
		dagql.Selector{Field: "withObject", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String("Items")}}},
		dagql.Selector{Field: "withCollection"},
		dagql.Selector{Field: "withCollectionKeys", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String("names")}}},
		dagql.Selector{Field: "withCollectionGet", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String("lookup")}}},
		dagql.Selector{Field: "withField", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String("names")}, {Name: "typeDef", Value: dagql.NewID[*core.TypeDef](keysID)}}},
		dagql.Selector{Field: "withFunction", Args: []dagql.NamedInput{{Name: "function", Value: dagql.NewID[*core.Function](getID)}}},
		dagql.Selector{Field: "withFunction", Args: []dagql.NamedInput{{Name: "function", Value: dagql.NewID[*core.Function](batchID)}}}))

	var metadata dagql.ObjectResult[*core.CollectionTypeDef]
	require.NoError(t, dag.Select(ctx, collection, &metadata, dagql.Selector{Field: "asCollection"}))
	var gotKey, gotValue, gotBatch dagql.ObjectResult[*core.TypeDef]
	require.NoError(t, dag.Select(ctx, metadata, &gotKey, dagql.Selector{Field: "keyType"}))
	require.NoError(t, dag.Select(ctx, metadata, &gotValue, dagql.Selector{Field: "valueType"}))
	require.NoError(t, dag.Select(ctx, metadata, &gotBatch, dagql.Selector{Field: "batchType"}))
	require.Equal(t, core.TypeDefKindString, gotKey.Self().Kind)
	require.Equal(t, "Item", gotValue.Self().AsObject.Value.Self().Name)
	require.Equal(t, "Items_Batch", gotBatch.Self().AsObject.Value.Self().Name)
	require.Len(t, gotBatch.Self().AsObject.Value.Self().Functions, 1)

	var public dagql.ObjectResult[*core.ObjectTypeDef]
	require.NoError(t, dag.Select(ctx, collection, &public, dagql.Selector{Field: "asObject"}))
	var fields, functions []string
	for _, field := range public.Self().Fields {
		fields = append(fields, field.Self().Name)
	}
	for _, fn := range public.Self().Functions {
		functions = append(functions, fn.Self().Name)
	}
	require.Equal(t, []string{"keys", "list", "batch"}, fields)
	require.Equal(t, []string{"get", "subset"}, functions)
	require.Equal(t, "key", public.Self().Functions[0].Self().Args[0].Self().Name)
	require.Equal(t, "names", collection.Self().AsObject.Value.Self().Fields[0].Self().Name)
}
