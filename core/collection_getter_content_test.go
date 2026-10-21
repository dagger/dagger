package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/stretchr/testify/require"
)

type collectionContentRuntime struct {
	*moduleOwnershipRuntime
	gets  atomic.Int32
	reads atomic.Int32
}

func (r *collectionContentRuntime) AsRuntime() (Runtime, bool)             { return r, true }
func (r *collectionContentRuntime) CloneForModuleSource(*ModuleSource) SDK { return r }
func (r *collectionContentRuntime) Runtime(context.Context, *SchemaBuilder, dagql.ObjectResult[*ModuleSource]) (ModuleRuntime, error) {
	return r, nil
}

func (r *collectionContentRuntime) Call(ctx context.Context, _ *engineutil.ExecutionMetadata, fn *FunctionCall, mod dagql.ObjectResult[*Module]) error {
	if _, err := ImplementationScopedModule(ctx, mod); err != nil {
		return err
	}
	var parent map[string]any
	if err := json.Unmarshal(fn.Parent, &parent); err != nil {
		return err
	}
	switch fn.Name {
	case "copy":
		r.gets.Add(1)
		value, err := json.Marshal(map[string]any{"message": parent["child"]})
		if err != nil {
			return err
		}
		return fn.ReturnValue(ctx, JSON(value))
	case "check":
		r.reads.Add(1)
		value, err := json.Marshal(parent["message"])
		if err != nil {
			return err
		}
		return fn.ReturnValue(ctx, JSON(value))
	default:
		return fmt.Errorf("unexpected function %q", fn.Name)
	}
}

func collectionContentFixture(t *testing.T, f *moduleOwnershipTest, ctx context.Context, variant, childName string) (func(string, string) dagql.ObjectResult[*ModuleObject], *collectionContentRuntime) {
	t.Helper()
	mod := f.module(ctx, "packages", moduleOwnershipModuleOpts{variant: variant})
	runtime := &collectionContentRuntime{moduleOwnershipRuntime: f.runtime}
	mod.Self().Source.Value.Self().SDKImpl = runtime
	parent := mod.Self().ObjectDefs[0].Self().AsObject.Value.Self()
	childType := moduleOwnershipTypeDef(t, f.dag, childName, false)
	child := childType.Self().AsObject.Value.Self()
	mod.Self().ObjectDefs = append(mod.Self().ObjectDefs, childType)
	stringType := parent.Functions[0].Self().ReturnType
	list := newTypeDefDetachedResult(t, f.dag, "content-key-list", &ListTypeDef{ElementTypeDef: stringType})
	keys := newTypeDefDetachedResult(t, f.dag, "content-keys", (&TypeDef{}).WithListOf(list))
	parent.Fields = append(parent.Fields, newTypeDefDetachedResult(t, f.dag, "content-key-field", NewFieldTypeDef("keys", keys, "", nil)))
	getter := parent.Functions[1].Self()
	getter.ReturnType = childType
	getter.Args = parent.Functions[0].Self().Args
	parent.Collection = &CollectionConfig{Enabled: true, Get: "copy"}
	f.install(ctx, f.dag, mod)
	require.NoError(t, (&ModuleObject{Module: mod, TypeDef: child}).Install(ctx, f.dag))

	get := func(version, value string) dagql.ObjectResult[*ModuleObject] {
		t.Helper()
		obj := &ModuleObject{Module: mod, TypeDef: parent, Fields: map[string]any{"keys": []any{"item"}, "message": version, "child": value}}
		frame := moduleObjectTestSyntheticCall("collection-"+variant+"-"+childName+"-"+version, obj)
		detached, err := dagql.NewObjectResultForCall(obj, f.dag, frame)
		require.NoError(t, err)
		attached, err := f.cache.GetOrInitCall(ctx, "collection-content", f.dag, &dagql.CallRequest{ResultCall: frame}, dagql.ValueFunc(detached))
		require.NoError(t, err)
		var result dagql.ObjectResult[*ModuleObject]
		require.NoError(t, f.dag.Select(ctx, attached.(dagql.ObjectResult[*ModuleObject]), &result, dagql.Selector{Field: "get", View: f.dag.View, Args: []dagql.NamedInput{{Name: "key", Value: dagql.String("item")}}}))
		return result
	}
	return get, runtime
}

func TestCollectionGetterReusesUnchangedChild(t *testing.T) {
	f := newModuleOwnershipTest(t)
	ctx := f.session("collection-content")
	get, runtime := collectionContentFixture(t, f, ctx, "", "Package")
	read := func(obj dagql.ObjectResult[*ModuleObject], want string) {
		t.Helper()
		var actual string
		require.NoError(t, f.dag.Select(ctx, obj, &actual, dagql.Selector{Field: "check", Args: []dagql.NamedInput{{Name: "label", Value: dagql.String("read")}}}))
		require.Equal(t, want, actual)
	}
	first := get("first", "stable")
	read(first, "stable")
	require.EqualValues(t, 1, runtime.gets.Load())
	require.EqualValues(t, 1, runtime.reads.Load())
	second := get("second", "stable")
	read(second, "stable")
	require.EqualValues(t, 2, runtime.gets.Load(), "the changed collection must execute its getter")
	require.EqualValues(t, 1, runtime.reads.Load(), "unchanged child content must reuse downstream work")
	changed := get("third", "changed")
	read(changed, "changed")
	require.EqualValues(t, 3, runtime.gets.Load())
	require.EqualValues(t, 2, runtime.reads.Load(), "changed child content must invalidate downstream work")
}

func TestCollectionGetterContentScope(t *testing.T) {
	f := newModuleOwnershipTest(t)
	ctx := f.session("collection-content")
	var previous string
	for _, scene := range []struct {
		variant, childName, view string
		scopedView               bool
	}{
		{"first", "Package", "", false},
		{"second", "Package", "", false},
		{"second", "OtherPackage", "", false},
		{"second", "OtherPackage", "other-view", false},
		{"second", "OtherPackage", "other-view", true},
	} {
		dag := newCoreDagqlServerForTest(t, f.root)
		installModuleOwnershipTestClasses(dag)
		installTypeDefTestClasses(dag)
		dag.View = call.View(scene.view)
		f.dag = dag
		f.root.Server.(*moduleObjectTestServer).dag = dag
		get, _ := collectionContentFixture(t, f, ctx, scene.variant, scene.childName)
		if scene.scopedView {
			object, ok := dag.ObjectType("Packages")
			require.True(t, ok)
			class := object.(dagql.Class[*ModuleObject])
			field, ok := class.Field("get", dag.View)
			require.True(t, ok)
			spec := *field.Spec
			spec.ViewFilter = dagql.ExactView(scene.view)
			field.Spec = &spec
			class.Extend(spec, func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
				return field.Func(ctx, self.(dagql.ObjectResult[*ModuleObject]), args, dag.View)
			})
		}
		value := get("scope-"+scene.view, "same")
		frame, err := value.ResultCall()
		require.NoError(t, err)
		require.NotEmpty(t, frame.ContentDigest())
		current := frame.ContentDigest().String()
		t.Logf("variant=%s type=%s serverView=%s resultView=%s digest=%s", scene.variant, scene.childName, scene.view, frame.View, current)
		if scene.view != "" && !scene.scopedView {
			require.Equal(t, previous, current, "global fields should reuse content across unrelated schema views")
		} else {
			require.NotEqual(t, previous, current, "content identity must include implementation, type and effective view")
		}
		previous = current
	}
}
