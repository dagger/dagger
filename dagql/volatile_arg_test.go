package dagql_test

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/internal/points"
)

// Calls that differ only in a volatile argument are one call: the first runs,
// with its value, and the others share its result. The argument is not in the
// result's ID. A non-volatile argument still tells calls apart, and a dynamic
// input hook, which rebuilds the resolver's arguments from the call, keeps the
// volatile value.
func TestVolatileArg(t *testing.T) {
	for _, hook := range []bool{false, true} {
		name := "plain"
		if hook {
			name = "dynamic-inputs"
		}
		t.Run(name, func(t *testing.T) {
			ctx := dagql.ContextWithCache(testContext(), newCache(t))
			srv := newExternalDagqlServerForTest(t, Query{})
			points.Install[Query](srv)

			var ran []bool
			hinted := dagql.Func("hinted", func(_ context.Context, self *points.Point, args struct {
				Label string
				Hint  bool `default:"false"`
			}) (*points.Point, error) {
				ran = append(ran, args.Hint)
				return self, nil
			}).Args(dagql.Arg("label"), dagql.Arg("hint").Volatile())
			if hook {
				hinted.Spec.GetDynamicInput = func(context.Context, dagql.AnyResult, map[string]dagql.Input, call.View, *dagql.CallRequest) error {
					return nil
				}
			}
			dagql.Fields[*points.Point]{hinted}.Install(srv)

			sel := func(label string, hint bool) *call.ID {
				var out dagql.ObjectResult[*points.Point]
				assert.NilError(t, srv.Select(ctx, srv.Root(), &out,
					dagql.Selector{Field: "point"},
					dagql.Selector{Field: "hinted", Args: []dagql.NamedInput{
						{Name: "label", Value: dagql.String(label)},
						{Name: "hint", Value: dagql.Boolean(hint)},
					}},
				))
				id, err := out.RecipeID(ctx)
				assert.NilError(t, err)
				return id
			}
			hintedA := sel("a", true)
			plainA := sel("a", false)
			plainB := sel("b", false)

			assert.DeepEqual(t, []bool{true, false}, ran)
			assert.Equal(t, hintedA.Digest(), plainA.Digest())
			assert.Assert(t, hintedA.Digest() != plainB.Digest())
			for _, arg := range hintedA.Args() {
				assert.Assert(t, arg.Name() != "hint", "a volatile argument must not be in the call's ID")
			}
		})
	}
}
