package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

type moduleObjectTestServer struct {
	*mockServer
	cache       *dagql.SessionCache
	dag         *dagql.Server
	root        *Query
	defaultDeps *ModDeps
}

type moduleObjectHandleTestObj struct {
	Value string
}

func (*moduleObjectHandleTestObj) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleObjectHandleTestObj",
		NonNull:   true,
	}
}

func installModuleObjectHandleTestObjClass(srv *dagql.Server) {
	class := dagql.NewClass(srv, dagql.ClassOpts[*moduleObjectHandleTestObj]{
		Typed: &moduleObjectHandleTestObj{},
	})
	class.Install(
		dagql.Field[*moduleObjectHandleTestObj]{
			Spec: &dagql.FieldSpec{
				Name: "value",
				Type: dagql.String(""),
			},
			Func: func(ctx context.Context, self dagql.ObjectResult[*moduleObjectHandleTestObj], _ map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
				return dagql.NewResultForCurrentCall(ctx, dagql.String(self.Self().Value))
			},
		},
	)
	srv.InstallObject(class)
}

type moduleObjectSemanticIfaceChild struct {
	Value string
}

func (*moduleObjectSemanticIfaceChild) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Child",
		NonNull:   true,
	}
}

func installModuleObjectSemanticIfaceChildClass(srv *dagql.Server) {
	class := dagql.NewClass(srv, dagql.ClassOpts[*moduleObjectSemanticIfaceChild]{
		Typed: &moduleObjectSemanticIfaceChild{},
	})
	class.Install(
		dagql.Field[*moduleObjectSemanticIfaceChild]{
			Spec: &dagql.FieldSpec{
				Name: "value",
				Type: dagql.String(""),
			},
			Func: func(ctx context.Context, self dagql.ObjectResult[*moduleObjectSemanticIfaceChild], _ map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
				return dagql.NewResultForCurrentCall(ctx, dagql.String(self.Self().Value))
			},
		},
	)
	srv.InstallObject(class)
}

func moduleObjectTestSyntheticCall(op string, typ dagql.Typed) *dagql.ResultCall {
	return &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType(typ.Type()),
	}
}

func installModuleObjectTestModuleClass(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Module]{Typed: &Module{}}))
}

func (s *moduleObjectTestServer) Cache(context.Context) (*dagql.SessionCache, error) {
	return s.cache, nil
}

func (s *moduleObjectTestServer) Server(context.Context) (*dagql.Server, error) {
	return s.dag, nil
}

func (s *moduleObjectTestServer) DefaultDeps(context.Context) (*ModDeps, error) {
	if s.defaultDeps != nil {
		return s.defaultDeps, nil
	}
	return NewModDeps(s.root, nil), nil
}

func TestModuleObjectAttachOwnedResultsRecurses(t *testing.T) {
	t.Parallel()

	direct, err := dagql.NewResultForCall(dagql.String("direct"), moduleObjectTestSyntheticCall("moduleObjectDirect", dagql.String("")))
	assert.NilError(t, err)
	listItem, err := dagql.NewResultForCall(dagql.String("list"), moduleObjectTestSyntheticCall("moduleObjectList", dagql.String("")))
	assert.NilError(t, err)
	nested, err := dagql.NewResultForCall(dagql.String("nested"), moduleObjectTestSyntheticCall("moduleObjectNested", dagql.String("")))
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
		recipeID, err := res.RecipeID()
		assert.NilError(t, err)
		attached := res.WithContentDigestAny(digest.FromString(recipeID.Digest().String())).(dagql.AnyResult)
		attachedByDigest[recipeID.Digest().String()] = attached
		return attached, nil
	}

	deps, err := obj.AttachOwnedResults(context.Background(), nil, attach)
	assert.NilError(t, err)
	assert.Equal(t, 3, len(deps))

	directField, ok := obj.Fields["direct"].(dagql.AnyResult)
	assert.Assert(t, ok)
	directRecipeID, err := direct.RecipeID()
	assert.NilError(t, err)
	expectedDirectID, err := attachedByDigest[directRecipeID.Digest().String()].RecipeID()
	assert.NilError(t, err)
	actualDirectID, err := directField.RecipeID()
	assert.NilError(t, err)
	assert.Equal(t, expectedDirectID.Digest(), actualDirectID.Digest())

	listField, ok := obj.Fields["list"].([]any)
	assert.Assert(t, ok)
	listRes, ok := listField[0].(dagql.AnyResult)
	assert.Assert(t, ok)
	listRecipeID, err := listItem.RecipeID()
	assert.NilError(t, err)
	expectedListID, err := attachedByDigest[listRecipeID.Digest().String()].RecipeID()
	assert.NilError(t, err)
	actualListID, err := listRes.RecipeID()
	assert.NilError(t, err)
	assert.Equal(t, expectedListID.Digest(), actualListID.Digest())

	nestedField, ok := obj.Fields["nested"].(map[string]any)
	assert.Assert(t, ok)
	nestedRes, ok := nestedField["child"].(dagql.AnyResult)
	assert.Assert(t, ok)
	nestedRecipeID, err := nested.RecipeID()
	assert.NilError(t, err)
	expectedNestedID, err := attachedByDigest[nestedRecipeID.Digest().String()].RecipeID()
	assert.NilError(t, err)
	actualNestedID, err := nestedRes.RecipeID()
	assert.NilError(t, err)
	assert.Equal(t, expectedNestedID.Digest(), actualNestedID.Digest())

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
		root:       root,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root, sc)
	testSrv.dag = dag

	ctx = ContextWithQuery(ctx, root)

	callFrame := moduleObjectTestSyntheticCall("persistedModuleObjectValue", dagql.String(""))
	initial, err := dagql.NewResultForCall(dagql.String("hello"), callFrame)
	assert.NilError(t, err)
	res, err := sc.GetOrInitCall(ctx, dag, &dagql.CallRequest{ResultCall: callFrame}, dagql.ValueFunc(initial))
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
	expectedID, err := initial.RecipeID()
	assert.NilError(t, err)
	actualID, err := decodedRes.RecipeID()
	assert.NilError(t, err)
	assert.Equal(t, expectedID.Digest(), actualID.Digest())
}

func TestModulePersistedTypeDefsRoundTripPreservesNullableValidity(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "")
	assert.NilError(t, err)
	sc := dagql.NewSessionCache(cacheIface)

	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root, sc)
	testSrv.dag = dag
	ctx = ContextWithQuery(ctx, root)

	nestedObjDef := NewObjectTypeDef("Nested", "", nil)
	ifaceDef := NewInterfaceTypeDef("Iface", "")
	enumDef := NewEnumTypeDef("Choice", "", nil)
	enumDef.Members = []*EnumMemberTypeDef{
		NewEnumMemberTypeDef("one", "one", "", nil, nil),
	}
	objDef := NewObjectTypeDef("Thing", "", nil)
	objDef.Fields = []*FieldTypeDef{
		{
			Name:         "child",
			OriginalName: "child",
			TypeDef: &TypeDef{
				Kind:     TypeDefKindObject,
				AsObject: dagql.NonNull(nestedObjDef),
			},
		},
		{
			Name:         "iface",
			OriginalName: "iface",
			TypeDef: &TypeDef{
				Kind:        TypeDefKindInterface,
				AsInterface: dagql.NonNull(ifaceDef),
			},
		},
		{
			Name:         "choice",
			OriginalName: "choice",
			TypeDef: &TypeDef{
				Kind:   TypeDefKindEnum,
				AsEnum: dagql.NonNull(enumDef),
			},
		},
	}

	mod := &Module{
		NameField:    "Test",
		OriginalName: "Test",
		SDKConfig:    &SDKConfig{},
		Deps:         NewModDeps(root, nil),
		ObjectDefs: []*TypeDef{
			{
				Kind:     TypeDefKindObject,
				AsObject: dagql.NonNull(objDef),
			},
		},
		InterfaceDefs: []*TypeDef{
			{
				Kind:        TypeDefKindInterface,
				AsInterface: dagql.NonNull(ifaceDef),
			},
		},
		EnumDefs: []*TypeDef{
			{
				Kind:   TypeDefKindEnum,
				AsEnum: dagql.NonNull(enumDef),
			},
		},
	}

	payload, err := mod.EncodePersistedObject(ctx, nil)
	assert.NilError(t, err)

	decodedTyped, err := (&Module{}).DecodePersistedObject(ctx, dag, 0, nil, payload)
	assert.NilError(t, err)
	decoded, ok := decodedTyped.(*Module)
	assert.Assert(t, ok)

	assert.Equal(t, 1, len(decoded.ObjectDefs))
	assert.Equal(t, 1, len(decoded.InterfaceDefs))
	assert.Equal(t, 1, len(decoded.EnumDefs))
	assert.Assert(t, decoded.ObjectDefs[0].AsObject.Valid)
	assert.Assert(t, decoded.InterfaceDefs[0].AsInterface.Valid)
	assert.Assert(t, decoded.EnumDefs[0].AsEnum.Valid)

	decodedFields := decoded.ObjectDefs[0].AsObject.Value.Fields
	assert.Equal(t, 3, len(decodedFields))
	assert.Assert(t, decodedFields[0].TypeDef.AsObject.Valid)
	assert.Assert(t, decodedFields[1].TypeDef.AsInterface.Valid)
	assert.Assert(t, decodedFields[2].TypeDef.AsEnum.Valid)
	assert.Equal(t, "Thing", decoded.ObjectDefs[0].AsObject.Value.Name)
	assert.Equal(t, "Iface", decoded.InterfaceDefs[0].AsInterface.Value.Name)
	assert.Equal(t, "Choice", decoded.EnumDefs[0].AsEnum.Value.Name)
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
		root:       root,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root, sc)
	testSrv.dag = dag
	ctx = ContextWithQuery(ctx, root)
	installModuleObjectTestModuleClass(dag)

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
	modRes, err := dagql.NewObjectResultForCall(mod, dag, moduleObjectTestSyntheticCall("moduleObjectConvertCurrentFieldIDModule", mod))
	assert.NilError(t, err)

	childDetached, err := dagql.NewObjectResultForCall(root, dag, moduleObjectTestSyntheticCall("staleRef", root))
	assert.NilError(t, err)
	childAny, err := sc.AttachResult(ctx, dag, childDetached)
	assert.NilError(t, err)
	child, ok := childAny.(dagql.ObjectResult[*Query])
	assert.Assert(t, ok)

	parentCall := moduleObjectTestSyntheticCall("parentObj", &ModuleObject{TypeDef: objDef})
	ctx = dagql.ContextWithCall(ctx, parentCall)

	objType := &ModuleObjectType{typeDef: objDef, mod: modRes}
	encoded, err := objType.ConvertToSDKInput(ctx, &ModuleObject{
		Module:  modRes,
		TypeDef: objDef,
		Fields: map[string]any{
			"ref": child,
		},
	})
	assert.NilError(t, err)

	fields, ok := encoded.(map[string]any)
	assert.Assert(t, ok)
	expectedID, err := child.ID()
	assert.NilError(t, err)
	expectedEnc, err := expectedID.Encode()
	assert.NilError(t, err)
	assert.Equal(t, expectedEnc, fields["ref"])
}

func TestModuleObjectConvertToSDKInputRewritesStoredResults(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "")
	assert.NilError(t, err)
	sc := dagql.NewSessionCache(cacheIface)
	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root, sc)
	testSrv.dag = dag
	ctx = ContextWithQuery(ctx, root)
	installModuleObjectTestModuleClass(dag)

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
	modRes, err := dagql.NewObjectResultForCall(mod, dag, moduleObjectTestSyntheticCall("moduleObjectRewriteStoredResultsModule", mod))
	assert.NilError(t, err)
	parentType := &ModuleObjectType{
		typeDef: parentObjDef,
		mod:     modRes,
	}
	parentCall := moduleObjectTestSyntheticCall("moduleObjectParent", &ModuleObject{TypeDef: parentObjDef})
	ctx = dagql.ContextWithCall(ctx, parentCall)

	childDetached, err := dagql.NewResultForCall(&ModuleObject{
		Module:  modRes,
		TypeDef: childObjDef,
		Fields: map[string]any{
			"name": "child",
		},
	}, moduleObjectTestSyntheticCall("moduleObjectChild", &ModuleObject{TypeDef: childObjDef}))
	assert.NilError(t, err)
	child, err := sc.AttachResult(ctx, dag, childDetached)
	assert.NilError(t, err)

	converted, err := parentType.ConvertToSDKInput(ctx, &ModuleObject{
		Module:  modRes,
		TypeDef: parentObjDef,
		Fields: map[string]any{
			"child":   child,
			"private": map[string]any{"ref": child},
		},
	})
	assert.NilError(t, err)

	fields, ok := converted.(map[string]any)
	assert.Assert(t, ok)
	childID, err := child.ID()
	assert.NilError(t, err)
	childEnc, err := childID.Encode()
	assert.NilError(t, err)
	assert.Equal(t, childEnc, fields["child"])

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
		root:       root,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root, sc)
	testSrv.dag = dag
	ctx = ContextWithQuery(ctx, root)
	installModuleObjectTestModuleClass(dag)

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
	modRes, err := dagql.NewObjectResultForCall(mod, dag, moduleObjectTestSyntheticCall("moduleObjectPersistedRefsModule", mod))
	assert.NilError(t, err)
	parentType := &ModuleObjectType{
		typeDef: parentObjDef,
		mod:     modRes,
	}
	parentCall := moduleObjectTestSyntheticCall("moduleObjectPersistedParent", &ModuleObject{TypeDef: parentObjDef})
	ctx = dagql.ContextWithCall(ctx, parentCall)

	childCall := moduleObjectTestSyntheticCall("moduleObjectPersistedChild", &ModuleObject{TypeDef: childObjDef})
	childInitial, err := dagql.NewResultForCall(&ModuleObject{
		Module:  modRes,
		TypeDef: childObjDef,
		Fields: map[string]any{
			"name": "child",
		},
	}, childCall)
	assert.NilError(t, err)
	child, err := sc.GetOrInitCall(ctx, dag, &dagql.CallRequest{ResultCall: childCall}, dagql.ValueFunc(childInitial))
	assert.NilError(t, err)

	obj := &ModuleObject{
		Module:  modRes,
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

	decodedTyped, err := obj.DecodePersistedObject(ctx, dag, 0, nil, payload)
	assert.NilError(t, err)
	decoded, ok := decodedTyped.(*ModuleObject)
	assert.Assert(t, ok)

	converted, err := parentType.ConvertToSDKInput(ctx, decoded)
	assert.NilError(t, err)
	fields, ok := converted.(map[string]any)
	assert.Assert(t, ok)
	childID, err := child.ID()
	assert.NilError(t, err)
	childEnc, err := childID.Encode()
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

func TestModuleObjectRawHandleFieldBecomesStaleAfterProducerSessionClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	baseCache, err := dagql.NewCache(ctx, "")
	assert.NilError(t, err)

	producerCache := dagql.NewSessionCache(baseCache)
	producerRoot := &Query{}
	producerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      producerCache,
		root:       producerRoot,
	}
	producerRoot.Server = producerSrv
	producerDag := dagql.NewServer(producerRoot, producerCache)
	producerSrv.dag = producerDag
	installModuleObjectHandleTestObjClass(producerDag)
	producerCtx := ContextWithQuery(ctx, producerRoot)

	childDetached, err := dagql.NewObjectResultForCall(
		&moduleObjectHandleTestObj{Value: "hello"},
		producerDag,
		&dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "module_object_handle_child",
			Type:        dagql.NewResultCallType((&moduleObjectHandleTestObj{}).Type()),
		},
	)
	assert.NilError(t, err)
	childAttachedAny, err := producerCache.AttachResult(producerCtx, producerDag, childDetached)
	assert.NilError(t, err)
	childAttached, ok := childAttachedAny.(dagql.ObjectResult[*moduleObjectHandleTestObj])
	assert.Assert(t, ok)

	childID, err := childAttached.ID()
	assert.NilError(t, err)
	childEnc, err := childID.Encode()
	assert.NilError(t, err)

	obj := &ModuleObject{
		Fields: map[string]any{
			"child": childEnc,
		},
	}
	deps, err := obj.AttachOwnedResults(ctx, nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		t.Fatalf("unexpected attach of %T", res)
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, len(deps))
	_, stillRawString := obj.Fields["child"].(string)
	assert.Assert(t, stillRawString)

	assert.NilError(t, producerCache.ReleaseAndClose(producerCtx))

	consumerCache := dagql.NewSessionCache(baseCache)
	consumerRoot := &Query{}
	consumerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      consumerCache,
		root:       consumerRoot,
	}
	consumerRoot.Server = consumerSrv
	consumerDag := dagql.NewServer(consumerRoot, consumerCache)
	consumerSrv.dag = consumerDag
	installModuleObjectHandleTestObjClass(consumerDag)
	consumerCtx := ContextWithQuery(ctx, consumerRoot)

	var staleID call.ID
	assert.NilError(t, staleID.Decode(childEnc))
	_, err = consumerDag.Load(consumerCtx, &staleID)
	assert.ErrorContains(t, err, "missing shared result")
}

func TestModuleObjectAttachOwnedResultsRetainsSemanticInterfaceHandleField(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	baseCache, err := dagql.NewCache(ctx, "")
	assert.NilError(t, err)

	ifaceDef := NewInterfaceTypeDef("Iface", "")
	childObjDef := NewObjectTypeDef("Child", "", nil)
	parentObjDef := NewObjectTypeDef("Parent", "", nil)
	parentObjDef.Fields = append(parentObjDef.Fields, &FieldTypeDef{
		Name:         "child",
		OriginalName: "child",
		TypeDef: (&TypeDef{
			Kind:        TypeDefKindInterface,
			AsInterface: dagql.NonNull(ifaceDef),
		}),
	})
	mod := &Module{
		NameField: "test",
		Deps:      NewModDeps(nil, nil),
		ObjectDefs: []*TypeDef{
			(&TypeDef{}).WithObject("Child", "", nil, nil),
			(&TypeDef{}).WithObject("Parent", "", nil, nil),
		},
		InterfaceDefs: []*TypeDef{{
			Kind:        TypeDefKindInterface,
			AsInterface: dagql.NonNull(ifaceDef),
		}},
	}
	mod.ObjectDefs[0].AsObject.Value = childObjDef
	mod.ObjectDefs[1].AsObject.Value = parentObjDef

	producerCache := dagql.NewSessionCache(baseCache)
	producerRoot := &Query{}
	producerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      producerCache,
		root:       producerRoot,
	}
	producerRoot.Server = producerSrv
	producerDag := dagql.NewServer(producerRoot, producerCache)
	producerSrv.dag = producerDag
	producerCtx := ContextWithQuery(ctx, producerRoot)
	mod.Deps = NewModDeps(producerRoot, nil)
	installModuleObjectTestModuleClass(producerDag)
	installModuleObjectSemanticIfaceChildClass(producerDag)

	producerModRes, err := dagql.NewObjectResultForCall(mod, producerDag, moduleObjectTestSyntheticCall("semanticHandleProducerModule", mod))
	assert.NilError(t, err)
	mod.Deps = NewModDeps(producerRoot, []Mod{NewUserMod(producerModRes)})
	producerSrv.defaultDeps = mod.Deps

	childCall := moduleObjectTestSyntheticCall("semanticHandleChild", &moduleObjectSemanticIfaceChild{})
	childDetached, err := dagql.NewObjectResultForCall(&moduleObjectSemanticIfaceChild{
		Value: "hello",
	}, producerDag, childCall)
	assert.NilError(t, err)
	childAttachedAny, err := producerCache.AttachResult(producerCtx, producerDag, childDetached)
	assert.NilError(t, err)
	childAttached, ok := childAttachedAny.(dagql.ObjectResult[*moduleObjectSemanticIfaceChild])
	assert.Assert(t, ok)
	childID, err := childAttached.ID()
	assert.NilError(t, err)
	childEnc, err := childID.Encode()
	assert.NilError(t, err)

	parentCall := moduleObjectTestSyntheticCall("semanticHandleParent", &ModuleObject{TypeDef: parentObjDef})
	parentDetached, err := dagql.NewResultForCall(&ModuleObject{
		Module:  producerModRes,
		TypeDef: parentObjDef,
		Fields: map[string]any{
			"child": childEnc,
		},
	}, parentCall)
	assert.NilError(t, err)
	_, err = producerCache.GetOrInitCall(
		producerCtx,
		producerDag,
		&dagql.CallRequest{
			ResultCall:    parentCall,
			IsPersistable: true,
		},
		dagql.ValueFunc(parentDetached),
	)
	assert.NilError(t, err)

	assert.NilError(t, producerCache.ReleaseAndClose(producerCtx))

	consumerCache := dagql.NewSessionCache(baseCache)
	consumerRoot := &Query{}
	consumerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      consumerCache,
		root:       consumerRoot,
	}
	consumerRoot.Server = consumerSrv
	consumerDag := dagql.NewServer(consumerRoot, consumerCache)
	consumerSrv.dag = consumerDag
	consumerCtx := ContextWithQuery(ctx, consumerRoot)
	installModuleObjectTestModuleClass(consumerDag)
	installModuleObjectSemanticIfaceChildClass(consumerDag)
	consumerModRes, err := dagql.NewObjectResultForCall(mod, consumerDag, moduleObjectTestSyntheticCall("semanticHandleConsumerModule", mod))
	assert.NilError(t, err)
	mod.Deps = NewModDeps(consumerRoot, []Mod{NewUserMod(consumerModRes)})
	consumerSrv.defaultDeps = mod.Deps

	var retainedID call.ID
	assert.NilError(t, retainedID.Decode(childEnc))
	_, err = consumerDag.Load(consumerCtx, &retainedID)
	assert.NilError(t, err)
}
