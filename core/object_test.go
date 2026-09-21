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
	"github.com/dagger/dagger/engine"
)

type moduleObjectTestServer struct {
	*mockServer
	cache       *dagql.Cache
	dag         *dagql.Server
	root        *Query
	defaultDeps *SchemaBuilder
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

func moduleObjectTestSyntheticCall(op string, typ dagql.Typed) *dagql.ResultCall {
	return &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType(typ.Type()),
	}
}

func installModuleObjectTestModuleClass(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Module]{Typed: &Module{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleSource]{Typed: &ModuleSource{}}))
	dagql.Fields[*Module]{
		dagql.Func("_implementationScoped", func(_ context.Context, self *Module, _ struct{}) (*Module, error) {
			return self, nil
		}),
	}.Install(srv)
}

func (s *moduleObjectTestServer) Cache(context.Context) (*dagql.Cache, error) {
	return s.cache, nil
}

func (s *moduleObjectTestServer) Server(context.Context) (*dagql.Server, error) {
	return s.dag, nil
}

func (s *moduleObjectTestServer) DefaultDeps(context.Context) (*SchemaBuilder, error) {
	if s.defaultDeps != nil {
		return s.defaultDeps, nil
	}
	return NewSchemaBuilder(s.root, nil), nil
}

func TestModuleObjectAttachDependencyResultsRecurses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cacheIface, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = dagql.ContextWithCache(ctx, cacheIface)

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
		recipeID, err := res.RecipeID(ctx)
		assert.NilError(t, err)
		attached, err := res.WithContentDigestAny(ctx, digest.FromString(recipeID.Digest().String()))
		assert.NilError(t, err)
		attachedByDigest[recipeID.Digest().String()] = attached
		return attached, nil
	}

	deps, err := obj.AttachDependencyResults(ctx, nil, attach)
	assert.NilError(t, err)
	assert.Equal(t, 3, len(deps))

	directField, ok := obj.Fields["direct"].(dagql.AnyResult)
	assert.Assert(t, ok)
	directRecipeID, err := direct.RecipeID(ctx)
	assert.NilError(t, err)
	expectedDirectID, err := attachedByDigest[directRecipeID.Digest().String()].RecipeID(ctx)
	assert.NilError(t, err)
	actualDirectID, err := directField.RecipeID(ctx)
	assert.NilError(t, err)
	assert.Equal(t, expectedDirectID.Digest(), actualDirectID.Digest())

	listField, ok := obj.Fields["list"].([]any)
	assert.Assert(t, ok)
	listRes, ok := listField[0].(dagql.AnyResult)
	assert.Assert(t, ok)
	listRecipeID, err := listItem.RecipeID(ctx)
	assert.NilError(t, err)
	expectedListID, err := attachedByDigest[listRecipeID.Digest().String()].RecipeID(ctx)
	assert.NilError(t, err)
	actualListID, err := listRes.RecipeID(ctx)
	assert.NilError(t, err)
	assert.Equal(t, expectedListID.Digest(), actualListID.Digest())

	nestedField, ok := obj.Fields["nested"].(map[string]any)
	assert.Assert(t, ok)
	nestedRes, ok := nestedField["child"].(dagql.AnyResult)
	assert.Assert(t, ok)
	nestedRecipeID, err := nested.RecipeID(ctx)
	assert.NilError(t, err)
	expectedNestedID, err := attachedByDigest[nestedRecipeID.Digest().String()].RecipeID(ctx)
	assert.NilError(t, err)
	actualNestedID, err := nestedRes.RecipeID(ctx)
	assert.NilError(t, err)
	assert.Equal(t, expectedNestedID.Digest(), actualNestedID.Digest())

	assert.Equal(t, "unchanged", obj.Fields["scalar"])
}

func TestDecodePersistedModuleObjectValueResultRefLoadsResult(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = dagql.ContextWithCache(ctx, cacheIface)
	sc := cacheIface

	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := newCoreDagqlServerForTest(t, root)
	testSrv.dag = dag

	ctx = ContextWithQuery(ctx, root)

	callFrame := moduleObjectTestSyntheticCall("persistedModuleObjectValue", dagql.String(""))
	initial, err := dagql.NewResultForCall(dagql.String("hello"), callFrame)
	assert.NilError(t, err)
	res, err := sc.GetOrInitCall(ctx, "test-session", dag, &dagql.CallRequest{ResultCall: callFrame}, dagql.ValueFunc(initial))
	assert.NilError(t, err)
	resultID, err := sc.PersistedResultID(res)
	assert.NilError(t, err)

	decoded, err := decodePersistedModuleObjectValue(ctx, dagql.NewPersistDecodeContext(dag, 0, nil), persistedModuleObjectValue{
		Kind:     persistedModuleObjectValueKindResultRef,
		ResultID: resultID,
	})
	assert.NilError(t, err)
	decodedRes, ok := decoded.(dagql.AnyResult)
	assert.Assert(t, ok)
	assert.Assert(t, decodedRes != nil)
	expectedID, err := initial.RecipeID(ctx)
	assert.NilError(t, err)
	actualID, err := decodedRes.RecipeID(ctx)
	assert.NilError(t, err)
	assert.Equal(t, expectedID.Digest(), actualID.Digest())
}

func TestModulePersistedTypeDefsRoundTripPreservesNullableValidity(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	sc := cacheIface

	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := newCoreDagqlServerForTest(t, root)
	testSrv.dag = dag
	installTypeDefTestClasses(dag)
	ctx = dagql.ContextWithCache(ctx, sc)
	ctx = ContextWithQuery(ctx, root)

	nestedObjDef := newTypeDefAttachedResult(t, ctx, sc, dag, "nestedObjectTypeDef", NewObjectTypeDef("Nested", "", nil))
	ifaceDef := newTypeDefAttachedResult(t, ctx, sc, dag, "ifaceTypeDef", NewInterfaceTypeDef("Iface", ""))
	enumMember := newTypeDefAttachedResult(t, ctx, sc, dag, "enumMemberTypeDef", NewEnumMemberTypeDef("one", "one", "", nil, dagql.ObjectResult[*SourceMap]{}))
	enumDefSelf := NewEnumTypeDef("Choice", "", dagql.ObjectResult[*SourceMap]{})
	enumDefSelf.Members = dagql.ObjectResultArray[*EnumMemberTypeDef]{enumMember}
	enumDef := newTypeDefAttachedResult(t, ctx, sc, dag, "enumTypeDef", enumDefSelf)

	childTypeDef := newTypeDefAttachedResult(t, ctx, sc, dag, "childFieldTypeDef", (&TypeDef{}).WithObjectTypeDef(nestedObjDef))
	ifaceTypeDef := newTypeDefAttachedResult(t, ctx, sc, dag, "ifaceFieldTypeDef", (&TypeDef{}).WithInterfaceTypeDef(ifaceDef))
	choiceTypeDef := newTypeDefAttachedResult(t, ctx, sc, dag, "choiceFieldTypeDef", (&TypeDef{}).WithEnumTypeDef(enumDef))

	childField := newTypeDefAttachedResult(t, ctx, sc, dag, "childField", NewFieldTypeDef("child", childTypeDef, "", nil))
	ifaceField := newTypeDefAttachedResult(t, ctx, sc, dag, "ifaceField", NewFieldTypeDef("iface", ifaceTypeDef, "", nil))
	choiceField := newTypeDefAttachedResult(t, ctx, sc, dag, "choiceField", NewFieldTypeDef("choice", choiceTypeDef, "", nil))

	objDefSelf := NewObjectTypeDef("Thing", "", nil)
	objDefSelf.Fields = dagql.ObjectResultArray[*FieldTypeDef]{childField, ifaceField, choiceField}
	objDef := newTypeDefAttachedResult(t, ctx, sc, dag, "thingObjectTypeDef", objDefSelf)

	objTypeDef := newTypeDefAttachedResult(t, ctx, sc, dag, "thingTypeDef", (&TypeDef{}).WithObjectTypeDef(objDef))
	ifaceTypeDefTop := newTypeDefAttachedResult(t, ctx, sc, dag, "ifaceTopTypeDef", (&TypeDef{}).WithInterfaceTypeDef(ifaceDef))
	enumTypeDefTop := newTypeDefAttachedResult(t, ctx, sc, dag, "enumTopTypeDef", (&TypeDef{}).WithEnumTypeDef(enumDef))

	mod := &Module{
		NameField:     "Test",
		OriginalName:  "Test",
		SDKConfig:     &SDKConfig{},
		Deps:          NewSchemaBuilder(root, nil),
		ObjectDefs:    dagql.ObjectResultArray[*TypeDef]{objTypeDef},
		InterfaceDefs: dagql.ObjectResultArray[*TypeDef]{ifaceTypeDefTop},
		EnumDefs:      dagql.ObjectResultArray[*TypeDef]{enumTypeDefTop},
	}

	payload, err := mod.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(sc, 0, nil))
	assert.NilError(t, err)

	decodedTyped, err := (&Module{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(dag, 0, nil), payload.JSON)
	assert.NilError(t, err)
	decoded, ok := decodedTyped.(*Module)
	assert.Assert(t, ok)

	assert.Equal(t, 1, len(decoded.ObjectDefs))
	assert.Equal(t, 1, len(decoded.InterfaceDefs))
	assert.Equal(t, 1, len(decoded.EnumDefs))
	assert.Assert(t, decoded.ObjectDefs[0].Self().AsObject.Valid)
	assert.Assert(t, decoded.InterfaceDefs[0].Self().AsInterface.Valid)
	assert.Assert(t, decoded.EnumDefs[0].Self().AsEnum.Valid)

	decodedFields := decoded.ObjectDefs[0].Self().AsObject.Value.Self().Fields
	assert.Equal(t, 3, len(decodedFields))
	assert.Assert(t, decodedFields[0].Self().TypeDef.Self().AsObject.Valid)
	assert.Assert(t, decodedFields[1].Self().TypeDef.Self().AsInterface.Valid)
	assert.Assert(t, decodedFields[2].Self().TypeDef.Self().AsEnum.Valid)
	assert.Equal(t, "Thing", decoded.ObjectDefs[0].Self().AsObject.Value.Self().Name)
	assert.Equal(t, "Iface", decoded.InterfaceDefs[0].Self().AsInterface.Value.Self().Name)
	assert.Equal(t, "Choice", decoded.EnumDefs[0].Self().AsEnum.Value.Self().Name)
}

func TestModuleObjectConvertToSDKInputUsesCurrentFieldID(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	sc := cacheIface

	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := newCoreDagqlServerForTest(t, root)
	testSrv.dag = dag
	ctx = dagql.ContextWithCache(ctx, sc)
	ctx = ContextWithQuery(ctx, root)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "module-object-current-field-client",
		SessionID: "module-object-current-field-session",
	})
	installModuleObjectTestModuleClass(dag)
	installTypeDefTestClasses(dag)

	ifaceDef := &InterfaceTypeDef{
		Name:         "Iface",
		OriginalName: "Iface",
	}
	ifaceDefRes := newTypeDefDetachedResult(t, dag, "moduleObjectIface", ifaceDef)
	fieldType := newTypeDefDetachedResult(t, dag, "moduleObjectFieldType", (&TypeDef{}).WithInterfaceTypeDef(ifaceDefRes))
	fieldDef := newTypeDefDetachedResult(t, dag, "moduleObjectField", NewFieldTypeDef("ref", fieldType, "", nil))
	objDef := &ObjectTypeDef{
		Name:         "Obj",
		OriginalName: "Obj",
		Fields:       dagql.ObjectResultArray[*FieldTypeDef]{fieldDef},
	}
	objDefRes := newTypeDefDetachedResult(t, dag, "moduleObjectObj", objDef)
	mod := &Module{
		Deps: NewSchemaBuilder(root, nil),
		ObjectDefs: dagql.ObjectResultArray[*TypeDef]{
			newTypeDefDetachedResult(t, dag, "moduleObjectObjTypeDef", (&TypeDef{}).WithObjectTypeDef(objDefRes)),
		},
		InterfaceDefs: dagql.ObjectResultArray[*TypeDef]{
			newTypeDefDetachedResult(t, dag, "moduleObjectIfaceTypeDef", (&TypeDef{}).WithInterfaceTypeDef(ifaceDefRes)),
		},
	}
	modRes, err := dagql.NewObjectResultForCall(mod, dag, moduleObjectTestSyntheticCall("moduleObjectConvertCurrentFieldIDModule", mod))
	assert.NilError(t, err)

	childDetached, err := dagql.NewObjectResultForCall(root, dag, moduleObjectTestSyntheticCall("staleRef", root))
	assert.NilError(t, err)
	childAny, err := sc.AttachResult(ctx, "test-session", dag, childDetached)
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
	cacheIface, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	sc := cacheIface
	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := newCoreDagqlServerForTest(t, root)
	testSrv.dag = dag
	ctx = dagql.ContextWithCache(ctx, sc)
	ctx = ContextWithQuery(ctx, root)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "module-object-rewrite-client",
		SessionID: "module-object-rewrite-session",
	})
	installModuleObjectTestModuleClass(dag)
	installTypeDefTestClasses(dag)

	childObjDef := NewObjectTypeDef("Child", "", nil)
	childObjDefRes := newTypeDefDetachedResult(t, dag, "moduleObjectRewriteChildObj", childObjDef)
	parentObjDef := NewObjectTypeDef("Parent", "", nil)
	childTypeDef := newTypeDefDetachedResult(t, dag, "moduleObjectRewriteChildTypeDef", (&TypeDef{}).WithObjectTypeDef(childObjDefRes))
	parentObjDef.Fields = append(parentObjDef.Fields, newTypeDefDetachedResult(t, dag, "moduleObjectRewriteChildField", NewFieldTypeDef("child", childTypeDef, "", nil)))
	parentObjDefRes := newTypeDefDetachedResult(t, dag, "moduleObjectRewriteParentObj", parentObjDef)
	mod := &Module{
		NameField: "test",
		Deps:      NewSchemaBuilder(nil, nil),
		ObjectDefs: dagql.ObjectResultArray[*TypeDef]{
			newTypeDefDetachedResult(t, dag, "moduleObjectRewriteChildTopTypeDef", (&TypeDef{}).WithObjectTypeDef(childObjDefRes)),
			newTypeDefDetachedResult(t, dag, "moduleObjectRewriteParentTopTypeDef", (&TypeDef{}).WithObjectTypeDef(parentObjDefRes)),
		},
	}
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
	child, err := sc.AttachResult(ctx, "test-session", dag, childDetached)
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
	cacheIface, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	sc := cacheIface

	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := newCoreDagqlServerForTest(t, root)
	testSrv.dag = dag
	ctx = dagql.ContextWithCache(ctx, sc)
	ctx = ContextWithQuery(ctx, root)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "module-object-persisted-client",
		SessionID: "module-object-persisted-session",
	})
	installModuleObjectTestModuleClass(dag)
	installTypeDefTestClasses(dag)

	childObjDef := NewObjectTypeDef("Child", "", nil)
	childObjDefRes := newTypeDefDetachedResult(t, dag, "moduleObjectPersistedChildObj", childObjDef)
	parentObjDef := NewObjectTypeDef("Parent", "", nil)
	childTypeDef := newTypeDefDetachedResult(t, dag, "moduleObjectPersistedChildTypeDef", (&TypeDef{}).WithObjectTypeDef(childObjDefRes))
	parentObjDef.Fields = append(parentObjDef.Fields, newTypeDefDetachedResult(t, dag, "moduleObjectPersistedChildField", NewFieldTypeDef("child", childTypeDef, "", nil)))
	parentObjDefRes := newTypeDefDetachedResult(t, dag, "moduleObjectPersistedParentObj", parentObjDef)
	mod := &Module{
		NameField: "test",
		Deps:      NewSchemaBuilder(nil, nil),
		ObjectDefs: dagql.ObjectResultArray[*TypeDef]{
			newTypeDefDetachedResult(t, dag, "moduleObjectPersistedChildTopTypeDef", (&TypeDef{}).WithObjectTypeDef(childObjDefRes)),
			newTypeDefDetachedResult(t, dag, "moduleObjectPersistedParentTopTypeDef", (&TypeDef{}).WithObjectTypeDef(parentObjDefRes)),
		},
	}
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
	child, err := sc.GetOrInitCall(ctx, "test-session", dag, &dagql.CallRequest{ResultCall: childCall}, dagql.ValueFunc(childInitial))
	assert.NilError(t, err)

	obj := &ModuleObject{
		Module:  modRes,
		TypeDef: parentObjDef,
		Fields: map[string]any{
			"child": child,
		},
	}
	payload, err := obj.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(sc, 0, nil))
	assert.NilError(t, err)

	var persisted persistedModuleObjectPayload
	assert.NilError(t, json.Unmarshal(payload.JSON, &persisted))
	assert.Equal(t, persistedModuleObjectValueKindResultRef, persisted.Fields["child"].Kind)

	decodedTyped, err := obj.DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(dag, 0, nil), payload.JSON)
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
	dag := newTypeDefTestDag(t)
	childObjDef := NewObjectTypeDef("Child", "", nil)
	childObjDefRes := newTypeDefDetachedResult(t, dag, "moduleObjectRawChildObj", childObjDef)
	parentObjDef := NewObjectTypeDef("Parent", "", nil)
	childTypeDef := newTypeDefDetachedResult(t, dag, "moduleObjectRawChildTypeDef", (&TypeDef{}).WithObjectTypeDef(childObjDefRes))
	parentObjDef.Fields = append(parentObjDef.Fields, newTypeDefDetachedResult(t, dag, "moduleObjectRawChildField", NewFieldTypeDef("child", childTypeDef, "", nil)))

	childID := call.New().Append((&ModuleObject{TypeDef: childObjDef}).Type(), "moduleObjectRawCallID")
	obj := &ModuleObject{
		TypeDef: parentObjDef,
		Fields: map[string]any{
			"child": childID,
		},
	}

	_, err := obj.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(nil, 0, nil))
	assert.ErrorContains(t, err, "unexpected raw call ID in semantic field")
}

func TestModuleObjectEncodeAllowsRawCallIDInPrivateField(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	objDef := NewObjectTypeDef("Parent", "", nil)

	refID := call.New().Append(dagql.String("").Type(), "moduleObjectPrivateCallID")
	obj := &ModuleObject{
		TypeDef: objDef,
		Fields: map[string]any{
			"private": refID,
		},
	}

	payload, err := obj.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(nil, 0, nil))
	assert.NilError(t, err)

	var persisted persistedModuleObjectPayload
	assert.NilError(t, json.Unmarshal(payload.JSON, &persisted))
	assert.Equal(t, persistedModuleObjectValueKindCallID, persisted.Fields["private"].Kind)
}

// A module object can store another result's encoded handle in a private
// (undeclared) field: SDKs serialize stored objects as ID strings, and the
// field is absent from the typedef. Attachment must decode the handle,
// attach the referenced result, and record a real dependency edge so the
// result stays retained for as long as the owning object's cached state is
// reusable, even after the producing session closes. Previously the handle
// stayed a raw string invisible to retention, and later sessions loading
// cached state failed with "missing shared result".
func TestModuleObjectPrivateHandleFieldRetainedAcrossProducerSessionClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	baseCache, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	producerCache := baseCache
	producerRoot := &Query{}
	producerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      producerCache,
		root:       producerRoot,
	}
	producerRoot.Server = producerSrv
	producerDag := newCoreDagqlServerForTest(t, producerRoot)
	producerSrv.dag = producerDag
	installModuleObjectHandleTestObjClass(producerDag)
	producerCtx := engine.ContextWithClientMetadata(ContextWithQuery(ctx, producerRoot), &engine.ClientMetadata{
		ClientID:  "module-object-producer-client",
		SessionID: "module-object-producer-session",
	})
	producerCtx = dagql.ContextWithCache(producerCtx, producerCache)

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
	childAttachedAny, err := producerCache.AttachResult(producerCtx, "module-object-producer-session", producerDag, childDetached)
	assert.NilError(t, err)
	childAttached, ok := childAttachedAny.(dagql.ObjectResult[*moduleObjectHandleTestObj])
	assert.Assert(t, ok)

	childID, err := childAttached.ID()
	assert.NilError(t, err)
	childEnc, err := childID.Encode()
	assert.NilError(t, err)

	// The parent typedef does not declare the "child" field, modeling a
	// private field carried only in object state.
	parentObjDef := NewObjectTypeDef("Parent", "", nil)
	parentObj := &ModuleObject{
		TypeDef: parentObjDef,
		Fields: map[string]any{
			"child": childEnc,
		},
	}
	parentCall := moduleObjectTestSyntheticCall("module_object_handle_parent", parentObj)
	parentDetached, err := dagql.NewResultForCall(parentObj, parentCall)
	assert.NilError(t, err)
	_, err = producerCache.GetOrInitCall(
		producerCtx,
		"module-object-producer-session",
		producerDag,
		&dagql.CallRequest{
			ResultCall:    parentCall,
			IsPersistable: true,
		},
		dagql.ValueFunc(parentDetached),
	)
	assert.NilError(t, err)

	// Attachment rewrote the raw handle string to the attached result.
	_, isResult := parentObj.Fields["child"].(dagql.AnyResult)
	assert.Assert(t, isResult)

	// The rewritten value persists as a real result ref, not an opaque scalar.
	encoded, err := parentObj.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(producerCache, 0, nil))
	assert.NilError(t, err)
	var persisted persistedModuleObjectPayload
	assert.NilError(t, json.Unmarshal(encoded.JSON, &persisted))
	assert.Equal(t, persistedModuleObjectValueKindResultRef, persisted.Fields["child"].Kind)

	assert.NilError(t, producerCache.ReleaseSession(producerCtx, "module-object-producer-session"))

	consumerCache := baseCache
	consumerRoot := &Query{}
	consumerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      consumerCache,
		root:       consumerRoot,
	}
	consumerRoot.Server = consumerSrv
	consumerDag := newCoreDagqlServerForTest(t, consumerRoot)
	consumerSrv.dag = consumerDag
	installModuleObjectHandleTestObjClass(consumerDag)
	consumerCtx := engine.ContextWithClientMetadata(ContextWithQuery(ctx, consumerRoot), &engine.ClientMetadata{
		ClientID:  "module-object-consumer-client",
		SessionID: "module-object-consumer-session",
	})
	consumerCtx = dagql.ContextWithCache(consumerCtx, consumerCache)

	var retainedID call.ID
	assert.NilError(t, retainedID.Decode(childEnc))
	loadedAny, err := consumerDag.Load(consumerCtx, &retainedID)
	assert.NilError(t, err)
	loaded, ok := dagql.UnwrapAs[*moduleObjectHandleTestObj](loadedAny)
	assert.Assert(t, ok)
	assert.Equal(t, "hello", loaded.Value)
}

// Handle references can also appear as *call.ID / call.ID values (e.g. from
// persisted decode) or nested inside lists and maps; all of them must be
// attached and rewritten just like top-level handle strings.
func TestModuleObjectAttachDependencyResultsRewritesHandleVariants(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	root := &Query{}
	srv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      cache,
		root:       root,
	}
	root.Server = srv
	dag := newCoreDagqlServerForTest(t, root)
	srv.dag = dag
	installModuleObjectHandleTestObjClass(dag)
	testCtx := engine.ContextWithClientMetadata(ContextWithQuery(ctx, root), &engine.ClientMetadata{
		ClientID:  "module-object-variants-client",
		SessionID: "module-object-variants-session",
	})
	testCtx = dagql.ContextWithCache(testCtx, cache)

	childDetached, err := dagql.NewObjectResultForCall(
		&moduleObjectHandleTestObj{Value: "variant"},
		dag,
		&dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "module_object_handle_variant_child",
			Type:        dagql.NewResultCallType((&moduleObjectHandleTestObj{}).Type()),
		},
	)
	assert.NilError(t, err)
	childAttachedAny, err := cache.AttachResult(testCtx, "module-object-variants-session", dag, childDetached)
	assert.NilError(t, err)
	childID, err := childAttachedAny.ID()
	assert.NilError(t, err)
	childEnc, err := childID.Encode()
	assert.NilError(t, err)

	recipeID := call.New().Append((&moduleObjectHandleTestObj{}).Type(), "moduleObjectVariantRecipe")
	recipeEnc, err := recipeID.Encode()
	assert.NilError(t, err)

	obj := &ModuleObject{
		Fields: map[string]any{
			"ptr":       childID,
			"val":       *childID,
			"idable":    dagql.NewAnyID(childID),
			"nested":    map[string]any{"inner": []any{childEnc}},
			"nilPtr":    (*call.ID)(nil),
			"recipe":    recipeEnc,
			"recipePtr": recipeID,
		},
	}
	deps, err := obj.AttachDependencyResults(testCtx, nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		return res, nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 4, len(deps))

	_, ok := obj.Fields["ptr"].(dagql.AnyResult)
	assert.Assert(t, ok)
	_, ok = obj.Fields["val"].(dagql.AnyResult)
	assert.Assert(t, ok)
	_, ok = obj.Fields["idable"].(dagql.AnyResult)
	assert.Assert(t, ok)
	nested, ok := obj.Fields["nested"].(map[string]any)
	assert.Assert(t, ok)
	inner, ok := nested["inner"].([]any)
	assert.Assert(t, ok)
	_, ok = inner[0].(dagql.AnyResult)
	assert.Assert(t, ok)

	// Nil and recipe-form values pass through unchanged: recipe IDs are
	// self-contained and loadable, so they need no dependency edge.
	nilPtr, ok := obj.Fields["nilPtr"].(*call.ID)
	assert.Assert(t, ok)
	assert.Assert(t, nilPtr == nil)
	assert.Equal(t, recipeEnc, obj.Fields["recipe"])
	assert.Equal(t, recipeID, obj.Fields["recipePtr"])
}

// A value that decodes as a live handle must not be silently kept as an
// opaque scalar when no server is reachable: that would recreate the
// dangling-reference bug, so attachment fails loudly instead.
func TestModuleObjectAttachHandleErrorsWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	root := &Query{}
	srv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      cache,
		root:       root,
	}
	root.Server = srv
	dag := newCoreDagqlServerForTest(t, root)
	srv.dag = dag
	installModuleObjectHandleTestObjClass(dag)
	setupCtx := engine.ContextWithClientMetadata(ContextWithQuery(ctx, root), &engine.ClientMetadata{
		ClientID:  "module-object-no-server-client",
		SessionID: "module-object-no-server-session",
	})
	setupCtx = dagql.ContextWithCache(setupCtx, cache)

	childDetached, err := dagql.NewObjectResultForCall(
		&moduleObjectHandleTestObj{Value: "no-server"},
		dag,
		&dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "module_object_handle_no_server_child",
			Type:        dagql.NewResultCallType((&moduleObjectHandleTestObj{}).Type()),
		},
	)
	assert.NilError(t, err)
	childAttachedAny, err := cache.AttachResult(setupCtx, "module-object-no-server-session", dag, childDetached)
	assert.NilError(t, err)
	childID, err := childAttachedAny.ID()
	assert.NilError(t, err)
	childEnc, err := childID.Encode()
	assert.NilError(t, err)

	obj := &ModuleObject{
		Fields: map[string]any{
			"child": childEnc,
		},
	}
	// Bare context: no query, no ambient server, no attachment resolver hint.
	_, err = obj.AttachDependencyResults(ctx, nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		return res, nil
	})
	assert.ErrorContains(t, err, "no dagql server")
}

// Attachment hooks can run without an ambient dagql server in context (e.g.
// direct AttachResult users); the cache falls back to stamping the resolver
// so stored handles still load and gain dependency edges.
func TestModuleObjectAttachHandleWithoutAmbientServer(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	root := &Query{}
	srv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      cache,
		root:       root,
	}
	root.Server = srv
	dag := newCoreDagqlServerForTest(t, root)
	srv.dag = dag
	installModuleObjectHandleTestObjClass(dag)
	// No ContextWithQuery: only client metadata and the cache, so the hook
	// can only find a server through the resolver fallback.
	testCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "module-object-no-ambient-client",
		SessionID: "module-object-no-ambient-session",
	})
	testCtx = dagql.ContextWithCache(testCtx, cache)

	childDetached, err := dagql.NewObjectResultForCall(
		&moduleObjectHandleTestObj{Value: "no-ambient"},
		dag,
		&dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "module_object_handle_no_ambient_child",
			Type:        dagql.NewResultCallType((&moduleObjectHandleTestObj{}).Type()),
		},
	)
	assert.NilError(t, err)
	childAttachedAny, err := cache.AttachResult(testCtx, "module-object-no-ambient-session", dag, childDetached)
	assert.NilError(t, err)
	childID, err := childAttachedAny.ID()
	assert.NilError(t, err)
	childEnc, err := childID.Encode()
	assert.NilError(t, err)

	parentObjDef := NewObjectTypeDef("Parent", "", nil)
	parentObj := &ModuleObject{
		TypeDef: parentObjDef,
		Fields: map[string]any{
			"child": childEnc,
		},
	}
	parentDetached, err := dagql.NewResultForCall(parentObj, moduleObjectTestSyntheticCall("module_object_handle_no_ambient_parent", parentObj))
	assert.NilError(t, err)
	_, err = cache.AttachResult(testCtx, "module-object-no-ambient-session", dag, parentDetached)
	assert.NilError(t, err)

	_, isResult := parentObj.Fields["child"].(dagql.AnyResult)
	assert.Assert(t, isResult)
}

func TestModuleObjectAttachDependencyResultsRetainsSemanticInterfaceHandleField(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	baseCache, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	buildModule := func(dag *dagql.Server) (*Module, *ObjectTypeDef, *ObjectTypeDef) {
		ifaceDef := NewInterfaceTypeDef("Iface", "")
		ifaceDefRes := newTypeDefDetachedResult(t, dag, "semanticIfaceDef", ifaceDef)
		childObjDef := NewObjectTypeDef("Child", "", nil)
		childObjDefRes := newTypeDefDetachedResult(t, dag, "semanticChildObjDef", childObjDef)
		parentObjDef := NewObjectTypeDef("Parent", "", nil)
		ifaceTypeDef := newTypeDefDetachedResult(t, dag, "semanticIfaceTypeDef", (&TypeDef{}).WithInterfaceTypeDef(ifaceDefRes))
		parentObjDef.Fields = append(parentObjDef.Fields, newTypeDefDetachedResult(t, dag, "semanticChildField", NewFieldTypeDef("child", ifaceTypeDef, "", nil)))
		parentObjDefRes := newTypeDefDetachedResult(t, dag, "semanticParentObjDef", parentObjDef)

		mod := &Module{
			NameField: "test",
			Deps:      NewSchemaBuilder(nil, nil),
			ObjectDefs: dagql.ObjectResultArray[*TypeDef]{
				newTypeDefDetachedResult(t, dag, "semanticChildTopTypeDef", (&TypeDef{}).WithObjectTypeDef(childObjDefRes)),
				newTypeDefDetachedResult(t, dag, "semanticParentTopTypeDef", (&TypeDef{}).WithObjectTypeDef(parentObjDefRes)),
			},
			InterfaceDefs: dagql.ObjectResultArray[*TypeDef]{
				newTypeDefDetachedResult(t, dag, "semanticIfaceTopTypeDef", (&TypeDef{}).WithInterfaceTypeDef(ifaceDefRes)),
			},
		}
		return mod, childObjDef, parentObjDef
	}

	producerCache := baseCache
	producerRoot := &Query{}
	producerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      producerCache,
		root:       producerRoot,
	}
	producerRoot.Server = producerSrv
	producerDag := newCoreDagqlServerForTest(t, producerRoot)
	producerSrv.dag = producerDag
	installTypeDefTestClasses(producerDag)
	producerCtx := engine.ContextWithClientMetadata(ContextWithQuery(ctx, producerRoot), &engine.ClientMetadata{
		ClientID:  "semantic-producer-client",
		SessionID: "semantic-producer-session",
	})
	producerCtx = dagql.ContextWithCache(producerCtx, producerCache)
	mod, childObjDef, parentObjDef := buildModule(producerDag)
	mod.Deps = NewSchemaBuilder(producerRoot, nil)
	installModuleObjectTestModuleClass(producerDag)

	producerSourceRes, err := dagql.NewObjectResultForCall(&ModuleSource{
		Kind: ModuleSourceKindDir,
	}, producerDag, moduleObjectTestSyntheticCall("semanticHandleProducerSource", &ModuleSource{}))
	assert.NilError(t, err)
	mod.Source = dagql.NonNull(producerSourceRes)

	producerModDetached, err := dagql.NewObjectResultForCall(mod, producerDag, moduleObjectTestSyntheticCall("semanticHandleProducerModule", mod))
	assert.NilError(t, err)
	producerModAny, err := producerCache.AttachResult(producerCtx, "semantic-producer-session", producerDag, producerModDetached)
	assert.NilError(t, err)
	producerModRes, ok := producerModAny.(dagql.ObjectResult[*Module])
	assert.Assert(t, ok)
	producerSrv.defaultDeps = NewSchemaBuilder(producerRoot, []Mod{NewUserMod(producerModRes)})
	producerDepDag, err := producerSrv.defaultDeps.Schema(producerCtx)
	assert.NilError(t, err)

	childCall := moduleObjectTestSyntheticCall("semanticHandleChild", &ModuleObject{TypeDef: childObjDef})
	childDetached, err := dagql.NewResultForCall(&ModuleObject{
		Module:  producerModRes,
		TypeDef: childObjDef,
		Fields:  map[string]any{},
	}, childCall)
	assert.NilError(t, err)
	childAttachedAny, err := producerCache.AttachResult(producerCtx, "semantic-producer-session", producerDepDag, childDetached)
	assert.NilError(t, err)
	childAttached, ok := childAttachedAny.(dagql.ObjectResult[*ModuleObject])
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
	parentAttached, err := producerCache.GetOrInitCall(
		producerCtx,
		"semantic-producer-session",
		producerDepDag,
		&dagql.CallRequest{
			ResultCall:    parentCall,
			IsPersistable: true,
		},
		dagql.ValueFunc(parentDetached),
	)
	assert.NilError(t, err)

	// Reading the retained field must still resolve its implementation, even
	// though that type is absent from the interface module's dependencies.
	parentObj, ok := parentAttached.(dagql.ObjectResult[*ModuleObject])
	assert.Assert(t, ok)
	field, err := objField(producerCtx, producerModRes, parentObjDef.Fields[0].Self())
	assert.NilError(t, err)
	read, err := field.Func(producerCtx, parentObj, nil, "")
	assert.NilError(t, err)
	readID, err := read.ID()
	assert.NilError(t, err)
	assert.Equal(t, childID.EngineResultID(), readID.EngineResultID())

	// A declared SDK handle must enter the persisted reference grammar too.
	// Keeping its original string retains liveness but bypasses relocation.
	record := coreRelocationRecord(t, producerCtx, producerCache, parentAttached)
	var payload persistedModuleObjectPayload
	assert.NilError(t, json.Unmarshal(record.Envelope.ObjectJSON, &payload))
	assert.Equal(t, persistedModuleObjectValueKindResultRef, payload.Fields["child"].Kind)
	assert.Equal(t, childID.EngineResultID(), payload.Fields["child"].ResultID)
	relocated := childID.EngineResultID() + 1000
	out, err := dagql.VisitEncodedReferences(record, func(ref *dagql.PersistedRef) error {
		if ref.ResultID == childID.EngineResultID() {
			ref.ResultID = relocated
		}
		return nil
	})
	assert.NilError(t, err)
	assert.NilError(t, json.Unmarshal(out.Envelope.ObjectJSON, &payload))
	assert.Equal(t, relocated, payload.Fields["child"].ResultID)

	assert.NilError(t, producerCache.ReleaseSession(producerCtx, "semantic-producer-session"))

	consumerCache := baseCache
	consumerRoot := &Query{}
	consumerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      consumerCache,
		root:       consumerRoot,
	}
	consumerRoot.Server = consumerSrv
	consumerDag := newCoreDagqlServerForTest(t, consumerRoot)
	consumerSrv.dag = consumerDag
	installTypeDefTestClasses(consumerDag)
	consumerCtx := engine.ContextWithClientMetadata(ContextWithQuery(ctx, consumerRoot), &engine.ClientMetadata{
		ClientID:  "semantic-consumer-client",
		SessionID: "semantic-consumer-session",
	})
	consumerCtx = dagql.ContextWithCache(consumerCtx, consumerCache)
	installModuleObjectTestModuleClass(consumerDag)
	consumerMod, _, _ := buildModule(consumerDag)
	consumerMod.Deps = NewSchemaBuilder(consumerRoot, nil)
	consumerSourceRes, err := dagql.NewObjectResultForCall(&ModuleSource{
		Kind: ModuleSourceKindDir,
	}, consumerDag, moduleObjectTestSyntheticCall("semanticHandleConsumerSource", &ModuleSource{}))
	assert.NilError(t, err)
	consumerMod.Source = dagql.NonNull(consumerSourceRes)

	consumerModDetached, err := dagql.NewObjectResultForCall(consumerMod, consumerDag, moduleObjectTestSyntheticCall("semanticHandleConsumerModule", consumerMod))
	assert.NilError(t, err)
	consumerModAny, err := consumerCache.AttachResult(consumerCtx, "semantic-consumer-session", consumerDag, consumerModDetached)
	assert.NilError(t, err)
	consumerModRes, ok := consumerModAny.(dagql.ObjectResult[*Module])
	assert.Assert(t, ok)
	consumerSrv.defaultDeps = NewSchemaBuilder(consumerRoot, []Mod{NewUserMod(consumerModRes)})
	consumerDepDag, err := consumerSrv.defaultDeps.Schema(consumerCtx)
	assert.NilError(t, err)

	var retainedID call.ID
	assert.NilError(t, retainedID.Decode(childEnc))
	_, err = consumerDepDag.Load(consumerCtx, &retainedID)
	assert.NilError(t, err)
}

// TestModuleObjectNestedNumbersSurvivePersistenceAndSDKConversion saves a
// module object whose private state holds numbers nested inside maps and
// arrays, restores it from the persisted payload twice, and converts it back
// to SDK input. A module receives its own state back through that conversion,
// so a number that rounds through float64 anywhere on the path hands the next
// call a different value than the one it returned.
func TestModuleObjectNestedNumbersSurvivePersistenceAndSDKConversion(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	sc, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	root := &Query{}
	testSrv := &moduleObjectTestServer{mockServer: &mockServer{}, cache: sc, root: root}
	root.Server = testSrv
	dag := newCoreDagqlServerForTest(t, root)
	testSrv.dag = dag
	ctx = dagql.ContextWithCache(ctx, sc)
	ctx = ContextWithQuery(ctx, root)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "module-object-numbers-client",
		SessionID: "module-object-numbers-session",
	})
	installModuleObjectTestModuleClass(dag)
	installTypeDefTestClasses(dag)

	// Counter declares `counts: [Int!]!`; "state" below is private data the
	// module kept for itself, so the two conversion routes are both exercised.
	intTypeDef := newTypeDefAttachedResult(t, ctx, sc, dag, "moduleObjectNumbersInt", (&TypeDef{}).WithKind(TypeDefKindInteger))
	listTypeDef := newTypeDefAttachedResult(t, ctx, sc, dag, "moduleObjectNumbersList", (&TypeDef{}).WithListOf(
		newTypeDefAttachedResult(t, ctx, sc, dag, "moduleObjectNumbersListDef", &ListTypeDef{ElementTypeDef: intTypeDef}),
	))
	objDef := NewObjectTypeDef("Counter", "", nil)
	objDef.Fields = append(objDef.Fields, newTypeDefDetachedResult(t, dag, "moduleObjectNumbersCountsField", NewFieldTypeDef("counts", listTypeDef, "", nil)))
	objDefRes := newTypeDefDetachedResult(t, dag, "moduleObjectNumbersObj", objDef)
	mod := &Module{
		NameField:  "test",
		Deps:       NewSchemaBuilder(nil, nil),
		ObjectDefs: dagql.ObjectResultArray[*TypeDef]{newTypeDefDetachedResult(t, dag, "moduleObjectNumbersTopTypeDef", (&TypeDef{}).WithObjectTypeDef(objDefRes))},
	}
	modRes, err := dagql.NewObjectResultForCall(mod, dag, moduleObjectTestSyntheticCall("moduleObjectNumbersModule", mod))
	assert.NilError(t, err)
	objType := &ModuleObjectType{typeDef: objDef, mod: modRes}
	ctx = dagql.ContextWithCall(ctx, moduleObjectTestSyntheticCall("moduleObjectNumbersParent", &ModuleObject{TypeDef: objDef}))

	// The shape an SDK hands back: private state the module kept for itself,
	// decoded with UseNumber, with numbers nested inside a map and a list.
	// "state" is not a declared field of Counter, so it travels through the
	// private-field conversion.
	const bigInt = "9007199254740993" // 2^53+1: the first integer float64 cannot hold
	const maxInt64 = "9223372036854775807"
	const minInt64 = "-9223372036854775808"
	freshState := map[string]any{
		"ids": []any{json.Number(bigInt), json.Number(maxInt64), json.Number(minInt64)},
		"limits": map[string]any{
			"ratio":  json.Number("0.1"),
			"nested": map[string]any{"deep": json.Number(bigInt)},
		},
	}
	declaredCounts := []any{json.Number(bigInt), json.Number(maxInt64), json.Number(minInt64)}
	obj := &ModuleObject{Module: modRes, TypeDef: objDef, Fields: map[string]any{
		"counts": declaredCounts,
		"state":  freshState,
	}}

	freshInput, err := objType.ConvertToSDKInput(ctx, obj)
	assert.NilError(t, err)
	freshJSON, err := json.Marshal(freshInput)
	assert.NilError(t, err)

	enc := dagql.NewPersistEncodeContext(sc, 0, nil)
	dec := dagql.NewPersistDecodeContext(dag, 0, nil)
	payload, err := obj.EncodePersistedObject(ctx, enc)
	assert.NilError(t, err)

	var persisted persistedModuleObjectPayload
	assert.NilError(t, json.Unmarshal(payload.JSON, &persisted))
	deep := persisted.Fields["state"].Fields["limits"].Fields["nested"].Fields["deep"]
	assert.Equal(t, persistedModuleObjectValueKindScalar, deep.Kind)
	assert.Equal(t, bigInt, string(deep.ScalarJSON), "the saved token is the exact integer, not a float")

	current := obj
	currentPayload := payload.JSON
	for round := 1; round <= 2; round++ {
		decodedTyped, err := current.DecodePersistedObject(ctx, dec, currentPayload)
		assert.NilError(t, err)
		decoded, ok := decodedTyped.(*ModuleObject)
		assert.Assert(t, ok)

		state, ok := decoded.Fields["state"].(map[string]any)
		assert.Assert(t, ok, "round %d", round)
		ids, ok := state["ids"].([]any)
		assert.Assert(t, ok, "round %d", round)
		assert.Equal(t, json.Number(bigInt), ids[0], "round %d: the large integer is exact inside a list", round)
		assert.Equal(t, json.Number(maxInt64), ids[1], "round %d", round)
		assert.Equal(t, json.Number(minInt64), ids[2], "round %d", round)
		counts, ok := decoded.Fields["counts"].([]any)
		assert.Assert(t, ok, "round %d", round)
		assert.Equal(t, json.Number(bigInt), counts[0], "round %d: a declared Int list stays exact too", round)
		assert.Equal(t, json.Number(maxInt64), counts[1], "round %d", round)
		assert.Equal(t, json.Number(minInt64), counts[2], "round %d", round)
		limits, ok := state["limits"].(map[string]any)
		assert.Assert(t, ok, "round %d", round)
		assert.Equal(t, json.Number("0.1"), limits["ratio"], "round %d", round)
		nested, ok := limits["nested"].(map[string]any)
		assert.Assert(t, ok, "round %d", round)
		assert.Equal(t, json.Number(bigInt), nested["deep"], "round %d: nesting depth does not lose precision", round)

		converted, err := objType.ConvertToSDKInput(ctx, decoded)
		assert.NilError(t, err)
		convertedJSON, err := json.Marshal(converted)
		assert.NilError(t, err)
		assert.Equal(t, string(freshJSON), string(convertedJSON), "round %d: SDK input matches what the module returned", round)

		// Second save: the restored object re-encodes to the same bytes.
		reencoded, err := decoded.EncodePersistedObject(ctx, enc)
		assert.NilError(t, err)
		assert.Equal(t, string(currentPayload), string(reencoded.JSON), "round %d: the second save preserves the tokens", round)
		current, currentPayload = decoded, reencoded.JSON
	}

	// Deliberate control: reading the same saved bytes through an untyped
	// json.Unmarshal, as the payload decoder did before it used a lossless
	// reader, rounds the nested integer.
	var lossy persistedModuleObjectPayload
	assert.NilError(t, json.Unmarshal(payload.JSON, &lossy))
	var rounded any
	assert.NilError(t, json.Unmarshal(lossy.Fields["state"].Fields["limits"].Fields["nested"].Fields["deep"].ScalarJSON, &rounded))
	roundedJSON, err := json.Marshal(rounded)
	assert.NilError(t, err)
	assert.Assert(t, string(roundedJSON) != bigInt, "untyped decoding must lose the large integer; got %s", string(roundedJSON))
}

func TestModuleObjectAttachDependencyResultsPreservesInlineMap(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	sc := cacheIface
	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := newCoreDagqlServerForTest(t, root)
	testSrv.dag = dag
	ctx = dagql.ContextWithCache(ctx, sc)
	ctx = ContextWithQuery(ctx, root)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "module-object-rewrite-client",
		SessionID: "module-object-rewrite-session",
	})
	installModuleObjectTestModuleClass(dag)
	installTypeDefTestClasses(dag)

	childObjDef := NewObjectTypeDef("Child", "", nil)
	childObjDefRes := newTypeDefDetachedResult(t, dag, "moduleObjectRewriteChildObj", childObjDef)
	parentObjDef := NewObjectTypeDef("Parent", "", nil)
	childTypeDef := newTypeDefDetachedResult(t, dag, "moduleObjectRewriteChildTypeDef", (&TypeDef{}).WithObjectTypeDef(childObjDefRes))
	parentObjDef.Fields = append(parentObjDef.Fields, newTypeDefDetachedResult(t, dag, "moduleObjectRewriteChildField", NewFieldTypeDef("child", childTypeDef, "", nil)))
	parentObjDefRes := newTypeDefDetachedResult(t, dag, "moduleObjectRewriteParentObj", parentObjDef)
	mod := &Module{
		NameField: "test",
		Deps:      NewSchemaBuilder(nil, nil),
		ObjectDefs: dagql.ObjectResultArray[*TypeDef]{
			newTypeDefDetachedResult(t, dag, "moduleObjectRewriteChildTopTypeDef", (&TypeDef{}).WithObjectTypeDef(childObjDefRes)),
			newTypeDefDetachedResult(t, dag, "moduleObjectRewriteParentTopTypeDef", (&TypeDef{}).WithObjectTypeDef(parentObjDefRes)),
		},
	}
	modRes, err := dagql.NewObjectResultForCall(mod, dag, moduleObjectTestSyntheticCall("moduleObjectRewriteStoredResultsModule", mod))
	assert.NilError(t, err)
	parentCall := moduleObjectTestSyntheticCall("moduleObjectParent", &ModuleObject{TypeDef: parentObjDef})
	ctx = dagql.ContextWithCall(ctx, parentCall)

	inline := map[string]any{"name": "child"}
	obj := &ModuleObject{Module: modRes, TypeDef: parentObjDef, Fields: map[string]any{"child": inline}}
	parent, err := dagql.NewResultForCall(obj, parentCall)
	assert.NilError(t, err)
	before, err := json.Marshal(obj.Fields)
	assert.NilError(t, err)
	deps, err := obj.AttachDependencyResults(ctx, parent, func(value dagql.AnyResult) (dagql.AnyResult, error) {
		// Use an independent child call: this test invokes the attachment
		// hook directly rather than from the cache's parent-registration phase.
		detached, err := dagql.NewResultForCall(value.Unwrap(), moduleObjectTestSyntheticCall("inlineChild", value.Unwrap()))
		if err != nil {
			return nil, err
		}
		return sc.AttachResult(ctx, "test-session", dag, detached)
	})
	assert.NilError(t, err)
	assert.Equal(t, len(deps), 1)
	assert.DeepEqual(t, obj.Fields["child"], inline)
	// ParentFields is sent as raw JSON to the SDK function. It must remain
	// an inline object, even though attachment created a dependency result.
	after, err := json.Marshal(obj.Fields)
	assert.NilError(t, err)
	assert.Equal(t, string(after), string(before))
}
