package dagql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanResolvesInputsAndModuleWithoutRunningBody(t *testing.T) {
	ctx, cache, srv := persistedListTestCache(t, "")
	module := persistedListTestResult(t, ctx, cache, srv, "module", NewInt(1))
	moduleID, err := module.ID()
	require.NoError(t, err)
	type args struct {
		Name     String `default:"schema"`
		Injected Optional[String]
	}
	var bodies, hooks, providers int
	field := NodeFuncWithDynamicInputs("planned", func(context.Context, ObjectResult[*persistCodecRoot], args) (String, error) {
		bodies++
		return "body", nil
	}, func(ctx context.Context, _ ObjectResult[*persistCodecRoot], input args, req *CallRequest) error {
		hooks++
		require.Equal(t, NewString("schema"), input.Name)
		require.False(t, input.Injected.Valid)
		req.TTL = 30
		req.IsPersistable = true
		return req.SetArgInput(ctx, "injected", Opt(NewString("resolved")), false)
	}).WithInput(ImplicitInput{Name: "observed", Resolver: func(_ context.Context, inputs map[string]Input) (Input, error) {
		if input, ok := inputs["injected"].(Optional[String]); ok && input.Valid {
			return input.Value, nil
		}
		return NewString("before-hook"), nil
	}})
	field.Spec.ModuleProvider = func(_ context.Context, current *Server) (*ResultCallModule, error) {
		providers++
		require.Same(t, srv, current)
		return &ResultCallModule{Name: "provided", ResultRef: &ResultCallRef{ResultID: moduleID.EngineResultID()}}, nil
	}
	Fields[*persistCodecRoot]{field}.Install(srv)
	root := srv.Root().(ObjectResult[*persistCodecRoot])
	plan, err := root.Plan(ctx, srv, Selector{Field: "planned"})
	require.NoError(t, err)
	require.Zero(t, bodies)
	require.Equal(t, 1, hooks)
	require.Equal(t, 1, providers)
	require.Equal(t, "planned", plan.Field)
	require.Equal(t, int64(30), plan.TTL)
	require.True(t, plan.IsPersistable)
	require.Nil(t, plan.Receiver, "the query root has no receiver recipe")
	require.Equal(t, moduleID.EngineResultID(), plan.Module.ResultRef.ResultID)
	require.Len(t, plan.Args, 1, "schema defaults are decoded but only rewritten or explicit args enter the recipe")
	require.Equal(t, "injected", plan.Args[0].Name)
	require.Equal(t, "resolved", plan.Args[0].Value.StringValue)
	require.Len(t, plan.ImplicitInputs, 1)
	require.Equal(t, "resolved", plan.ImplicitInputs[0].Value.StringValue)
	var actual *CallRequest
	srv.ObserveCalls(func(ctx context.Context, req *CallRequest) (context.Context, func(AnyResult, bool, error)) {
		actual = req.Clone()
		return ctx, nil
	})
	var result String
	require.NoError(t, srv.Select(ctx, srv.Root(), &result, Selector{Field: "planned"}))
	require.Equal(t, 1, bodies)
	plannedDigest, err := plan.RecipeDigest(ctx)
	require.NoError(t, err)
	actualDigest, err := actual.RecipeDigest(ctx)
	require.NoError(t, err)
	require.Equal(t, plannedDigest, actualDigest, "normal selection uses the same prepared identity")
}
