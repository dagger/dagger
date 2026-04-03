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
	cacheIface, err := dagql.NewCache(ctx, "", nil)
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
	cacheIface, err := dagql.NewCache(ctx, "", nil)
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
	dag := dagql.NewServer(root)
	testSrv.dag = dag

	ctx = ContextWithQuery(ctx, root)

	callFrame := moduleObjectTestSyntheticCall("persistedModuleObjectValue", dagql.String(""))
	initial, err := dagql.NewResultForCall(dagql.String("hello"), callFrame)
	assert.NilError(t, err)
	res, err := sc.GetOrInitCall(ctx, "test-session", dag, &dagql.CallRequest{ResultCall: callFrame}, dagql.ValueFunc(initial))
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
	expectedID, err := initial.RecipeID(ctx)
	assert.NilError(t, err)
	actualID, err := decodedRes.RecipeID(ctx)
	assert.NilError(t, err)
	assert.Equal(t, expectedID.Digest(), actualID.Digest())
}

func TestModulePersistedTypeDefsRoundTripPreservesNullableValidity(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "", nil)
	assert.NilError(t, err)
	sc := cacheIface

	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root)
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

	payload, err := mod.EncodePersistedObject(ctx, sc)
	assert.NilError(t, err)

	decodedTyped, err := (&Module{}).DecodePersistedObject(ctx, dag, 0, nil, payload)
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
	cacheIface, err := dagql.NewCache(ctx, "", nil)
	assert.NilError(t, err)
	sc := cacheIface

	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root)
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
	cacheIface, err := dagql.NewCache(ctx, "", nil)
	assert.NilError(t, err)
	sc := cacheIface
	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root)
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
	cacheIface, err := dagql.NewCache(ctx, "", nil)
	assert.NilError(t, err)
	sc := cacheIface

	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := dagql.NewServer(root)
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
	dag := newTypeDefTestDag()
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

	_, err := obj.EncodePersistedObject(ctx, nil)
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

	payload, err := obj.EncodePersistedObject(ctx, nil)
	assert.NilError(t, err)

	var persisted persistedModuleObjectPayload
	assert.NilError(t, json.Unmarshal(payload, &persisted))
	assert.Equal(t, persistedModuleObjectValueKindCallID, persisted.Fields["private"].Kind)
}

func TestModuleObjectRawHandleFieldBecomesStaleAfterProducerSessionClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	baseCache, err := dagql.NewCache(ctx, "", nil)
	assert.NilError(t, err)

	producerCache := baseCache
	producerRoot := &Query{}
	producerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      producerCache,
		root:       producerRoot,
	}
	producerRoot.Server = producerSrv
	producerDag := dagql.NewServer(producerRoot)
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

	obj := &ModuleObject{
		Fields: map[string]any{
			"child": childEnc,
		},
	}
	deps, err := obj.AttachDependencyResults(ctx, nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		t.Fatalf("unexpected attach of %T", res)
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, len(deps))
	_, stillRawString := obj.Fields["child"].(string)
	assert.Assert(t, stillRawString)

	assert.NilError(t, producerCache.ReleaseSession(producerCtx, "module-object-producer-session"))

	consumerCache := baseCache
	consumerRoot := &Query{}
	consumerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      consumerCache,
		root:       consumerRoot,
	}
	consumerRoot.Server = consumerSrv
	consumerDag := dagql.NewServer(consumerRoot)
	consumerSrv.dag = consumerDag
	installModuleObjectHandleTestObjClass(consumerDag)
	consumerCtx := engine.ContextWithClientMetadata(ContextWithQuery(ctx, consumerRoot), &engine.ClientMetadata{
		ClientID:  "module-object-consumer-client",
		SessionID: "module-object-consumer-session",
	})
	consumerCtx = dagql.ContextWithCache(consumerCtx, consumerCache)

	var staleID call.ID
	assert.NilError(t, staleID.Decode(childEnc))
	_, err = consumerDag.Load(consumerCtx, &staleID)
	assert.ErrorContains(t, err, "missing shared result")
}

func TestModuleObjectAttachDependencyResultsRetainsSemanticInterfaceHandleField(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	baseCache, err := dagql.NewCache(ctx, "", nil)
	assert.NilError(t, err)

	buildModule := func(dag *dagql.Server) (*Module, *ObjectTypeDef) {
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
		return mod, parentObjDef
	}

	producerCache := baseCache
	producerRoot := &Query{}
	producerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      producerCache,
		root:       producerRoot,
	}
	producerRoot.Server = producerSrv
	producerDag := dagql.NewServer(producerRoot)
	producerSrv.dag = producerDag
	installTypeDefTestClasses(producerDag)
	producerCtx := engine.ContextWithClientMetadata(ContextWithQuery(ctx, producerRoot), &engine.ClientMetadata{
		ClientID:  "semantic-producer-client",
		SessionID: "semantic-producer-session",
	})
	producerCtx = dagql.ContextWithCache(producerCtx, producerCache)
	mod, parentObjDef := buildModule(producerDag)
	mod.Deps = NewSchemaBuilder(producerRoot, nil)
	installModuleObjectTestModuleClass(producerDag)
	installModuleObjectSemanticIfaceChildClass(producerDag)

	producerModRes, err := dagql.NewObjectResultForCall(mod, producerDag, moduleObjectTestSyntheticCall("semanticHandleProducerModule", mod))
	assert.NilError(t, err)
	mod.Deps = NewSchemaBuilder(producerRoot, []Mod{NewUserMod(producerModRes)})
	producerSrv.defaultDeps = mod.Deps

	childCall := moduleObjectTestSyntheticCall("semanticHandleChild", &moduleObjectSemanticIfaceChild{})
	childDetached, err := dagql.NewObjectResultForCall(&moduleObjectSemanticIfaceChild{
		Value: "hello",
	}, producerDag, childCall)
	assert.NilError(t, err)
	childAttachedAny, err := producerCache.AttachResult(producerCtx, "semantic-producer-session", producerDag, childDetached)
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
		"semantic-producer-session",
		producerDag,
		&dagql.CallRequest{
			ResultCall:    parentCall,
			IsPersistable: true,
		},
		dagql.ValueFunc(parentDetached),
	)
	assert.NilError(t, err)

	assert.NilError(t, producerCache.ReleaseSession(producerCtx, "semantic-producer-session"))

	consumerCache := baseCache
	consumerRoot := &Query{}
	consumerSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      consumerCache,
		root:       consumerRoot,
	}
	consumerRoot.Server = consumerSrv
	consumerDag := dagql.NewServer(consumerRoot)
	consumerSrv.dag = consumerDag
	installTypeDefTestClasses(consumerDag)
	consumerCtx := engine.ContextWithClientMetadata(ContextWithQuery(ctx, consumerRoot), &engine.ClientMetadata{
		ClientID:  "semantic-consumer-client",
		SessionID: "semantic-consumer-session",
	})
	consumerCtx = dagql.ContextWithCache(consumerCtx, consumerCache)
	installModuleObjectTestModuleClass(consumerDag)
	installModuleObjectSemanticIfaceChildClass(consumerDag)
	consumerMod, _ := buildModule(consumerDag)
	consumerModRes, err := dagql.NewObjectResultForCall(consumerMod, consumerDag, moduleObjectTestSyntheticCall("semanticHandleConsumerModule", consumerMod))
	assert.NilError(t, err)
	consumerMod.Deps = NewSchemaBuilder(consumerRoot, []Mod{NewUserMod(consumerModRes)})
	consumerSrv.defaultDeps = consumerMod.Deps

	var retainedID call.ID
	assert.NilError(t, retainedID.Decode(childEnc))
	_, err = consumerDag.Load(consumerCtx, &retainedID)
	assert.NilError(t, err)
}
