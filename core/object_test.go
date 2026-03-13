package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opencontainers/go-digest"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

type moduleObjectTestServer struct {
	*mockServer
	cache *dagql.SessionCache
	dag   *dagql.Server
}

func (s *moduleObjectTestServer) Cache(context.Context) (*dagql.SessionCache, error) {
	return s.cache, nil
}

func (s *moduleObjectTestServer) Server(context.Context) (*dagql.Server, error) {
	return s.dag, nil
}

func TestModuleObjectAttachOwnedResultsRecurses(t *testing.T) {
	t.Parallel()

	directID := call.New().Append(dagql.String("").Type(), "moduleObjectDirect")
	listID := call.New().Append(dagql.String("").Type(), "moduleObjectList")
	nestedID := call.New().Append(dagql.String("").Type(), "moduleObjectNested")

	direct, err := dagql.NewResultForID(dagql.String("direct"), directID)
	assert.NilError(t, err)
	listItem, err := dagql.NewResultForID(dagql.String("list"), listID)
	assert.NilError(t, err)
	nested, err := dagql.NewResultForID(dagql.String("nested"), nestedID)
	assert.NilError(t, err)

	obj := &ModuleObject{
		Fields: map[string]any{
			"direct": direct,
			"list":   []any{listItem},
			"nested": map[string]any{"child": nested},
			"scalar": "unchanged",
		},
	}

	attachedByDigest := map[string]dagql.AnyResult{}
	attach := func(res dagql.AnyResult) (dagql.AnyResult, error) {
		attached := res.WithContentDigestAny(digest.FromString(res.ID().Digest().String())).(dagql.AnyResult)
		attachedByDigest[res.ID().Digest().String()] = attached
		return attached, nil
	}

	deps, err := obj.AttachOwnedResults(context.Background(), attach)
	assert.NilError(t, err)
	assert.Equal(t, 3, len(deps))

	directField, ok := obj.Fields["direct"].(dagql.AnyResult)
	assert.Assert(t, ok)
	assert.Equal(t, attachedByDigest[directID.Digest().String()].ID().Digest(), directField.ID().Digest())

	listField, ok := obj.Fields["list"].([]any)
	assert.Assert(t, ok)
	listRes, ok := listField[0].(dagql.AnyResult)
	assert.Assert(t, ok)
	assert.Equal(t, attachedByDigest[listID.Digest().String()].ID().Digest(), listRes.ID().Digest())

	nestedField, ok := obj.Fields["nested"].(map[string]any)
	assert.Assert(t, ok)
	nestedRes, ok := nestedField["child"].(dagql.AnyResult)
	assert.Assert(t, ok)
	assert.Equal(t, attachedByDigest[nestedID.Digest().String()].ID().Digest(), nestedRes.ID().Digest())

	assert.Equal(t, "unchanged", obj.Fields["scalar"])
}

func TestDecodePersistedModuleObjectValueResultRefLoadsResult(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "")
	assert.NilError(t, err)
	sc := dagql.NewSessionCache(cacheIface)

	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root, sc)
	testSrv.dag = dag

	ctx = ContextWithQuery(ctx, root)

	id := call.New().Append(dagql.String("").Type(), "persistedModuleObjectValue")
	initial, err := dagql.NewResultForID(dagql.String("hello"), id)
	assert.NilError(t, err)
	res, err := sc.GetOrInitCall(ctx, dagql.CacheKey{ID: id}, dagql.ValueFunc(initial))
	assert.NilError(t, err)
	resultID, err := sc.PersistedResultID(res)
	assert.NilError(t, err)

	decoded, err := decodePersistedModuleObjectValue(ctx, dag, persistedModuleObjectValue{
		Kind:     persistedModuleObjectValueKindResultRef,
		ResultID: resultID,
	})
	assert.NilError(t, err)
	decodedRes, ok := decoded.(dagql.AnyResult)
	assert.Assert(t, ok)
	assert.Assert(t, decodedRes != nil)
	assert.Equal(t, id.Digest(), decodedRes.ID().Digest())
}

func TestModuleObjectConvertToSDKInputUsesCurrentFieldID(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "")
	assert.NilError(t, err)
	sc := dagql.NewSessionCache(cacheIface)

	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root, sc)
	testSrv.dag = dag
	ctx = ContextWithQuery(ctx, root)

	ifaceDef := &InterfaceTypeDef{
		Name:         "Iface",
		OriginalName: "Iface",
	}
	fieldType := &TypeDef{
		Kind:        TypeDefKindInterface,
		AsInterface: dagql.NonNull(ifaceDef),
	}
	fieldDef := &FieldTypeDef{
		Name:         "ref",
		OriginalName: "ref",
		TypeDef:      fieldType,
	}
	objDef := &ObjectTypeDef{
		Name:         "Obj",
		OriginalName: "Obj",
		Fields:       []*FieldTypeDef{fieldDef},
	}
	mod := &Module{
		Deps: NewModDeps(root, nil),
		ObjectDefs: []*TypeDef{{
			Kind:     TypeDefKindObject,
			AsObject: dagql.NonNull(objDef),
		}},
		InterfaceDefs: []*TypeDef{{
			Kind:        TypeDefKindInterface,
			AsInterface: dagql.NonNull(ifaceDef),
		}},
	}

	staleID := call.New().Append(root.Type(), "staleRef")
	child, err := dagql.NewObjectResultForID(root, dag, staleID)
	assert.NilError(t, err)

	parentID := call.New().Append((&ModuleObject{TypeDef: objDef}).Type(), "parentObj")
	ctx = dagql.ContextWithID(ctx, parentID)

	objType := &ModuleObjectType{typeDef: objDef, mod: mod}
	encoded, err := objType.ConvertToSDKInput(ctx, &ModuleObject{
		Module:  mod,
		TypeDef: objDef,
		Fields: map[string]any{
			"ref": child,
		},
	})
	assert.NilError(t, err)

	fields, ok := encoded.(map[string]any)
	assert.Assert(t, ok)
	expectedID, err := moduleObjectFieldID(ctx, parentID, nil, fieldDef)
	assert.NilError(t, err)
	expectedEnc, err := expectedID.Encode()
	assert.NilError(t, err)
	assert.Equal(t, expectedEnc, fields["ref"])
}

func TestModuleObjectConvertToSDKInputRewritesStoredResults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	childObjDef := NewObjectTypeDef("Child", "", nil)
	parentObjDef := NewObjectTypeDef("Parent", "", nil)
	parentObjDef.Fields = append(parentObjDef.Fields, &FieldTypeDef{
		Name:         "child",
		OriginalName: "child",
		TypeDef:      (&TypeDef{}).WithObject("Child", "", nil, nil),
	})
	mod := &Module{
		NameField: "test",
		Deps:      NewModDeps(nil, nil),
		ObjectDefs: []*TypeDef{
			(&TypeDef{}).WithObject("Child", "", nil, nil),
			(&TypeDef{}).WithObject("Parent", "", nil, nil),
		},
	}
	mod.ObjectDefs[0].AsObject.Value = childObjDef
	mod.ObjectDefs[1].AsObject.Value = parentObjDef
	parentType := &ModuleObjectType{
		typeDef: parentObjDef,
		mod:     mod,
	}
	parentID := call.New().Append((&ModuleObject{TypeDef: parentObjDef}).Type(), "moduleObjectParent")
	ctx = dagql.ContextWithID(ctx, parentID)

	childID := call.New().Append(dagql.String("").Type(), "moduleObjectChild")
	child, err := dagql.NewResultForID(&ModuleObject{
		Module:  mod,
		TypeDef: childObjDef,
		Fields: map[string]any{
			"name": "child",
		},
	}, childID)
	assert.NilError(t, err)

	converted, err := parentType.ConvertToSDKInput(ctx, &ModuleObject{
		Module:  mod,
		TypeDef: parentObjDef,
		Fields: map[string]any{
			"child":   child,
			"private": map[string]any{"ref": child},
		},
	})
	assert.NilError(t, err)

	fields, ok := converted.(map[string]any)
	assert.Assert(t, ok)
	childEnc, err := childID.Encode()
	assert.NilError(t, err)
	fieldID, err := moduleObjectFieldID(ctx, parentID, nil, parentObjDef.Fields[0])
	assert.NilError(t, err)
	fieldEnc, err := fieldID.Encode()
	assert.NilError(t, err)
	assert.Equal(t, fieldEnc, fields["child"])

	privateFields, ok := fields["private"].(map[string]any)
	assert.Assert(t, ok)
	assert.Equal(t, childEnc, privateFields["ref"])
}

func TestModuleObjectPersistedResultRefsRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "")
	assert.NilError(t, err)
	sc := dagql.NewSessionCache(cacheIface)

	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root, sc)
	testSrv.dag = dag
	ctx = ContextWithQuery(ctx, root)

	childObjDef := NewObjectTypeDef("Child", "", nil)
	parentObjDef := NewObjectTypeDef("Parent", "", nil)
	parentObjDef.Fields = append(parentObjDef.Fields, &FieldTypeDef{
		Name:         "child",
		OriginalName: "child",
		TypeDef:      (&TypeDef{}).WithObject("Child", "", nil, nil),
	})
	mod := &Module{
		NameField: "test",
		Deps:      NewModDeps(nil, nil),
		ObjectDefs: []*TypeDef{
			(&TypeDef{}).WithObject("Child", "", nil, nil),
			(&TypeDef{}).WithObject("Parent", "", nil, nil),
		},
	}
	mod.ObjectDefs[0].AsObject.Value = childObjDef
	mod.ObjectDefs[1].AsObject.Value = parentObjDef
	parentType := &ModuleObjectType{
		typeDef: parentObjDef,
		mod:     mod,
	}
	parentID := call.New().Append((&ModuleObject{TypeDef: parentObjDef}).Type(), "moduleObjectPersistedParent")
	ctx = dagql.ContextWithID(ctx, parentID)

	childID := call.New().Append((&ModuleObject{TypeDef: childObjDef}).Type(), "moduleObjectPersistedChild")
	childInitial, err := dagql.NewResultForID(&ModuleObject{
		Module:  mod,
		TypeDef: childObjDef,
		Fields: map[string]any{
			"name": "child",
		},
	}, childID)
	assert.NilError(t, err)
	child, err := sc.GetOrInitCall(ctx, dagql.CacheKey{ID: childID}, dagql.ValueFunc(childInitial))
	assert.NilError(t, err)

	obj := &ModuleObject{
		Module:  mod,
		TypeDef: parentObjDef,
		Fields: map[string]any{
			"child": child,
		},
	}
	payload, err := obj.EncodePersistedObject(ctx, sc)
	assert.NilError(t, err)

	var persisted persistedModuleObjectPayload
	assert.NilError(t, json.Unmarshal(payload, &persisted))
	assert.Equal(t, persistedModuleObjectValueKindResultRef, persisted.Fields["child"].Kind)

	decodedTyped, err := obj.DecodePersistedObject(ctx, dag, nil, payload)
	assert.NilError(t, err)
	decoded, ok := decodedTyped.(*ModuleObject)
	assert.Assert(t, ok)

	converted, err := parentType.ConvertToSDKInput(ctx, decoded)
	assert.NilError(t, err)
	fields, ok := converted.(map[string]any)
	assert.Assert(t, ok)
	fieldID, err := moduleObjectFieldID(ctx, parentID, nil, parentObjDef.Fields[0])
	assert.NilError(t, err)
	childEnc, err := fieldID.Encode()
	assert.NilError(t, err)
	assert.Equal(t, childEnc, fields["child"])
}

func TestModuleObjectEncodeRejectsRawCallIDInSemanticField(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	childObjDef := NewObjectTypeDef("Child", "", nil)
	parentObjDef := NewObjectTypeDef("Parent", "", nil)
	parentObjDef.Fields = append(parentObjDef.Fields, &FieldTypeDef{
		Name:         "child",
		OriginalName: "child",
		TypeDef:      (&TypeDef{}).WithObject("Child", "", nil, nil),
	})
	mod := &Module{
		NameField: "test",
		Deps:      NewModDeps(nil, nil),
		ObjectDefs: []*TypeDef{
			(&TypeDef{}).WithObject("Child", "", nil, nil),
			(&TypeDef{}).WithObject("Parent", "", nil, nil),
		},
	}
	mod.ObjectDefs[0].AsObject.Value = childObjDef
	mod.ObjectDefs[1].AsObject.Value = parentObjDef

	childID := call.New().Append((&ModuleObject{TypeDef: childObjDef}).Type(), "moduleObjectRawCallID")
	obj := &ModuleObject{
		Module:  mod,
		TypeDef: parentObjDef,
		Fields: map[string]any{
			"child": childID,
		},
	}

	_, err := obj.EncodePersistedObject(ctx, nil)
	assert.ErrorContains(t, err, "unexpected raw call ID in semantic field")
}

func TestModuleObjectEncodeAllowsRawCallIDInPrivateField(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	objDef := NewObjectTypeDef("Parent", "", nil)
	mod := &Module{
		NameField:  "test",
		Deps:       NewModDeps(nil, nil),
		ObjectDefs: []*TypeDef{(&TypeDef{}).WithObject("Parent", "", nil, nil)},
	}
	mod.ObjectDefs[0].AsObject.Value = objDef

	refID := call.New().Append(dagql.String("").Type(), "moduleObjectPrivateCallID")
	obj := &ModuleObject{
		Module:  mod,
		TypeDef: objDef,
		Fields: map[string]any{
			"private": refID,
		},
	}

	payload, err := obj.EncodePersistedObject(ctx, nil)
	assert.NilError(t, err)

	var persisted persistedModuleObjectPayload
	assert.NilError(t, json.Unmarshal(payload, &persisted))
	assert.Equal(t, persistedModuleObjectValueKindCallID, persisted.Fields["private"].Kind)
}
