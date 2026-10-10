package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestExpertiseOwnerContext(t *testing.T) {
	assertOwner := func(ctx context.Context, want string, valid bool) {
		t.Helper()
		owner, ok := ExpertiseOwner(ctx)
		require.Equal(t, want, owner)
		require.Equal(t, valid, ok)
	}
	ctx := t.Context()
	assertOwner(ctx, "", false)
	outer := WithExpertiseOwner(ctx, "outer")
	assertOwner(outer, "outer", true)
	assertOwner(WithExpertiseOwner(outer, "inner"), "outer", true)
	assertOwner(ctx, "", false)

	// Simulate fresh nested client requests, which do not inherit Go context
	// values but can resolve the engine-side FunctionCall for their client.
	for range 3 {
		owner, valid := ExpertiseOwner(outer)
		fnCall := newFunctionCall(FunctionCall{expertiseOwner: expertiseOwner{key: owner, valid: valid}})
		outer = ContextWithQuery(ctx, NewRoot(&mockServer{functionCall: fnCall}))
		assertOwner(outer, "outer", true)
		assertOwner(WithExpertiseOwner(outer, "nested"), "outer", true)
	}

	replayed := expertiseCallContext(outer, []CallInput{{Name: expertiseOwnerArg, Value: dagql.Opt(dagql.String("recorded"))}})
	assertOwner(replayed, "recorded", true)
	unowned := expertiseCallContext(outer, []CallInput{{Name: expertiseOwnerArg, Value: dagql.Opt(dagql.String(""))}})
	assertOwner(unowned, "", false)
	assertOwner(WithExpertiseOwner(unowned, "new-entry"), "new-entry", true)
	assertOwner(WithExpertiseOwner(WithExpertiseOwner(ctx, ""), "inner"), "", true)
}

func TestExpertiseOwnerCallCacheAndReplay(t *testing.T) {
	md := &engine.ClientMetadata{ClientID: "expertise-client", SessionID: "expertise-session"}
	ctx := engine.ContextWithClientMetadata(t.Context(), md)
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	srv, err := dagql.NewServer(ctx, &Query{})
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Module]{Typed: &Module{}}))
	mod, err := dagql.NewObjectResultForCall(&Module{}, srv, testResultCall("module", &Module{}, nil))
	require.NoError(t, err)
	fn := &ModuleFunction{mod: mod, metadata: &Function{}}
	calls := 0
	field := dagql.Func("owner", func(ctx context.Context, _ *Query, args struct {
		Owner dagql.Optional[dagql.String] `name:"_expertiseOwner" internal:"true"`
	}) (*Module, error) {
		calls++
		ctx = expertiseCallContext(ctx, []CallInput{{Name: expertiseOwnerArg, Value: args.Owner}})
		owner, _ := ExpertiseOwner(ctx)
		return &Module{NameField: owner}, nil
	})
	field.Spec.GetDynamicInput = fn.DynamicInputsForCall
	dagql.Fields[*Query]{field}.Install(srv)

	for _, owner := range []string{"entry-A", "entry-B", ""} {
		t.Run(owner, func(t *testing.T) {
			callCtx := ctx
			if owner != "" {
				callCtx = WithExpertiseOwner(ctx, owner)
			}
			before := calls
			var result dagql.AnyResult
			for range 2 {
				require.NoError(t, srv.Select(callCtx, srv.Root(), &result, dagql.Selector{Field: "owner"}))
				value, ok := dagql.UnwrapAs[*Module](result)
				require.True(t, ok)
				require.Equal(t, owner, value.NameField)
			}
			require.Equal(t, before+1, calls, "each owner has its own reusable cache entry")
			recipe, err := result.RecipeID(callCtx)
			require.NoError(t, err)
			require.Equal(t, owner, recipe.Arg(expertiseOwnerArg).Value().ToInput())
			encoded, err := recipe.Encode()
			require.NoError(t, err)
			var decoded call.ID
			require.NoError(t, decoded.Decode(encoded))

			// Rebuild with a cold cache under a different ambient owner. Explicit
			// stamps must remain stable even when replay invokes the resolver.
			replayCtx := WithExpertiseOwner(ctx, "replay-caller")
			replayCache, err := dagql.NewCache(replayCtx, "", nil, nil)
			require.NoError(t, err)
			replayCtx = dagql.ContextWithCache(replayCtx, replayCache)
			replayed, err := srv.Load(replayCtx, &decoded)
			require.NoError(t, err)
			value, ok := dagql.UnwrapAs[*Module](replayed)
			require.True(t, ok)
			require.Equal(t, owner, value.NameField)
			require.Equal(t, before+2, calls)
		})
	}

	// The recorded scope is engine plumbing, not a module SDK argument.
	inputs, err := fn.setCallInputs(ctx, &CallOpts{Inputs: []CallInput{{Name: expertiseOwnerArg, Value: dagql.Opt(dagql.String("entry-A"))}}})
	require.NoError(t, err)
	require.Empty(t, inputs)
	require.True(t, expertiseOwnerInputSpec().Internal)
}
