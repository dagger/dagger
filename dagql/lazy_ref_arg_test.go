package dagql_test

import (
	"context"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/internal/points"
)

type moduleToolSet struct{}

func (*moduleToolSet) Type() *ast.Type {
	return &ast.Type{NamedType: "ModuleToolSet", NonNull: true}
}

// TestObjectTypeForIDUsesModuleProvenance covers lazy references to user-module
// objects when the loading server lacks the type or has an older same-named
// definition. The recipe deliberately names no real field: resolving its type
// must use module provenance without evaluating the object recipe.
func TestObjectTypeForIDUsesModuleProvenance(t *testing.T) {
	cache := newCache(t)
	ctx := dagql.ContextWithCache(testContext(), cache)

	bootstrap := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](bootstrap)
	var moduleRef dagql.ObjectResult[*points.Point]
	assert.NilError(t, bootstrap.Select(ctx, bootstrap.Root(), &moduleRef,
		dagql.Selector{Field: "point"},
	))
	moduleRefID, err := moduleRef.RecipeID(ctx)
	assert.NilError(t, err)

	moduleSchema := newExternalDagqlServerForTest(t, Query{})
	moduleSchema.InstallObject(dagql.NewClass[*moduleToolSet](moduleSchema))
	dagql.Fields[*moduleToolSet]{
		dagql.Func("check", func(context.Context, *moduleToolSet, struct{}) (string, error) {
			return "ok", nil
		}),
	}.Install(moduleSchema)

	bootstrap.SetResultServerForCall(func(_ context.Context, frame *dagql.ResultCall) (*dagql.Server, error) {
		assert.Assert(t, frame.Module != nil)
		assert.Assert(t, frame.Module.ResultRef != nil)
		assert.Assert(t, frame.Module.ResultRef.ResultID != 0)
		return moduleSchema, nil
	})

	objectID := call.New().Append((&moduleToolSet{}).Type(), "missingConstructor",
		call.WithModule(call.NewModule(moduleRefID, "tools", "", "")),
	)
	objType, definingServer, ok, err := bootstrap.ObjectTypeAndServerForID(ctx, objectID)
	assert.NilError(t, err)
	assert.Assert(t, ok)
	assert.Equal(t, moduleSchema, definingServer)
	assert.Equal(t, "ModuleToolSet", objType.TypeName())
	_, ok = objType.FieldSpec("check", "")
	assert.Assert(t, ok)

	// Reloading a module can leave an older definition of the same type in
	// the caller's schema. The recipe's module, not that name match, must
	// remain authoritative when a state-returning tool is rebound.
	bootstrap.InstallObject(dagql.NewClass[*moduleToolSet](bootstrap))
	dagql.Fields[*moduleToolSet]{
		dagql.Func("oldCheck", func(context.Context, *moduleToolSet, struct{}) (string, error) {
			return "old", nil
		}),
	}.Install(bootstrap)
	objType, definingServer, ok, err = bootstrap.ObjectTypeAndServerForID(ctx, objectID)
	assert.NilError(t, err)
	assert.Assert(t, ok)
	assert.Equal(t, moduleSchema, definingServer)
	_, ok = objType.FieldSpec("check", "")
	assert.Assert(t, ok)
	_, ok = objType.FieldSpec("oldCheck", "")
	assert.Assert(t, !ok)

	// Without module provenance, ordinary/core type lookup still uses the
	// caller's schema rather than inventing a defining module.
	localID := call.New().Append((&moduleToolSet{}).Type(), "missingConstructor")
	objType, definingServer, ok, err = bootstrap.ObjectTypeAndServerForID(ctx, localID)
	assert.NilError(t, err)
	assert.Assert(t, ok)
	assert.Equal(t, bootstrap, definingServer)
	_, ok = objType.FieldSpec("oldCheck", "")
	assert.Assert(t, ok)
}

// TestLoadRecipeUsesModuleProvenance exercises the cold evaluation path, not
// just type recovery: the constructor must run in the recorded module's schema,
// including when the bootstrap schema has a same-named constructor.
func TestLoadRecipeUsesModuleProvenance(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing constructor", true: "shadowed constructor"}[shadow], func(t *testing.T) {
			ctx := dagql.ContextWithCache(testContext(), newCache(t))
			bootstrap := newExternalDagqlServerForTest(t, Query{})
			points.Install[Query](bootstrap)
			moduleSchema := newExternalDagqlServerForTest(t, Query{})
			moduleSchema.InstallObject(dagql.NewClass[*moduleToolSet](moduleSchema))

			var constructorCalls, shadowCalls, schemaCalls int
			constructor := dagql.Func("editor", func(ctx context.Context, _ Query, _ struct{}) (*moduleToolSet, error) {
				assert.Equal(t, moduleSchema, dagql.CurrentDagqlServer(ctx))
				constructorCalls++
				return &moduleToolSet{}, nil
			})
			dagql.Fields[Query]{constructor}.Install(moduleSchema)
			if shadow {
				bootstrap.InstallObject(dagql.NewClass[*moduleToolSet](bootstrap))
				dagql.Fields[Query]{
					dagql.Func("editor", func(context.Context, Query, struct{}) (*moduleToolSet, error) {
						shadowCalls++
						return &moduleToolSet{}, nil
					}),
				}.Install(bootstrap)
			}
			bootstrap.SetResultServerForCall(func(ctx context.Context, frame *dagql.ResultCall) (*dagql.Server, error) {
				schemaCalls++
				assert.Assert(t, frame.Module != nil && frame.Module.ResultRef != nil)
				assert.Assert(t, frame.Module.ResultRef.ResultID != 0)
				assert.Equal(t, "editor", frame.Module.Name)
				constructor.Spec.Module = frame.Module
				return moduleSchema, nil
			})

			mod := recipeTestModule(1, "editor")
			id := call.New().Append((&moduleToolSet{}).Type(), "editor", call.WithModule(mod))
			loaded, err := bootstrap.Load(ctx, id)
			assert.NilError(t, err)
			assert.Equal(t, "ModuleToolSet", loaded.Type().Name())
			assert.Equal(t, 1, constructorCalls)
			assert.Equal(t, 0, shadowCalls)
			assert.Equal(t, 1, schemaCalls)
			loadedID, err := loaded.RecipeID(ctx)
			assert.NilError(t, err)
			assert.Equal(t, id.Digest(), loadedID.Digest())

			// A warm digest hit must not load provenance or rebuild a schema.
			_, err = bootstrap.Load(ctx, id)
			assert.NilError(t, err)
			assert.Equal(t, 1, constructorCalls)
			assert.Equal(t, 1, schemaCalls)
		})
	}
}

// TestLoadRecipeModuleCoreCallsAndLazyRefs crosses module schemas with an eager
// argument returning a core type. The module method must use its defining schema
// to classify its lazy argument and leave that recipe untouched.
func TestLoadRecipeModuleCoreCallsAndLazyRefs(t *testing.T) {
	ctx := dagql.ContextWithCache(testContext(), newCache(t))
	bootstrap := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](bootstrap)
	first := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](first)
	second := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](second)

	first.InstallObject(dagql.NewClass[*moduleToolSet](first))
	var firstCalls, secondCalls, combineCalls int
	firstConstructor := dagql.Func("editor", func(ctx context.Context, _ Query, _ struct{}) (*moduleToolSet, error) {
		assert.Equal(t, first, dagql.CurrentDagqlServer(ctx))
		firstCalls++
		return &moduleToolSet{}, nil
	})
	dagql.Fields[Query]{firstConstructor}.Install(first)
	secondConstructor := dagql.Func("helper", func(ctx context.Context, _ Query, _ struct{}) (*points.Point, error) {
		assert.Equal(t, second, dagql.CurrentDagqlServer(ctx))
		secondCalls++
		return &points.Point{X: 20}, nil
	})
	dagql.Fields[Query]{secondConstructor}.Install(second)
	combine := dagql.Func("combine", func(ctx context.Context, _ *moduleToolSet, args struct {
		Other dagql.ID[*points.Point]
		Lazy  dagql.AnyID
	}) (*points.Point, error) {
		assert.Equal(t, first, dagql.CurrentDagqlServer(ctx))
		combineCalls++
		other, err := args.Other.Load(ctx, first)
		if err != nil {
			return nil, err
		}
		return &points.Point{X: 10 + other.Self().X}, nil
	}).Args(dagql.Arg("lazy").LazyRef())
	dagql.Fields[*moduleToolSet]{combine}.Install(first)

	bootstrap.SetResultServerForCall(func(ctx context.Context, frame *dagql.ResultCall) (*dagql.Server, error) {
		assert.Assert(t, frame.Module != nil && frame.Module.ResultRef != nil)
		assert.Assert(t, frame.Module.ResultRef.ResultID != 0)
		// The schema resolver is given only the defining module, not unrelated
		// versions/modules from the receiver or arguments.
		assert.Assert(t, frame.Receiver == nil)
		assert.Equal(t, 0, len(frame.Args))
		switch frame.Module.Name {
		case "first":
			firstConstructor.Spec.Module = frame.Module
			combine.Spec.Module = frame.Module
			return first, nil
		case "second":
			secondConstructor.Spec.Module = frame.Module
			return second, nil
		default:
			t.Fatalf("unexpected module %q", frame.Module.Name)
			return nil, nil
		}
	})

	pointType := (&points.Point{}).Type()
	firstModule := recipeTestModule(1, "first")
	firstID := call.New().Append((&moduleToolSet{}).Type(), "editor", call.WithModule(firstModule))
	secondModule := recipeTestModule(2, "second")
	secondID := call.New().Append(pointType, "helper", call.WithModule(secondModule))
	// This recipe cannot be evaluated; it is deliberately only a lazy ref.
	lazyID := call.New().Append(pointType, "mustNotRun")
	combinedID := firstID.Append(pointType, "combine",
		call.WithModule(firstModule),
		call.WithArgs(
			call.NewArgument("other", call.NewLiteralID(secondID), false),
			call.NewArgument("lazy", call.NewLiteralID(lazyID), false),
		),
	)
	// A core call following the module method still works on the loaded class.
	id := combinedID.Append(pointType, "shiftLeft")
	loaded, err := bootstrap.Load(ctx, id)
	assert.NilError(t, err)
	var x int
	assert.NilError(t, bootstrap.Select(ctx, loaded, &x, dagql.Selector{Field: "x"}))
	assert.Equal(t, 29, x)
	assert.Equal(t, 1, firstCalls)
	assert.Equal(t, 1, secondCalls)
	assert.Equal(t, 1, combineCalls)
}

// Core points stand in for module objects: their distinct, cold recipes can be
// loaded by the bootstrap schema before the module-aware hook is called.
func recipeTestModule(x int, name string) *call.Module {
	id := call.New().Append((&points.Point{}).Type(), "point",
		call.WithArgs(call.NewArgument("x", dagql.NewInt(x).ToLiteral(), false)),
	)
	return call.NewModule(id, name, "", "")
}

// TestLazyRefArgNotEvaluatedOnLoad reproduces the failure mode where
// restoring a persisted session re-evaluated a side-effecting tool call that had
// been recorded only as a lazy-ref argument (LLM.withTools(object:)). A
// LazyRef ID argument must be carried by reference when its receiver's ID
// is loaded from the recipe, so loading never re-runs the call that produced it.
func TestLazyRefArgNotEvaluatedOnLoad(t *testing.T) {
	cache := newCache(t)
	ctx := dagql.ContextWithCache(testContext(), cache)
	srv := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](srv)

	// snitch stands in for a side-effecting call (like TuiQa.start building the
	// CLI): it bumps a counter every time it is evaluated.
	var snitchCalls int
	dagql.Fields[*points.Point]{
		dagql.Func("snitch", func(_ context.Context, self *points.Point, _ struct{}) (*points.Point, error) {
			snitchCalls++
			return self, nil
		}),
	}.Install(srv)

	// bindTool mirrors LLM.withTools: it takes an object by ID purely to record
	// it structurally, returning its own receiver unchanged. The object arg is
	// LazyRef, so loading a bindTool result must not evaluate it.
	dagql.Fields[*points.Point]{
		dagql.Func("bindTool", func(_ context.Context, self *points.Point, _ struct {
			Object dagql.AnyID
		}) (*points.Point, error) {
			return self, nil
		}).Args(
			dagql.Arg("object").LazyRef(),
		),
	}.Install(srv)

	// Build the side-effecting object and grab its recipe ID.
	var snitched dagql.ObjectResult[*points.Point]
	assert.NilError(t, srv.Select(ctx, srv.Root(), &snitched,
		dagql.Selector{Field: "point", Args: []dagql.NamedInput{
			{Name: "x", Value: dagql.NewInt(6)},
			{Name: "y", Value: dagql.NewInt(7)},
		}},
		dagql.Selector{Field: "snitch"},
	))
	assert.Equal(t, 1, snitchCalls, "snitch should have been evaluated once when first produced")

	snitchRecipeID, err := snitched.RecipeID(ctx)
	assert.NilError(t, err)

	// Bind the snitch object as a lazy-ref tool on a fresh point.
	var bound dagql.ObjectResult[*points.Point]
	assert.NilError(t, srv.Select(ctx, srv.Root(), &bound,
		dagql.Selector{Field: "point", Args: []dagql.NamedInput{
			{Name: "x", Value: dagql.NewInt(1)},
			{Name: "y", Value: dagql.NewInt(2)},
		}},
		dagql.Selector{Field: "bindTool", Args: []dagql.NamedInput{
			{Name: "object", Value: dagql.NewAnyID(snitchRecipeID)},
		}},
	))

	boundRecipeID, err := bound.RecipeID(ctx)
	assert.NilError(t, err)

	// Load the recipe on a fresh server + cache, simulating resuming a persisted
	// session in a new engine where the recorded snitch result is not cached.
	freshCache := newCache(t)
	freshCtx := dagql.ContextWithCache(testContext(), freshCache)
	freshSrv := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](freshSrv)
	freshSnitchCalls := 0
	dagql.Fields[*points.Point]{
		dagql.Func("snitch", func(_ context.Context, self *points.Point, _ struct{}) (*points.Point, error) {
			freshSnitchCalls++
			return self, nil
		}),
	}.Install(freshSrv)
	dagql.Fields[*points.Point]{
		dagql.Func("bindTool", func(_ context.Context, self *points.Point, _ struct {
			Object dagql.AnyID
		}) (*points.Point, error) {
			return self, nil
		}).Args(
			dagql.Arg("object").LazyRef(),
		),
	}.Install(freshSrv)

	loaded, err := freshSrv.Load(freshCtx, boundRecipeID)
	assert.NilError(t, err, "loading the recipe must not fail")

	// The whole point: the lazy-ref arg (snitch) was NOT re-evaluated.
	assert.Equal(t, 0, freshSnitchCalls, "lazy-ref arg must not be evaluated on load")

	// The receiver was still reconstructed correctly.
	var x, y int
	assert.NilError(t, freshSrv.Select(freshCtx, loaded, &x, dagql.Selector{Field: "x"}))
	assert.NilError(t, freshSrv.Select(freshCtx, loaded, &y, dagql.Selector{Field: "y"}))
	assert.Equal(t, 1, x)
	assert.Equal(t, 2, y)
}

// TestLazyRefArgPreservesCallIdentity checks that marking an arg
// LazyRef does not change the receiver's recipe digest: the argument is
// still part of the call, just carried by reference.
func TestLazyRefArgPreservesCallIdentity(t *testing.T) {
	cache := newCache(t)
	ctx := dagql.ContextWithCache(testContext(), cache)
	srv := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](srv)

	dagql.Fields[*points.Point]{
		dagql.Func("bindTool", func(_ context.Context, self *points.Point, _ struct {
			Object dagql.AnyID
		}) (*points.Point, error) {
			return self, nil
		}).Args(
			dagql.Arg("object").LazyRef(),
		),
	}.Install(srv)

	mkArg := func(x, y int) *call.ID {
		var p dagql.ObjectResult[*points.Point]
		assert.NilError(t, srv.Select(ctx, srv.Root(), &p, dagql.Selector{
			Field: "point", Args: []dagql.NamedInput{
				{Name: "x", Value: dagql.NewInt(x)},
				{Name: "y", Value: dagql.NewInt(y)},
			},
		}))
		id, err := p.RecipeID(ctx)
		assert.NilError(t, err)
		return id
	}

	bind := func(arg *call.ID) *call.ID {
		var b dagql.ObjectResult[*points.Point]
		assert.NilError(t, srv.Select(ctx, srv.Root(), &b,
			dagql.Selector{Field: "point", Args: []dagql.NamedInput{
				{Name: "x", Value: dagql.NewInt(0)},
				{Name: "y", Value: dagql.NewInt(0)},
			}},
			dagql.Selector{Field: "bindTool", Args: []dagql.NamedInput{
				{Name: "object", Value: dagql.NewAnyID(arg)},
			}},
		))
		id, err := b.RecipeID(ctx)
		assert.NilError(t, err)
		return id
	}

	// Different lazy-ref args must yield different receiver identities: the arg
	// still participates in the call, it's just not evaluated eagerly on load.
	a := bind(mkArg(1, 1))
	b := bind(mkArg(2, 2))
	assert.Assert(t, a.Digest() != b.Digest(), "different lazy-ref args must give different call digests")

	// The same lazy-ref arg is stable.
	c := bind(mkArg(1, 1))
	assert.Equal(t, a.Digest(), c.Digest(), "identical lazy-ref args must give identical call digests")
}
