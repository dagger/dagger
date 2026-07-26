package dagql_test

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/internal/points"
)

// TestStructuralOnlyArgNotEvaluatedOnLoad reproduces the failure mode where
// restoring a persisted session re-evaluated a side-effecting tool call that had
// been recorded only as a structural argument (LLM.withTools(object:)). A
// StructuralOnly ID argument must be carried by reference when its receiver's ID
// is loaded from the recipe, so loading never re-runs the call that produced it.
func TestStructuralOnlyArgNotEvaluatedOnLoad(t *testing.T) {
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
	// StructuralOnly, so loading a bindTool result must not evaluate it.
	dagql.Fields[*points.Point]{
		dagql.Func("bindTool", func(_ context.Context, self *points.Point, _ struct {
			Object dagql.AnyID
		}) (*points.Point, error) {
			return self, nil
		}).Args(
			dagql.Arg("object").StructuralOnly(),
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

	// Bind the snitch object as a structural tool on a fresh point.
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
			dagql.Arg("object").StructuralOnly(),
		),
	}.Install(freshSrv)

	loaded, err := freshSrv.Load(freshCtx, boundRecipeID)
	assert.NilError(t, err, "loading the recipe must not fail")

	// The whole point: the structural arg (snitch) was NOT re-evaluated.
	assert.Equal(t, 0, freshSnitchCalls, "structural-only arg must not be evaluated on load")

	// The receiver was still reconstructed correctly.
	var x, y int
	assert.NilError(t, freshSrv.Select(freshCtx, loaded, &x, dagql.Selector{Field: "x"}))
	assert.NilError(t, freshSrv.Select(freshCtx, loaded, &y, dagql.Selector{Field: "y"}))
	assert.Equal(t, 1, x)
	assert.Equal(t, 2, y)
}

// TestStructuralOnlyArgPreservesCallIdentity checks that marking an arg
// StructuralOnly does not change the receiver's recipe digest: the argument is
// still part of the call, just carried by reference.
func TestStructuralOnlyArgPreservesCallIdentity(t *testing.T) {
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
			dagql.Arg("object").StructuralOnly(),
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

	// Different structural args must yield different receiver identities: the arg
	// still participates in the call, it's just not evaluated eagerly on load.
	a := bind(mkArg(1, 1))
	b := bind(mkArg(2, 2))
	assert.Assert(t, a.Digest() != b.Digest(), "different structural args must give different call digests")

	// The same structural arg is stable.
	c := bind(mkArg(1, 1))
	assert.Equal(t, a.Digest(), c.Digest(), "identical structural args must give identical call digests")
}
